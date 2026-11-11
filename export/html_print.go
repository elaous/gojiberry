package export

import (
	"fmt"
	"strings"
)

func tocAnchor(i int) string { return fmt.Sprintf("toc-%d", i) }

func cssString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\' || r == '"':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case r == '<' || r == '>' || r == '&':
			fmt.Fprintf(&sb, "\\%X ", r)
		case r < 0x20 || r == 0x7f:
			sb.WriteByte(' ')
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

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
		fmt.Fprintf(&sb, "@page :left{%s%s}\n", clearBoxes(), boxes(snap.EvenHeader, snap.EvenFooter))
	}
	if snap.FirstDiffers {
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

func clearBoxes() string {
	var sb strings.Builder
	for _, n := range []string{"top-left", "top-center", "top-right", "bottom-left", "bottom-center", "bottom-right"} {
		fmt.Fprintf(&sb, "@%s{content:none}", n)
	}
	return sb.String()
}

func htmlTOCAtStart(snap Snapshot) string {
	if snap.TOCAt > 0 && len(snap.Blocks) > 0 {
		return ""
	}
	return htmlTOC(snap)
}
