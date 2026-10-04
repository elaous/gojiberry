package export

import (
	"strconv"
	"strings"
)

// Markdown renders a Snapshot to Markdown, one block at a time. Markdown
// was a natural first export format: this editor's own Markdown-shortcut
// input already establishes headings/bullets/numbered items/quotes/code
// blocks as "#"/"-"/"1."/">"/"```", so exporting is close to the inverse
// of that existing parse, not a new format to design from scratch.
// Block-level layout properties with no Markdown equivalent (alignment,
// indent, column count, line spacing) are intentionally dropped:
// Markdown captures structure and inline formatting, not visual layout,
// the same "content over presentation" line PlainText draws even more
// starkly.
type Markdown struct{}

func (Markdown) Export(snap Snapshot) ([]byte, error) {
	var sb strings.Builder
	for i, b := range snap.Blocks {
		switch b.Kind {
		case Image:
			// A BlockImage line's own text content is always empty --
			// the image itself is Block.ImageSrc, already a data: URL,
			// which Markdown's own image syntax can reference directly
			// with no separate asset step. The caption becomes the
			// image's own alt text, plus a separate italic line right
			// below it -- GitHub-flavored Markdown (and most other
			// renderers) never actually displays alt text, only a
			// tooltip/accessibility attribute, so an alt-only caption
			// would be invisible to a reader of the rendered document.
			sb.WriteString("![" + escapeMarkdownText(b.ImageCaption) + "](" + b.ImageSrc + ")\n\n")
			if b.ImageCaption != "" {
				sb.WriteString("*" + escapeMarkdownText(b.ImageCaption) + "*\n\n")
			}
			continue
		case TableCell:
			// A table spans multiple consecutive blocks (b.TableRows*
			// b.TableCols of them, the dense row-major grid
			// model.TableBoundsAt/retagTable maintain) -- rendered as a
			// whole GFM pipe table the moment this loop reaches the
			// table's own first cell (row 0, col 0), reading the rest
			// directly out of snap.Blocks[i:i+n] rather than trying to
			// accumulate them one at a time; every other cell in the
			// same table is then skipped outright by the `continue`
			// below; see renderMarkdownTable's own doc comment for the
			// "no colspan/rowspan in GFM" simplification a merged
			// table's own cells take here.
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
			// No inline-formatting or escaping inside a fenced code
			// block -- its content is verbatim by definition.
			sb.WriteString("```\n" + b.Segments[0].Text + "\n```\n\n")
			continue
		}

		var prefix string
		switch b.Kind {
		case Heading1:
			prefix = "# "
		case Heading2:
			prefix = "## "
		case Heading3:
			prefix = "### "
		case BulletItem:
			prefix = strings.Repeat("  ", b.Level) + "- "
		case NumberedItem:
			prefix = strings.Repeat("  ", b.Level) + strconv.Itoa(b.NumberedIndex) + ". "
		case Quote:
			prefix = "> "
		}

		content := markdownInline(b.Segments)
		if b.Kind == Quote {
			// A soft-broken line within a quote is still the same
			// quote -- continue the "> " marker on its own next line
			// too, or every line after the first would silently fall
			// out of the blockquote in a real Markdown renderer.
			content = strings.ReplaceAll(content, "\n", "\n> ")
		}
		sb.WriteString(prefix + content + "\n\n")
	}
	return []byte(strings.TrimRight(sb.String(), "\n") + "\n"), nil
}

// renderMarkdownTable renders cells -- one table's own full row-major
// TableRows*TableCols cell slice -- as a GFM pipe table: row 0 becomes
// the header row (mirroring model.TableCellColors' own "row 0 is
// always the header" convention), followed by the "|---|---|..."
// separator GFM requires, then every remaining row.
//
// GFM tables have no colspan/rowspan syntax at all, so a cell covered
// by another cell's own merge (TableCovered) renders as an empty
// column rather than repeating the owner's own content into it (which
// would misleadingly read as separate, duplicated data) -- a real,
// disclosed simplification: only the owner's own upper-left grid
// position keeps the merged content, the columns/rows it visually
// spanned in the live editor collapse to blank cells here. Column
// widths aren't preserved either (TableColWidths has no Markdown
// equivalent) -- every renderer pads/sizes GFM table columns by its
// own content-width heuristic regardless of what's written in the
// source, so there'd be nothing to preserve it *for*.
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

// markdownTableCellInline is markdownInline's own table-cell
// counterpart: a GFM table row is exactly one line, so a soft break
// inside a cell becomes a literal "<br>" (the same "no native syntax,
// fall back to inline HTML" fallback wrapMarkdownRun's own Underline/
// Superscript/Subscript cases already take) rather than
// markdownInline's own two-trailing-spaces-then-newline convention,
// which would silently break the table's own row structure. A literal
// "|" is additionally backslash-escaped -- GFM's own column
// delimiter, on top of escapeMarkdownText's usual set.
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

// markdownInline renders segs as Markdown inline content, wrapping
// each run in whatever syntax its own formatting needs. A soft line
// break becomes Markdown's own hard-break convention (two trailing
// spaces then a newline) rather than ending the block outright, since
// it's still the same paragraph.
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

// wrapMarkdownRun wraps text in f's own formatting syntax. Bold and
// italic nest outermost-in as **bold** / *italic* the same order
// every common Markdown flavor expects for combined emphasis
// (***bold italic***); Underline/Superscript/Subscript have no native
// Markdown syntax at all, so they fall back to inline HTML the way
// GitHub's own Markdown renderer (and most others) already accept
// passed through unescaped.
func wrapMarkdownRun(text string, f Format) string {
	if text == "" {
		return text
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

// escapeMarkdownText backslash-escapes characters Markdown would
// otherwise parse as syntax, so the user's own typed content (e.g. a
// literal "*" or "[") round-trips as plain text rather than silently
// becoming emphasis or a broken link when the exported file is later
// rendered.
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
