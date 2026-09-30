package canvas

// Table is the data behind a TableView: a rectangular grid of strings
// whose first row is the header. It always has at least one row and one
// column, and every row always has the same number of cells.
type Table struct {
	Cells [][]string
}

// NewTable returns a table of rows x cols empty cells (minimum 1x1).
func NewTable(rows, cols int) *Table {
	rows, cols = max(rows, 1), max(cols, 1)
	t := &Table{Cells: make([][]string, rows)}
	for r := range t.Cells {
		t.Cells[r] = make([]string, cols)
	}
	return t
}

// NewTableFrom builds a table from rows, padding short rows with empty
// cells so the grid is rectangular. The input is copied.
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
func (t *Table) Cols() int { return len(t.Cells[0]) }

// Cell returns the text at (row, col), or "" if out of range.
func (t *Table) Cell(row, col int) string {
	if !t.InRange(row, col) {
		return ""
	}
	return t.Cells[row][col]
}

// SetCell sets the text at (row, col); out-of-range is a no-op.
func (t *Table) SetCell(row, col int, text string) {
	if t.InRange(row, col) {
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

// InsertRow inserts an empty row so it becomes row index at (clamped to
// [0, Rows()]).
func (t *Table) InsertRow(at int) {
	at = min(max(at, 0), t.Rows())
	t.Cells = append(t.Cells, nil)
	copy(t.Cells[at+1:], t.Cells[at:])
	t.Cells[at] = make([]string, t.Cols())
}

// InsertColumn inserts an empty column so it becomes column index at
// (clamped to [0, Cols()]).
func (t *Table) InsertColumn(at int) {
	at = min(max(at, 0), t.Cols())
	for r, row := range t.Cells {
		row = append(row, "")
		copy(row[at+1:], row[at:])
		row[at] = ""
		t.Cells[r] = row
	}
}

// DeleteRow removes row index at. The last remaining row is never
// removed (it is cleared instead), keeping the table non-empty.
func (t *Table) DeleteRow(at int) {
	if at < 0 || at >= t.Rows() {
		return
	}
	if t.Rows() == 1 {
		clear(t.Cells[0])
		return
	}
	t.Cells = append(t.Cells[:at], t.Cells[at+1:]...)
}

// DeleteColumn removes column index at. The last remaining column is
// never removed (it is cleared instead).
func (t *Table) DeleteColumn(at int) {
	if at < 0 || at >= t.Cols() {
		return
	}
	if t.Cols() == 1 {
		for _, row := range t.Cells {
			row[0] = ""
		}
		return
	}
	for r, row := range t.Cells {
		t.Cells[r] = append(row[:at], row[at+1:]...)
	}
}
