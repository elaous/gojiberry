package canvas

import "testing"

func fieldTable() (*Table, *FormFieldObject) {
	t := NewTableFrom([][]string{{"Item", "Qty"}, {"Pens", ""}, {"Total", "=SUM(B2:B2)"}})
	f := NewFormField(FormNumber, "Qty")
	t.SetField(1, 1, f)
	return t, f
}

func TestAFieldCellHoldsTheFieldsWords(t *testing.T) {
	tb, f := fieldTable()
	if tb.Cell(1, 1) != "Qty: " || tb.FieldAt(1, 1) != f {
		t.Fatalf("cell %q field %v", tb.Cell(1, 1), tb.FieldAt(1, 1))
	}
	f.Value = "4"
	tb.SyncFields()
	if tb.Cell(1, 1) != "Qty: 4" || tb.Display(1, 1) != "Qty: 4" || tb.Values()[1][1] != "Qty: 4" {
		t.Fatalf("after filling: %q %q %q", tb.Cell(1, 1), tb.Display(1, 1), tb.Values()[1][1])
	}
}

func TestAFormulaReadsWhatWasFilledIn(t *testing.T) {
	tb, f := fieldTable()
	f.Value = "4"
	tb.SyncFields()
	if got := tb.Values()[2][1]; got != "4" {
		t.Fatalf("SUM over the field = %q, want 4", got)
	}
	f.Value = "=1+1" // what a person typed is text, never a formula
	tb.SyncFields()
	if got := tb.Values()[1][1]; got != "Qty: =1+1" {
		t.Fatalf("cell %q", got)
	}
}

func TestFieldsFollowTheirCells(t *testing.T) {
	tb, f := fieldTable()
	tb.InsertRow(1)
	if tb.FieldAt(2, 1) != f || tb.FieldAt(1, 1) != nil {
		t.Fatalf("row inserted above: field at (2,1)=%v (1,1)=%v", tb.FieldAt(2, 1), tb.FieldAt(1, 1))
	}
	tb.InsertColumn(0)
	if tb.FieldAt(2, 2) != f {
		t.Fatal("column inserted before it")
	}
	tb.DeleteRow(1)
	if tb.FieldAt(1, 2) != f {
		t.Fatal("row above deleted")
	}
	tb.DeleteRow(1)
	if len(tb.Fields) != 0 {
		t.Fatalf("a field goes with its row: %v", tb.Fields)
	}
}

func TestDuplicatedRowGetsAnEmptyCopyOfTheField(t *testing.T) {
	tb, f := fieldTable()
	f.Value = "4"
	tb.SyncFields()
	tb.DuplicateRow(1)
	g := tb.FieldAt(2, 1)
	if g == nil || g == f || g.Label != "Qty" || g.Value != "" || f.Value != "4" {
		t.Fatalf("copy %+v", g)
	}
}

func TestWritingOverAFieldCellEndsTheField(t *testing.T) {
	tb, _ := fieldTable()
	tb.SetCell(1, 1, "plain")
	if tb.FieldAt(1, 1) != nil || tb.Cell(1, 1) != "plain" {
		t.Fatal("still a field")
	}
	tb, f := fieldTable()
	f.Value = "3"
	tb.Clear()
	if tb.FieldAt(1, 1) == nil || f.Value != "" || tb.Cell(1, 1) != "Qty: " {
		t.Fatal("clearing keeps the field and empties it")
	}
}

func TestPressingAFieldCellFillsOrEditsItLikeAFormula(t *testing.T) {
	v, _ := newView()
	f := NewFormField(FormText, "Name")
	v.Table.SetField(1, 1, f)
	var filled, edited int
	v.OnFieldFill = func(int, int, *FormFieldObject) { filled++ }
	v.OnFieldEdit = func(int, int, *FormFieldObject) { edited++ }
	v.MouseDown(100, 60)
	if filled != 1 || edited != 0 || !v.locked {
		t.Fatalf("calculated view: filled %d edited %d locked %v", filled, edited, v.locked)
	}
	v.SetShowFormulas(true)
	v.MouseDown(100, 60)
	if edited != 1 {
		t.Fatalf("formula view: edited %d", edited)
	}
	f.Locked = true
	v.MouseDown(100, 60)
	if edited != 1 {
		t.Fatal("a locked field cannot be changed")
	}
}

func TestPressingACheckboxCellTicksIt(t *testing.T) {
	v, _ := newView()
	f := NewFormField(FormCheckbox, "Agree")
	v.Table.SetField(1, 0, f)
	changes := 0
	v.OnChange = func(*Table) { changes++ }
	v.MouseDown(20, 60)
	if f.Value != "true" || v.Table.Cell(1, 0) != "[x] Agree" || changes != 1 {
		t.Fatalf("ticked: %q %q %d", f.Value, v.Table.Cell(1, 0), changes)
	}
	v.MouseDown(20, 60)
	if f.Value != "" || v.Table.Cell(1, 0) != "[ ] Agree" {
		t.Fatal("unticked")
	}
}

func TestPressingAFieldInATableInTheText(t *testing.T) {
	tb := NewTableFrom([][]string{{"A", "B"}, {"c", "d"}})
	f := NewFormField(FormText, "Name")
	tb.SetField(1, 1, f)
	o := NewTableObject(tb)
	o.Measure(&fakePainter{}, 1000, 1000)
	pad := o.pad
	if pad == 0 {
		t.Fatal("a table with a field has room around it for its grips")
	}
	filled := 0
	o.View.OnFieldFill = func(r, c int, _ *FormFieldObject) {
		if r == 1 && c == 1 {
			filled++
		}
	}
	if used, _ := o.ClickAt(pad+10, pad+10); used {
		t.Fatal("an ordinary cell takes no press")
	}
	if used, changed := o.ClickAt(pad+100, pad+40); !used || changed || filled != 1 {
		t.Fatalf("field cell: used %v changed %v filled %d", used, changed, filled)
	}
	cb := NewFormField(FormCheckbox, "Agree")
	tb.SetField(1, 0, cb)
	if used, changed := o.ClickAt(pad+10, pad+40); !used || !changed || cb.Value != "true" {
		t.Fatalf("checkbox: used %v changed %v value %q", used, changed, cb.Value)
	}
}

func TestTheSwitchOfATableInTheText(t *testing.T) {
	tb := NewTableFrom([][]string{{"A", "B"}, {"c", "=1+1"}})
	o := NewTableObject(tb)
	p := &fakePainter{}
	o.Measure(p, 1000, 1000)
	r := o.switchRect()
	if used, changed := o.ClickAt(r[0]+2, r[1]+2); !used || changed || !o.View.ShowFormulas {
		t.Fatalf("first press: used %v changed %v showing %v", used, changed, o.View.ShowFormulas)
	}
	o.ClickAt(r[0]+2, r[1]+2)
	if o.View.ShowFormulas {
		t.Fatal("second press goes back to the values")
	}
	plain := NewTableObject(NewTableFrom([][]string{{"A"}, {"b"}}))
	plain.Measure(p, 1000, 1000)
	if used, _ := plain.ClickAt(plain.switchRect()[0]+2, 5); used {
		t.Fatal("a table with no formula or field has no switch")
	}
	// in the formula view a field cell is changed, not filled in
	pad2 := o.pad
	f := NewFormField(FormText, "Name")
	tb.SetField(1, 0, f)
	var filled, edited int
	o.View.OnFieldFill = func(int, int, *FormFieldObject) { filled++ }
	o.View.OnFieldEdit = func(int, int, *FormFieldObject) { edited++ }
	o.ClickAt(pad2+10, pad2+40)
	o.ClickAt(r[0]+2, r[1]+2)
	o.ClickAt(pad2+10, pad2+40)
	if filled != 1 || edited != 1 {
		t.Fatalf("filled %d edited %d", filled, edited)
	}
	flow := &TextFlow{}
	flow.Items = []Item{Inline{Object: o, Block: true}}
	flow.ShowTableFormulas(false)
	if o.View.ShowFormulas {
		t.Fatal("see all")
	}
	flow.ShowTableFormulas(true)
	if !o.View.ShowFormulas {
		t.Fatal("edit all")
	}
}
