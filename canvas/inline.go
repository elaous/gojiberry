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
}

// NewTableObject wraps t for placing in a flow with Inline{Block: true}.
func NewTableObject(t *Table) *TableObject {
	return &TableObject{View: NewTableView(t, nil)}
}

// Measure implements InlineObject.
func (o *TableObject) Measure(p Painter, maxW, maxH float64) (float64, float64) {
	o.View.Painter = p
	o.View.X, o.View.Y = 0, 0
	edges, rows := o.View.ColumnEdges(), o.View.RowEdges()
	o.w = min(edges[len(edges)-1], maxW)
	o.h = min(rows[len(rows)-1]+6, maxH) // a little air below the table
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
	o.View.X, o.View.Y = x, y
	o.View.Draw()
	if o.View.Table.HasFormulaOrField() {
		o.drawSwitch(p, x, y)
	}
}

// ClickAt implements ClickObject: a press on the switch at the top right changes the view, and a press on a cell that is
// a form field fills it in (a checkbox is ticked) or, in the formula view, changes it. The table is read-only in the
// text otherwise, so a press anywhere else is not used.
func (o *TableObject) ClickAt(x, y float64) (used, changed bool) {
	v := o.View
	if v == nil || v.Table == nil || v.Painter == nil || !v.Table.HasFormulaOrField() {
		return false, false
	}
	if r := o.switchRect(); x >= r[0] && x < r[0]+r[2] && y >= r[1] && y < r[1]+r[3] {
		v.SetShowFormulas(!v.ShowFormulas) // a view setting: nothing to save
		return true, false
	}
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
	_ InlineObject = (*Rule)(nil)
	_ InlineObject = (*TableObject)(nil)
)
