package canvas

import (
	"reflect"
	"testing"
)

func TestNewTableMinimumSize(t *testing.T) {
	tb := NewTable(0, -3)
	if tb.Rows() != 1 || tb.Cols() != 1 {
		t.Fatalf("got %dx%d, want 1x1", tb.Rows(), tb.Cols())
	}
}

func TestNewTableFromPadsRagged(t *testing.T) {
	tb := NewTableFrom([][]string{{"a", "b", "c"}, {"d"}})
	want := [][]string{{"a", "b", "c"}, {"d", "", ""}}
	if !reflect.DeepEqual(tb.Cells, want) {
		t.Fatalf("got %v, want %v", tb.Cells, want)
	}
}

func TestAddRowAndColumn(t *testing.T) {
	tb := NewTableFrom([][]string{{"a", "b"}, {"c", "d"}})
	tb.AddColumn()
	tb.AddRow()
	want := [][]string{{"a", "b", ""}, {"c", "d", ""}, {"", "", ""}}
	if !reflect.DeepEqual(tb.Cells, want) {
		t.Fatalf("got %v, want %v", tb.Cells, want)
	}
}

func TestInsertKeepsOrderAndClamps(t *testing.T) {
	tb := NewTableFrom([][]string{{"a", "b"}, {"c", "d"}})
	tb.InsertColumn(1)
	tb.InsertRow(0)
	tb.InsertRow(99)
	want := [][]string{{"", "", ""}, {"a", "", "b"}, {"c", "", "d"}, {"", "", ""}}
	if !reflect.DeepEqual(tb.Cells, want) {
		t.Fatalf("got %v, want %v", tb.Cells, want)
	}
}

func TestDeleteNeverEmptiesTable(t *testing.T) {
	tb := NewTableFrom([][]string{{"a", "b"}, {"c", "d"}})
	tb.DeleteRow(0)
	tb.DeleteColumn(1)
	if !reflect.DeepEqual(tb.Cells, [][]string{{"c"}}) {
		t.Fatalf("got %v", tb.Cells)
	}
	tb.DeleteRow(0)
	tb.DeleteColumn(0)
	if tb.Rows() != 1 || tb.Cols() != 1 || tb.Cell(0, 0) != "" {
		t.Fatalf("got %v, want a cleared 1x1", tb.Cells)
	}
	tb.DeleteRow(5) // out of range: no-op
}

func TestCellOutOfRange(t *testing.T) {
	tb := NewTable(1, 1)
	tb.SetCell(3, 3, "x")
	if tb.Cell(3, 3) != "" || tb.Cell(-1, 0) != "" {
		t.Fatal("out-of-range access should be a no-op / empty")
	}
}
