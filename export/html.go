package export

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// HTML renders a Snapshot to a standalone HTML document. Unlike
// Markdown/PlainText, bullet/numbered lists need *real* nested
// <ul>/<ol> markup (a browser or any other HTML consumer expects
// valid nesting, not Markdown's flat "  - " indent convention), so
// this walks blocks with a stack of currently-open list tags -- one
// entry per nesting depth -- instead of Markdown's simple per-line
// prefix: openList/closeList/closeItem below are the standard
// flat-leveled-items-to-nested-list machinery.
type HTML struct {
	// Theme is design-token CSS (the output of tokens.Set.CSS: a :root rule of --gj-color-*,
	// --gj-font-* and --gj-space-* custom properties). When it is set the document carries it in a
	// <style> and its text, page and links use the tokens, so the exported file looks like the
	// editor's theme. It is checked for anything that could end the style element or open a rule
	// of its own, and the export fails rather than write it.
	Theme string
}

func (h HTML) Export(snap Snapshot) ([]byte, error) {
	if h.Theme != "" && !safeThemeCSS(h.Theme) {
		return nil, errors.New("export: the HTML theme is not a plain :root block of custom properties")
	}
	return []byte(renderHTMLDocument(snap, false, h.Theme)), nil
}

// themeRules use the tokens with a fallback each, so a theme that leaves one out still renders.
const themeRules = "body{color:var(--gj-color-text,#1a1a1a);background:var(--gj-color-background,#fff);font-size:var(--gj-font-1,16px)}" +
	"a{color:var(--gj-color-link,#0b5cad)}"

// safeThemeCSS accepts one ":root{...}" block of "--name:value;" declarations and nothing else.
func safeThemeCSS(css string) bool {
	body, ok := strings.CutPrefix(css, ":root{")
	if !ok {
		return false
	}
	body, ok = strings.CutSuffix(body, "}")
	if !ok || strings.ContainsAny(body, "{}<>\\@\"'\n\r") {
		return false
	}
	for _, decl := range strings.Split(strings.TrimSuffix(body, ";"), ";") {
		name, _, found := strings.Cut(decl, ":")
		if !found || !strings.HasPrefix(name, "--") {
			return false
		}
	}
	return true
}

// renderHTMLDocument is HTML.Export's own body, shared with EPUB's
// content.xhtml (xhtml true selects self-closing void elements and
// the XHTML-namespaced root <html>, EPUB3's own strict-XML
// requirement) rather than duplicating the block-walking loop for a
// difference of only two element spellings.
func renderHTMLDocument(snap Snapshot, xhtml bool, theme string) string {
	var body strings.Builder
	var stack []BlockKind // stack[i] = the list kind (Bullet/Numbered) open at nesting depth i

	closeItem := func() { body.WriteString("</li>") }
	closeList := func() {
		kind := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if kind == NumberedItem {
			body.WriteString("</ol>\n")
		} else {
			body.WriteString("</ul>\n")
		}
	}
	openList := func(kind BlockKind) {
		if kind == NumberedItem {
			body.WriteString("<ol>\n")
		} else {
			body.WriteString("<ul>\n")
		}
		stack = append(stack, kind)
	}
	closeAllLists := func() {
		for len(stack) > 0 {
			closeItem()
			closeList()
		}
	}

	br := "<br>\n"
	imgSelfClose := ">"
	if xhtml {
		br = "<br/>\n"
		imgSelfClose = "/>"
	}

	for bi, b := range snap.Blocks {
		id := ""
		if snap.TOC && !xhtml {
			id = " id=\"" + tocAnchor(bi) + "\""
		}
		if b.Kind != BulletItem && b.Kind != NumberedItem {
			closeAllLists()
			switch b.Kind {
			case Heading1:
				body.WriteString("<h1" + id + ">" + htmlInline(b.Segments, br) + "</h1>\n")
			case Heading2:
				body.WriteString("<h2" + id + ">" + htmlInline(b.Segments, br) + "</h2>\n")
			case Heading3:
				body.WriteString("<h3" + id + ">" + htmlInline(b.Segments, br) + "</h3>\n")
			case Quote:
				body.WriteString("<blockquote><p>" + htmlInline(b.Segments, br) + "</p></blockquote>\n")
			case Code:
				// No inline formatting/escaping-of-syntax inside a code
				// block -- just HTML-escape the literal characters (a
				// stray "<" must not become a tag), the same
				// "verbatim" treatment Markdown's own fenced block
				// takes.
				body.WriteString("<pre><code>" + escapeHTMLText(rawCodeText(b.Segments)) + "</code></pre>\n")
			case Image:
				// A caption becomes both the <img>'s own alt text (screen
				// readers, and the browser's own broken-image fallback)
				// and a visible <figcaption> underneath -- wrapping the
				// <img> in a <figure> only when there's a caption to
				// caption, so an uncaptioned image's own markup is
				// unchanged from before task #158.
				alt := escapeHTMLAttr(b.ImageCaption)
				if b.ImageAlt != "" {
					alt = escapeHTMLAttr(b.ImageAlt) // a description of its own is better than the caption
				} else if b.ImageDecorative && b.ImageCaption == "" {
					alt = ""
				}
				img := "<img src=\"" + escapeHTMLAttr(b.ImageSrc) + "\" alt=\"" + alt + "\"" + imgSelfClose + "\n"
				if b.ImageCaption == "" {
					body.WriteString(img)
				} else {
					body.WriteString("<figure>\n" + img + "<figcaption>" + escapeHTMLText(b.ImageCaption) + "</figcaption>\n</figure>\n")
				}
			case TableCell:
				// A real <table>, unlike Markdown/PlainText's own
				// placeholder-turned-pipe-table/ASCII-grid: HTML natively
				// supports colspan/rowspan, so a merged owner cell's own
				// span carries across intact rather than degrading to a
				// blank covered column (renderMarkdownTable/
				// renderPlainTextTable's own disclosed simplification).
				// Row 0 becomes <th> (mirroring model.TableCellColors'
				// own "row 0 is always the header" convention, same as
				// PlainText's own box-drawn grid deliberately does NOT
				// single out -- HTML has a real <th> to use, so this
				// format can afford to). A covered cell contributes no
				// <td>/<th> of its own at all -- its owner's own
				// colspan/rowspan already reserves that grid position,
				// the normal way an HTML table represents a merge.
				if b.TableRow == 0 && b.TableCol == 0 {
					body.WriteString("<table>\n")
				}
				if b.TableCol == 0 {
					body.WriteString("<tr>")
				}
				if !b.TableCovered {
					tag := "td"
					if b.TableRow == 0 {
						tag = "th"
					}
					var attrs string
					if b.TableColSpan > 1 {
						attrs += ` colspan="` + strconv.Itoa(b.TableColSpan) + `"`
					}
					if b.TableRowSpan > 1 {
						attrs += ` rowspan="` + strconv.Itoa(b.TableRowSpan) + `"`
					}
					var css string
					if fill, ok := hexFill(b.TableBackground); ok {
						css += "background:#" + fill + ";"
					}
					if a := map[int]string{1: "center", 2: "right"}[b.TableAlign]; a != "" {
						css += "text-align:" + a + ";"
					}
					if css != "" {
						attrs += ` style="` + css + `"`
					}
					body.WriteString("<" + tag + attrs + ">" + htmlInline(b.Segments, br) + "</" + tag + ">")
				}
				if b.TableCol == b.TableCols-1 {
					body.WriteString("</tr>\n")
				}
				if b.TableRow == b.TableRows-1 && b.TableCol == b.TableCols-1 {
					body.WriteString("</table>\n")
				}
			default:
				body.WriteString("<p>" + htmlInline(b.Segments, br) + "</p>\n")
			}
			continue
		}

		// A list block: depth/level comparison decides whether this
		// item goes deeper (a new nested list, inside the still-open
		// parent <li>), stays a sibling at the current depth (close
		// the previous <li>, start a new one), or dedents (close
		// lists back up to the target depth first).
		depth := len(stack)
		level := b.Level
		if level >= depth {
			for len(stack) <= level {
				openList(b.Kind)
			}
		} else {
			for len(stack)-1 > level {
				closeItem()
				closeList()
			}
			closeItem()
			if stack[len(stack)-1] != b.Kind {
				// The same nesting depth switched from a bullet to a
				// numbered list (or back) -- close and reopen the
				// list tag itself so it matches this item's own
				// kind, or a numbered item would render inside a
				// <ul> claiming to be a bullet.
				closeList()
				openList(b.Kind)
			}
		}
		body.WriteString("<li>" + htmlInline(b.Segments, br))
	}
	closeAllLists()

	if xhtml {
		return `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
			`<!DOCTYPE html>` + "\n" +
			`<html xmlns="http://www.w3.org/1999/xhtml"` + langAttrs(snap.Lang, true) + `>` + "\n" +
			"<head><meta charset=\"utf-8\"/><title>" + escapeHTMLText(snap.Title) + "</title></head>\n<body>\n" +
			body.String() + "</body>\n</html>\n"
	}
	return "<!doctype html>\n<html" + langAttrs(snap.Lang, false) + ">\n<head>\n<meta charset=\"utf-8\">\n<title>" +
		escapeHTMLText(snap.Title) + "</title>\n" + themeStyle(theme) + htmlPrintStyle(snap) + "</head>\n<body>\n" +
		htmlTOC(snap) + body.String() + "</body>\n</html>\n"
}

// themeStyle is the <style> element of a theme, or nothing.
func themeStyle(theme string) string {
	if theme == "" {
		return ""
	}
	return "<style>" + theme + themeRules + "</style>\n"
}

// htmlInline renders segs as HTML inline content, wrapping each run
// in HTML tags (wrapHTMLRun) and HTML-escaping plain text
// (escapeHTMLText). A soft line break becomes br (a plain <br> for
// HTML, a self-closed <br/> for XHTML -- see renderHTMLDocument).
func htmlInline(segs []Segment, br string) string {
	var sb strings.Builder
	for _, seg := range segs {
		if seg.SoftBreak {
			sb.WriteString(br)
			continue
		}
		sb.WriteString(wrapHTMLRun(escapeHTMLText(seg.Text), seg.Format))
	}
	return sb.String()
}

// wrapHTMLRun wraps text in f's own formatting tags, nested in the
// same outermost-in order Markdown's own wrapMarkdownRun uses for its
// own syntax (bold outermost, the link innermost).
func wrapHTMLRun(text string, f Format) string {
	if text == "" {
		return text
	}
	if f.Change != NoChange {
		tag := "ins"
		if f.Change == Deleted {
			tag = "del"
		}
		attrs := ""
		if f.Author != "" {
			attrs += " data-author=\"" + escapeHTMLAttr(f.Author) + "\""
		}
		if f.Time != 0 {
			attrs += " datetime=\"" + time.Unix(f.Time, 0).UTC().Format(time.RFC3339) + "\""
		}
		inner := wrapHTMLRun(text, Format{Bold: f.Bold, Italic: f.Italic, Underline: f.Underline, Strikethrough: f.Strikethrough, Superscript: f.Superscript, Subscript: f.Subscript, Href: f.Href, Lang: f.Lang})
		return "<" + tag + attrs + ">" + inner + "</" + tag + ">"
	}
	if f.Lang != "" {
		text = "<span lang=\"" + escapeHTMLAttr(f.Lang) + "\">" + text + "</span>"
	}
	if f.Href != "" && safeHref(f.Href) {
		text = "<a href=\"" + escapeHTMLAttr(f.Href) + "\">" + text + "</a>"
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
		text = "<s>" + text + "</s>"
	}
	if f.Italic {
		text = "<em>" + text + "</em>"
	}
	if f.Bold {
		text = "<strong>" + text + "</strong>"
	}
	return text
}

// escapeHTMLText escapes the three characters that would otherwise
// change HTML's own parse (an unescaped "<" starting a tag, "&"
// starting an entity) so the user's own typed content -- including a
// literal "<" or "&" -- round-trips as plain text rather than
// silently vanishing or breaking the exported document's markup.
func escapeHTMLText(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// escapeHTMLAttr escapes the characters that matter inside a
// double-quoted HTML attribute value (an href or an image src) --
// narrower than escapeHTMLText since attribute text is never parsed
// as markup, only ever terminated early by an unescaped '"'.
func escapeHTMLAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;") // not allowed in an XML attribute (EPUB's XHTML)
	s = strings.ReplaceAll(s, ">", "&gt;")
	return strings.ReplaceAll(s, "\"", "&quot;")
}

// langAttrs is the lang attribute of an <html> element for a document language (xml:lang too for XHTML), or "".
func langAttrs(lang string, xhtml bool) string {
	if lang == "" {
		return ""
	}
	a := ` lang="` + escapeHTMLAttr(lang) + `"`
	if xhtml {
		a += ` xml:lang="` + escapeHTMLAttr(lang) + `"`
	}
	return a
}
