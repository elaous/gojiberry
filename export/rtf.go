package export

import (
	"fmt"
	"strconv"
	"strings"
)

// RTF renders a Snapshot to RTF.
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
	layout := ""
	if snap.PageWidth > 0 && snap.PageHeight > 0 {
		layout = fmt.Sprintf(`\paperw%d\paperh%d\margl%d\margr%d\margt%d\margb%d`+"\n",
			rtfPxToTwips(snap.PageWidth), rtfPxToTwips(snap.PageHeight), rtfPxToTwips(snap.MarginLeft), rtfPxToTwips(snap.MarginRight), rtfPxToTwips(snap.MarginTop), rtfPxToTwips(snap.MarginBottom))
	}
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

func rtfPxToTwips(px float64) int {
	return int(px * 15)
}

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

func wrapRTFRun(text string, f Format) string {
	if text == "" {
		return text
	}
	formatted := wrapRTFRunFormatting(text, f)
	if f.NoteRef > 0 {
		n := strconv.Itoa(f.NoteRef)
		return `{\*\bkmkstart _fnref` + n + `}{\field{\*\fldinst HYPERLINK \\l _fn` + n + `}{\fldrslt ` + formatted + `}}{\*\bkmkend _fnref` + n + `}`
	}
	if f.Href != "" && safeHref(f.Href) {
		return `{\field{\*\fldinst HYPERLINK "` + escapeRTFText(f.Href) + `"}{\fldrslt \cf2 ` + formatted + `}}`
	}
	return formatted
}

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

func rtfBodySize(th docTheme) string {
	if th.bodyPt > 0 {
		return strconv.Itoa(int(th.bodyPt*2 + 0.5))
	}
	return "24"
}

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
