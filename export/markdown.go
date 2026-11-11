package export

import (
	"strconv"
	"strings"
)

// Markdown renders a Snapshot to Markdown.
type Markdown struct{}

func (Markdown) Export(snap Snapshot) ([]byte, error) {
	var sb strings.Builder
	for i, b := range snap.Blocks {
		switch b.Kind {
		case Image:
			alt := b.ImageCaption
			if b.ImageAlt != "" {
				alt = b.ImageAlt
			}
			sb.WriteString("![" + escapeMarkdownText(alt) + "](" + b.ImageSrc + ")\n\n")
			if b.ImageCaption != "" {
				sb.WriteString("*" + escapeMarkdownText(b.ImageCaption) + "*\n\n")
			}
			continue
		case TableCell:
			if b.TableRow == 0 && b.TableCol == 0 {
				n := b.TableRows * b.TableCols
				if n > 0 && i+n <= len(snap.Blocks) {
					sb.WriteString(renderMarkdownTable(snap.Blocks[i : i+n]))
				} else {
					sb.WriteString("[Table]\n\n")
				}
			}
			continue
		case Code:
			sb.WriteString("```" + cleanCodeLang(b.CodeLang) + "\n" + b.Segments[0].Text + "\n```\n\n")
			continue
		}

		if b.Kind == Heading2 && b.Start < 0 && i+1 < len(snap.Blocks) && snap.Blocks[i+1].Note > 0 {
			continue
		}
		var prefix string
		switch b.Kind {
		case Heading1:
			prefix = "# "
		case Heading2:
			prefix = "## "
		case Heading3:
			prefix = strings.Repeat("#", b.HeadingLevel()) + " "
		case BulletItem:
			prefix = strings.Repeat("  ", b.Level) + "- "
		case NumberedItem:
			prefix = strings.Repeat("  ", b.Level) + strconv.Itoa(b.NumberedIndex) + ". "
		case Quote:
			prefix = "> "
		}
		if b.Note > 0 {
			prefix = "[^" + strconv.Itoa(b.Note) + "]: " // a footnote definition; the renderer links it both ways
		}

		content := markdownInline(b.Segments)
		if b.Kind == Quote {
			content = strings.ReplaceAll(content, "\n", "\n> ")
		}
		sb.WriteString(prefix + content + "\n\n")
	}
	return []byte(strings.TrimRight(sb.String(), "\n") + "\n"), nil
}

func renderMarkdownTable(cells []Block) string {
	rows, cols := cells[0].TableRows, cells[0].TableCols
	var sb strings.Builder
	for r := 0; r < rows; r++ {
		sb.WriteString("|")
		for c := 0; c < cols; c++ {
			cell := cells[r*cols+c]
			var text string
			if !cell.TableCovered {
				text = markdownTableCellInline(cell.Segments)
			}
			sb.WriteString(" " + text + " |")
		}
		sb.WriteString("\n")
		if r == 0 {
			sb.WriteString("|")
			for c := 0; c < cols; c++ {
				sb.WriteString(" --- |")
			}
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n")
	return sb.String()
}

func markdownTableCellInline(segs []Segment) string {
	var sb strings.Builder
	for _, seg := range segs {
		if seg.SoftBreak {
			sb.WriteString("<br>")
			continue
		}
		text := strings.ReplaceAll(escapeMarkdownText(seg.Text), "|", `\|`)
		sb.WriteString(wrapMarkdownRun(text, seg.Format))
	}
	return sb.String()
}

func markdownInline(segs []Segment) string {
	var sb strings.Builder
	for _, seg := range segs {
		if seg.SoftBreak {
			sb.WriteString("  \n")
			continue
		}
		sb.WriteString(wrapMarkdownRun(escapeMarkdownText(seg.Text), seg.Format))
	}
	return sb.String()
}

func wrapMarkdownRun(text string, f Format) string {
	if text == "" {
		return text
	}
	if f.NoteRef > 0 {
		return "[^" + strconv.Itoa(f.NoteRef) + "]" // a footnote mark: the note below has the same label
	}
	if f.Href != "" && safeHref(f.Href) {
		text = "[" + text + "](" + f.Href + ")"
	}
	if f.Underline {
		text = "<u>" + text + "</u>"
	}
	if f.Superscript {
		text = "<sup>" + text + "</sup>"
	}
	if f.Subscript {
		text = "<sub>" + text + "</sub>"
	}
	if f.Strikethrough {
		text = "~~" + text + "~~"
	}
	if f.Italic {
		text = "*" + text + "*"
	}
	if f.Bold {
		text = "**" + text + "**"
	}
	return text
}

func escapeMarkdownText(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case '\\', '*', '_', '`', '[', ']':
			sb.WriteByte('\\')
		}
		sb.WriteRune(r)
	}
	return sb.String()
}
