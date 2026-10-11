package export

import (
	"strings"
	"unicode/utf8"
)

// PlainText renders a Snapshot to plain text -- the simplest format by
// design: no concept of headings, lists, quotes, code fences, or
// inline formatting (bold/italic/links/...), so every one of those is
// dropped outright rather than approximated with some plain-text
// stand-in syntax, leaving just each block's own characters and a
// blank line between blocks. A soft line break becomes a plain newline
// (plain text has no separate "hard break" concept the way Markdown's
// own two-trailing-spaces convention needs) -- including inside a code
// block, unlike every other format here: PlainText applies this
// conversion uniformly across every block kind, it never special-cases
// Code the way Markdown/HTML/RTF/DOCX's own verbatim-reconstruction
// does.
type PlainText struct{}

func (PlainText) Export(snap Snapshot) ([]byte, error) {
	var sb strings.Builder
	for i, b := range snap.Blocks {
		switch b.Kind {
		case Image:
			if b.ImageCaption != "" {
				sb.WriteString("[Image] " + b.ImageCaption + "\n\n")
			} else if b.ImageAlt != "" {
				sb.WriteString("[Image: " + b.ImageAlt + "]\n\n")
			} else {
				sb.WriteString("[Image]\n\n")
			}
			continue
		case TableCell:
			// See Markdown's own TableCell case (markdown.go) for the
			// same "read the whole table out of snap.Blocks[i:i+n] the
			// moment this loop reaches its own first cell" shape --
			// renderPlainTextTable's own doc comment has the ASCII-grid
			// specifics.
			if b.TableRow == 0 && b.TableCol == 0 {
				n := b.TableRows * b.TableCols
				if n > 0 && i+n <= len(snap.Blocks) {
					sb.WriteString(renderPlainTextTable(snap.Blocks[i : i+n]))
				} else {
					sb.WriteString("[Table]\n\n")
				}
			}
			continue
		}
		sb.WriteString(plainTextInline(b.Segments) + "\n\n")
	}
	return []byte(strings.TrimRight(sb.String(), "\n") + "\n"), nil
}

// renderPlainTextTable renders cells -- one table's own full row-major
// TableRows*TableCols cell slice -- as a plain ASCII box-drawn grid
// ("+---+---+\n| a | b |\n+---+---+\n..."), a separator line both
// above and below every row rather than singling out row 0 as a
// header the way Markdown/HTML do: PlainText "has no concept of
// headings" at all by design (this file's own doc comment), so every
// row stays visually identical here too. Column widths come from the
// widest cell text actually in that column (a covered cell renders as
// an empty string, the same "no colspan/rowspan representation"
// simplification renderMarkdownTable's own doc comment explains for
// GFM -- ASCII art has no such concept either).
//
// A cell's own soft break becomes " / " rather than a real newline --
// the one deliberate exception to this format's usual "every soft
// break becomes a plain newline, uniformly" rule (plainTextInline's
// own doc comment): a real newline mid-cell would grow that table row
// to multiple physical lines, misaligning every column after it
// unless this function also tracked and padded per-row line counts,
// real complexity for a plain-text fallback format.
func renderPlainTextTable(cells []Block) string {
	rows, cols := cells[0].TableRows, cells[0].TableCols
	texts := make([]string, len(cells))
	for i, c := range cells {
		if !c.TableCovered {
			texts[i] = plainTextTableCellInline(c.Segments)
		}
	}

	colWidths := make([]int, cols)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			if w := utf8.RuneCountInString(texts[r*cols+c]); w > colWidths[c] {
				colWidths[c] = w
			}
		}
	}

	var sb strings.Builder
	separator := func() {
		sb.WriteString("+")
		for _, w := range colWidths {
			sb.WriteString(strings.Repeat("-", w+2) + "+")
		}
		sb.WriteString("\n")
	}
	separator()
	for r := 0; r < rows; r++ {
		sb.WriteString("|")
		for c := 0; c < cols; c++ {
			text := texts[r*cols+c]
			pad := colWidths[c] - utf8.RuneCountInString(text)
			sb.WriteString(" " + text + strings.Repeat(" ", pad) + " |")
		}
		sb.WriteString("\n")
		separator()
	}
	sb.WriteString("\n")
	return sb.String()
}

// plainTextTableCellInline concatenates segs' own text with no
// formatting applied, the same "content over presentation" treatment
// plainTextInline already gives every other block -- see
// renderPlainTextTable's own doc comment for why a soft break becomes
// " / " here instead of plainTextInline's usual real newline.
func plainTextTableCellInline(segs []Segment) string {
	var sb strings.Builder
	for _, seg := range segs {
		if seg.SoftBreak {
			sb.WriteString(" / ")
			continue
		}
		sb.WriteString(seg.Text)
	}
	return sb.String()
}

// plainTextInline concatenates segs' own text with no formatting
// applied at all, converting a soft break to a real newline.
func plainTextInline(segs []Segment) string {
	var sb strings.Builder
	for _, seg := range segs {
		if seg.SoftBreak {
			sb.WriteByte('\n')
			continue
		}
		sb.WriteString(seg.Text)
	}
	return sb.String()
}
