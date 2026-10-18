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
	marks := tocMarks(snap, func(block int) string { return "_Toc" + strconv.Itoa(100000+block) })
	for i, b := range snap.Blocks {
		if snap.tocHere(i) {
			rtfContents(&body, snap, contentWidthTwips, marks)
		}
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
			switch b.HeadingLevel() {
			case 4:
				pre = `{\pard\sa200\b\fs26 `
			case 5:
				pre = `{\pard\sa200\b\fs24 `
			case 6:
				pre = `{\pard\sa200\b\i\fs24 `
			default:
				pre = `{\pard\sa200\b\fs28 `
			}
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
		inner := rtfInline(b.Segments)
		if b.Note > 0 {
			// a footnote: the mark in the text links here (bookmark _fnN), and a link at its end goes back to the mark
			n := strconv.Itoa(b.Note)
			inner = `{\*\bkmkstart _fn` + n + `}` + inner + `{\*\bkmkend _fn` + n + `} ` +
				`{\field{\*\fldinst HYPERLINK \\l _fnref` + n + `}{\fldrslt \u8617?}}`
		}
		if name, ok := marks[i]; ok {
			inner = `{\*\bkmkstart ` + name + `}` + inner + `{\*\bkmkend ` + name + `}`
		}
		body.WriteString(pre + inner + `\par}` + "\n")
	}
	if snap.tocEnd() {
		rtfContents(&body, snap, contentWidthTwips, marks)
	}
	// The sheet and margins, and the running header and footer (the page number a PAGE field and the
	// page count a NUMPAGES field).
	layout := ""
	if snap.PageWidth > 0 && snap.PageHeight > 0 {
		layout = fmt.Sprintf(`\paperw%d\paperh%d\margl%d\margr%d\margt%d\margb%d`+"\n",
			rtfPxToTwips(snap.PageWidth), rtfPxToTwips(snap.PageHeight), rtfPxToTwips(snap.MarginLeft), rtfPxToTwips(snap.MarginRight), rtfPxToTwips(snap.MarginTop), rtfPxToTwips(snap.MarginBottom))
	}
	// With facing pages, \headerr and \footerr are the odd pages' and \headerl and \footerl the even pages'.
	oddH, oddF := "header", "footer"
	if snap.EvenDiffers {
		layout += `\facingp` + "\n"
		oddH, oddF = "headerr", "footerr"
		if snap.EvenHeader != "" {
			layout += rtfDecor("headerl", snap.EvenHeader, snap.Title, contentWidthTwips) + "\n"
		}
		if snap.EvenFooter != "" {
			layout += rtfDecor("footerl", snap.EvenFooter, snap.Title, contentWidthTwips) + "\n"
		}
	}
	if snap.Header != "" {
		layout += rtfDecor(oddH, snap.Header, snap.Title, contentWidthTwips) + "\n"
	}
	if snap.Footer != "" {
		layout += rtfDecor(oddF, snap.Footer, snap.Title, contentWidthTwips) + "\n"
	}
	if snap.FirstDiffers {
		// \titlepg gives the first page its own header and footer (\headerf, \footerf): bare when empty.
		layout += `\titlepg` + "\n"
		if snap.FirstHeader != "" {
			layout += strings.Replace(rtfDecor("header", snap.FirstHeader, snap.Title, contentWidthTwips), `{\header`, `{\headerf`, 1) + "\n"
		}
		if snap.FirstFooter != "" {
			layout += strings.Replace(rtfDecor("footer", snap.FirstFooter, snap.Title, contentWidthTwips), `{\footer`, `{\footerf`, 1) + "\n"
		}
	}
	th := themeOf(snap)
	head, start := rtfThemeHead(th)
	return []byte(`{\rtf1\ansi\deff0{\fonttbl{\f0 Helvetica;}{\f1 Courier New;}}` + head + `\fs` + rtfBodySize(th) + start + "\n" +
		layout + body.String() + "}\n"), nil
}

// rtfContents writes the table of contents that starts the document (when the snapshot asks for one and has
// headings): the title, then an entry for each heading, linked to a bookmark on it, with a PAGEREF field for
// its page. Word fills the page numbers in when it opens the file (the fields are marked dirty); the cached
// number is a dash. The bookmark name of each heading (by block index) is in marks, made beforehand (tocMarks).
func rtfContents(body *strings.Builder, snap Snapshot, width int, marks map[int]string) {
	if !snap.TOC {
		return
	}
	entries := snap.Outline()
	if len(entries) == 0 {
		return
	}
	if width <= 0 {
		width = 9000
	}
	body.WriteString(`{\pard\sa240\keepn\b\fs40 ` + escapeRTFText(snap.tocTitle()) + `\par}` + "\n")
	for _, e := range entries {
		name := marks[e.Block]
		leader := `\tldot`
		if snap.TOCStyle == "plain" {
			leader = ""
		}
		fmt.Fprintf(body, `{\pard\sa60\li%d\tqr%s\tx%d `, (e.Level-1)*360, leader, width)
		body.WriteString(`{\field\flddirty{\*\fldinst HYPERLINK \\l ` + name + `}{\fldrslt ` + escapeRTFText(e.Text) + `}}\tab `)
		body.WriteString(`{\field\flddirty{\*\fldinst PAGEREF ` + name + ` \\h}{\fldrslt \endash}}\par}` + "\n")
	}
	body.WriteString(`{\pard\page\par}` + "\n")
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
	colWidths := tableColumnWidths(cells, float64(contentWidthTwips), 15) // a CSS pixel is 15 twips

	var sb strings.Builder
	for r := 0; r < rows; r++ {
		sb.WriteString(`\trowd\trgaph108\trleft0\trkeep`) // \trkeep: the row is not split across pages
		if h := cells[r*cols].TableRowH; h > 0 {
			sb.WriteString(`\trrh` + strconv.Itoa(int(h*15))) // at least this high
		}
		keep := ""
		if keepRowWithNext(r, rows) {
			keep = `\keepn` // the paragraphs of the row stay with the next row
		}
		x := 0.0
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
			x += colWidths[c]
			sb.WriteString(`\cellx` + strconv.Itoa(int(x)))
		}
		sb.WriteString("\n")
		for c := 0; c < cols; c++ {
			cell := cells[r*cols+c]
			if cell.TableCovered {
				sb.WriteString(`\pard\intbl` + keep + `\cell` + "\n")
			} else {
				sb.WriteString(`\pard\intbl` + keep + ` ` + rtfInline(cell.Segments) + `\cell` + "\n")
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
	if f.NoteRef > 0 {
		// the mark of a footnote: a link to the note (bookmark _fnN), itself the target of the note's way back
		n := strconv.Itoa(f.NoteRef)
		return `{\*\bkmkstart _fnref` + n + `}{\field{\*\fldinst HYPERLINK \\l _fn` + n + `}{\fldrslt ` + formatted + `}}{\*\bkmkend _fnref` + n + `}`
	}
	if f.Href != "" && safeHref(f.Href) {
		return `{\field{\*\fldinst HYPERLINK "` + escapeRTFText(f.Href) + `"}{\fldrslt \cf2 ` + formatted + `}}`
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

// rtfBodySize is the default font size in half-points: 24 (12pt), or the theme's body size.
func rtfBodySize(th docTheme) string {
	if th.bodyPt > 0 {
		return strconv.Itoa(int(th.bodyPt*2 + 0.5))
	}
	return "24"
}

// rtfThemeHead is the colour table (text is colour 1, links colour 2: black and the usual blue unless the
// theme names them) and the control word that starts the text in colour 1, which only a theme with a text
// colour writes. Link results are set in colour 2.
func rtfThemeHead(th docTheme) (table, start string) {
	text, link := th.text, th.link
	if text == "" {
		text = "000000"
	}
	if link == "" {
		link = "0563C1"
	}
	cw := func(hex string) string {
		r, g, b := rgb(hex)
		return fmt.Sprintf(`\red%d\green%d\blue%d;`, r, g, b)
	}
	table = `{\colortbl;` + cw(text) + cw(link) + `}`
	if th.text != "" {
		start = `\cf1`
	}
	return table, start
}
