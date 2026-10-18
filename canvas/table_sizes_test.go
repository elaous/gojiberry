package canvas

import "testing"

func TestColumnAndRowSizesFollowTheirColumnsAndRows(t *testing.T) {
	tb := NewTableFrom([][]string{{"a", "b", "c"}, {"d", "e", "f"}, {"g", "h", "i"}})
	tb.SetColumnWidth(1, 120)
	tb.SetRowHeight(2, 50)
	tb.InsertColumn(0)
	tb.InsertRow(0)
	if tb.ColumnWidth(2) != 120 || tb.RowHeightSet(3) != 50 || tb.ColumnWidth(1) != 0 || tb.RowHeightSet(0) != 0 {
		t.Fatalf("after insert: %v %v", tb.ColWidths, tb.RowHeights)
	}
	tb.MoveColumn(2, 0)
	if tb.ColumnWidth(0) != 120 {
		t.Fatalf("after move: %v", tb.ColWidths)
	}
	tb.DeleteColumn(0)
	tb.DeleteRow(3)
	if len(tb.ColWidths) != 0 && tb.ColumnWidth(0) != 0 || tb.RowHeightSet(3) != 0 {
		t.Fatalf("after delete: %v %v", tb.ColWidths, tb.RowHeights)
	}
	tb.SetColumnWidth(0, 5)
	if tb.ColumnWidth(0) != MinSetColumnWidth {
		t.Fatalf("too narrow: %v", tb.ColumnWidth(0))
	}
	tb.SetColumnWidth(0, 0)
	for c := range tb.Cols() {
		if tb.ColumnWidth(c) != 0 {
			t.Fatal("reset")
		}
	}
	if tb.ColWidths != nil {
		t.Fatal("nothing set any more is nil")
	}
}

func TestARowKeepsItsHeightWhenSorted(t *testing.T) {
	tb := NewTableFrom([][]string{{"n"}, {"b"}, {"a"}})
	tb.SetRowHeight(1, 60) // "b"
	tb.SortByColumn(0, false)
	if tb.Cell(2, 0) != "b" || tb.RowHeightSet(2) != 60 || tb.RowHeightSet(1) != 0 {
		t.Fatalf("%v %v", tb.Cells, tb.RowHeights)
	}
}

func TestSizesSetByHandAreTheLayout(t *testing.T) {
	v := NewTableView(NewTableFrom([][]string{{"a", "b"}, {"c", "d"}}), &fakePainter{})
	v.Table.SetColumnWidth(0, 150)
	v.Table.SetRowHeight(1, 50)
	if e := v.ColumnEdges(); e[1]-e[0] != 150 || e[2]-e[1] != 80 {
		t.Fatalf("columns %v", e)
	}
	if r := v.RowEdges(); r[1]-r[0] != 32 || r[2]-r[1] != 50 {
		t.Fatalf("rows %v", r)
	}
}

func TestDraggingAGripResizesTheColumnOrRowInTheText(t *testing.T) {
	f, _ := newFlow("", 400, 300)
	tb := NewTableFrom([][]string{{"a", "b"}, {"c", "d"}})
	o := NewTableObject(tb)
	f.Items = []Item{Inline{Object: o, Block: true}, Chars{Text: "x"}}
	f.SetMode(ModeEdit)
	lay := f.Layout()
	ob := lay.Lines[0].Objects[0]
	pad := o.pad
	// the edge between the two columns, on the grips above them
	x0, y0 := ob.X+pad+80, ob.Y+3
	if !f.MouseDown(x0, y0) {
		t.Fatal("a press on the edge of a column grip begins a resize")
	}
	f.MouseMove(x0+40, y0)
	f.MouseUp(x0+40, y0)
	if tb.ColumnWidth(0) != 120 {
		t.Fatalf("column width %v", tb.ColumnWidth(0))
	}
	// the row grip: the edge under the first row
	x1, y1 := ob.X+3, ob.Y+pad+32
	f.MouseDown(x1, y1)
	f.MouseMove(x1, y1+20)
	f.MouseUp(x1, y1+20)
	if tb.RowHeightSet(0) != 52 {
		t.Fatalf("row height %v", tb.RowHeightSet(0))
	}
	// not in edit mode: nothing is resized
	f.SetMode(ModeView)
	f.MouseDown(ob.X+pad+120, ob.Y+3)
	f.MouseMove(ob.X+pad+160, ob.Y+3)
	f.MouseUp(ob.X+pad+160, ob.Y+3)
	if tb.ColumnWidth(0) != 120 {
		t.Fatalf("resized outside edit mode: %v", tb.ColumnWidth(0))
	}
}
