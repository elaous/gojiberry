package canvas

// A form field in a table cell works like a formula there: the table has two views (the switch at its top right, the eye
// or f(x)). In the calculated view the cell shows what was filled in and a press fills it in (TableView.OnFieldFill); in
// the formula view the cell shows what the field is (its kind and label) and a press changes it (OnFieldEdit), unless the
// field is locked. The text of the cell is the field's words (label: value), kept up to date by SyncFields, so every
// export, the reading copy and Markdown carry it as text; Markdown has nothing to say that a cell is a field. A formula
// that points at a field cell reads the value that was filled in.

// SetField makes the cell a form field (the owner, for a covered one). The cell's text becomes the field's words.
func (t *Table) SetField(row, col int, f *FormFieldObject) {
	if !t.InRange(row, col) || f == nil {
		return
	}
	row, col = t.Owner(row, col)
	if t.Fields == nil {
		t.Fields = map[CellPos]*FormFieldObject{}
	}
	t.Fields[CellPos{row, col}] = f
	t.syncField(row, col, f)
}

// FieldAt is the field of the cell (of the owner, for a covered one), nil for an ordinary cell.
func (t *Table) FieldAt(row, col int) *FormFieldObject {
	row, col = t.Owner(row, col)
	return t.Fields[CellPos{row, col}]
}

// RemoveField turns the cell back into an ordinary cell holding the field's words as text; it reports whether there was
// a field.
func (t *Table) RemoveField(row, col int) bool {
	row, col = t.Owner(row, col)
	if _, ok := t.Fields[CellPos{row, col}]; !ok {
		return false
	}
	delete(t.Fields, CellPos{row, col})
	return true
}

// FieldList is the table's fields in reading order.
func (t *Table) FieldList() []*FormFieldObject {
	var out []*FormFieldObject
	for r := range t.Cells {
		for c := range t.Cells[r] {
			if f := t.Fields[CellPos{r, c}]; f != nil {
				out = append(out, f)
			}
		}
	}
	return out
}

// SyncFields rewrites the text of every field cell from its field; call it after a field changed.
func (t *Table) SyncFields() {
	for p, f := range t.Fields {
		if t.InRange(p.Row, p.Col) {
			t.syncField(p.Row, p.Col, f)
		}
	}
}

func (t *Table) syncField(row, col int, f *FormFieldObject) {
	words := f.Words()
	if isFormula(words) { // a label such as "=x" must not turn the cell into a formula
		words = "\\" + words
	}
	t.Cells[row][col] = words
}

// valueCells is Cells with the field cells replaced by what was filled in, which is what a formula reads.
func (t *Table) valueCells() [][]string {
	if len(t.Fields) == 0 {
		return t.Cells
	}
	out := make([][]string, len(t.Cells))
	for r := range t.Cells {
		out[r] = append([]string(nil), t.Cells[r]...)
	}
	for p, f := range t.Fields {
		if t.InRange(p.Row, p.Col) {
			out[p.Row][p.Col] = f.Value
			if isFormula(f.Value) { // what a person typed is never a formula
				out[p.Row][p.Col] = "\\" + f.Value
			}
		}
	}
	return out
}

// cloneFields copies the fields (the callbacks are not copied).
func cloneFields(in map[CellPos]*FormFieldObject) map[CellPos]*FormFieldObject {
	if len(in) == 0 {
		return nil
	}
	out := make(map[CellPos]*FormFieldObject, len(in))
	for p, f := range in {
		c := *f
		c.Options = append([]string(nil), f.Options...)
		c.OnFill, c.OnEdit = nil, nil
		out[p] = &c
	}
	return out
}

// HasFormulaOrField says the table has something the switch changes the view of: a formula or a form field.
func (t *Table) HasFormulaOrField() bool {
	if len(t.Fields) > 0 {
		return true
	}
	for _, row := range t.Cells {
		for _, c := range row {
			if isFormula(c) {
				return true
			}
		}
	}
	return false
}

// The switch of a table in the text is a grip like the ones of an editable table (HitFormulaSwitch): beside its top right
// corner, an eye while the values and what was filled in are shown ("see all"), a pen while the formulas and the
// definitions of the fields are ("edit all"). Where the column leaves no room beside the table it overlaps the corner.
func (o *TableObject) switchRect() [4]float64 {
	hs := o.View.Style.HandleSize
	return [4]float64{min(o.tableW+o.View.Style.BarGap, o.w-hs), 0, hs, hs}
}

func (o *TableObject) drawSwitch(p Painter, x, y float64) {
	v := o.View
	r := o.switchRect()
	px, py := x+r[0]+2, y+r[1]+2 // as the grips of an editable table: the pill is the grip less two pixels all round
	pw, ph := r[2]-4, r[3]-4
	v.fillPill(px, py, pw, ph, false, false)
	ink := v.iconColor(false)
	p.SetFillStyle(ink)
	p.SetStrokeStyle(ink)
	p.SetLineWidth(1.2)
	cx, cy := px+pw/2, py+ph/2
	p.BeginPath()
	if v.ShowFormulas { // a pen
		p.MoveTo(cx-5, cy+5)
		p.LineTo(cx-5, cy+2)
		p.LineTo(cx+3, cy-6)
		p.LineTo(cx+6, cy-3)
		p.LineTo(cx-2, cy+5)
		p.LineTo(cx-5, cy+5)
		p.Stroke()
		return
	}
	p.MoveTo(cx-6, cy) // an eye
	p.LineTo(cx-3.5, cy-2.5)
	p.LineTo(cx, cy-3.8)
	p.LineTo(cx+3.5, cy-2.5)
	p.LineTo(cx+6, cy)
	p.LineTo(cx+3.5, cy+2.5)
	p.LineTo(cx, cy+3.8)
	p.LineTo(cx-3.5, cy+2.5)
	p.LineTo(cx-6, cy)
	p.Stroke()
	p.FillRect(cx-1.5, cy-1.5, 3, 3)
}

// ShowTableFormulas puts every table of the flow in the formula view ("edit all": formulas as typed, fields as what
// they are, a press on a field changes it) or the calculated view ("see all"). It is a view setting and is not saved.
func (f *TextFlow) ShowTableFormulas(on bool) {
	for _, o := range f.Tables() {
		o.View.SetShowFormulas(on)
	}
}
