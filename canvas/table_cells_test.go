package canvas

import (
	"reflect"
	"testing"
)

func grid() *Table {
	return NewTableFrom([][]string{{"A", "B", "C", "D"}, {"1", "2", "3", "4"}, {"5", "6", "7", "8"}, {"9", "10", "11", "12"}})
}

func TestMergeRangeKeepsTextAndCovers(t *testing.T) {
	tb := grid()
	if tb.MergeRange(1, 1, 1, 1) {
		t.Error("one cell is not a merge")
	}
	if !tb.MergeRange(2, 2, 1, 1) { // corners in either order
		t.Fatal("merge failed")
	}
	if got := tb.Cell(1, 1); got != "2 3 6 7" {
		t.Errorf("owner text %q", got)
	}
	if tb.Cell(1, 2) != "" || tb.Cell(2, 1) != "" || tb.Cell(2, 2) != "" {
		t.Error("covered cells must be empty")
	}
	if r, c := tb.Span(1, 1); r != 2 || c != 2 {
		t.Errorf("span %d x %d", r, c)
	}
	if r, c := tb.Span(2, 2); r != 0 || c != 0 || !tb.Covered(2, 2) || tb.Covered(1, 1) {
		t.Error("covered cells have no span")
	}
	if r, c := tb.Owner(2, 1); r != 1 || c != 1 {
		t.Errorf("owner %d,%d", r, c)
	}
	if r, c := tb.Span(0, 0); r != 1 || c != 1 {
		t.Error("an ordinary cell spans 1 x 1")
	}
	if !tb.Unmerge(2, 2) || len(tb.Merges) != 0 || tb.Cell(1, 1) != "2 3 6 7" {
		t.Error("unmerge keeps the text in the owner")
	}
	if tb.Unmerge(0, 0) {
		t.Error("nothing to unmerge")
	}
}

func TestMergeSwallowsAMergeItCutsThrough(t *testing.T) {
	tb := grid()
	tb.MergeRange(0, 0, 0, 1)
	tb.MergeRange(1, 1, 2, 2) // overlaps cell (1,1) only: the block is exactly that
	tb.MergeRange(0, 1, 1, 1) // cuts through both merges
	if len(tb.Merges) != 1 {
		t.Fatalf("merges %+v", tb.Merges)
	}
	m := tb.Merges[0]
	if m != (Merge{0, 0, 3, 3}) && (m.Row != 0 || m.Col != 0 || m.Rows < 2 || m.Cols < 2) {
		t.Errorf("block %+v", m)
	}
}

func TestInsertAndDeleteAdjustMerges(t *testing.T) {
	tb := grid()
	tb.MergeRange(1, 1, 2, 2)
	tb.SetCellStyle(1, 1, CellStyle{Bold: true})
	tb.InsertRow(0) // above: everything moves down
	if !reflect.DeepEqual(tb.Merges, []Merge{{2, 1, 2, 2}}) || !tb.StyleAt(2, 1).Bold {
		t.Errorf("after insert above: %+v %+v", tb.Merges, tb.Styles)
	}
	tb.InsertRow(3) // inside the merge: it grows
	if tb.Merges[0].Rows != 3 {
		t.Errorf("a row inside a merge makes it taller: %+v", tb.Merges)
	}
	tb.DeleteRow(3)
	tb.DeleteRow(0)
	if !reflect.DeepEqual(tb.Merges, []Merge{{1, 1, 2, 2}}) {
		t.Errorf("after deleting those again: %+v", tb.Merges)
	}
	tb.InsertColumn(2) // inside the merge
	if tb.Merges[0].Cols != 3 {
		t.Errorf("columns: %+v", tb.Merges)
	}
	tb.DeleteColumn(2)
	if tb.Merges[0].Cols != 2 {
		t.Errorf("columns after delete: %+v", tb.Merges)
	}
	tb2 := grid()
	tb2.MergeRange(1, 0, 2, 0)
	tb2.DeleteRow(1)
	if len(tb2.Merges) != 0 || tb2.Cell(1, 0) != "1 5" && tb2.Cell(1, 0) != "5 1" && tb2.Cell(1, 0) != "1 5" {
		t.Errorf("owner row deleted: merges %+v, cell %q", tb2.Merges, tb2.Cell(1, 0))
	}
	tb3 := grid()
	tb3.MergeRange(1, 0, 1, 2)
	tb3.DeleteRow(1)
	if len(tb3.Merges) != 0 {
		t.Errorf("merges %+v", tb3.Merges)
	}
	one := NewTable(1, 1)
	one.SetCellStyle(0, 0, CellStyle{Bold: true})
	one.DeleteRow(0)
	if len(one.Styles) != 0 {
		t.Error("clearing the last row clears styles")
	}
}

func TestStylesFollowTheirCellsWhenRowsMoveAndSort(t *testing.T) {
	tb := NewTableFrom([][]string{{"h"}, {"b"}, {"a"}, {"c"}})
	tb.SetCellStyle(2, 0, CellStyle{Background: "#ff0"}) // "a"
	tb.SortByColumn(0, false)
	if tb.Cell(1, 0) != "a" || tb.StyleAt(1, 0).Background != "#ff0" || !tb.StyleAt(2, 0).IsZero() {
		t.Errorf("after sort: %v %+v", tb.Cells, tb.Styles)
	}
	tb.MoveRow(1, 3)
	if tb.Cell(3, 0) != "a" || tb.StyleAt(3, 0).Background != "#ff0" {
		t.Errorf("after move: %v %+v", tb.Cells, tb.Styles)
	}
	tb.MergeRange(1, 0, 2, 0)
	tb.SortByColumn(0, true)
	if len(tb.Merges) != 0 {
		t.Error("sorting tears merges apart, so it drops them")
	}
	tb.DuplicateRow(1)
	tb.SetCellStyle(1, 0, CellStyle{Bold: true})
	tb.DuplicateRow(1)
	if !tb.StyleAt(2, 0).Bold {
		t.Error("a duplicated row keeps its look")
	}
}

func TestCellStyleOfACoveredCellIsItsOwners(t *testing.T) {
	tb := grid()
	tb.MergeRange(0, 0, 1, 1)
	tb.SetCellStyle(1, 1, CellStyle{Italic: true})
	if !tb.StyleAt(0, 0).Italic || !tb.StyleAt(1, 1).Italic {
		t.Error("styling a covered cell styles the merged cell")
	}
	tb.SetCellStyle(0, 0, CellStyle{})
	if len(tb.Styles) != 0 {
		t.Error("a zero style removes it")
	}
}
