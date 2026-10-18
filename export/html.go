package export

import (
	"encoding/base64"
	"errors"
	"fmt"
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
	// SubmitTo is where a reader's answers go: an https address (posted as a form) or a mailto address (opens the
	// reader's mail with the answers). When it is set and the document has form fields, they sit in a form with a
	// button; otherwise the fields only hold what is written in them. Anything else fails the export.
	SubmitTo string
	// SubmitLabel is the button's text ("Send" when empty).
	SubmitLabel string
}

// safeSubmitTo says whether an address is one an exported form may send to: https or mailto, with nothing that could
// leave the attribute or be read as markup.
func safeSubmitTo(a string) bool {
	if !strings.HasPrefix(a, "https://") && !strings.HasPrefix(a, "mailto:") {
		return false
	}
	for _, r := range a {
		if r <= ' ' || r == '"' || r == '\'' || r == '<' || r == '>' || r == '`' || r == 0x7f {
			return false
		}
	}
	return len(a) > len("mailto:")
}

func (h HTML) Export(snap Snapshot) ([]byte, error) {
	if h.SubmitTo != "" {
		if !safeSubmitTo(h.SubmitTo) {
			return nil, errors.New("export: the form address is not an https or mailto address")
		}
		snap.FormAction, snap.FormButton = h.SubmitTo, h.SubmitLabel
	}
	if h.Theme != "" && !safeThemeCSS(h.Theme) {
		return nil, errors.New("export: the HTML theme is not a plain :root block of custom properties")
	}
	theme := h.Theme
	if theme == "" {
		theme = safeSnapTheme(snap) // the document's own, when it is safe; a stored file cannot make the export fail with it
	}
	return []byte(renderHTMLDocument(snap, false, theme)), nil
}

// safeSnapTheme is the snapshot's theme when it is a plain :root block, else "".
func safeSnapTheme(snap Snapshot) string {
	if snap.Theme != "" && safeThemeCSS(snap.Theme) {
		return snap.Theme
	}
	return ""
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
		if bi > 0 && snap.tocHere(bi) && !xhtml {
			// contents in the middle of the document (at the start they come before <main>)
			closeAllLists()
			body.WriteString(htmlTOC(snap))
		}
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
				h := "h" + strconv.Itoa(b.HeadingLevel())
				body.WriteString("<" + h + id + ">" + htmlInline(b.Segments, br) + "</" + h + ">\n")
			case Quote:
				body.WriteString("<blockquote><p>" + htmlInline(b.Segments, br) + "</p></blockquote>\n")
			case Code:
				// No inline formatting/escaping-of-syntax inside a code
				// block -- just HTML-escape the literal characters (a
				// stray "<" must not become a tag), the same
				// "verbatim" treatment Markdown's own fenced block
				// takes.
				class := ""
				if lang := cleanCodeLang(b.CodeLang); lang != "" {
					class = ` class="language-` + lang + `"`
				}
				body.WriteString("<pre><code" + class + ">" + escapeHTMLText(rawCodeText(b.Segments)) + "</code></pre>\n")
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
				described := ""
				if bi+1 < len(snap.Blocks) && snap.Blocks[bi+1].DescribesImage {
					described = " aria-describedby=\"longdesc-" + strconv.Itoa(bi+1) + "\"" // the long description follows
				}
				img := "<img src=\"" + escapeHTMLAttr(b.ImageSrc) + "\" alt=\"" + alt + "\"" + described + imgSelfClose + "\n"
				if b.ImageCaption == "" {
					body.WriteString(img)
				} else {
					body.WriteString("<figure>\n" + img + "<figcaption>" + escapeHTMLText(b.ImageCaption) + "</figcaption>\n</figure>\n")
				}
			case MediaClip:
				body.WriteString(htmlMedia(b, snap.Lang))
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
					if b.TableRow == 0 && b.TableColW > 0 {
						css += "width:" + strconv.FormatFloat(b.TableColW, 'f', 0, 64) + "px;"
					}
					if b.TableRowH > 0 {
						css += "height:" + strconv.FormatFloat(b.TableRowH, 'f', 0, 64) + "px;"
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
				if b.Equation != "" {
					body.WriteString("<p class=\"equation\">" + MathML(b.Equation, plainTextInline(b.Segments)) + "</p>\n")
					break
				}
				if b.DescribesImage {
					body.WriteString("<p class=\"longdesc\" id=\"longdesc-" + strconv.Itoa(bi) + "\">" + htmlInline(b.Segments, br) + "</p>\n")
					break
				}
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
		if b.Note > 0 {
			// A footnote: the mark in the text links here, and this links back to the mark.
			n := strconv.Itoa(b.Note)
			body.WriteString("<li id=\"fn-" + n + "\">" + htmlInline(b.Segments, br) + " <a href=\"#fnref-" + n + "\" aria-label=\"Back to the text of footnote " + n + "\">\u21a9</a>")
			continue
		}
		body.WriteString("<li>" + htmlInline(b.Segments, br))
	}
	closeAllLists()
	if len(snap.Blocks) > 0 && snap.tocEnd() && !xhtml {
		body.WriteString(htmlTOC(snap))
	}

	if xhtml {
		return `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
			`<!DOCTYPE html>` + "\n" +
			`<html xmlns="http://www.w3.org/1999/xhtml"` + langAttrs(snap.Lang, true) + writingAttr(snap) + `>` + "\n" +
			"<head><meta charset=\"utf-8\"/><title>" + escapeHTMLText(snap.Title) + "</title>\n" + xhtmlThemeStyle(theme) + "</head>\n<body>\n" +
			body.String() + "</body>\n</html>\n"
	}
	return "<!doctype html>\n<html" + langAttrs(snap.Lang, false) + writingAttr(snap) + ">\n<head>\n<meta charset=\"utf-8\">\n<title>" +
		escapeHTMLText(snap.Title) + "</title>\n" + themeStyle(theme) + htmlPrintStyle(snap) + "</head>\n<body>\n" +
		htmlTOCAtStart(snap) + "<main>\n" + htmlFormWrap(snap, body.String()) + "</main>\n</body>\n</html>\n"
}

// writingAttr is the attribute that sets vertical text on the page: the lines top to bottom, from right to left.
func writingAttr(snap Snapshot) string {
	if !snap.Vertical {
		return ""
	}
	return ` style="writing-mode:vertical-rl"`
}

// xhtmlThemeStyle is themeStyle for an XHTML page: the text of the style element must be well formed, so an
// ampersand is written as an entity.
func xhtmlThemeStyle(theme string) string {
	return strings.ReplaceAll(themeStyle(theme), "&", "&amp;")
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
		if seg.Format.Field != nil {
			sb.WriteString(htmlFormControl(*seg.Format.Field))
			continue
		}
		if seg.Format.Formula != "" {
			sb.WriteString(MathMLInline(seg.Format.Formula, seg.Text))
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
	if f.NoteRef > 0 {
		n := strconv.Itoa(f.NoteRef)
		return "<sup id=\"fnref-" + n + "\"><a href=\"#fn-" + n + "\" aria-label=\"Footnote " + n + "\">" + text + "</a></sup>"
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

// htmlMedia is an audio or video clip: the player with its caption track, the title as a caption, and the
// transcript under it for a reader who cannot (or would rather not) play it. An address that is not safe to
// link to leaves the player without a source, but the title and the transcript stay.
func htmlMedia(b Block, lang string) string {
	tag := "video"
	if b.MediaKind == "audio" {
		tag = "audio"
	}
	var sb strings.Builder
	sb.WriteString("<figure class=\"media\">\n<" + tag + " controls")
	if b.MediaURL != "" && safeHref(b.MediaURL) {
		sb.WriteString(" src=\"" + escapeHTMLAttr(b.MediaURL) + "\"")
	}
	if tag == "video" && b.MediaW > 0 && b.MediaH > 0 {
		sb.WriteString(fmt.Sprintf(" width=\"%d\" height=\"%d\"", int(b.MediaW), int(b.MediaH)))
	}
	if b.MediaTitle != "" {
		sb.WriteString(" aria-label=\"" + escapeHTMLAttr(b.MediaTitle) + "\"")
	}
	sb.WriteString(">\n")
	track := ""
	switch {
	case strings.TrimSpace(b.MediaCaptions) != "":
		track = "data:text/vtt;charset=utf-8;base64," + base64.StdEncoding.EncodeToString([]byte(b.MediaCaptions))
	case b.MediaCaptionsURL != "" && safeHref(b.MediaCaptionsURL):
		track = b.MediaCaptionsURL
	}
	if track != "" {
		sb.WriteString("<track kind=\"captions\" label=\"Captions\"")
		if lang != "" {
			sb.WriteString(" srclang=\"" + escapeHTMLAttr(lang) + "\"")
		}
		sb.WriteString(" src=\"" + escapeHTMLAttr(track) + "\" default>\n")
	}
	sb.WriteString("</" + tag + ">\n")
	if b.MediaTitle != "" {
		sb.WriteString("<figcaption>" + escapeHTMLText(b.MediaTitle) + "</figcaption>\n")
	}
	if t := strings.TrimSpace(b.MediaTranscript); t != "" {
		sb.WriteString("<details>\n<summary>Transcript</summary>\n")
		for _, para := range strings.Split(t, "\n") {
			if para = strings.TrimSpace(para); para != "" {
				sb.WriteString("<p>" + escapeHTMLText(para) + "</p>\n")
			}
		}
		sb.WriteString("</details>\n")
	}
	sb.WriteString("</figure>\n")
	return sb.String()
}
