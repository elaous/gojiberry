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

func TestMoveRow(t *testing.T) {
	tb := NewTableFrom([][]string{{"h"}, {"a"}, {"b"}, {"c"}})
	tb.MoveRow(3, 1)
	tb.MoveRow(1, 2)
	tb.MoveRow(1, 1)
	tb.MoveRow(9, 0)
	want := [][]string{{"h"}, {"a"}, {"c"}, {"b"}}
	if !reflect.DeepEqual(tb.Cells, want) {
		t.Fatalf("got %v, want %v", tb.Cells, want)
	}
}

func TestMoveColumn(t *testing.T) {
	tb := NewTableFrom([][]string{{"a", "b", "c"}, {"1", "2", "3"}})
	tb.MoveColumn(2, 0)
	want := [][]string{{"c", "a", "b"}, {"3", "1", "2"}}
	if !reflect.DeepEqual(tb.Cells, want) {
		t.Fatalf("got %v, want %v", tb.Cells, want)
	}
	tb.MoveColumn(0, 2)
	tb.MoveColumn(0, 9)
	want = [][]string{{"a", "b", "c"}, {"1", "2", "3"}}
	if !reflect.DeepEqual(tb.Cells, want) {
		t.Fatalf("got %v, want %v", tb.Cells, want)
	}
}

func TestDuplicateRowIsIndependentCopy(t *testing.T) {
	tb := NewTableFrom([][]string{{"h"}, {"a"}})
	tb.DuplicateRow(1)
	tb.SetCell(1, 0, "x")
	if tb.Rows() != 3 || tb.Cell(2, 0) != "a" {
		t.Fatalf("got %v", tb.Cells)
	}
	tb.DuplicateRow(7)
}

func TestSortByColumn(t *testing.T) {
	tb := NewTableFrom([][]string{{"n"}, {"10"}, {"9"}, {"100"}})
	tb.SortByColumn(0, false)
	if !reflect.DeepEqual(tb.Cells, [][]string{{"n"}, {"9"}, {"10"}, {"100"}}) {
		t.Fatalf("numeric: %v", tb.Cells)
	}
	tb.SortByColumn(0, true)
	if tb.Cell(1, 0) != "100" {
		t.Fatalf("desc: %v", tb.Cells)
	}
	tb = NewTableFrom([][]string{{"n"}, {"b"}, {"A"}, {"c"}})
	tb.SortByColumn(0, false)
	if !reflect.DeepEqual(tb.Cells, [][]string{{"n"}, {"A"}, {"b"}, {"c"}}) {
		t.Fatalf("text: %v", tb.Cells)
	}
	tb.SortByColumn(5, false)
}

func TestClear(t *testing.T) {
	tb := NewTableFrom([][]string{{"a", "b"}, {"c", "d"}})
	tb.Clear()
	if tb.Rows() != 2 || tb.Cols() != 2 || tb.Cell(1, 1) != "" {
		t.Fatalf("got %v", tb.Cells)
	}
}
