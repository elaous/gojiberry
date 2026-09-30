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
	GuideColor      string // dashed-look line down each column gap
	GripColor       string
	GripActiveColor string
	GripW, GripH    float64 // size of the draggable grip above each gap
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
	}
}

// Line is one laid-out line of text. Start/End are rune offsets into
// TextFlow.Text (End exclusive, trailing spaces excluded), which is
// what later per-character formatting will hang off.
type Line struct {
	Text       string
	X, Y, W    float64 // left edge, top of the line box, measured text width
	Col        int
	Start, End int
}

// Layout is the result of flowing text into a page.
type Layout struct {
	Lines []Line
	// Overflow is true if the text didn't all fit; End is the rune
	// offset where layout stopped (len(text) when it all fit).
	Overflow bool
	End      int
}

// TextFlow lays out a string into a page of Width x Height with its
// top-left corner at (X, Y), word-wrapping it using the Style's font
// metrics, optionally in several columns, and optionally around
// Obstacles such as a wrapped Image.
//
// Columns are given as an array of widths separated by Gap. In edit
// mode a grip above each gap can be dragged with the mouse to resize
// the two neighbouring columns (their combined width is preserved).
// View mode just draws the text.
type TextFlow struct {
	Text          string
	Style         TextStyle
	X, Y          float64
	Width, Height float64
	// Columns are the column widths, left to right. Empty means one
	// column spanning Width. Use SetColumnCount for equal columns.
	Columns []float64
	Gap     float64
	// MinColumnWidth stops a drag from collapsing a column (default 40).
	MinColumnWidth float64

	ChromeStyle FlowStyle
	Painter     Painter

	// OnChange is called when the columns are resized by dragging.
	OnChange func(*TextFlow)

	obstacles []Obstacle
	mode      Mode
	hover     int // gap index under the pointer, or -1
	drag      *flowDrag
}

type flowDrag struct {
	gap            int
	startX         float64
	startL, startR float64
}

// NewTextFlow returns a flow of text in a width x height page at (x, y),
// with DefaultTextStyle, one column, in ModeView.
func NewTextFlow(text string, x, y, width, height float64) *TextFlow {
	return &TextFlow{
		Text: text, Style: DefaultTextStyle(), X: x, Y: y, Width: width, Height: height,
		ChromeStyle: DefaultFlowStyle(), hover: -1,
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
		f.drag, f.hover = nil, -1
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

// SetColumnCount splits the page into n equal columns (minimum 1)
// separated by Gap.
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

// columns returns the effective column widths.
func (f *TextFlow) columns() []float64 {
	if len(f.Columns) == 0 {
		return []float64{f.Width}
	}
	return f.Columns
}

// columnX returns the left edge of each column.
func (f *TextFlow) columnX() []float64 {
	cols := f.columns()
	xs := make([]float64, len(cols))
	x := f.X
	for i, w := range cols {
		xs[i] = x
		x += w + f.Gap
	}
	return xs
}

// Layout flows the text and returns where every line goes.
func (f *TextFlow) Layout() Layout {
	lh := f.Style.Leading()
	cols, xs := f.columns(), f.columnX()
	runes := []rune(f.Text)
	p := f.Painter
	p.SetFont(f.Style.Font())
	minSeg := 2 * max(f.Style.FontSize, 1)

	col, y := 0, f.Y
	// nextSegment finds the next band of free space to put a line in,
	// advancing down the column (and on to the next column) past
	// bands the obstacles leave too narrow to use.
	nextSegment := func() (x0, x1 float64, ok bool) {
		for {
			if y+lh > f.Y+f.Height+1e-9 {
				col++
				y = f.Y
			}
			if col >= len(cols) {
				return 0, 0, false
			}
			x0, x1 = f.freeSpan(xs[col], xs[col]+cols[col], y, y+lh)
			if x1-x0 >= min(minSeg, cols[col]) {
				return x0, x1, true
			}
			y += lh
		}
	}

	var out Layout
	pos := 0
	for pos <= len(runes) {
		end := pos
		for end < len(runes) && runes[end] != '\n' {
			end++
		}
		// Paragraph is runes[pos:end].
		if end == pos {
			if _, _, ok := nextSegment(); !ok {
				out.Overflow, out.End = true, pos
				return out
			}
			y += lh // blank line
		}
		for s := pos; s < end; {
			x0, x1, ok := nextSegment()
			if !ok {
				out.Overflow, out.End = true, s
				return out
			}
			textEnd, next := f.breakLine(runes, s, end, x1-x0)
			text := string(runes[s:textEnd])
			out.Lines = append(out.Lines, Line{
				Text: text, X: x0, Y: y, W: p.MeasureTextWidth(text),
				Col: col, Start: s, End: textEnd,
			})
			y += lh
			s = next
		}
		pos = end + 1
	}
	out.End = len(runes)
	return out
}

// breakLine picks how much of runes[start:end] fits in width w: it
// returns where the line's text ends (trailing spaces excluded) and
// where the next line starts (skipping the spaces).
func (f *TextFlow) breakLine(runes []rune, start, end int, w float64) (textEnd, next int) {
	p := f.Painter
	textEnd, next = -1, start
	for e := start; e < end; {
		j := e
		for j < end && runes[j] != ' ' {
			j++
		}
		k := j
		for k < end && runes[k] == ' ' {
			k++
		}
		if j > start && p.MeasureTextWidth(string(runes[start:j])) > w {
			break
		}
		textEnd, next = j, k
		e = k
	}
	if textEnd >= 0 && textEnd > start {
		return textEnd, next
	}
	// A single word wider than the line: break inside it.
	c := start + 1
	for c < end && p.MeasureTextWidth(string(runes[start:c+1])) <= w {
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

// gripRect returns the draggable grip above gap i (between column i and
// i+1): x, y, w, h.
func (f *TextFlow) gripRect(i int) [4]float64 {
	cols, xs := f.columns(), f.columnX()
	cx := xs[i] + cols[i] + f.Gap/2
	s := f.ChromeStyle
	return [4]float64{cx - s.GripW/2, f.Y - 2 - s.GripH, s.GripW, s.GripH}
}

// gapCount is the number of draggable gaps.
func (f *TextFlow) gapCount() int { return len(f.columns()) - 1 }

// GripAt returns the gap whose grip is at (x, y), or -1.
func (f *TextFlow) GripAt(x, y float64) int {
	for i := range f.gapCount() {
		if inRect(x, y, f.gripRect(i)) {
			return i
		}
	}
	return -1
}

// Dragging reports whether a gap is being dragged.
func (f *TextFlow) Dragging() bool { return f.drag != nil }

// MouseDown starts dragging a gap if (x, y) is on its grip (edit mode).
func (f *TextFlow) MouseDown(x, y float64) bool {
	if f.mode != ModeEdit {
		return false
	}
	i := f.GripAt(x, y)
	if i < 0 {
		return false
	}
	cols := f.columns()
	f.Columns = slices.Clone(cols) // own the slice before mutating it
	f.drag = &flowDrag{gap: i, startX: x, startL: cols[i], startR: cols[i+1]}
	return true
}

// MouseMove drags the gap (keeping the two neighbours' combined width)
// or updates which grip is hovered.
func (f *TextFlow) MouseMove(x, y float64) bool {
	if f.mode != ModeEdit {
		return false
	}
	if d := f.drag; d != nil {
		total := d.startL + d.startR
		left := min(max(d.startL+x-d.startX, f.minColumn()), total-f.minColumn())
		if left < f.minColumn() { // the pair is narrower than two minimums: leave it
			return false
		}
		if f.Columns[d.gap] == left {
			return false
		}
		f.Columns[d.gap], f.Columns[d.gap+1] = left, total-left
		if f.OnChange != nil {
			f.OnChange(f)
		}
		return true
	}
	h := f.GripAt(x, y)
	changed := h != f.hover
	f.hover = h
	return changed
}

// MouseUp ends a drag.
func (f *TextFlow) MouseUp(x, y float64) bool {
	if f.drag == nil {
		return false
	}
	f.drag = nil
	return true
}

// MouseLeave clears the hovered grip (a drag in progress continues).
func (f *TextFlow) MouseLeave() bool {
	changed := f.hover != -1
	f.hover = -1
	return changed
}

// CSSCursor implements CursorProvider: col-resize over a grip or while
// dragging.
func (f *TextFlow) CSSCursor() string {
	if f.mode == ModeEdit && (f.drag != nil || f.hover >= 0) {
		return "col-resize"
	}
	return ""
}

// Draw paints the page, the text and (in edit mode) the column guides
// and grips.
func (f *TextFlow) Draw() {
	p, cs := f.Painter, f.ChromeStyle
	p.SetFillStyle(cs.PageBG)
	p.FillRect(f.X, f.Y, f.Width, f.Height)
	p.SetStrokeStyle(cs.PageBorder)
	p.SetLineWidth(1)
	p.StrokeRect(f.X+0.5, f.Y+0.5, f.Width-1, f.Height-1)

	lay := f.Layout()
	p.SetFont(f.Style.Font())
	p.SetTextAlign("left")
	p.SetTextBaseline("middle")
	p.SetFillStyle(f.Style.Color)
	lh := f.Style.Leading()
	for _, ln := range lay.Lines {
		p.FillText(ln.Text, ln.X, ln.Y+lh/2)
	}

	if f.mode != ModeEdit {
		return
	}
	cols, xs := f.columns(), f.columnX()
	for i := range f.gapCount() {
		active := f.hover == i || (f.drag != nil && f.drag.gap == i)
		cx := xs[i] + cols[i] + f.Gap/2
		p.SetFillStyle(cs.GuideColor)
		p.FillRect(cx-0.5, f.Y, 1, f.Height)

		g := f.gripRect(i)
		if active {
			p.SetFillStyle(cs.GripActiveColor)
		} else {
			p.SetFillStyle(cs.GripColor)
		}
		p.FillRect(g[0], g[1], g[2], g[3])
		p.SetFillStyle("#ffffff")
		for k := range 3 { // three vertical lines: a column grip
			p.FillRect(g[0]+g[2]/2-5+float64(k)*4, g[1]+4, 2, g[3]-8)
		}
	}
}

var _ Component = (*TextFlow)(nil)
