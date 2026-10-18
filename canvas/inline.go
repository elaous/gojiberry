package canvas

// InlineObject is a canvas object that sits in a TextFlow's line of
// text like a (very large) character: an Image in inline mode, a table
// from the Markdown transform, a horizontal rule.
type InlineObject interface {
	// Measure returns the size the object wants given the room it has:
	// at most maxW wide and maxH high (the column and the page's text
	// area). An object that can scale or clip itself to fit does so;
	// this is how an inline image "tries to fit the image inside the
	// page borders". p measures text.
	Measure(p Painter, maxW, maxH float64) (w, h float64)
	// DrawAt draws the object with its top-left corner at (x, y), at
	// the size Measure last returned.
	DrawAt(p Painter, x, y float64)
}

// DescentObject is an InlineObject with a baseline of its own inside its box: Descent is how far the baseline is above
// the bottom of the box (after Measure). A non-block object sits on the line's baseline; with Descent the object's
// baseline is on it instead, so a formula with a subscript or a fraction lines up with the words round it.
type DescentObject interface {
	InlineObject
	Descent() float64
}

// Inline is a TextFlow item that places an InlineObject in the text.
// It takes one position in the text stream (so offsets and carets
// still line up). Block objects get a line to themselves (a table, a
// rule); others share a line with the text around them, sitting on the
// baseline and making the line taller if need be.
type Inline struct {
	Object InlineObject
	Block  bool
}

func (Inline) flowItem() {}

// objectRune stands for an Inline object in the text stream.
const objectRune = '￼'

// PlacedObject is an inline object laid out on a Line.
type PlacedObject struct {
	Object InlineObject
	X, Y   float64 // absolute top-left
	W, H   float64
	Offset int // text-stream offset
	// Descent is how far the object's own baseline is above its bottom (a DescentObject); zero for the others.
	Descent float64
}

// Rule is a horizontal rule, a block InlineObject spanning the column.
// Use it as *Rule: it remembers the width it was measured at.
type Rule struct {
	Color     string
	Thickness float64 // default 1
	w         float64
}

// Measure implements InlineObject.
func (r *Rule) Measure(_ Painter, maxW, _ float64) (float64, float64) {
	r.w = maxW
	return maxW, 14
}

// DrawAt implements InlineObject.
func (r *Rule) DrawAt(p Painter, x, y float64) {
	color, th := r.Color, r.Thickness
	if color == "" {
		color = "#c4c7cd"
	}
	if th <= 0 {
		th = 1
	}
	p.SetFillStyle(color)
	p.FillRect(x, y+7-th/2, r.w, th)
}

// TableObject is a Table drawn read-only in the text, as a block
// InlineObject. A table wider than its column (or taller than the
// page) is clipped to it.
type TableObject struct {
	View *TableView
	w, h float64
	// pad is the room around a table with a formula or a form field, above and at both sides, where the grips of its
	// columns, rows and the eye/pen switch are drawn (the same grips as an editable table's); tableW is the table's own width.
	pad, tableW float64
	resize      *tableResize
}

// NewTableObject wraps t for placing in a flow with Inline{Block: true}.
func NewTableObject(t *Table) *TableObject {
	return &TableObject{View: NewTableView(t, nil)}
}

// Measure implements InlineObject.
func (o *TableObject) Measure(p Painter, maxW, maxH float64) (float64, float64) {
	o.View.Painter = p
	o.View.X, o.View.Y = 0, 0
	o.pad = o.View.Style.HandleSize + o.View.Style.BarGap // room for the grips, on every table
	edges, rows := o.View.ColumnEdges(), o.View.RowEdges()
	o.tableW = min(edges[len(edges)-1], maxW-2*o.pad)
	o.w = o.tableW + 2*o.pad
	o.h = min(o.pad+rows[len(rows)-1]+6, maxH) // a little air below the table
	return o.w, o.h
}

// DrawAt implements InlineObject.
func (o *TableObject) DrawAt(p Painter, x, y float64) {
	o.View.Painter = p
	o.View.menu.Painter = p
	if c, ok := p.(interface {
		Save()
		Restore()
		ClipRect(x, y, w, h float64)
	}); ok {
		c.Save()
		c.ClipRect(x, y, o.w, o.h)
		defer c.Restore()
	}
	o.View.X, o.View.Y = x+o.pad, y+o.pad
	o.View.Draw()
	o.View.penSwitch = true
	o.View.showSwitch = o.View.Table.HasFormulaOrField() // the eye/pen only where there is something to see or edit
	o.View.drawHandles(o.View.layout())
}

// DragStart implements DragObject: a press on a column grip near the edge between two columns (or at the right edge of
// the last), or on a row grip near the edge under a row, starts a resize of that column or row.
func (o *TableObject) DragStart(x, y float64) bool {
	v := o.View
	if v == nil || v.Table == nil || v.Painter == nil {
		return false
	}
	x, y = x-o.pad, y-o.pad
	hs, gap := v.Style.HandleSize, v.Style.BarGap
	oldX, oldY := v.X, v.Y
	v.X, v.Y = 0, 0
	g := v.layout()
	v.X, v.Y = oldX, oldY
	const near = 4.0
	o.resize = nil
	if y >= -gap-hs && y < -gap+near { // the grips above the columns
		for c := 0; c < v.Table.Cols(); c++ {
			if abs(x-g.colX[c+1]) <= near {
				o.resize = &tableResize{col: true, index: c, start: x, size: g.colX[c+1] - g.colX[c]}
			}
		}
	}
	if o.resize == nil && x >= -gap-hs && x < -gap+near { // the grips left of the rows
		for r := 0; r < v.Table.Rows(); r++ {
			if abs(y-g.rowY[r+1]) <= near {
				o.resize = &tableResize{index: r, start: y, size: g.rowY[r+1] - g.rowY[r]}
			}
		}
	}
	return o.resize != nil
}

// DragMove implements DragObject.
func (o *TableObject) DragMove(x, y float64) bool {
	d := o.resize
	if d == nil {
		return false
	}
	t := o.View.Table
	if d.col {
		w := d.size + (x - o.pad) - d.start
		before := t.ColumnWidth(d.index)
		t.SetColumnWidth(d.index, w)
		return t.ColumnWidth(d.index) != before
	}
	h := d.size + (y - o.pad) - d.start
	before := t.RowHeightSet(d.index)
	t.SetRowHeight(d.index, h)
	return t.RowHeightSet(d.index) != before
}

// DragEnd implements DragObject.
func (o *TableObject) DragEnd() { o.resize = nil }

// tableResize is a column or row being resized: where the press was and how big it was then.
type tableResize struct {
	col         bool
	index       int
	start, size float64
}

// ClickAt implements ClickObject: a press on the switch beside the top right corner changes the view, and a press on a
// cell that is a form field fills it in (a checkbox is ticked) or, in the formula view, changes it. The table is
// read-only in the text otherwise, so a press anywhere else is not used.
func (o *TableObject) ClickAt(x, y float64) (used, changed bool) {
	v := o.View
	if v == nil || v.Table == nil || v.Painter == nil {
		return false, false
	}
	if r := o.switchRect(); o.View.Table.HasFormulaOrField() && x >= r[0] && x < r[0]+r[2] && y >= r[1] && y < r[1]+r[3] {
		v.SetShowFormulas(!v.ShowFormulas) // a view setting: nothing to save
		return true, false
	}
	x, y = x-o.pad, y-o.pad
	oldX, oldY := v.X, v.Y
	v.X, v.Y = 0, 0
	g := v.layout()
	v.X, v.Y = oldX, oldY
	if x < 0 || y < 0 || x >= g.right() || y >= g.bottom() {
		return false, false
	}
	row, col := v.Table.Owner(indexAt(g.rowY, y), indexAt(g.colX, x))
	f := v.Table.Fields[CellPos{row, col}]
	if f == nil {
		return false, false
	}
	before := f.Value
	v.pressField(row, col, f)
	return true, f.Value != before
}

var (
	_ ClickObject  = (*TableObject)(nil)
	_ DragObject   = (*TableObject)(nil)
	_ InlineObject = (*Rule)(nil)
	_ InlineObject = (*TableObject)(nil)
)
