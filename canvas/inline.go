package canvas

import "time"

// InlineObject is a canvas object placed in a TextFlow line.
type InlineObject interface {
	// Measure returns the object's size for the room it has.
	Measure(p Painter, maxW, maxH float64) (w, h float64)
	// DrawAt draws the object with its top-left at (x, y).
	DrawAt(p Painter, x, y float64)
}

type DescentObject interface {
	InlineObject
	Descent() float64
}

type Inline struct {
	Object InlineObject
	Block  bool
}

func (Inline) flowItem() {}

const objectRune = '￼'

// PlacedObject is an inline object laid out on a Line.
type PlacedObject struct {
	Object InlineObject
	X, Y   float64 // absolute top-left
	W, H   float64
	Offset int // text-stream offset
	// Descent is the baseline height above the box bottom.
	Descent float64
}

type Rule struct {
	Color     string
	Thickness float64 // default 1
	w         float64
}

func (r *Rule) Measure(_ Painter, maxW, _ float64) (float64, float64) {
	r.w = maxW
	return maxW, 14
}

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

type TableObject struct {
	View        *TableView
	w, h        float64
	pad, tableW float64
	resize      *tableResize
	Allow       func() bool
	lastEdge    *tableResize
	lastAt      time.Time
}

// NewTableObject wraps t for placing in a flow.
func NewTableObject(t *Table) *TableObject {
	return &TableObject{View: NewTableView(t, nil)}
}

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
	o.View.showSwitch = o.View.Table.HasFormulaOrField()
	g := o.View.layout()
	o.View.drawSelection(g)
	o.View.drawHandles(g)
}

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
	if o.resize != nil && o.Allow != nil && !o.Allow() {
		o.resize, o.lastEdge = nil, nil
		return true
	}
	if d := o.resize; d != nil {
		now := time.Now()
		if o.lastEdge != nil && o.lastEdge.col == d.col && o.lastEdge.index == d.index && now.Sub(o.lastAt) < 500*time.Millisecond {
			if d.col {
				v.Table.SetColumnWidth(d.index, 0)
			} else {
				v.Table.SetRowHeight(d.index, 0)
			}
			o.lastEdge, o.resize = nil, nil
			return true
		}
		o.lastEdge, o.lastAt = d, now
	}
	return o.resize != nil
}

// Select selects a column, a row or the whole table.
func (o *TableObject) Select(sel Selection) {
	if sel.Kind == SelColumn && (sel.Index < 0 || sel.Index >= o.View.Table.Cols()) || sel.Kind == SelRow && (sel.Index < 0 || sel.Index >= o.View.Table.Rows()) {
		sel = Selection{}
	}
	o.View.sel = sel
}

// Selected returns what is selected in the table.
func (o *TableObject) Selected() Selection { return o.View.sel }

func (o *TableObject) gripAt(x, y float64) Selection {
	v := o.View
	hs, gap := v.Style.HandleSize, v.Style.BarGap
	oldX, oldY := v.X, v.Y
	v.X, v.Y = 0, 0
	g := v.layout()
	v.X, v.Y = oldX, oldY
	x, y = x-o.pad, y-o.pad
	switch {
	case x >= -gap-hs && x < -gap && y >= -gap-hs && y < -gap: // the icon in the corner
		return Selection{Kind: SelTable}
	case y >= -gap-hs && y < -gap && x >= 0 && x < g.right():
		return Selection{SelColumn, indexAt(g.colX, x)}
	case x >= -gap-hs && x < -gap && y >= 0 && y < g.bottom():
		return Selection{SelRow, indexAt(g.rowY, y)}
	}
	return Selection{}
}

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

func (o *TableObject) DragEnd() { o.resize = nil }

type tableResize struct {
	col         bool
	index       int
	start, size float64
}

// ClickAt handles a press on the corner switch.
func (o *TableObject) ClickAt(x, y float64) (used, changed bool) {
	v := o.View
	if v == nil || v.Table == nil || v.Painter == nil {
		return false, false
	}
	if r := o.switchRect(); o.View.Table.HasFormulaOrField() && x >= r[0] && x < r[0]+r[2] && y >= r[1] && y < r[1]+r[3] {
		v.SetShowFormulas(!v.ShowFormulas) // a view setting: nothing to save
		return true, false
	}
	if sel := o.gripAt(x, y); sel.Kind != SelNone { // a grip: select what it stands for
		o.Select(sel)
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
	v.sel = Selection{} // a press in the cells ends the selection
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
