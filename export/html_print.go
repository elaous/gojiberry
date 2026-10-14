package export

import (
	"fmt"
	"strings"
)

// The HTML export prints well: a print stylesheet sets the sheet and its margins, keeps headings with
// the text after them, keeps tables rows, figures and code from splitting, and puts the running header
// and footer in the page margin boxes (@page rules, which current Chromium prints and Firefox and Safari
// ignore: the document is the same without them). The table of contents links to the headings; where
// the print engine supports target-counter (paged media tools such as Prince or WeasyPrint) it also
// shows the page numbers, which a browser's own layout does not know.

// tocAnchor is the id of the heading at block index i.
func tocAnchor(i int) string { return fmt.Sprintf("toc-%d", i) }

// cssString writes s as a CSS string literal.
func cssString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\' || r == '"':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case r == '<' || r == '>' || r == '&':
			fmt.Fprintf(&sb, "\\%X ", r) // never a tag or entity, whatever the style element is in
		case r < 0x20 || r == 0x7f:
			sb.WriteByte(' ')
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// cssContent is the value of a margin box's content property for a header or footer text: the text
// pieces as strings and the fields as counters.
func cssContent(text, title string) string {
	if text == "" {
		return ""
	}
	var parts []string
	rest := text
	for rest != "" {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			parts = append(parts, cssString(rest))
			break
		}
		if i > 0 {
			parts = append(parts, cssString(rest[:i]))
		}
		rest = rest[i:]
		switch {
		case strings.HasPrefix(rest, fieldPage):
			parts = append(parts, "counter(page)")
			rest = rest[len(fieldPage):]
		case strings.HasPrefix(rest, fieldPages):
			parts = append(parts, "counter(pages)")
			rest = rest[len(fieldPages):]
		case strings.HasPrefix(rest, fieldTitle):
			parts = append(parts, cssString(title))
			rest = rest[len(fieldTitle):]
		default:
			parts = append(parts, cssString("{"))
			rest = rest[1:]
		}
	}
	return strings.Join(parts, " ")
}

// htmlPrintStyle is the print stylesheet of an HTML page.
// A snapshot with no sheet (a hand-made one that sets no page size) gets none: there is nothing to print on.
func htmlPrintStyle(snap Snapshot) string {
	if snap.PageWidth <= 0 || snap.PageHeight <= 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<style>\n")
	sb.WriteString(fmt.Sprintf("@page{size:%gpx %gpx;margin:%gpx %gpx %gpx %gpx;", snap.PageWidth, snap.PageHeight, snap.MarginTop, snap.MarginRight, snap.MarginBottom, snap.MarginLeft))
	boxes := func(head, foot string) string {
		var sb strings.Builder
		for _, side := range []struct{ name, text string }{{"top", head}, {"bottom", foot}} {
			l, c, r := decorParts(side.text)
			for _, box := range []struct{ pos, text string }{{"left", l}, {"center", c}, {"right", r}} {
				if content := cssContent(box.text, snap.Title); content != "" {
					fmt.Fprintf(&sb, "@%s-%s{content:%s;font-size:9pt;color:#555}", side.name, box.pos, content)
				}
			}
		}
		return sb.String()
	}
	sb.WriteString(boxes(snap.Header, snap.Footer))
	sb.WriteString("}\n")
	if snap.EvenDiffers {
		// Even pages are the left-hand ones when the first page is on the right (left-to-right text).
		fmt.Fprintf(&sb, "@page :left{%s%s}\n", clearBoxes(), boxes(snap.EvenHeader, snap.EvenFooter))
	}
	if snap.FirstDiffers {
		// The first page names every box again: the ones it does not use are emptied, so none of the running text carries over.
		fmt.Fprintf(&sb, "@page :first{%s%s}\n", clearBoxes(), boxes(snap.FirstHeader, snap.FirstFooter))
	}
	sb.WriteString("@media print{" +
		"h1,h2,h3{break-after:avoid}" +
		"p,li,blockquote{orphans:3;widows:3}" +
		"pre,figure,img,tr{break-inside:avoid}" +
		"thead{display:table-header-group;break-after:avoid}" +
		"tbody tr:last-child{break-before:avoid}" +
		"nav.toc{break-after:page}" +
		"nav.toc a{color:inherit;text-decoration:none}" +
		"nav.toc a::after{content:leader('.') target-counter(attr(href),page)}" +
		"nav.toc-plain a::after{content:leader(' ') target-counter(attr(href),page)}" +
		"}\n")
	sb.WriteString("nav.toc ol{list-style:none;padding-left:0}nav.toc .toc-l2{margin-left:1.5em}nav.toc .toc-l3{margin-left:3em}\n")
	sb.WriteString("</style>\n")
	return sb.String()
}

// htmlTOC is the table of contents of an HTML page, or nothing when the snapshot has none or no headings.
func htmlTOC(snap Snapshot) string {
	if !snap.TOC {
		return ""
	}
	entries := snap.Outline()
	if len(entries) == 0 {
		return ""
	}
	var sb strings.Builder
	title := escapeHTMLText(snap.tocTitle())
	class := "toc"
	if snap.TOCStyle == "plain" {
		class = "toc toc-plain"
	}
	sb.WriteString(`<nav class="` + class + `" aria-label="` + escapeHTMLAttr(snap.tocTitle()) + `">` + "\n<h2>" + title + "</h2>\n<ol>\n")
	for _, e := range entries {
		fmt.Fprintf(&sb, "<li class=\"toc-l%d\"><a href=\"#%s\">%s</a></li>\n", e.Level, tocAnchor(e.Block), escapeHTMLText(e.Text))
	}
	sb.WriteString("</ol>\n</nav>\n")
	return sb.String()
}

// clearBoxes empties the six margin boxes of a page rule, so the running text of the other pages does not carry over.
func clearBoxes() string {
	var sb strings.Builder
	for _, n := range []string{"top-left", "top-center", "top-right", "bottom-left", "bottom-center", "bottom-right"} {
		fmt.Fprintf(&sb, "@%s{content:none}", n)
	}
	return sb.String()
}
