package a11ycheck

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/elaous/gojiberry/docfile"
)

// checkTable looks at the header row of a table. In every export the first row is the header (a <th> row
// in HTML, a tagged header row in PDF, a header row in Word), and the document model has no per-table
// flag to turn that off, so a header that is "not marked" cannot happen; what can happen is a header
// row with no text in it, which leaves a screen-reader user without a name for the column. A table of one
// row has no data rows to name, so it is left alone, and so is a header cell that a merge covers.
func checkTable(t *docfile.TableItem, offset, item int) []Finding {
	if len(t.Rows) < 2 {
		return nil
	}
	covered := map[int]bool{}
	for _, m := range t.Merges {
		if m.Row == 0 {
			for c := m.Col + 1; c < m.Col+m.Cols; c++ {
				covered[c] = true
			}
		}
	}
	var empty []int // 1-based columns
	cells := 0
	for c, text := range t.Rows[0].Cells {
		if covered[c] {
			continue
		}
		cells++
		if strings.TrimSpace(text) == "" {
			empty = append(empty, c+1)
		}
	}
	switch {
	case cells == 0:
		return nil
	case len(empty) == cells:
		return []Finding{{Rule: RuleTableHeaderMissing, Severity: Error, Offset: offset, Item: item,
			Message: "A table has no header row: its first row has no text, so a reader cannot tell what each column holds.",
			Fix:     "Write a short name for each column in the first row."}}
	case len(empty) > 0:
		return []Finding{{Rule: RuleTableHeaderEmpty, Severity: Warning, Offset: offset, Item: item,
			Message: fmt.Sprintf("A table header cell is empty (column %s), so a reader hears nothing as the name of its column.", joinInts(empty)),
			Fix:     "Write a name for that column in the first row."}}
	}
	return nil
}

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = fmt.Sprint(n)
	}
	return strings.Join(s, ", ")
}

// fakeListRun gathers consecutive paragraphs that start with a typed marker ("- ", "• ", "1. ") but are not
// list items, to report them together once there are two (one such line is often just a dash in a sentence).
// Blank paragraphs between them do not end the run; anything else does.
type fakeListRun struct {
	family      string // "bullet" or "number" of the run in progress
	n           int
	start, item int
	text        string
	end         int      // where the last paragraph of the run ends
	spans       [][2]int // the typed marker of each paragraph: its offset and length in runes
}

// add takes a finished paragraph that is not a heading and returns the findings so far.
func (r *fakeListRun) add(out []Finding, text string, listKind, start, item int) []Finding {
	if strings.TrimSpace(text) == "" {
		return out
	}
	family := ""
	if listKind == 0 {
		family = markerFamily(text)
	}
	if family == "" || (r.n > 0 && family != r.family) {
		out = r.flush(out)
	}
	if family == "" {
		return out
	}
	if r.n == 0 {
		r.family, r.start, r.item, r.text = family, start, item, text
	}
	r.n++
	r.end = start + len([]rune(text))
	r.spans = append(r.spans, [2]int{start, markerLength(text)})
	return out
}

func (r *fakeListRun) flush(out []Finding) []Finding {
	defer func() { *r = fakeListRun{} }()
	if r.n < 2 {
		return out
	}
	kind := "bullets"
	if r.family == "number" {
		kind = "numbers"
	}
	arg := 0
	if r.family == "number" {
		arg = 1
	}
	return append(out, Finding{Rule: RuleFakeList, Severity: Warning, Offset: r.start, Item: r.item, Text: excerpt(r.text),
		Message: fmt.Sprintf("%d paragraphs start with typed %s instead of being a list, so a screen reader does not announce them as one.", r.n, kind),
		Fix:     "Select them and use the bullet or numbered list button, then delete the typed markers.",
		Action:  ActionToList, Arg: arg, Length: r.end - r.start, Spans: r.spans})
}

// markerFamily says whether text starts with a typed list marker: a bullet character or a dash or
// asterisk followed by a space, or a number with a point or bracket and a space.
func markerFamily(text string) string {
	rs := []rune(strings.TrimLeftFunc(text, unicode.IsSpace))
	if len(rs) < 2 {
		return ""
	}
	if strings.ContainsRune("-*•◦▪‣–·", rs[0]) && unicode.IsSpace(rs[1]) {
		return "bullet"
	}
	i := 0
	if rs[0] == '(' {
		i++
	}
	d := i
	for i < len(rs) && i-d < 3 && rs[i] >= '0' && rs[i] <= '9' {
		i++
	}
	if i > d && i+1 < len(rs) && (rs[i] == '.' || rs[i] == ')') && unicode.IsSpace(rs[i+1]) {
		return "number"
	}
	return ""
}

// markerLength is how many runes of text the typed list marker takes, with the spaces before and after it:
// what a fix has to remove once the paragraph is a real list item. It is 0 for text with no marker.
func markerLength(text string) int {
	if markerFamily(text) == "" {
		return 0
	}
	rs := []rune(text)
	i := 0
	for i < len(rs) && unicode.IsSpace(rs[i]) {
		i++
	}
	if rs[i] == '(' {
		i++
	}
	if strings.ContainsRune("-*•◦▪‣–·", rs[i]) {
		i++
	} else {
		for i < len(rs) && rs[i] >= '0' && rs[i] <= '9' {
			i++
		}
		i++ // the point or bracket
	}
	for i < len(rs) && unicode.IsSpace(rs[i]) && rs[i] != '\n' {
		i++
	}
	return i
}
