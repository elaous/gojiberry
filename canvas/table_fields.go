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
