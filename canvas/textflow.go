package canvas

import (
	"slices"
	"sort"
)

// Obstacle is something text must flow around (see Image). Exclusion
// reports, for the horizontal band y0..y1 (canvas coordinates), the
// x-range text must stay out of; ok is false if the band is clear.
type Obstacle interface {
	Exclusion(y0, y1 float64) (x0, x1 float64, ok bool)
}

// FlowStyle is the chrome TextFlow draws in edit mode, and its page.
type FlowStyle struct {
	PageBG          string
	PageBorder      string
	GuideColor      string // line down each column gap
	GripColor       string
	GripActiveColor string
	GripW, GripH    float64 // size of the draggable grip above each gap
	SelectColor     string  // tint over selected text
}

// DefaultFlowStyle is a light look matching DefaultTableStyle.
func DefaultFlowStyle() FlowStyle {
	return FlowStyle{
		PageBG:          "#ffffff",
		PageBorder:      "#d4d6da",
		GuideColor:      "#b9a9ff",
		GripColor:       "#7b818b",
		GripActiveColor: "#7c5cff",
		GripW:           16,
		GripH:           16,
		SelectColor:     "rgba(124, 92, 255, 0.28)",
	}
}

// Run is a stretch of a Line in one font and color.
type Run struct {
	Text       string
	X, W       float64 // offset from the line's X, and measured width
	Font       string  // CSS font
	Color      string
	Start, End int // rune offsets into the text stream

	// Decorations. DY is the baseline shift of a superscript (negative)
	// or subscript (positive); Size is the size the run is drawn at.
	Underline, Strike bool
	Background        string
	DY, Size          float64
}

// Line is one laid-out line of text. Start/End are rune offsets into
// the text stream (all Chars items concatenated; End exclusive, trailing
// spaces excluded). A blank paragraph is a Line with no text.
type Line struct {
	Text       string
	X, Y, W, H float64 // left edge, top of the line box, measured text width, line height
	Baseline   float64 // y to draw the runs' text at
	Page, Col  int
	Start, End int
	ColX, ColW float64 // the column the line is in
	Runs       []Run
	Objects    []PlacedObject // inline objects on this line
}

// BreakMark records a break the layout honoured or made: a
// BreakNatural when text overflowed Page's last column, or the user's
// BreakPage/BreakColumn. Page and Col are where the break happened;
// Offset is the text-stream offset the flow resumed at.
type BreakMark struct {
	Kind      BreakKind
	Page, Col int
	Offset    int
}

// Band is a stretch of a page's text area using one column layout: the
// first of a page, or one started by a ColumnConfig.
type Band struct {
	Page        int
	Top, Bottom float64   // vertical extent actually used
	Xs, Widths  []float64 // each column's left edge and width
	Gap         float64
	Avail       float64 // the text area's width
	Cfg         int     // index of the ColumnConfig in Items, or -1 for the base Columns/Gap
}

// Layout is the result of flowing text into pages.
type Layout struct {
	Lines  []Line
	Breaks []BreakMark
	Bands  []Band
	// Overflow is true if the text didn't all fit in the pages; End is
	// the text-stream offset where layout stopped (the stream's length
	// when it all fit).
	Overflow bool
	End      int
	// Pages is how many pages the text reached (always at least 1).
	Pages int
}

// TextFlow lays out a content stream into pages, word-wrapping it using
// the Style's font metrics, in columns, around Obstacles such as a
// wrapped Image, and across any number of pages.
//
// The content is Items (or just Text, for a single unformatted run):
// Chars (text with a Format), ColumnConfig (the column widths and gap
// from here on -- they persist until the next one) and Break (a page or
// column break). Pages is the sheets to flow into; with no Pages the
// flow fills the single page rectangle (X, Y, Width, Height). The flow
// is continuous: when a page's last column is full the text carries on
// at the top of the next page (a natural break); a user BreakPage or
// BreakColumn forces it to move on early; if it runs out of pages
// Layout.Overflow is set.
//
// The base column layout is Columns (widths, default one full-width
// column) and Gap. In edit mode a grip above each column gap can be
// dragged with the mouse to resize the two neighbouring columns (their
// combined width is preserved); it edits whichever ColumnConfig (or the
// base) that band uses. View mode just draws the text.
type TextFlow struct {
	Text  string
	Items []Item
	Style TextStyle

	// Transform, if set, turns Source into the content (Items and Text
	// are then ignored): see Transform and Markdown. The result is
	// cached until Source changes; call InvalidateTransform after
	// changing the transform's own settings.
	Source    string
	Transform Transform

	// Pages, if set, supplies the page rectangles (see PageSet); X, Y,
	// Width and Height are then ignored.
	Pages         *PageSet
	X, Y          float64
	Width, Height float64

	Columns []float64
	Gap     float64
	// MinColumnWidth stops a drag from collapsing a column (default 40).
	MinColumnWidth float64

	ChromeStyle FlowStyle
	Painter     Painter
	// Transparent skips drawing the single-page background (for a flow
	// inside another component).
	Transparent bool

	// OnChange is called when the columns are resized by dragging.
	OnChange func(*TextFlow)

	derived    []Item
	derivedSrc string
	derivedOK  bool

	sel     flowSel
	formats *FormatClipboard

	// editing state (see flowedit.go)
	editing  bool     // the caret is in the text (edit mode, after a click)
	caretOff bool     // blink: the caret is hidden right now
	extra    []Cursor // further cursors; sel is the primary one
	pending  Format   // format for the next typed text when nothing is selected
	edited   bool     // Items hold the content now (see materialize)

	obstacles []Obstacle
	mode      Mode
	hover     grip
	drag      *flowDrag
	data      *flowData // from the latest Layout, for caret queries
}

type grip struct{ band, gap int }

var noGrip = grip{-1, -1}

type flowDrag struct {
	grip           grip
	cfg            int
	widths         []float64
	startX         float64
	startL, startR float64
}

// NewTextFlow returns a flow of text in a single width x height page at
// (x, y), with DefaultTextStyle, one column, in ModeView.
func NewTextFlow(text string, x, y, width, height float64) *TextFlow {
	return &TextFlow{
		Text: text, Style: DefaultTextStyle(), X: x, Y: y, Width: width, Height: height,
		ChromeStyle: DefaultFlowStyle(), hover: noGrip,
	}
}

// SetPainter implements PainterSetter.
func (f *TextFlow) SetPainter(p ImagePainter) { f.Painter = p }

// Mode returns the current mode.
func (f *TextFlow) Mode() Mode { return f.mode }

// SetMode switches mode; leaving edit mode cancels any drag. Returns
// true if a redraw is needed.
func (f *TextFlow) SetMode(m Mode) bool {
	if m == f.mode {
		return false
	}
	f.mode = m
	if m == ModeView {
		f.drag, f.hover = nil, noGrip
		f.stopEditing()
	}
	return true
}

// AddObstacle makes text flow around o (no-op if already added).
func (f *TextFlow) AddObstacle(o Obstacle) {
	if !slices.Contains(f.obstacles, o) {
		f.obstacles = append(f.obstacles, o)
	}
}

// RemoveObstacle stops text flowing around o.
func (f *TextFlow) RemoveObstacle(o Obstacle) {
	f.obstacles = slices.DeleteFunc(f.obstacles, func(x Obstacle) bool { return x == o })
}

// SetColumnCount makes the base layout n equal columns (minimum 1)
// separated by Gap, sized for the single page (X, Y, Width, Height).
func (f *TextFlow) SetColumnCount(n int) {
	n = max(n, 1)
	w := (f.Width - f.Gap*float64(n-1)) / float64(n)
	f.Columns = make([]float64, n)
	for i := range f.Columns {
		f.Columns[i] = w
	}
}

func (f *TextFlow) minColumn() float64 {
	if f.MinColumnWidth > 0 {
		return f.MinColumnWidth
	}
	return 40
}

func (f *TextFlow) items() []Item {
	if f.Transform != nil && !f.edited {
		if !f.derivedOK || f.derivedSrc != f.Source {
			f.derived, f.derivedSrc, f.derivedOK = f.Transform.Transform(f.Source), f.Source, true
		}
		return f.derived
	}
	if len(f.Items) > 0 {
		return f.Items
	}
	return []Item{Chars{Text: f.Text}}
}

// InvalidateTransform makes the next layout run the Transform again
// (it otherwise reruns only when Source changes).
func (f *TextFlow) InvalidateTransform() { f.derivedOK = false }

func (f *TextFlow) pageCount() int {
	if f.Pages != nil {
		return len(f.Pages.Pages)
	}
	return 1
}

// pageRect is the rectangle text flows into on a page.
func (f *TextFlow) pageRect(page int) (x, y, w, h float64) {
	if f.Pages != nil {
		return f.Pages.ContentRect(page)
	}
	return f.X, f.Y, f.Width, f.Height
}

// config returns column config idx (-1: the base Columns and Gap).
func (f *TextFlow) config(idx int) ColumnConfig {
	if idx >= 0 {
		if c, ok := f.items()[idx].(ColumnConfig); ok {
			return c
		}
	}
	return ColumnConfig{Widths: f.Columns, Gap: f.Gap}
}

// setConfigWidths writes widths back to config idx (-1: base).
func (f *TextFlow) setConfigWidths(idx int, widths []float64) {
	if idx >= 0 {
		items := f.items()
		if c, ok := items[idx].(ColumnConfig); ok {
			c.Widths = widths
			items[idx] = c
			return
		}
	}
	f.Columns = widths
}

// --- layout ---------------------------------------------------------

// flowData is the flattened content stream a Layout was made from.
type flowData struct {
	runes   []rune
	rs      []int // per rune: index into styles
	styles  []TextStyle
	formats []Format // per style: the Format it was resolved from
	ops     []flowOp
	f       *TextFlow

	objs       map[int]Inline // inline objects by text-stream offset
	maxW, maxH float64        // room objects are measured against right now
	dims       map[objKey][2]float64
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

// opColumns is the flowOp kind of a ColumnConfig (the other kinds are
// BreakKinds).
const opColumns BreakKind = -1

func (f *TextFlow) flatten() *flowData {
	d := &flowData{f: f, objs: map[int]Inline{}, dims: map[objKey][2]float64{}, maxW: 1e9, maxH: 1e9}
	styleID := map[Format]int{}
	for idx, it := range f.items() {
		switch it := it.(type) {
		case Chars:
			id, ok := styleID[it.Format]
			if !ok {
				id = len(d.styles)
				styleID[it.Format] = id
				d.styles = append(d.styles, it.Format.resolve(f.Style))
				d.formats = append(d.formats, it.Format)
			}
			for _, r := range it.Text {
				d.runes = append(d.runes, r)
				d.rs = append(d.rs, id)
			}
		case Inline:
			id, ok := styleID[Format{}]
			if !ok {
				id = len(d.styles)
				styleID[Format{}] = id
				d.styles = append(d.styles, Format{}.resolve(f.Style))
				d.formats = append(d.formats, Format{})
			}
			d.objs[len(d.runes)] = it
			d.runes = append(d.runes, objectRune)
			d.rs = append(d.rs, id)
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

// objDims returns the size of the inline object at offset i against the
// current room (maxW x maxH), measuring it once per distinct room.
func (d *flowData) objDims(i int) (w, h float64) {
	k := objKey{i, d.maxW, d.maxH}
	if v, ok := d.dims[k]; ok {
		return v[0], v[1]
	}
	w, h = d.objs[i].Object.Measure(d.f.Painter, d.maxW, d.maxH)
	d.dims[k] = [2]float64{w, h}
	return w, h
}

func (d *flowData) isObj(i int) bool { _, ok := d.objs[i]; return ok }

// measure returns the width of runes[a:b], each rune in its style and
// each inline object at its size.
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
		w += p.MeasureTextWidth(string(d.runes[a:e]))
		a = e
	}
	return w
}

// lineParts is runes[a:b] laid out as one line: styled runs and objects
// positioned from x=0 (objects' Y is relative to the line's top).
type lineParts struct {
	runs          []Run
	objs          []PlacedObject
	width, height float64
	size          float64 // largest font size on the line
	objH          float64 // tallest object
	block         bool    // the line is a block object alone
}

func (d *flowData) partsOf(a, b int) lineParts {
	p := d.f.Painter
	var lp lineParts
	for a < b {
		if in, ok := d.objs[a]; ok {
			w, h := d.objDims(a)
			lp.objs = append(lp.objs, PlacedObject{Object: in.Object, X: lp.width, W: w, H: h, Offset: a})
			lp.width += w
			lp.objH = max(lp.objH, h)
			lp.block = lp.block || in.Block
			a++
			continue
		}
		id, e := d.rs[a], a+1
		for e < b && d.rs[e] == id && !d.isObj(e) {
			e++
		}
		st := d.styles[id]
		p.SetFont(st.Font())
		text := string(d.runes[a:e])
		w := p.MeasureTextWidth(text)
		lp.runs = append(lp.runs, Run{
			Text: text, X: lp.width, W: w, Font: st.Font(), Color: st.Color, Start: a, End: e,
			Underline: st.Underline, Strike: st.Strike, Background: st.Background,
			DY: st.baselineShift(), Size: st.drawSize(),
		})
		lp.width += w
		lp.height = max(lp.height, st.Leading())
		lp.size = max(lp.size, st.FontSize)
		a = e
	}
	return lp
}

// bandRec is a Band being built.
type bandRec struct {
	Band
	registered bool
}

// flowState is where the next line goes. It is a value so a placement
// can be tried and discarded.
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
}

// Layout flows the content and returns where every line goes.
func (f *TextFlow) Layout() Layout {
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

// newBand starts a band using column config cfg on page at top (a
// negative top means the page's top).
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

// fitColumns returns widths made to fit a text area w wide: none means
// one full-width column; widths that, with their gaps, are too wide are
// scaled down proportionally (or collapse to one column if the gaps
// alone don't fit).
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

// enter makes band the current band of the state, at its top.
func (l *layouter) enter(st *flowState, page int, top float64) {
	st.page, st.col = page, 0
	st.band = l.newBand(st.cfg, page, top)
	st.y, st.maxY = st.band.Top, st.band.Top
}

// overflowAt stops layout, reporting the flow didn't fit.
func (l *layouter) overflowAt(offset int) {
	l.out.Overflow, l.out.End, l.stop = true, offset, true
}

func (l *layouter) mark(kind BreakKind, st flowState, offset int) {
	l.out.Breaks = append(l.out.Breaks, BreakMark{Kind: kind, Page: st.page, Col: st.col, Offset: offset})
}

// nextPage moves to the top of the next page, or reports overflow.
func (l *layouter) nextPage(st *flowState) bool {
	if st.page+1 >= l.f.pageCount() {
		return false
	}
	l.enter(st, st.page+1, -1)
	return true
}

// apply performs a ColumnConfig or user Break.
func (l *layouter) apply(op flowOp) {
	st := l.st
	switch op.kind {
	case opColumns: // ColumnConfig: a new band below the text so far
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

// find locates where a line of height lh goes, from st: it moves down
// the column, on to the next column, and on to the next page as the
// text area fills, and skips bands an obstacle leaves too narrow to
// use. It returns the new state, the free x-range, any natural breaks
// made on the way, and false if the pages ran out.
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

// place commits a line (or a blank line, with runs == nil).
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

// segment lays out the text in runes[s:e] (between two ops, or the
// stream's ends) as paragraphs. A blank paragraph is a blank line,
// except that a '\n' ending a segment before an op makes no extra one;
// at the very end of the stream (last) it does, so a caret can sit on
// the empty line after a final newline.
func (l *layouter) segment(s, e int, last bool) {
	if s >= e && !last {
		return
	}
	d := l.d
	base := l.f.Style.Leading()
	for ps := s; ; {
		pe := ps
		for pe < e && d.runes[pe] != '\n' {
			pe++
		}
		if ps == pe {
			if ps == e && !last {
				return
			}
			st, x0, _, marks, ok := l.find(l.st, base, ps)
			if !ok {
				l.overflowAt(ps)
				return
			}
			l.place(st, marks, Line{X: x0, H: base, Start: ps, End: ps, Baseline: st.y + base*0.75})
		}
		for p := ps; p < pe; {
			next, ok := l.line(p, pe)
			if !ok {
				return
			}
			p = next
		}
		if pe == e {
			return
		}
		ps = pe + 1
	}
}

// line places one line starting at rune p of the paragraph ending at pe,
// returning where the next line starts.
func (l *layouter) line(p, pe int) (int, bool) {
	d := l.d
	lh := l.f.Style.Leading()
	for try := 0; ; try++ {
		st, x0, x1, marks, ok := l.find(l.st, lh, p)
		if !ok {
			l.overflowAt(p)
			return 0, false
		}
		_, _, _, ph := l.f.pageRect(st.page)
		d.maxW, d.maxH = x1-x0, ph
		textEnd, next := l.breakLine(p, pe, x1-x0)
		lp := d.partsOf(p, textEnd)
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
			baselineOff = (h-lp.size)/2 + lp.size*0.8
		}
		line := Line{
			Text: string(d.runes[p:textEnd]), X: x0, W: lp.width, H: h, Start: p, End: textEnd, Runs: lp.runs,
			Baseline: st.y + baselineOff,
		}
		for _, o := range lp.objs {
			o.X += x0
			if lp.block {
				o.Y = st.y
			} else {
				o.Y = st.y + baselineOff - o.H
			}
			line.Objects = append(line.Objects, o)
		}
		l.place(st, marks, line)
		return next, true
	}
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
			break
		}
		textEnd, next = j, k
		e = k
	}
	if textEnd > start {
		return textEnd, next
	}
	// A single word wider than the line: break inside it.
	c := start + 1
	for c < end && !d.isObj(c) && d.measure(start, c+1) <= w {
		c++
	}
	return c, c
}

// freeSpan returns the widest stretch of [c0, c1] that no obstacle
// covers in the band y0..y1.
func (f *TextFlow) freeSpan(c0, c1, y0, y1 float64) (float64, float64) {
	type iv struct{ a, b float64 }
	var cut []iv
	for _, o := range f.obstacles {
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

// --- caret queries (used by editable components) --------------------

// CaretRect returns where a caret at text-stream offset goes: its x, the
// top y and the height of its line. ok is false if there is no layout
// to place it in.
func (f *TextFlow) CaretRect(offset int) (x, y, h float64, ok bool) {
	lay := f.Layout()
	if len(lay.Lines) == 0 {
		return 0, 0, 0, false
	}
	idx := len(lay.Lines) - 1
	for i, ln := range lay.Lines {
		if offset <= ln.End {
			idx = i
			break
		}
	}
	ln := lay.Lines[idx]
	offset = min(max(offset, ln.Start), ln.End)
	return ln.X + f.data.measure(ln.Start, offset), ln.Y, ln.H, true
}

// OffsetAt returns the text-stream offset nearest (x, y), for placing a
// caret from a click. With columns it picks the line whose column the
// point is in, so a click in the second column doesn't land in the first.
func (f *TextFlow) OffsetAt(x, y float64) int {
	o, _ := f.offsetAt(x, y)
	return o
}

// offsetAt is OffsetAt that also says whether the point is on a line that
// starts at the offset right after a break (see Cursor.After).
func (f *TextFlow) offsetAt(x, y float64) (offset int, afterBreak bool) {
	lay := f.Layout()
	if len(lay.Lines) == 0 {
		return 0, false
	}
	best, bestScore := 0, 1e18
	for i, c := range lay.Lines {
		dy := 0.0
		switch {
		case y < c.Y:
			dy = c.Y - y
		case y >= c.Y+c.H:
			dy = y - (c.Y + c.H)
		}
		dx := 0.0
		colW := c.ColW
		if colW == 0 {
			colW = c.W
		}
		switch {
		case x < c.ColX:
			dx = c.ColX - x
		case x >= c.ColX+colW:
			dx = x - (c.ColX + colW)
		}
		if score := dy*1000 + dx; score < bestScore {
			best, bestScore = i, score
		}
	}
	ln := lay.Lines[best]
	if x <= ln.X {
		return ln.Start, ln.Start > 0 && f.data.hasOpAt(ln.Start)
	}
	prev := 0.0
	for o := ln.Start + 1; o <= ln.End; o++ {
		w := f.data.measure(ln.Start, o)
		if x < ln.X+(prev+w)/2 {
			return o - 1, o-1 == ln.Start && ln.Start > 0 && f.data.hasOpAt(ln.Start)
		}
		prev = w
	}
	return ln.End, false
}

// --- column grips ----------------------------------------------------

// gripRect returns the draggable grip above gap g of band b: x, y, w, h.
func (f *TextFlow) gripRect(b Band, g int) [4]float64 {
	cx := b.Xs[g] + b.Widths[g] + b.Gap/2
	s := f.ChromeStyle
	return [4]float64{cx - s.GripW/2, b.Top - 2 - s.GripH, s.GripW, s.GripH}
}

// GripAt returns the band and gap index of the grip at (x, y), or
// (-1, -1).
func (f *TextFlow) GripAt(x, y float64) (band, gap int) {
	g := f.gripAt(f.Layout().Bands, x, y)
	return g.band, g.gap
}

func (f *TextFlow) gripAt(bands []Band, x, y float64) grip {
	for bi, b := range bands {
		for g := range len(b.Xs) - 1 {
			if inRect(x, y, f.gripRect(b, g)) {
				return grip{bi, g}
			}
		}
	}
	return noGrip
}

// Dragging reports whether a gap is being dragged.
func (f *TextFlow) Dragging() bool { return f.drag != nil }

// MouseDown starts dragging a gap if (x, y) is on its grip (edit mode).
func (f *TextFlow) MouseDown(x, y float64) bool {
	if f.mode != ModeEdit {
		return f.beginSelect(x, y)
	}
	bands := f.Layout().Bands
	g := f.gripAt(bands, x, y)
	if g == noGrip {
		return f.beginSelect(x, y)
	}
	f.ClearSelection()
	b := bands[g.band]
	f.drag = &flowDrag{
		grip: g, cfg: b.Cfg, widths: slices.Clone(b.Widths), startX: x,
		startL: b.Widths[g.gap], startR: b.Widths[g.gap+1],
	}
	return true
}

// MouseMove drags the gap (keeping the two neighbours' combined width)
// or updates which grip is hovered.
func (f *TextFlow) MouseMove(x, y float64) bool {
	if f.sel.dragging {
		o := f.OffsetAt(x, y)
		changed := o != f.sel.caret
		f.sel.caret, f.sel.after = o, false
		return changed
	}
	if f.mode != ModeEdit {
		return false
	}
	if d := f.drag; d != nil {
		total := d.startL + d.startR
		left := min(max(d.startL+x-d.startX, f.minColumn()), total-f.minColumn())
		if left < f.minColumn() || d.widths[d.grip.gap] == left { // pair narrower than two minimums, or no change
			return false
		}
		d.widths[d.grip.gap], d.widths[d.grip.gap+1] = left, total-left
		f.setConfigWidths(d.cfg, slices.Clone(d.widths))
		if f.OnChange != nil {
			f.OnChange(f)
		}
		return true
	}
	h := f.gripAt(f.Layout().Bands, x, y)
	changed := h != f.hover
	f.hover = h
	return changed
}

// MouseUp ends a drag.
func (f *TextFlow) MouseUp(x, y float64) bool {
	if f.sel.dragging {
		f.sel.dragging = false
		return true
	}
	if f.drag == nil {
		return false
	}
	f.drag = nil
	return true
}

// MouseLeave clears the hovered grip (a drag in progress continues).
func (f *TextFlow) MouseLeave() bool {
	changed := f.hover != noGrip
	f.hover = noGrip
	return changed
}

// CSSCursor implements CursorProvider: col-resize over a grip or while
// dragging.
func (f *TextFlow) CSSCursor() string {
	if f.mode == ModeEdit && (f.drag != nil || f.hover != noGrip) {
		return "col-resize"
	}
	return ""
}

// --- drawing ---------------------------------------------------------

// Draw paints the text (and, for a single page, the page) and, in edit
// mode, the column guides and grips.
func (f *TextFlow) Draw() {
	p, cs := f.Painter, f.ChromeStyle
	if f.Pages == nil && !f.Transparent {
		p.SetFillStyle(cs.PageBG)
		p.FillRect(f.X, f.Y, f.Width, f.Height)
		p.SetStrokeStyle(cs.PageBorder)
		p.SetLineWidth(1)
		p.StrokeRect(f.X+0.5, f.Y+0.5, f.Width-1, f.Height-1)
	}

	lay := f.Layout()
	f.drawSelection(lay)
	defer f.drawCarets()
	p.SetTextAlign("left")
	p.SetTextBaseline("alphabetic")
	for _, ln := range lay.Lines {
		for _, r := range ln.Runs {
			x, y := ln.X+r.X, ln.Baseline+r.DY
			if r.Background != "" {
				p.SetFillStyle(r.Background)
				p.FillRect(x, ln.Y, r.W, ln.H)
			}
			p.SetFont(r.Font)
			p.SetFillStyle(r.Color)
			p.FillText(r.Text, x, y)
			if r.Underline {
				p.FillRect(x, y+1.5, r.W, 1)
			}
			if r.Strike {
				p.FillRect(x, y-r.Size*0.3, r.W, 1)
			}
		}
		for _, o := range ln.Objects {
			o.Object.DrawAt(p, o.X, o.Y)
		}
	}

	if f.mode != ModeEdit {
		return
	}
	for bi, b := range lay.Bands {
		_, py, _, ph := f.pageRect(b.Page)
		for g := range len(b.Xs) - 1 {
			active := f.hover == grip{bi, g} || (f.drag != nil && f.drag.grip == grip{bi, g})
			cx := b.Xs[g] + b.Widths[g] + b.Gap/2
			p.SetFillStyle(cs.GuideColor)
			p.FillRect(cx-0.5, b.Top, 1, py+ph-b.Top)

			r := f.gripRect(b, g)
			if active {
				p.SetFillStyle(cs.GripActiveColor)
			} else {
				p.SetFillStyle(cs.GripColor)
			}
			p.FillRect(r[0], r[1], r[2], r[3])
			p.SetFillStyle("#ffffff")
			for k := range 3 { // three vertical lines: a column grip
				p.FillRect(r[0]+r[2]/2-5+float64(k)*4, r[1]+4, 2, r[3]-8)
			}
		}
	}
}

var _ Component = (*TextFlow)(nil)
