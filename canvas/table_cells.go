package canvas

import (
	"slices"
	"strings"
)

// CellPos is a cell's place in a table.
type CellPos struct{ Row, Col int }

// Merge is a block of cells shown as one cell: the owner at (Row, Col) is its
// top left corner, and it spans Rows x Cols (at least 2 cells in all). The cells
// it covers are empty and cannot be edited or styled on their own.
type Merge struct{ Row, Col, Rows, Cols int }

// CellStyle is the formatting of one cell; the zero value is the table's own
// look.
type CellStyle struct {
	// Background and Color are CSS colours ("" keeps the table's).
	Background, Color string
	Bold, Italic      bool
	Align             Align
}

// IsZero reports whether the style changes nothing.
func (c CellStyle) IsZero() bool { return c == CellStyle{} }

// MergeAt returns the merge that holds the cell (as owner or covered cell).
func (t *Table) MergeAt(row, col int) (Merge, bool) {
	for _, m := range t.Merges {
		if row >= m.Row && row < m.Row+m.Rows && col >= m.Col && col < m.Col+m.Cols {
			return m, true
		}
	}
	return Merge{}, false
}

// Owner returns the cell that shows (row, col): the top left cell of its merge,
// or the cell itself.
func (t *Table) Owner(row, col int) (int, int) {
	if m, ok := t.MergeAt(row, col); ok {
		return m.Row, m.Col
	}
	return row, col
}

// Covered reports whether the cell is hidden under another cell's merge.
func (t *Table) Covered(row, col int) bool {
	m, ok := t.MergeAt(row, col)
	return ok && (m.Row != row || m.Col != col)
}

// Span returns how many rows and columns the cell at (row, col) takes: 1, 1
// for an ordinary cell, 0, 0 for a covered one.
func (t *Table) Span(row, col int) (rows, cols int) {
	m, ok := t.MergeAt(row, col)
	switch {
	case !ok:
		return 1, 1
	case m.Row != row || m.Col != col:
		return 0, 0
	}
	return m.Rows, m.Cols
}

// MergeRange merges the cells from (r0, c0) to (r1, c1) (either corner first)
// into one, which keeps the text of them all in reading order, separated by
// spaces. A merge the range only partly covers is taken in whole, so the block
// grows to hold it. It reports whether anything was merged (not for a single
// cell).
func (t *Table) MergeRange(r0, c0, r1, c1 int) bool {
	if !t.InRange(r0, c0) || !t.InRange(r1, c1) {
		return false
	}
	if r0 > r1 {
		r0, r1 = r1, r0
	}
	if c0 > c1 {
		c0, c1 = c1, c0
	}
	for grew := true; grew; { // swallow merges the block cuts through
		grew = false
		for _, m := range t.Merges {
			if m.Row+m.Rows-1 < r0 || m.Row > r1 || m.Col+m.Cols-1 < c0 || m.Col > c1 {
				continue
			}
			if m.Row < r0 || m.Col < c0 || m.Row+m.Rows-1 > r1 || m.Col+m.Cols-1 > c1 {
				r0, c0 = min(r0, m.Row), min(c0, m.Col)
				r1, c1 = max(r1, m.Row+m.Rows-1), max(c1, m.Col+m.Cols-1)
				grew = true
			}
		}
	}
	if r0 == r1 && c0 == c1 {
		return false
	}
	var texts []string
	for r := r0; r <= r1; r++ {
		for c := c0; c <= c1; c++ {
			if tx := strings.TrimSpace(t.Cells[r][c]); tx != "" {
				texts = append(texts, tx)
			}
			t.Cells[r][c] = ""
		}
	}
	t.Cells[r0][c0] = strings.Join(texts, " ")
	t.Merges = slices.DeleteFunc(t.Merges, func(m Merge) bool { // merges inside the block go
		return m.Row >= r0 && m.Row <= r1 && m.Col >= c0 && m.Col <= c1
	})
	t.Merges = append(t.Merges, Merge{Row: r0, Col: c0, Rows: r1 - r0 + 1, Cols: c1 - c0 + 1})
	for r := r0; r <= r1; r++ { // covered cells have no style of their own
		for c := c0; c <= c1; c++ {
			if r != r0 || c != c0 {
				delete(t.Styles, CellPos{r, c})
			}
		}
	}
	return true
}

// Unmerge splits the merge that holds the cell back into single cells (the text
// stays in the top left one). It reports whether there was one.
func (t *Table) Unmerge(row, col int) bool {
	m, ok := t.MergeAt(row, col)
	if !ok {
		return false
	}
	t.Merges = slices.DeleteFunc(t.Merges, func(x Merge) bool { return x == m })
	return true
}

// SetCellStyle sets the style of the cell (the owner, for a covered one); a zero
// style removes it.
func (t *Table) SetCellStyle(row, col int, st CellStyle) {
	if !t.InRange(row, col) {
		return
	}
	row, col = t.Owner(row, col)
	if st.IsZero() {
		delete(t.Styles, CellPos{row, col})
		return
	}
	if t.Styles == nil {
		t.Styles = map[CellPos]CellStyle{}
	}
	t.Styles[CellPos{row, col}] = st
}

// StyleAt returns the style of the cell (of the owner, for a covered one).
func (t *Table) StyleAt(row, col int) CellStyle {
	row, col = t.Owner(row, col)
	return t.Styles[CellPos{row, col}]
}

// shift moves merges and styles for a row or column inserted (delta 1) or
// removed (delta -1) at index atRow/atCol: dRow and dCol are 1, -1 or 0, and the
// other index is 0. A merge the change falls inside grows or shrinks.
func (t *Table) shift(atRow, atCol, dRow, dCol int) {
	at, d, rowWise := atCol, dCol, false
	if dRow != 0 {
		at, d, rowWise = atRow, dRow, true
	}
	pos := func(m *Merge) (*int, *int) {
		if rowWise {
			return &m.Row, &m.Rows
		}
		return &m.Col, &m.Cols
	}
	out := t.Merges[:0]
	for _, m := range t.Merges {
		start, size := pos(&m)
		switch {
		case d > 0 && *start >= at:
			*start++
		case d > 0 && at > *start && at < *start+*size: // a row put inside a merge
			*size++
		case d < 0 && *start > at:
			*start--
		case d < 0 && at >= *start && at < *start+*size: // a row taken out of a merge
			*size--
		}
		if m.Rows >= 1 && m.Cols >= 1 && (m.Rows > 1 || m.Cols > 1) {
			out = append(out, m)
		}
	}
	t.Merges = out
	t.remapStyles(func(p CellPos) CellPos {
		idx := &p.Col
		if rowWise {
			idx = &p.Row
		}
		switch {
		case d > 0 && *idx >= at:
			*idx++
		case d < 0 && *idx == at:
			return CellPos{-1, -1} // gone
		case d < 0 && *idx > at:
			*idx--
		}
		return p
	})
}

// remapStyles gives every style the position f returns for it; a negative
// position drops it.
func (t *Table) remapStyles(f func(CellPos) CellPos) {
	if len(t.Styles) == 0 {
		return
	}
	out := make(map[CellPos]CellStyle, len(t.Styles))
	for p, st := range t.Styles {
		if q := f(p); q.Row >= 0 && q.Col >= 0 {
			out[q] = st
		}
	}
	t.Styles = out
}

// removeRowOfMerges is called before row at is deleted: the text of a merge's
// owner in that row moves to the cell below it (which becomes the owner), so the
// text is not lost with the row.
func (t *Table) removeRowOfMerges(at int) {
	for i, m := range t.Merges {
		if m.Row == at && m.Rows > 1 {
			t.Cells[at+1][m.Col] = strings.TrimSpace(t.Cells[at][m.Col] + " " + t.Cells[at+1][m.Col])
			if st, ok := t.Styles[CellPos{at, m.Col}]; ok {
				if t.Styles == nil {
					t.Styles = map[CellPos]CellStyle{}
				}
				t.Styles[CellPos{at + 1, m.Col}] = st
			}
			t.Merges[i].Row++ // shift moves it back up, and it is a row shorter
			t.Merges[i].Rows--
		}
	}
}

// removeColumnOfMerges is removeRowOfMerges for a column.
func (t *Table) removeColumnOfMerges(at int) {
	for i, m := range t.Merges {
		if m.Col == at && m.Cols > 1 {
			t.Cells[m.Row][at+1] = strings.TrimSpace(t.Cells[m.Row][at] + " " + t.Cells[m.Row][at+1])
			if st, ok := t.Styles[CellPos{m.Row, at}]; ok {
				t.Styles[CellPos{m.Row, at + 1}] = st
			}
			t.Merges[i].Col++
			t.Merges[i].Cols--
		}
	}
}
