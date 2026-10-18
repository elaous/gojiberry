package canvas

// Column widths and row heights a person set by dragging a grip; 0 (or no entry) leaves the size to the table: a column
// as wide as its widest text (at least Style.MinColWidth), a row Style.RowHeight high.

// Smallest sizes a drag can make.
const (
	MinSetColumnWidth = 24.0
	MinSetRowHeight   = 18.0
	maxSetSize        = 2000.0
)

// SetColumnWidth sets the width of column col; 0 gives it back to the table.
func (t *Table) SetColumnWidth(col int, w float64) {
	t.ColWidths = setSize(t.ColWidths, col, w, t.Cols(), MinSetColumnWidth)
}

// SetRowHeight sets the height of row; 0 gives it back to the table.
func (t *Table) SetRowHeight(row int, h float64) {
	t.RowHeights = setSize(t.RowHeights, row, h, t.Rows(), MinSetRowHeight)
}

// ColumnWidth is the width set for the column, 0 when there is none.
func (t *Table) ColumnWidth(col int) float64 { return sizeAt(t.ColWidths, col) }

// RowHeightSet is the height set for the row, 0 when there is none.
func (t *Table) RowHeightSet(row int) float64 { return sizeAt(t.RowHeights, row) }

func sizeAt(s []float64, i int) float64 {
	if i < 0 || i >= len(s) {
		return 0
	}
	return s[i]
}

func setSize(s []float64, i int, v float64, n int, min float64) []float64 {
	if i < 0 || i >= n {
		return s
	}
	if v != v || v < 0 { // NaN or negative: no size
		v = 0
	}
	if v > 0 {
		v = max(min, v)
		v = minF(v, maxSetSize)
	}
	for len(s) < n {
		s = append(s, 0)
	}
	s = s[:n]
	s[i] = v
	for _, x := range s {
		if x != 0 {
			return s
		}
	}
	return nil // nothing set any more
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func insertSize(s []float64, at int) []float64 {
	if len(s) == 0 {
		return s
	}
	at = max(0, minInt(at, len(s)))
	s = append(s, 0)
	copy(s[at+1:], s[at:])
	s[at] = 0
	return s
}

func deleteSize(s []float64, at int) []float64 {
	if at >= 0 && at < len(s) {
		s = append(s[:at], s[at+1:]...)
	}
	return s
}

func moveSize(s []float64, from, to int) []float64 {
	if len(s) == 0 {
		return s
	}
	for len(s) <= max(from, to) {
		s = append(s, 0)
	}
	v := s[from]
	s = append(s[:from], s[from+1:]...)
	s = append(s, 0)
	copy(s[to+1:], s[to:])
	s[to] = v
	return s
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
