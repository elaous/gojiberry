package canvas

import (
	"slices"
	"sort"
	"unicode"
)

type flowData struct {
	runes   []rune
	disp    []rune // display text per rune, with case changes applied.
	rs      []int  // per rune: index into styles
	styles  []TextStyle
	formats []Format // per style: the Format it was resolved from
	ops     []flowOp
	f       *TextFlow

	paras      []paraRec      // paragraph formats, in stream order
	objs       map[int]Inline // inline objects by text-stream offset
	maxW, maxH float64        // room objects are measured against right now
	dims       map[objKey][2]float64
}

type paraRec struct {
	at int
	pf ParaFormat
}

func (d *flowData) paraIn(ps, pe int) ParaFormat {
	for _, r := range d.paras {
		if r.at >= ps && r.at <= pe {
			return r.pf
		}
	}
	return ParaFormat{}
}

type objKey struct {
	at         int
	maxW, maxH float64
}

type flowOp struct {
	at   int // text-stream offset the op sits at
	cfg  int // item index, for an opColumns op (else -1)
	kind BreakKind
}

const opColumns BreakKind = -1

func (f *TextFlow) flatten() *flowData {
	d := &flowData{f: f, objs: map[int]Inline{}, dims: map[objKey][2]float64{}, maxW: 1e9, maxH: 1e9}
	type styleKey struct {
		f, base Format
		small   bool
	}
	styleID := map[styleKey]int{}
	var paraBase Format // the character format of the current paragraph's style
	styleFor := func(fm Format, small bool) int {
		k := styleKey{fm, paraBase, small}
		id, ok := styleID[k]
		if !ok {
			id = len(d.styles)
			styleID[k] = id
			st := f.expand(paraBase, fm).resolve(f.Style)
			if fm.Link != "" && f.LinkColor != "" && f.expand(paraBase, fm).Color == "" {
				st.Color = f.LinkColor
			}
			if fm.Proposal != "" {
				proposalStyle(&st, fm.Proposal)
			}
			if small { // a small capital: the lower-case letter's capital at 0.8 size
				if st.FontSize <= 0 {
					st.FontSize = 16
				}
				st.FontSize *= 0.8
			}
			if k := f.TextScale; k > 0 && k != 1 {
				if st.FontSize <= 0 {
					st.FontSize = 16
				}
				st.FontSize *= k
				st.LineHeight *= k
			}
			d.styles = append(d.styles, st)
			d.formats = append(d.formats, fm)
		}
		return id
	}
	prevLetter := false
	notes := 0 // footnote marks so far, to number them
	for idx, it := range f.items() {
		switch it := it.(type) {
		case Chars:
			var ids [2]int
			var have [2]bool
			for _, r := range it.Text {
				shown, small := displayRune(it.Format.Case, r, prevLetter)
				prevLetter = unicode.IsLetter(r) || unicode.IsDigit(r) || (prevLetter && r == '\'')
				d.runes = append(d.runes, r)
				d.disp = append(d.disp, shown)
				k := 0
				if small {
					k = 1
				}
				if !have[k] {
					ids[k], have[k] = styleFor(it.Format, small), true
				}
				d.rs = append(d.rs, ids[k])
				if r == '\n' {
					paraBase = Format{} // the next paragraph starts clean, until its own style item
					have = [2]bool{}
				}
			}
		case Inline:
			switch o := it.Object.(type) {
			case *FootnoteRef:
				notes++
				o.N = notes
			case flowBinder:
				o.bindFlow(f)
			}
			id := styleFor(Format{}, false)
			d.objs[len(d.runes)] = it
			d.runes = append(d.runes, objectRune)
			d.disp = append(d.disp, objectRune)
			d.rs = append(d.rs, id)
			prevLetter = false
		case ParaFormat:
			d.paras = append(d.paras, paraRec{len(d.runes), it})
			_, paraBase = f.expandPara(it)
			prevLetter = false
		case ColumnConfig:
			d.ops = append(d.ops, flowOp{at: len(d.runes), cfg: idx, kind: opColumns})
		case Break:
			if it.Kind != BreakNatural {
				d.ops = append(d.ops, flowOp{at: len(d.runes), cfg: -1, kind: it.Kind})
			}
		}
	}
	if len(d.styles) == 0 {
		d.styles = []TextStyle{f.Style}
		d.formats = []Format{{}}
	}
	return d
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' }

func (d *flowData) objDims(i int) (w, h float64) {
	k := objKey{i, d.maxW, d.maxH}
	if v, ok := d.dims[k]; ok {
		return v[0], v[1]
	}
	if d.f.Vertical {
		h, w = d.objs[i].Object.Measure(d.f.Painter, d.maxH, d.maxW)
	} else {
		w, h = d.objs[i].Object.Measure(d.f.Painter, d.maxW, d.maxH)
	}
	d.dims[k] = [2]float64{w, h}
	return w, h
}

func (d *flowData) isObj(i int) bool { _, ok := d.objs[i]; return ok }

func (d *flowData) measure(a, b int) float64 {
	p := d.f.Painter
	w := 0.0
	for a < b {
		if d.isObj(a) {
			ow, _ := d.objDims(a)
			w += ow
			a++
			continue
		}
		id, e := d.rs[a], a+1
		for e < b && d.rs[e] == id && !d.isObj(e) {
			e++
		}
		p.SetFont(d.styles[id].Font())
		w += p.MeasureTextWidth(string(d.disp[a:e]))
		a = e
	}
	return w
}

type lineParts struct {
	runs          []Run
	objs          []PlacedObject
	width, height float64
	size          float64 // largest font size on the line
	objH          float64 // tallest object
	objAbove      float64
	objBelow      float64 // the most any object reaches below it
	block         bool    // the line is a block object alone
}

func (d *flowData) partsOfLv(a, b int, lv []uint8, off int, base uint8) lineParts {
	p := d.f.Painter
	var lp lineParts
	type item struct {
		run, obj int // the index in lp.runs or lp.objs, the other -1
		w        float64
		level    uint8
	}
	var items []item
	levelAt := func(i int) uint8 {
		if lv == nil || i-off < 0 || i-off >= len(lv) {
			return 0
		}
		return lv[i-off]
	}
	for a < b {
		if in, ok := d.objs[a]; ok {
			w, h := d.objDims(a)
			desc := 0.0
			if do, ok := in.Object.(DescentObject); ok && !in.Block {
				desc = min(max(do.Descent(), 0), h)
			}
			lp.objs = append(lp.objs, PlacedObject{Object: in.Object, X: lp.width, W: w, H: h, Offset: a, Descent: desc})
			items = append(items, item{run: -1, obj: len(lp.objs) - 1, w: w, level: base})
			lp.width += w
			lp.objH = max(lp.objH, h)
			lp.objAbove = max(lp.objAbove, h-desc)
			lp.objBelow = max(lp.objBelow, desc)
			lp.block = lp.block || in.Block
			a++
			continue
		}
		id, e := d.rs[a], a+1
		lvl := levelAt(a)
		for e < b && d.rs[e] == id && !d.isObj(e) && levelAt(e) == lvl {
			e++
		}
		st := d.styles[id]
		p.SetFont(st.Font())
		text := string(d.disp[a:e])
		w := p.MeasureTextWidth(text)
		lp.runs = append(lp.runs, Run{
			Text: text, X: lp.width, W: w, Font: st.Font(), Color: st.Color, Start: a, End: e,
			Underline: st.Underline, Strike: st.Strike, Background: st.Background,
			DY: st.baselineShift(), Size: st.drawSize(), Link: d.formats[id].Link, Comment: d.formats[id].Comment,
			Level: lvl,
		})
		items = append(items, item{run: len(lp.runs) - 1, obj: -1, w: w, level: lvl})
		lp.width += w
		lp.height = max(lp.height, st.Leading())
		lp.size = max(lp.size, st.FontSize)
		a = e
	}
	if lv != nil {
		levels := make([]uint8, len(items))
		for i, it := range items {
			levels[i] = it.level
		}
		x := 0.0
		for _, i := range visualOrder(levels) {
			it := items[i]
			if it.run >= 0 {
				lp.runs[it.run].X = x
			} else {
				lp.objs[it.obj].X = x
			}
			x += it.w
		}
	}
	return lp
}

type bandRec struct {
	Band
	registered bool
}

type flowState struct {
	page, col int
	y         float64
	band      *bandRec
	maxY      float64 // bottom of the lowest column used in the band
	cfg       int     // column config in effect
}

type layouter struct {
	f     *TextFlow
	d     *flowData
	st    flowState
	out   Layout
	bands []*bandRec
	stop  bool

	nums listNumbers
	para paraCtx

	hyph  bool
	hyphW float64
}

type paraCtx struct {
	pf     ParaFormat
	first  bool
	rtl    bool // the paragraph is laid out right to left
	levels []uint8
	ps     int
	marker string // the list marker to put on that line, if any
	left   float64

	dropW     float64
	dropLines int
	dropLH    float64
}

// Layout flows the content and returns where every line falls.
func (f *TextFlow) Layout() Layout {
	lay := f.layoutOnce()
	if f.Footnotes.Len() == 0 {
		f.fnBands, f.fnLines = nil, nil
		return lay
	}
	for pass := range 8 {
		bands, lines := f.footnotePlan(lay)
		if pass >= 3 {
			for i := range bands {
				if i < len(f.fnBands) {
					bands[i] = max(bands[i], f.fnBands[i])
				}
			}
		}
		same := slices.Equal(bands, f.fnBands)
		f.fnBands, f.fnLines = bands, lines
		if same {
			return lay
		}
		lay = f.layoutOnce()
	}
	return lay
}

func (f *TextFlow) layoutOnce() Layout {
	d := f.flatten()
	f.data = d
	if f.pageCount() == 0 { // a PageSet with no pages: nowhere to put anything
		return Layout{Overflow: len(d.runes) > 0}
	}
	l := &layouter{f: f, d: d}
	l.out.Pages = 1
	l.st = flowState{cfg: -1}
	l.enter(&l.st, 0, -1)

	pos := 0
	for _, op := range d.ops {
		l.segment(pos, op.at, false)
		if l.stop {
			break
		}
		l.apply(op)
		pos = op.at
	}
	if !l.stop {
		l.segment(pos, len(d.runes), true)
	}
	if !l.stop {
		l.out.End = len(d.runes)
	}
	for _, b := range l.bands {
		l.out.Bands = append(l.out.Bands, b.Band)
	}
	return l.out
}

func (l *layouter) newBand(cfg, page int, top float64) *bandRec {
	x, y, w, _ := l.f.pageRect(page)
	if top < 0 {
		top = y
	}
	cc := l.f.config(cfg)
	widths, gap := fitColumns(cc, w), cc.Gap
	xs := make([]float64, len(widths))
	cx := x
	for i, v := range widths {
		xs[i] = cx
		cx += v + gap
	}
	return &bandRec{Band: Band{Page: page, Top: top, Bottom: top, Xs: xs, Widths: widths, Gap: gap, Avail: w, Cfg: cfg}}
}

func fitColumns(cc ColumnConfig, w float64) []float64 {
	widths, gap := cc.Widths, cc.Gap
	n := len(widths)
	if n == 0 && cc.Count > 1 {
		n = cc.Count
		room := w - gap*float64(n-1)
		if room <= 0 {
			return []float64{w}
		}
		widths = make([]float64, n)
		for i := range widths {
			widths[i] = room / float64(n)
		}
		return widths
	}
	if n == 0 {
		return []float64{w}
	}
	sum := 0.0
	for _, v := range widths {
		sum += v
	}
	room := w - gap*float64(n-1)
	if room <= 0 || sum <= 0 {
		return []float64{w}
	}
	if sum <= room+1e-9 {
		return widths
	}
	scaled := make([]float64, n)
	for i, v := range widths {
		scaled[i] = v * room / sum
	}
	return scaled
}

func (l *layouter) enter(st *flowState, page int, top float64) {
	st.page, st.col = page, 0
	st.band = l.newBand(st.cfg, page, top)
	st.y, st.maxY = st.band.Top, st.band.Top
}

func (l *layouter) overflowAt(offset int) {
	l.out.Overflow, l.out.End, l.stop = true, offset, true
}

func (l *layouter) mark(kind BreakKind, st flowState, offset int) {
	l.out.Breaks = append(l.out.Breaks, BreakMark{Kind: kind, Page: st.page, Col: st.col, Offset: offset})
}

func (l *layouter) nextPage(st *flowState) bool {
	if st.page+1 >= l.f.pageCount() {
		return false
	}
	l.enter(st, st.page+1, -1)
	return true
}

func (l *layouter) apply(op flowOp) {
	st := l.st
	switch op.kind {
	case opColumns: // ColumnConfig starts a band below the text so far.
		top := st.maxY
		st.cfg = op.cfg
		l.enter(&st, st.page, top)
	case BreakPage:
		l.mark(BreakPage, st, op.at)
		if !l.nextPage(&st) {
			l.overflowAt(op.at)
			return
		}
		l.out.Pages = max(l.out.Pages, st.page+1)
	case BreakColumn:
		l.mark(BreakColumn, st, op.at)
		if st.col+1 < len(st.band.Xs) {
			st.col++
			st.y = st.band.Top
		} else {
			if !l.nextPage(&st) {
				l.overflowAt(op.at)
				return
			}
			l.out.Pages = max(l.out.Pages, st.page+1)
		}
	}
	l.st = st
}

func (l *layouter) find(st flowState, lh float64, offset int) (flowState, float64, float64, []BreakMark, bool) {
	var marks []BreakMark
	minSeg := 2 * max(l.f.Style.FontSize, 1)
	for {
		_, y0, _, h := l.f.pageRect(st.page)
		if st.y+lh > y0+h+1e-9 {
			if st.col+1 < len(st.band.Xs) {
				st.col++
				st.y = st.band.Top
				continue
			}
			marks = append(marks, BreakMark{Kind: BreakNatural, Page: st.page, Col: st.col, Offset: offset})
			if !l.nextPage(&st) {
				return st, 0, 0, marks, false
			}
			continue
		}
		c0 := st.band.Xs[st.col]
		cw := st.band.Widths[st.col]
		x0, x1 := l.f.freeSpan(c0, c0+cw, st.y, st.y+lh)
		if x1-x0 >= min(minSeg, cw) {
			return st, x0, x1, marks, true
		}
		st.y += lh
	}
}

func (l *layouter) place(st flowState, marks []BreakMark, line Line) {
	l.out.Breaks = append(l.out.Breaks, marks...)
	line.Page, line.Col, line.Y = st.page, st.col, st.y
	line.ColX, line.ColW = st.band.Xs[st.col], st.band.Widths[st.col]
	if !st.band.registered {
		st.band.registered = true
		l.bands = append(l.bands, st.band)
	}
	st.y += line.H
	st.maxY = max(st.maxY, st.y)
	st.band.Bottom = max(st.band.Bottom, st.y)
	l.out.Lines = append(l.out.Lines, line)
	l.out.Pages = max(l.out.Pages, st.page+1)
	l.st = st
}

func (l *layouter) segment(s, e int, last bool) {
	if s >= e && !last {
		return
	}
	d := l.d
	for ps := s; ; {
		pe := ps
		for pe < e && d.runes[pe] != '\n' {
			pe++
		}
		if ps == pe && ps == e && !last {
			return
		}
		l.beginParagraph(ps, pe)
		if ps == pe {
			if _, ok := l.line(ps, pe); !ok {
				return
			}
		}
		for p := ps; p < pe; {
			next, ok := l.line(p, pe)
			if !ok {
				return
			}
			p = next
		}
		l.endParagraph()
		if pe == e {
			return
		}
		ps = pe + 1
	}
}

func (l *layouter) beginParagraph(ps, pe int) {
	pf, _ := l.f.expandPara(l.d.paraIn(ps, pe))
	start := ps == 0 || l.d.runes[ps-1] == '\n'
	l.para = paraCtx{pf: pf, first: start, left: pf.leftIndent(), ps: ps}
	if pe > ps && !l.f.Vertical {
		l.para.rtl = IsRTLText(l.d.disp[ps:pe])
		l.para.levels = bidiLevels(l.d.disp[ps:pe], l.para.rtl)
	}
	if !start {
		return
	}
	if n := l.nums.next(pf.List); pf.List.Kind != ListNone {
		l.para.marker = pf.List.markerText(n)
	}
	if pf.SpaceBefore > 0 {
		l.st.y += pf.SpaceBefore
		l.st.maxY = max(l.st.maxY, l.st.y)
	}
}

func (l *layouter) endParagraph() {
	if l.para.dropLines > 0 && !l.stop { // a short paragraph: the cap hangs below it
		l.st.y += float64(l.para.dropLines) * l.para.dropLH
		l.st.maxY = max(l.st.maxY, l.st.y)
		l.para.dropLines = 0
	}
	if sa := l.para.pf.SpaceAfter; sa > 0 && !l.stop {
		l.st.y += sa
		l.st.maxY = max(l.st.maxY, l.st.y)
	}
}

func (f *TextFlow) freeSpan(c0, c1, y0, y1 float64) (float64, float64) {
	type iv struct{ a, b float64 }
	var cut []iv
	for _, o := range f.obstacles {
		if f.Vertical {
			break
		}
		if a, b, ok := o.Exclusion(y0, y1); ok {
			cut = append(cut, iv{a, b})
		}
	}
	sort.Slice(cut, func(i, j int) bool { return cut[i].a < cut[j].a })
	bestA, bestB := c0, c0
	at := c0
	consider := func(a, b float64) {
		if b-a > bestB-bestA {
			bestA, bestB = a, b
		}
	}
	for _, c := range cut {
		if c.b <= at {
			continue
		}
		if c.a >= c1 {
			break
		}
		consider(at, min(c.a, c1))
		at = max(at, c.b)
	}
	consider(at, c1)
	return bestA, bestB
}
