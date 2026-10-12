package export

import (
	"fmt"
	"strconv"
	"strings"
)

// RTF renders a Snapshot to RTF -- the last plain-text format written
// by hand: RTF is fundamentally ASCII text with backslash "control
// words" rather than a binary format, so no third-party dependency is
// needed. Each block becomes its own `{\pard ... \par}` group -- RTF
// scopes a formatting control word (`\b`, `\i`, ...) to the enclosing
// `{}` group, so closing a block's own group already resets its
// formatting for whatever comes after, with no explicit "\b0"-style
// reset needed. Unlike Markdown/HTML, list items get no real nesting
// or native list numbering (RTF's own `\pn` list machinery is real
// complexity this editor's bullet/numbered blocks don't need): a
// bullet item is a hanging-indent paragraph with a literal `\bullet`
// character, and a numbered item writes its own literal "N." the same
// way Markdown already does, both indented by Level. Image blocks
// don't embed the actual image data (real RTF image embedding is a
// hex dump of the raw decoded bytes plus width/height in twips -- a
// meaningfully separate feature, not attempted here) and fall back to
// the same "[Image]" placeholder PlainText uses.
type RTF struct{}

func (RTF) Export(snap Snapshot) ([]byte, error) {
	contentWidthTwips := rtfPxToTwips(snap.PageWidth - snap.MarginLeft - snap.MarginRight)
	var body strings.Builder
	for i, b := range snap.Blocks {
		switch b.Kind {
		case Image:
			caption := ""
			if b.ImageCaption != "" {
				caption = " " + escapeRTFText(b.ImageCaption)
			}
			body.WriteString(`{\pard\i [Image]` + caption + `\par}` + "\n")
			continue
		case TableCell:
			// See Markdown's own TableCell case (markdown.go) for the
			// same "read the whole table out of snap.Blocks[i:i+n] the
			// moment this loop reaches its own first cell" shape --
			// renderRTFTable's own doc comment has the RTF-table
			// specifics, including real \clmgf/\clmrg/\clvmgf/\clvmrg
			// merge support (unlike Markdown/PlainText's own disclosed
			// "no colspan/rowspan representation" gap).
			if b.TableRow == 0 && b.TableCol == 0 {
				n := b.TableRows * b.TableCols
				if n > 0 && i+n <= len(snap.Blocks) && contentWidthTwips > 0 {
					body.WriteString(renderRTFTable(snap.Blocks[i:i+n], contentWidthTwips))
				} else {
					body.WriteString(`{\pard\i [Table]\par}` + "\n")
				}
			}
			continue
		case Code:
			// \f1 (the fonttbl's own monospace entry) for the verbatim
			// text -- no inline-run formatting or escaping-of-syntax,
			// the same "code is content, not markup" treatment every
			// other format's own code-block case already takes.
			body.WriteString(`{\pard\f1 ` + escapeRTFText(rawCodeText(b.Segments)) + `\par}` + "\n")
			continue
		}

		var pre string
		switch b.Kind {
		case Heading1:
			pre = `{\pard\sa200\b\fs36 `
		case Heading2:
			pre = `{\pard\sa200\b\fs32 `
		case Heading3:
			pre = `{\pard\sa200\b\fs28 `
		case BulletItem:
			pre = `{\pard\fi-240\li` + strconv.Itoa(360*(b.Level+1)) + `\bullet\tab `
		case NumberedItem:
			pre = `{\pard\fi-240\li` + strconv.Itoa(360*(b.Level+1)) + " " +
				strconv.Itoa(b.NumberedIndex) + `.\tab `
		case Quote:
			pre = `{\pard\li720\i `
		default:
			pre = `{\pard `
		}
		body.WriteString(pre + rtfInline(b.Segments) + `\par}` + "\n")
	}
	// The sheet and margins, and the running header and footer (the page number a PAGE field and the
	// page count a NUMPAGES field).
	layout := ""
	if snap.PageWidth > 0 && snap.PageHeight > 0 {
		layout = fmt.Sprintf(`\paperw%d\paperh%d\margl%d\margr%d\margt%d\margb%d`+"\n",
			rtfPxToTwips(snap.PageWidth), rtfPxToTwips(snap.PageHeight), rtfPxToTwips(snap.MarginLeft), rtfPxToTwips(snap.MarginRight), rtfPxToTwips(snap.MarginTop), rtfPxToTwips(snap.MarginBottom))
	}
	if snap.Header != "" {
		layout += rtfDecor("header", snap.Header, snap.Title, contentWidthTwips) + "\n"
	}
	if snap.Footer != "" {
		layout += rtfDecor("footer", snap.Footer, snap.Title, contentWidthTwips) + "\n"
	}
	return []byte(`{\rtf1\ansi\deff0{\fonttbl{\f0 Helvetica;}{\f1 Courier New;}}\fs24` + "\n" +
		layout + body.String() + "}\n"), nil
}

// rtfDecor is a header or footer group ("header" or "footer") for a header text with fields: the left,
// centre and right parts sit on tab stops at the middle and the right edge of the text.
func rtfDecor(group, text, title string, width int) string {
	l, c, r := decorParts(text)
	part := func(s string) string {
		var sb strings.Builder
		for rest := s; rest != ""; {
			i := strings.IndexByte(rest, '{')
			if i < 0 {
				sb.WriteString(escapeRTFText(rest))
				break
			}
			sb.WriteString(escapeRTFText(rest[:i]))
			rest = rest[i:]
			switch {
			case strings.HasPrefix(rest, fieldPage):
				sb.WriteString(`{\field{\*\fldinst PAGE}{\fldrslt 1}}`)
				rest = rest[len(fieldPage):]
			case strings.HasPrefix(rest, fieldPages):
				sb.WriteString(`{\field{\*\fldinst NUMPAGES}{\fldrslt 1}}`)
				rest = rest[len(fieldPages):]
			case strings.HasPrefix(rest, fieldTitle):
				sb.WriteString(escapeRTFText(title))
				rest = rest[len(fieldTitle):]
			default:
				sb.WriteString(escapeRTFText("{"))
				rest = rest[1:]
			}
		}
		return sb.String()
	}
	return fmt.Sprintf(`{\%s\pard\tqc\tx%d\tqr\tx%d\fs18 `, group, width/2, width) + part(l) + `\tab ` + part(c) + `\tab ` + part(r) + `\par}`
}

// rtfPxToTwips converts one of Snapshot's own CSS-px geometry values
// to RTF's own twip unit (1/1440 inch, RTF's universal length unit for
// everything from cell boundaries to indents) -- 96 CSS px per inch is
// the same 96dpi convention pxToPt (pdf.go) already assumes for its
// own px-to-pt conversion, so 1440/96 = 15 twips per px.
func rtfPxToTwips(px float64) int {
	return int(px * 15)
}

// renderRTFTable renders cells -- one table's own full row-major
// TableRows*TableCols cell slice -- as a real RTF table: one
// `\trowd`/`\row` group per row, `\cellx<N>` cell boundaries (N
// cumulative twips from the row's own left edge, contentWidthTwips
// split evenly across columns -- Snapshot carries no per-column width
// fractions to preserve instead, the same equal-split simplification
// renderPlainTextTable's own doc comment already takes), and real
// \clmgf/\clmrg/\clvmgf/\clvmrg merge control words for a cell whose
// own tableMergeGrid entry (tablehelpers.go) spans more than one row
// or column -- unlike
// Markdown/PlainText, RTF's own table model supports cell merging
// natively, so this doesn't need to degrade a merge to a blank
// covered cell. Every `\cellx` still needs a matching `\cell` in the
// row body regardless of merge role (Word expects one `\cell` per
// declared column even for a merged-away continuation cell), so a
// covered cell contributes an empty `\pard\intbl\cell` rather than
// being skipped outright.
func renderRTFTable(cells []Block, contentWidthTwips int) string {
	rows, cols := cells[0].TableRows, cells[0].TableCols
	info := tableMergeGrid(cells)
	colWidth := contentWidthTwips / cols

	var sb strings.Builder
	for r := 0; r < rows; r++ {
		sb.WriteString(`\trowd\trgaph108\trleft0`)
		x := 0
		for c := 0; c < cols; c++ {
			m := info[r*cols+c]
			if m.colSpan > 1 {
				if c == m.c0 {
					sb.WriteString(`\clmgf`)
				} else {
					sb.WriteString(`\clmrg`)
				}
			}
			if m.rowSpan > 1 {
				if r == m.r0 {
					sb.WriteString(`\clvmgf`)
				} else {
					sb.WriteString(`\clvmrg`)
				}
			}
			x += colWidth
			sb.WriteString(`\cellx` + strconv.Itoa(x))
		}
		sb.WriteString("\n")
		for c := 0; c < cols; c++ {
			cell := cells[r*cols+c]
			if cell.TableCovered {
				sb.WriteString(`\pard\intbl\cell` + "\n")
			} else {
				sb.WriteString(`\pard\intbl ` + rtfInline(cell.Segments) + `\cell` + "\n")
			}
		}
		sb.WriteString(`\row` + "\n")
	}
	return sb.String()
}

// rtfInline renders segs as RTF inline content, wrapping each run in
// RTF control words (wrapRTFRun) and RTF-escaping plain text
// (escapeRTFText). A soft line break becomes `\line`, RTF's own
// same-paragraph line break (distinct from `\par`, which starts a new
// paragraph).
func rtfInline(segs []Segment) string {
	var sb strings.Builder
	for _, seg := range segs {
		if seg.SoftBreak {
			sb.WriteString(`\line` + "\n")
			continue
		}
		sb.WriteString(wrapRTFRun(escapeRTFText(seg.Text), seg.Format))
	}
	return sb.String()
}

// wrapRTFRun wraps text in f's own formatting control words, each run
// in its own `{}` group (see RTF.Export's own doc comment for why
// that makes an explicit reset unnecessary). A link becomes a real
// RTF hyperlink field (`\field`/`\fldinst HYPERLINK`/`\fldrslt`) with
// the visible text as its own result, formatted the same way any
// other run's own Format would be -- so a bold link still renders
// bold.
func wrapRTFRun(text string, f Format) string {
	if text == "" {
		return text
	}
	formatted := wrapRTFRunFormatting(text, f)
	if f.Href != "" && safeHref(f.Href) {
		return `{\field{\*\fldinst HYPERLINK "` + escapeRTFText(f.Href) + `"}{\fldrslt ` + formatted + `}}`
	}
	return formatted
}

// wrapRTFRunFormatting wraps text in whichever of f's own bold/
// italic/underline/strikethrough/superscript control words apply,
// all inside one shared `{}` group -- RTF control words are
// space-terminated (or terminated by the group's own closing brace),
// so `\b \i text` inside one group is bold-and-italic together,
// unlike Markdown's own nested-nesting-per-attribute syntax.
func wrapRTFRunFormatting(text string, f Format) string {
	var prefix string
	if f.Bold {
		prefix += `\b `
	}
	if f.Italic {
		prefix += `\i `
	}
	if f.Underline {
		prefix += `\ul `
	}
	if f.Strikethrough {
		prefix += `\strike `
	}
	if f.Superscript {
		prefix += `\super `
	}
	if f.Subscript {
		prefix += `\sub `
	}
	if prefix == "" {
		return text
	}
	return "{" + prefix + text + "}"
}

// escapeRTFText escapes RTF's own three syntactically-significant
// ASCII characters (a literal backslash/brace would otherwise be
// parsed as the start of a control word or a group boundary) and
// encodes every non-ASCII rune as RTF's own `\uN?` Unicode escape --
// RTF is fundamentally a 7-bit ASCII format, so a literal "café" or
// "→" byte sequence isn't valid content at all, only ASCII text and
// backslash-escapes are. N is the codepoint as a signed 16-bit
// integer (RTF's own convention, however unintuitive for a codepoint
// past 32767) followed by a plain "?" fallback character for readers
// that don't understand \u at all -- editor's own rune-based buffer
// never stores a codepoint needing a surrogate pair, so \u alone is
// always sufficient here, no surrogate-pair case to handle.
func escapeRTFText(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r == '\\' || r == '{' || r == '}':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case r > 127:
			sb.WriteString(`\u` + strconv.Itoa(int(int16(r))) + `?`)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
