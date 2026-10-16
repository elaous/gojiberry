package canvas

import "strings"

// TableFromMarkdown reads a pipe table:
//
//	| Item | Qty | Price | Total   |
//	| :--- | --: | ----: | ------: |
//	| Pens | 3   | 1.5   | =B2*C2  |
//
// The first row is the header, the second the separator (its colons give each column's alignment), and a cell keeps
// its text as written, so a cell that starts with = is a formula (see Values) and \| is a bar inside a cell. It reports
// false when the text is not a table (no header, or no separator row).
func TableFromMarkdown(md string) (*Table, bool) {
	var rows [][]string
	var aligns []Align
	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(rows) > 0 {
				break // the table ends at the first blank line
			}
			continue
		}
		if !strings.Contains(line, "|") {
			if len(rows) > 0 {
				break
			}
			return nil, false
		}
		cells := splitPipes(line)
		if len(rows) == 1 && aligns == nil {
			a, ok := separatorRow(cells)
			if !ok {
				return nil, false
			}
			aligns = a
			continue
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 || aligns == nil {
		return nil, false
	}
	t := NewTableFrom(rows)
	for c, a := range aligns {
		if a == AlignLeft || c >= t.Cols() {
			continue
		}
		for r := range t.Rows() {
			st := t.StyleAt(r, c)
			st.Align = a
			t.SetCellStyle(r, c, st)
		}
	}
	return t, true
}

// splitPipes cuts a table line at its bars, not at \|, and drops the bars at the two ends.
func splitPipes(line string) []string {
	line = strings.TrimSpace(line)
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			cur.WriteByte('|')
			i++
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(line[i])
		}
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	if strings.HasPrefix(line, "|") {
		cells = cells[1:]
	}
	if strings.HasSuffix(line, "|") && !strings.HasSuffix(line, "\\|") && len(cells) > 0 {
		cells = cells[:len(cells)-1]
	}
	return cells
}

func separatorRow(cells []string) ([]Align, bool) {
	if len(cells) == 0 {
		return nil, false
	}
	out := make([]Align, len(cells))
	for i, c := range cells {
		t := strings.Trim(c, ":")
		if t == "" || strings.Trim(t, "-") != "" {
			return nil, false
		}
		left, right := strings.HasPrefix(c, ":"), strings.HasSuffix(c, ":")
		switch {
		case left && right:
			out[i] = AlignCenter
		case right:
			out[i] = AlignRight
		}
	}
	return out, true
}
