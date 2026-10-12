package export

import (
	"strconv"
	"strings"
)

// Print layout shared by the paged formats: the fields of a running header or footer, and the outline
// a table of contents is made from.

// defaultTOCTitle is the heading of a table of contents when the snapshot names none.
const defaultTOCTitle = "Contents"

func (s Snapshot) tocTitle() string {
	if s.TOCTitle != "" {
		return s.TOCTitle
	}
	return defaultTOCTitle
}

// Field values of a header or footer text.
const (
	fieldPage  = "{page}"
	fieldPages = "{pages}"
	fieldTitle = "{title}"
)

// FillFields replaces the fields of a header or footer text: {page} by the page number, {pages} by the
// page count and {title} by the title. Text that is not a field is kept as it is.
func FillFields(text string, page, pages int, title string) string {
	return strings.NewReplacer(fieldPage, strconv.Itoa(page), fieldPages, strconv.Itoa(pages), fieldTitle, title).Replace(text)
}

// decorParts splits a header or footer text at its tabs into the left, centre and right parts: one part
// is the centre, two are left and right, three (or more, the rest joined to the third) are all three.
func decorParts(text string) (left, centre, right string) {
	parts := strings.SplitN(text, "\t", 3)
	switch len(parts) {
	case 1:
		return "", parts[0], ""
	case 2:
		return parts[0], "", parts[1]
	}
	return parts[0], parts[1], strings.ReplaceAll(parts[2], "\t", " ")
}

// OutlineEntry is one heading of the document: its level (1 to 3), its plain text and the index of its
// block in the Snapshot.
type OutlineEntry struct {
	Level int
	Text  string
	Block int
}

// Outline lists the document's headings in order, which a table of contents is made from. A heading with
// no text is left out.
func (s Snapshot) Outline() []OutlineEntry {
	var out []OutlineEntry
	for i, b := range s.Blocks {
		if b.Kind < Heading1 || b.Kind > Heading3 {
			continue
		}
		var sb strings.Builder
		for _, seg := range b.Segments {
			if seg.SoftBreak {
				sb.WriteByte(' ')
				continue
			}
			if seg.Format.Change == Deleted {
				continue
			}
			sb.WriteString(seg.Text)
		}
		if text := strings.Join(strings.Fields(sb.String()), " "); text != "" {
			out = append(out, OutlineEntry{Level: int(b.Kind-Heading1) + 1, Text: text, Block: i})
		}
	}
	return out
}
