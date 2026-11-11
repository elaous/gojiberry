package canvas

type cellRange struct {
	on                       bool
	anchorR, anchorC, hr, hc int
}

// SelectRange selects the block of cells between two corners.
func (v *TableView) SelectRange(r0, c0, r1, c1 int) {
	t := v.Table
	if !t.InRange(r0, c0) || !t.InRange(r1, c1) {
		return
	}
	v.row, v.col = t.Owner(r0, c0)
	v.everActive = true
	v.rng = cellRange{on: r0 != r1 || c0 != c1, anchorR: r0, anchorC: c0, hr: r1, hc: c1}
	v.sel = Selection{}
}

// Range returns the block the commands act on.
func (v *TableView) Range() (r0, c0, r1, c1 int, ok bool) {
	t := v.Table
	if !v.rng.on {
		if !v.everActive || !t.InRange(v.row, v.col) {
			return 0, 0, 0, 0, false
		}
		r, c := t.Owner(v.row, v.col)
		rs, cs := t.Span(r, c)
		return r, c, r + rs - 1, c + cs - 1, true
	}
	r0, r1 = min(v.rng.anchorR, v.rng.hr), max(v.rng.anchorR, v.rng.hr)
	c0, c1 = min(v.rng.anchorC, v.rng.hc), max(v.rng.anchorC, v.rng.hc)
	for grew := true; grew; { // include whole merges
		grew = false
		for _, m := range t.Merges {
			if m.Row+m.Rows-1 < r0 || m.Row > r1 || m.Col+m.Cols-1 < c0 || m.Col > c1 {
				continue
			}
			nr0, nc0 := min(r0, m.Row), min(c0, m.Col)
			nr1, nc1 := max(r1, m.Row+m.Rows-1), max(c1, m.Col+m.Cols-1)
			if nr0 != r0 || nc0 != c0 || nr1 != r1 || nc1 != c1 {
				r0, c0, r1, c1, grew = nr0, nc0, nr1, nc1, true
			}
		}
	}
	return r0, c0, r1, c1, true
}

func (v *TableView) extendRange(dr, dc int) {
	t := v.Table
	if !v.rng.on {
		v.rng = cellRange{on: true, anchorR: v.row, anchorC: v.col, hr: v.row, hc: v.col}
	}
	v.rng.hr = min(max(v.rng.hr+dr, 0), t.Rows()-1)
	v.rng.hc = min(max(v.rng.hc+dc, 0), t.Cols()-1)
	if v.rng.hr == v.rng.anchorR && v.rng.hc == v.rng.anchorC {
		v.rng.on = false
	}
}

// MergeSelection merges the selected block into one cell.
func (v *TableView) MergeSelection() bool {
	r0, c0, r1, c1, ok := v.Range()
	if !ok || !v.Table.MergeRange(r0, c0, r1, c1) {
		return false
	}
	v.row, v.col = r0, c0
	v.rng = cellRange{}
	v.changed()
	return true
}

// SplitSelection splits the merged cell holding the active cell.
func (v *TableView) SplitSelection() bool {
	r0, c0, _, _, ok := v.Range()
	if !ok || !v.Table.Unmerge(r0, c0) {
		return false
	}
	v.rng = cellRange{}
	v.changed()
	return true
}

// StyleSelection styles every cell in the selected block.
func (v *TableView) StyleSelection(fn func(*CellStyle)) bool {
	r0, c0, r1, c1, ok := v.Range()
	if !ok {
		return false
	}
	for r := r0; r <= r1; r++ {
		for c := c0; c <= c1; c++ {
			if v.Table.Covered(r, c) {
				continue
			}
			st := v.Table.StyleAt(r, c)
			fn(&st)
			v.Table.SetCellStyle(r, c, st)
		}
	}
	v.changed()
	return true
}

// MouseDownMods: Shift+click extends the selection.
func (v *TableView) MouseDownMods(x, y float64, m Mods) bool {
	if v.mode == ModeEdit && m.Shift && v.editing && v.menu != nil && !v.menu.IsOpen() {
		if h := v.HitTest(x, y); h.Kind == HitCell {
			v.rng = cellRange{on: true, anchorR: v.row, anchorC: v.col, hr: h.Row, hc: h.Col}
			if h.Row == v.row && h.Col == v.col {
				v.rng.on = false
			}
			v.sel = Selection{}
			return true
		}
	}
	return v.MouseDown(x, y)
}

func (v *TableView) drawRange(g geometry) {
	if !v.rng.on {
		return
	}
	r0, c0, r1, c1, ok := v.Range()
	if !ok {
		return
	}
	x0, y0, _, _ := g.cellRect(v.Table, r0, c0)
	x1, y1, w1, h1 := g.cellRect(v.Table, r1, c1)
	p, s := v.Painter, v.Style
	p.SetFillStyle(s.SelectColor)
	p.FillRect(x0, y0, x1+w1-x0, y1+h1-y0)
	p.SetStrokeStyle(s.ActiveColor)
	p.SetLineWidth(2)
	p.StrokeRect(x0+1, y0+1, x1+w1-x0-2, y1+h1-y0-2)
}

func (v *TableView) cellMenu() []MenuItem {
	r0, c0, r1, c1, ok := v.Range()
	merged := false
	if ok {
		_, merged = v.Table.MergeAt(r0, c0)
	}
	canMerge := ok && (r1 > r0 || c1 > c0)
	style := func(label string, fn func(*CellStyle)) MenuItem {
		return MenuItem{Label: label, Disabled: !ok, Action: func() { v.StyleSelection(fn); v.sel = Selection{} }}
	}
	var cur CellStyle
	if ok {
		cur = v.Table.StyleAt(r0, c0)
	}
	bg := func(label, css string) MenuItem {
		return style(check(cur.Background == css && css != "", label), func(st *CellStyle) { st.Background = css })
	}
	return []MenuItem{
		{Label: "Merge cells", Disabled: !canMerge, Action: func() { v.MergeSelection(); v.sel = Selection{} }},
		{Label: "Split cell", Disabled: !merged, Action: func() { v.SplitSelection(); v.sel = Selection{} }},
		{Separator: true},
		style(check(cur.Bold, "Cell bold"), func(st *CellStyle) { st.Bold = !cur.Bold }),
		style(check(cur.Italic, "Cell italic"), func(st *CellStyle) { st.Italic = !cur.Italic }),
		style(check(cur.Align == AlignLeft, "Cell align left"), func(st *CellStyle) { st.Align = AlignLeft }),
		style(check(cur.Align == AlignCenter, "Cell align centre"), func(st *CellStyle) { st.Align = AlignCenter }),
		style(check(cur.Align == AlignRight, "Cell align right"), func(st *CellStyle) { st.Align = AlignRight }),
		bg("Cell background yellow", "#fff3b0"),
		bg("Cell background green", "#c8f0d0"),
		bg("Cell background blue", "#cfe3ff"),
		bg("Cell background red", "#ffd0d0"),
		style("Cell background none", func(st *CellStyle) { st.Background = "" }),
	}
}
