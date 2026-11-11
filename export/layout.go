package export

import (
	"strconv"
	"strings"
)

const defaultTOCTitle = "Contents"

func (s Snapshot) tocTitle() string {
	if s.TOCTitle != "" {
		return s.TOCTitle
	}
	return defaultTOCTitle
}

const (
	fieldPage  = "{page}"
	fieldPages = "{pages}"
	fieldTitle = "{title}"
)

// FillFields replaces the {page}, {pages} and {title} fields.
func FillFields(text string, page, pages int, title string) string {
	return strings.NewReplacer(fieldPage, strconv.Itoa(page), fieldPages, strconv.Itoa(pages), fieldTitle, title).Replace(text)
}

// RunningParts gives a page's running text as three parts.
func RunningParts(text string, page, pages int, title string) [3]string {
	l, c, r := decorParts(text)
	return [3]string{FillFields(l, page, pages, title), FillFields(c, page, pages, title), FillFields(r, page, pages, title)}
}

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

// OutlineEntry is one heading: level, text and block index.
type OutlineEntry struct {
	Level int
	Text  string
	Block int
}

// Outline lists the document's headings in order.
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
		if s.TOCDepth > 0 && b.HeadingLevel() > s.TOCDepth {
			continue
		}
		if text := strings.Join(strings.Fields(sb.String()), " "); text != "" {
			out = append(out, OutlineEntry{Level: b.HeadingLevel(), Text: text, Block: i})
		}
	}
	return out
}

func keepRowWithNext(r, rows int) bool {
	return rows > 1 && (r == 0 || r == rows-2)
}

func (s Snapshot) tocHere(i int) bool { return s.TOC && s.TOCAt == i }
func (s Snapshot) tocEnd() bool       { return s.TOC && s.TOCAt >= len(s.Blocks) }

func tocMarks(snap Snapshot, name func(block int) string) map[int]string {
	marks := map[int]string{}
	if !snap.TOC {
		return marks
	}
	for _, e := range snap.Outline() {
		marks[e.Block] = name(e.Block)
	}
	return marks
}
