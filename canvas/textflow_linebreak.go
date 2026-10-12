package canvas

import (
	"unicode"
)

// line places one line starting at rune p of the paragraph ending at pe
// (p == pe: a blank paragraph's line), returning where the next line
// starts.
func (l *layouter) line(p, pe int) (int, bool) {
	d := l.d
	pf := l.para.pf
	base := l.f.Style.Leading()
	if pf.LineSpacing > 0 {
		base *= pf.LineSpacing
	}
	lh := base
	// A drop cap takes the paragraph's first letter out of its text.
	textStart := p
	var capText string
	var capStyle TextStyle
	if l.para.first && pf.DropCap > 0 && p < pe && !d.isObj(p) && !isSpace(d.runes[p]) {
		capText, capStyle = string(d.disp[p]), d.styles[d.rs[p]]
		capStyle.FontSize, capStyle.LineHeight = base*float64(pf.DropCap)*0.85, 0
		l.f.Painter.SetFont(capStyle.Font())
		l.para.dropW = l.f.Painter.MeasureTextWidth(capText) + 6
		l.para.dropLines, l.para.dropLH = pf.DropCap, base
		textStart = p + 1
	}
	for try := 0; ; try++ {
		st, x0, x1, marks, ok := l.find(l.st, lh, p)
		if !ok {
			l.overflowAt(p)
			return 0, false
		}
		// The paragraph's indents narrow the free span; if that leaves too
		// little (a narrow column), ignore them.
		left, right := l.para.left, pf.IndentRight
		if l.para.first && pf.List.Kind == ListNone {
			left += pf.FirstLine
		}
		if l.para.dropLines > 0 {
			left += l.para.dropW
		}
		ax0, ax1 := x0+max(left, 0), x1-right
		if ax1-ax0 < min(60, x1-x0) {
			ax0, ax1 = x0, x1
		}
		_, _, _, ph := l.f.pageRect(st.page)
		d.maxW, d.maxH = ax1-ax0, ph
		textEnd, next := textStart, textStart
		l.hyph, l.hyphW = false, 0
		if textStart < pe {
			textEnd, next = l.breakLine(textStart, pe, ax1-ax0)
		}
		hyph, hyphW := l.hyph, l.hyphW
		lp := d.partsOf(textStart, textEnd)
		if hyph {
			lp.width += hyphW
		}
		h, baselineOff := lp.height, 0.0
		switch {
		case lp.block:
			h = lp.objH
		case len(lp.objs) > 0:
			// Objects sit on the baseline; leave room under it for descenders.
			h = max(lp.height, lp.objH+0.25*lp.size)
			baselineOff = h - 0.2*lp.size
		}
		if h > lh+1e-9 && try < 3 {
			lh = h // taller than the band we tried: look again with the real height
			continue
		}
		if !lp.block {
			h = max(h, lh)
		}
		if baselineOff == 0 {
			size := lp.size
			if size == 0 {
				size = l.f.Style.FontSize
			}
			baselineOff = (h-size)/2 + size*0.8
		}
		x := ax0
		switch room := ax1 - ax0 - lp.width; pf.Align {
		case AlignCenter:
			x += max(room, 0) / 2
		case AlignRight:
			x += max(room, 0)
		}
		line := Line{
			Text: string(d.runes[textStart:textEnd]), X: x, W: lp.width, H: h, Start: textStart, End: textEnd, Runs: lp.runs,
			Baseline: st.y + baselineOff, QuoteBar: pf.QuoteBar, BarX: x0 + pf.IndentLeft - 8,
			Hyphen: hyph, HyphenW: hyphW,
		}
		for _, o := range lp.objs {
			o.X += x
			if lp.block {
				o.Y = st.y
			} else {
				o.Y = st.y + baselineOff - o.H
			}
			line.Objects = append(line.Objects, o)
		}
		if l.para.first && l.para.marker != "" {
			line.Marker = l.marker(p, ax0)
		}
		if capText != "" {
			line.DropCap = &DropCapMark{
				Text: capText, X: ax0 - l.para.dropW, Font: capStyle.Font(), Color: capStyle.Color,
				Baseline: st.y + baselineOff + float64(pf.DropCap-1)*h,
			}
		}
		l.para.first, l.para.marker = false, ""
		if l.para.dropLines > 0 {
			l.para.dropLines--
		}
		l.place(st, marks, line)
		return next, true
	}
}

// marker builds the list marker of the paragraph's first line, right
// aligned just left of the text at x.
func (l *layouter) marker(p int, x float64) *LineMarker {
	d := l.d
	st := l.f.Style
	if p < len(d.runes) && !d.isObj(p) {
		st = d.styles[d.rs[p]]
	}
	lst := l.para.pf.List
	m := &LineMarker{Text: l.para.marker, Font: st.Font(), Color: st.Color, Check: lst.Kind == ListCheck, Checked: lst.Checked && lst.Kind == ListCheck}
	if m.Color == "" {
		m.Color = "#222222"
	}
	pt := l.f.Painter
	pt.SetFont(m.Font)
	m.W = pt.MeasureTextWidth(m.Text)
	m.X = x - 6 - m.W
	return m
}

// breakLine picks how much of runes[start:end] fits in width w: it
// returns where the line's text ends (trailing spaces excluded) and
// where the next line starts (skipping the spaces). An inline object is
// one unbreakable word; a block object gets a line to itself.
func (l *layouter) breakLine(start, end int, w float64) (textEnd, next int) {
	d := l.d
	textEnd, next = -1, start
	for e := start; e < end; {
		j := e
		if d.isObj(j) {
			j++
		} else {
			for j < end && !isSpace(d.runes[j]) && !d.isObj(j) {
				j++
			}
		}
		k := j
		for k < end && isSpace(d.runes[k]) {
			k++
		}
		if in, ok := d.objs[e]; ok && in.Block {
			if e > start { // the block goes on the next line
				break
			}
			return e + 1, e + 1
		}
		if j > start && d.measure(start, j) > w {
			// This word does not fit: hyphenate it to fill the line, if it can be.
			if pos, hw, ok := l.hyphenate(start, e, j, w); ok {
				l.hyph, l.hyphW = true, hw
				return pos, pos
			}
			break
		}
		textEnd, next = j, k
		e = k
	}
	if textEnd > start {
		return textEnd, next
	}
	// A single word wider than the line: break inside it (at a hyphenation
	// point if there is a Hyphenator and one fits).
	if end > start {
		j := start
		for j < end && !isSpace(d.runes[j]) && !d.isObj(j) {
			j++
		}
		if pos, hw, ok := l.hyphenate(start, start, j, w); ok {
			l.hyph, l.hyphW = true, hw
			return pos, pos
		}
	}
	c := start + 1
	for c < end && !d.isObj(c) && d.measure(start, c+1) <= w {
		c++
	}
	return c, c
}

// hyphenate finds where to break the word runes[e:j] so that the line that
// starts at start, with a hyphen after it, fits in w: the last hyphenation
// point that does. ok is false without a Hyphenator, for a word with an inline
// object or a symbol in it, or when no point fits.
func (l *layouter) hyphenate(start, e, j int, w float64) (pos int, hyphW float64, ok bool) {
	h := l.f.Hyphenator
	d := l.d
	if h == nil || j-e < 5 {
		return 0, 0, false
	}
	// The letters of the word, without the punctuation around it.
	a, b := e, j
	for a < b && !isHyphenWordRune(d.runes[a]) {
		a++
	}
	for b > a && !isHyphenWordRune(d.runes[b-1]) {
		b--
	}
	for i := a; i < b; i++ {
		if d.isObj(i) || !isHyphenWordRune(d.runes[i]) {
			return 0, 0, false
		}
	}
	best := -1
	for _, p := range h.Positions(string(d.runes[a:b])) {
		at := a + p
		if at <= start || at >= j || at <= e {
			continue
		}
		hw := l.hyphenWidth(at - 1)
		if d.measure(start, at)+hw <= w && at > best {
			best, hyphW = at, hw
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return best, hyphW, true
}

// isHyphenWordRune is a character a word may be hyphenated through: letters and
// an apostrophe.
func isHyphenWordRune(r rune) bool { return unicode.IsLetter(r) || r == '\'' }

// hyphenWidth is the width of a hyphen in the style of the rune at i.
func (l *layouter) hyphenWidth(i int) float64 {
	p := l.f.Painter
	p.SetFont(l.d.styles[l.d.rs[i]].Font())
	return p.MeasureTextWidth("-")
}

// Hyphenator says where a word may be broken.
type Hyphenator interface {
	// Positions returns, in ascending order, the rune offsets k in word at which
	// word[:k] (plus a hyphen) may end a line and word[k:] start the next.
	Positions(word string) []int
}
