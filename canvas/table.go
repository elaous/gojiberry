package canvas

import (
	"cmp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type Table struct {
	Cells  [][]string
	Merges []Merge
	Styles map[CellPos]CellStyle
	// Fields are the cells that are form fields (see table_fields.go).
	Fields map[CellPos]*FormFieldObject
	// ColWidths and RowHeights are sizes set by dragging a grip.
	ColWidths, RowHeights []float64
}

func NewTable(rows, cols int) *Table {
	rows, cols = max(rows, 1), max(cols, 1)
	t := &Table{Cells: make([][]string, rows)}
	for r := range t.Cells {
		t.Cells[r] = make([]string, cols)
	}
	return t
}

// NewTableFrom builds a table from rows, padding short rows.
func NewTableFrom(rows [][]string) *Table {
	cols := 1
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	t := NewTable(len(rows), cols)
	for r, row := range rows {
		copy(t.Cells[r], row)
	}
	return t
}

// Rows is the number of rows, header included.
func (t *Table) Rows() int { return len(t.Cells) }

// Cols is the number of columns.
func (t *Table) Cols() int {
	if len(t.Cells) == 0 {
		return 0
	}
	return len(t.Cells[0])
}

func (t *Table) Cell(row, col int) string {
	if !t.InRange(row, col) {
		return ""
	}
	return t.Cells[row][col]
}

func (t *Table) SetCell(row, col int, text string) {
	if t.InRange(row, col) {
		if f := t.Fields[CellPos{row, col}]; f != nil && text != t.Cells[row][col] { // written over: an ordinary cell again
			delete(t.Fields, CellPos{row, col})
		}
		t.Cells[row][col] = text
	}
}

// InRange reports whether (row, col) is a cell of t.
func (t *Table) InRange(row, col int) bool {
	return row >= 0 && row < t.Rows() && col >= 0 && col < t.Cols()
}

// AddRow appends an empty row.
func (t *Table) AddRow() { t.InsertRow(t.Rows()) }

// AddColumn appends an empty column.
func (t *Table) AddColumn() { t.InsertColumn(t.Cols()) }

func (t *Table) insertRow(at int) {
	at = min(max(at, 0), t.Rows())
	t.Cells = append(t.Cells, nil)
	copy(t.Cells[at+1:], t.Cells[at:])
	t.Cells[at] = make([]string, t.Cols())
	t.RowHeights = insertSize(t.RowHeights, at)
	t.shift(at, 0, 1, 0)
}

func (t *Table) insertColumn(at int) {
	at = min(max(at, 0), t.Cols())
	for r, row := range t.Cells {
		row = append(row, "")
		copy(row[at+1:], row[at:])
		row[at] = ""
		t.Cells[r] = row
	}
	t.ColWidths = insertSize(t.ColWidths, at)
	t.shift(0, at, 0, 1)
}

func (t *Table) deleteRow(at int) {
	if at < 0 || at >= t.Rows() {
		return
	}
	if t.Rows() == 1 {
		clear(t.Cells[0])
		t.Merges, t.Styles, t.Fields, t.RowHeights = nil, nil, nil, nil
		return
	}
	t.removeRowOfMerges(at)
	t.Cells = append(t.Cells[:at], t.Cells[at+1:]...)
	t.RowHeights = deleteSize(t.RowHeights, at)
	t.shift(at, 0, -1, 0)
}

func (t *Table) deleteColumn(at int) {
	if at < 0 || at >= t.Cols() {
		return
	}
	if t.Cols() == 1 {
		for _, row := range t.Cells {
			row[0] = ""
		}
		t.Merges, t.Styles, t.Fields, t.ColWidths = nil, nil, nil, nil
		return
	}
	t.removeColumnOfMerges(at)
	for r, row := range t.Cells {
		t.Cells[r] = append(row[:at], row[at+1:]...)
	}
	t.ColWidths = deleteSize(t.ColWidths, at)
	t.shift(0, at, 0, -1)
}

func (t *Table) moveRow(from, to int) {
	if from < 0 || from >= t.Rows() || to < 0 || to >= t.Rows() || from == to {
		return
	}
	t.remapStyles(func(p CellPos) CellPos { p.Row = moved(p.Row, from, to); return p })
	t.RowHeights = moveSize(t.RowHeights, from, to)
	t.Merges = nil // a moved row would tear a merged block apart
	row := t.Cells[from]
	t.Cells = append(t.Cells[:from], t.Cells[from+1:]...)
	t.Cells = append(t.Cells, nil)
	copy(t.Cells[to+1:], t.Cells[to:])
	t.Cells[to] = row
}

func (t *Table) moveColumn(from, to int) {
	if from < 0 || from >= t.Cols() || to < 0 || to >= t.Cols() || from == to {
		return
	}
	t.remapStyles(func(p CellPos) CellPos { p.Col = moved(p.Col, from, to); return p })
	t.ColWidths = moveSize(t.ColWidths, from, to)
	t.Merges = nil
	for r, row := range t.Cells {
		v := row[from]
		row = append(row[:from], row[from+1:]...)
		row = append(row, "")
		copy(row[to+1:], row[to:])
		row[to] = v
		t.Cells[r] = row
	}
}

func (t *Table) duplicateRow(at int) {
	if at < 0 || at >= t.Rows() {
		return
	}
	t.insertRow(at + 1)
	copy(t.Cells[at+1], t.Cells[at])
	if h := t.RowHeightSet(at); h > 0 {
		t.SetRowHeight(at+1, h)
	}
	for c := 0; c < t.Cols(); c++ { // the copy looks like the original
		if st, ok := t.Styles[CellPos{at, c}]; ok {
			t.SetCellStyle(at+1, c, st)
		}
		if f := t.Fields[CellPos{at, c}]; f != nil {
			cp := cloneFields(map[CellPos]*FormFieldObject{{}: f})[CellPos{}]
			cp.Value = ""
			t.SetField(at+1, c, cp)
		}
	}
}

func (t *Table) sortByColumn(col int, desc bool) {
	if col < 0 || col >= t.Cols() {
		return
	}
	t.Merges = nil // sorting rows would tear a merged block apart
	n := t.Rows() - 1
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		c := compareCells(t.Cells[1+order[i]][col], t.Cells[1+order[j]][col])
		if desc {
			return c > 0
		}
		return c < 0
	})
	body := make([][]string, n)
	newRow := make([]int, n) // old body row -> new
	for i, o := range order {
		body[i] = t.Cells[1+o]
		newRow[o] = i
	}
	copy(t.Cells[1:], body)
	if len(t.RowHeights) > 0 { // a row keeps its height when it moves
		heights := slices.Clone(t.RowHeights)
		for old, nw := range newRow {
			if 1+old < len(heights) && 1+nw < len(heights) {
				heights[1+nw] = t.RowHeights[1+old]
			}
		}
		t.RowHeights = heights
	}
	t.remapStyles(func(p CellPos) CellPos {
		if p.Row >= 1 {
			p.Row = 1 + newRow[p.Row-1]
		}
		return p
	})
}

func compareCells(a, b string) int {
	fa, errA := strconv.ParseFloat(strings.TrimSpace(a), 64)
	fb, errB := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if errA == nil && errB == nil {
		return cmp.Compare(fa, fb)
	}
	return cmp.Compare(strings.ToLower(a), strings.ToLower(b))
}

// Clear empties every cell, keeping the table's shape.
func (t *Table) Clear() {
	for _, row := range t.Cells {
		clear(row)
	}
	for _, f := range t.Fields { // a field stays a field, emptied
		f.Clear()
	}
	t.SyncFields()
}

func moved(i, from, to int) int {
	switch {
	case i == from:
		return to
	case from < to && i > from && i <= to:
		return i - 1
	case from > to && i >= to && i < from:
		return i + 1
	}
	return i
}

func (t *Table) InsertRow(at int) { t.keepScheme(func() { t.insertRow(at) }) }

func (t *Table) InsertColumn(at int) { t.keepScheme(func() { t.insertColumn(at) }) }

func (t *Table) DeleteRow(at int) { t.keepScheme(func() { t.deleteRow(at) }) }

func (t *Table) DeleteColumn(at int) { t.keepScheme(func() { t.deleteColumn(at) }) }

func (t *Table) MoveRow(from, to int) { t.keepScheme(func() { t.moveRow(from, to) }) }

func (t *Table) MoveColumn(from, to int) { t.keepScheme(func() { t.moveColumn(from, to) }) }

func (t *Table) DuplicateRow(at int) { t.keepScheme(func() { t.duplicateRow(at) }) }

func (t *Table) SortByColumn(col int, desc bool) { t.keepScheme(func() { t.sortByColumn(col, desc) }) }
