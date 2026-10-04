package canvas

import "math"

// selectHandle selects what the clicked handle stands for and opens its
// context menu just below/right of the handle.
func (v *TableView) selectHandle(h Hit) {
	v.editing = false
	g := v.layout()
	s := v.Style
	var items []MenuItem
	var mx, my float64
	switch h.Kind {
	case HitColumnHandle:
		v.sel = Selection{SelColumn, h.Col}
		items = v.columnMenu(h.Col)
		mx, my = g.colX[h.Col], v.Y-s.BarGap
	case HitRowHandle:
		v.sel = Selection{SelRow, h.Row}
		items = v.rowMenu(h.Row)
		mx, my = v.X-s.BarGap, g.rowY[h.Row]
	default:
		v.sel = Selection{Kind: SelTable}
		items = v.tableMenu()
		mx, my = v.X-s.BarGap-s.HandleSize, v.Y-s.BarGap
	}
	bounds := v.viewport
	if bounds[2] == 0 {
		bounds = [4]float64{0, 0, v.CanvasW, v.CanvasH}
	}
	v.menu.OpenWithin(items, mx, my, bounds)
}

// apply wraps a table mutation from a menu item: it then fires OnChange.
func (v *TableView) apply(fn func(t *Table)) func() {
	return func() {
		fn(v.Table)
		v.editing = false
		v.changed()
	}
}

func (v *TableView) columnMenu(c int) []MenuItem {
	last := v.Table.Cols() - 1
	sel := func(i int) func(*Table) { return func(*Table) { v.sel = Selection{SelColumn, i} } }
	return []MenuItem{
		{Label: "Insert column left", Action: v.apply(func(t *Table) { t.InsertColumn(c); v.sel = Selection{} })},
		{Label: "Insert column right", Action: v.apply(func(t *Table) { t.InsertColumn(c + 1); v.sel = Selection{} })},
		{Separator: true},
		{Label: "Move column left", Disabled: c == 0, Action: v.apply(func(t *Table) { t.MoveColumn(c, c-1); sel(c - 1)(t) })},
		{Label: "Move column right", Disabled: c == last, Action: v.apply(func(t *Table) { t.MoveColumn(c, c+1); sel(c + 1)(t) })},
		{Separator: true},
		{Label: "Sort A to Z", Action: v.apply(func(t *Table) { t.SortByColumn(c, false) })},
		{Label: "Sort Z to A", Action: v.apply(func(t *Table) { t.SortByColumn(c, true) })},
		{Separator: true},
		{Label: "Delete column", Danger: true, Action: v.apply(func(t *Table) { t.DeleteColumn(c); v.sel = Selection{} })},
	}
}

func (v *TableView) rowMenu(r int) []MenuItem {
	last := v.Table.Rows() - 1
	header := r == 0 // the header can't move, gain a row above it, or be deleted
	sel := func(i int) { v.sel = Selection{SelRow, i} }
	return []MenuItem{
		{Label: "Insert row above", Disabled: header, Action: v.apply(func(t *Table) { t.InsertRow(r); v.sel = Selection{} })},
		{Label: "Insert row below", Action: v.apply(func(t *Table) { t.InsertRow(r + 1); v.sel = Selection{} })},
		{Separator: true},
		{Label: "Move row up", Disabled: r <= 1, Action: v.apply(func(t *Table) { t.MoveRow(r, r-1); sel(r - 1) })},
		{Label: "Move row down", Disabled: header || r == last, Action: v.apply(func(t *Table) { t.MoveRow(r, r+1); sel(r + 1) })},
		{Label: "Duplicate row", Disabled: header, Action: v.apply(func(t *Table) { t.DuplicateRow(r); v.sel = Selection{} })},
		{Separator: true},
		{Label: "Delete row", Danger: true, Disabled: header, Action: v.apply(func(t *Table) { t.DeleteRow(r); v.sel = Selection{} })},
	}
}

func (v *TableView) tableMenu() []MenuItem {
	items := []MenuItem{
		{Label: "Add row", Action: v.apply(func(t *Table) { t.AddRow(); v.sel = Selection{} })},
		{Label: "Add column", Action: v.apply(func(t *Table) { t.AddColumn(); v.sel = Selection{} })},
		{Separator: true},
		{Label: "Clear contents", Action: v.apply(func(t *Table) { t.Clear() })},
		{Separator: true},
		{Label: check(v.Wrap, "Wrap text around"), Disabled: v.flow == nil, Action: func() { v.Wrap = !v.Wrap; v.sel = Selection{}; v.changed() }},
	}
	items = append(items, MenuItem{Separator: true})
	items = append(items, v.cellMenu()...)
	if v.OnDeleteTable != nil {
		items = append(items,
			MenuItem{Separator: true},
			MenuItem{Label: "Delete table", Danger: true, Action: func() { v.sel = Selection{}; v.OnDeleteTable() }})
	}
	return items
}

// drawSelection tints and outlines the selected cells.
func (v *TableView) drawSelection(g geometry) {
	x0, y0, x1, y1 := v.X, v.Y, g.right(), g.bottom()
	switch v.sel.Kind {
	case SelNone:
		return
	case SelColumn:
		x0, x1 = g.colX[v.sel.Index], g.colX[v.sel.Index+1]
	case SelRow:
		y0, y1 = g.rowY[v.sel.Index], g.rowY[v.sel.Index+1]
	}
	p, s := v.Painter, v.Style
	p.SetFillStyle(s.SelectColor)
	p.FillRect(x0, y0, x1-x0, y1-y0)
	p.SetStrokeStyle(s.ActiveColor)
	p.SetLineWidth(2)
	p.StrokeRect(x0+1, y0+1, x1-x0-2, y1-y0-2)
}

// drawHandles paints the grip above each column, left of each row, and
// the table icon in the corner.
func (v *TableView) drawHandles(g geometry) {
	s := v.Style
	hs, gap := s.HandleSize, s.BarGap
	for c := range v.Table.Cols() {
		v.drawHandle(g.colX[c], v.Y-gap-hs, g.colX[c+1]-g.colX[c], hs,
			v.hover == Hit{Kind: HitColumnHandle, Col: c}, v.sel == Selection{SelColumn, c})
	}
	for r := range v.Table.Rows() {
		v.drawHandle(v.X-gap-hs, g.rowY[r], hs, g.rowY[r+1]-g.rowY[r],
			v.hover == Hit{Kind: HitRowHandle, Row: r}, v.sel == Selection{SelRow, r})
	}

	// Table handle: a pill with the 2x2 boxes.
	p := v.Painter
	x, y := v.X-gap-hs, v.Y-gap-hs
	px, py, pw, ph := x+2, y+2, hs-4, hs-4
	v.fillPill(px, py, pw, ph, v.hover.Kind == HitTableHandle, v.sel.Kind == SelTable)
	box, sp := 3.0, 2.0
	bx, by := px+(pw-2*box-sp)/2, py+(ph-2*box-sp)/2
	p.SetFillStyle(v.iconColor(v.sel.Kind == SelTable))
	for i := range 4 {
		p.FillRect(bx+float64(i%2)*(box+sp), by+float64(i/2)*(box+sp), box, box)
	}
}

func (v *TableView) iconColor(selected bool) string {
	if selected {
		return "#ffffff"
	}
	return v.Style.HandleIconColor
}

// fillPill fills a rounded pill (a rectangle with its corners cut by
// the painter's rectangles, radius 4): the grip look of Obsidian's tables.
func (v *TableView) fillPill(x, y, w, h float64, hovered, selected bool) {
	p, s := v.Painter, v.Style
	switch {
	case selected:
		p.SetFillStyle(s.ActiveColor)
	case hovered:
		p.SetFillStyle(s.BarHoverColor)
	default:
		p.SetFillStyle(s.BarColor)
	}
	const r = 4
	if w < 2*r || h < 2*r {
		p.FillRect(x, y, w, h)
		return
	}
	for k := range r {
		d := float64(r) - float64(k) - 0.5
		inset := float64(r) - math.Sqrt(float64(r*r)-d*d)
		p.FillRect(x+inset, y+float64(k), w-2*inset, 1)
		p.FillRect(x+inset, y+h-float64(k)-1, w-2*inset, 1)
	}
	p.FillRect(x, y+r, w, h-2*r)
}

// drawHandle paints one column or row handle: a small pill centered on
// the edge, with six grip dots (three across for a column, two for a row).
func (v *TableView) drawHandle(x, y, w, h float64, hovered, selected bool) {
	column := w > h
	pw, ph := 26.0, h-4
	if !column {
		pw, ph = w-4, 26.0
	}
	pw, ph = min(pw, w-4), min(ph, h-4)
	px, py := x+(w-pw)/2, y+(h-ph)/2
	v.fillPill(px, py, pw, ph, hovered, selected)
	v.Painter.SetFillStyle(v.iconColor(selected))
	const dot, gap = 2.0, 3.0
	across, down := 3, 2
	if !column {
		across, down = 2, 3
	}
	gw, gh := float64(across)*dot+float64(across-1)*gap, float64(down)*dot+float64(down-1)*gap
	gx, gy := px+(pw-gw)/2, py+(ph-gh)/2
	for i := range across {
		for j := range down {
			v.Painter.FillRect(gx+float64(i)*(dot+gap), gy+float64(j)*(dot+gap), dot, dot)
		}
	}
}
