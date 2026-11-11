package export

import (
	"strings"
	"unicode/utf8"
)

// PlainText renders a Snapshot to plain text.
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
