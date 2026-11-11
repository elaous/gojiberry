package canvas

// SetField makes a cell a form field.
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

func (t *Table) FieldAt(row, col int) *FormFieldObject {
	row, col = t.Owner(row, col)
	return t.Fields[CellPos{row, col}]
}

// RemoveField turns a field cell back into an ordinary cell.
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

// SyncFields rewrites every field cell's text from its field.
func (t *Table) SyncFields() {
	for p, f := range t.Fields {
		if t.InRange(p.Row, p.Col) {
			t.syncField(p.Row, p.Col, f)
		}
	}
}

func (t *Table) syncField(row, col int, f *FormFieldObject) {
	words := f.Words()
	if isFormula(words) { // a label such as "=x" must not become a formula
		words = "\\" + words
	}
	t.Cells[row][col] = words
}

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

func (o *TableObject) switchRect() [4]float64 {
	hs := o.View.Style.HandleSize
	return [4]float64{o.pad + o.tableW + o.View.Style.BarGap, 0, hs, hs}
}

// ShowTableFormulas shows formulas in every table of the flow.
func (f *TextFlow) ShowTableFormulas(on bool) {
	for _, o := range f.Tables() {
		o.View.SetShowFormulas(on)
	}
}
