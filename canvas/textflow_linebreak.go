package canvas

import (
	"unicode"
)

func (l *layouter) line(p, pe int) (int, bool) {
	d := l.d
	pf := l.para.pf
	base := l.f.Style.Leading()
	if pf.LineSpacing > 0 {
		base *= pf.LineSpacing
	}
	lh := base
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
		left, right := l.para.left, pf.IndentRight
		if l.para.first && pf.List.Kind == ListNone {
			left += pf.FirstLine
		}
		if l.para.dropLines > 0 {
			left += l.para.dropW
		}
		ax0, ax1 := x0+max(left, 0), x1-right
		if l.para.rtl {
			ax0, ax1 = x0+max(right, 0), x1-left
		}
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
		base := uint8(0)
		if l.para.rtl {
			base = 1
		}
		lp := d.partsOfLv(textStart, textEnd, l.para.levels, l.para.ps, base)
		if hyph {
			lp.width += hyphW
		}
		h, baselineOff := lp.height, 0.0
		switch {
		case lp.block:
			h = lp.objH
		case len(lp.objs) > 0:
			below := max(lp.objBelow, 0.2*lp.size)
			h = max(lp.height, lp.objAbove+0.05*lp.size+below)
			baselineOff = h - below
		}
		if h > lh+1e-9 && try < 3 {
			lh = h
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
		align := pf.Align
		if l.para.rtl {
			switch align {
			case AlignLeft:
				align = AlignRight
			case AlignRight:
				align = AlignLeft
			}
		}
		switch room := ax1 - ax0 - lp.width; align {
		case AlignCenter:
			x += max(room, 0) / 2
		case AlignRight:
			x += max(room, 0)
		}
		line := Line{
			Text: string(d.runes[textStart:textEnd]), X: x, W: lp.width, H: h, Start: textStart, End: textEnd, Runs: lp.runs,
			Baseline: st.y + baselineOff, QuoteBar: pf.QuoteBar, BarX: x0 + pf.IndentLeft - 8,
			Hyphen: hyph, HyphenW: hyphW, RTL: l.para.rtl, Bidi: l.para.levels != nil,
		}
		if l.para.rtl {
			line.BarX = x1 - pf.IndentLeft + 5
		}
		for _, o := range lp.objs {
			o.X += x
			if lp.block {
				o.Y = st.y
			} else {
				o.Y = st.y + baselineOff - o.H + o.Descent
			}
			line.Objects = append(line.Objects, o)
		}
		if l.para.first && l.para.marker != "" {
			line.Marker = l.marker(p, ax0)
			if l.para.rtl {
				line.Marker.X = ax1 + 6
			}
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

func (l *layouter) hyphenate(start, e, j int, w float64) (pos int, hyphW float64, ok bool) {
	h := l.f.Hyphenator
	d := l.d
	if h == nil || j-e < 5 {
		return 0, 0, false
	}
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

func isHyphenWordRune(r rune) bool { return unicode.IsLetter(r) || r == '\'' }

func (l *layouter) hyphenWidth(i int) float64 {
	p := l.f.Painter
	p.SetFont(l.d.styles[l.d.rs[i]].Font())
	return p.MeasureTextWidth("-")
}

// Hyphenator says where a word may be broken.
type Hyphenator interface {
	Positions(word string) []int
}
