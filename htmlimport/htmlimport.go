// Package htmlimport reads an HTML page into a docfile.Document.
//
// It reads what a document needs and leaves the page out:
//
//   - headings (h1 to h6), paragraphs, block quotes, preformatted text and code, horizontal rules
//   - bullet and numbered lists, with nesting
//   - strong, em, u, s and del, sup and sub, mark, code, and the colour and background colour of an element's
//     style attribute
//   - links (http, https, mailto and in-page only; a javascript: or data: link is dropped, the text stays)
//   - pictures whose source is a data: URL, with their alternative text (a picture on a server is not
//     fetched: an importer must not make a request for a page's author)
//   - tables (the text of each cell, with column spans) and the language of an element (lang)
//   - the title
//
// Not read: scripts, styles, forms and controls, frames, embedded objects, SVG and canvas, audio and video,
// and layout (columns, floats, positions, margins). Inspect counts these, so a person can be told what an
// import leaves out. The parser is the HTML5 one of golang.org/x/net/html, so broken markup reads the way a
// browser reads it; the input is limited in size and depth.
package htmlimport

import (
	"bytes"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
)

// ErrTooLarge is returned for a page over MaxBytes.
var ErrTooLarge = errors.New("htmlimport: the page is too large")

const (
	// MaxBytes is the largest page read.
	MaxBytes = 32 << 20
	maxDepth = 256
	maxCells = 200_000
	maxCols  = 256
)

var headingScales = [6]float64{2, 1.6, 1.35, 1.2, 1.05, 1}

// Skipped counts what a page holds that Import leaves out.
type Skipped struct {
	Scripts  int // script and noscript elements
	Styles   int // style elements and stylesheets
	Forms    int // forms and their controls
	Frames   int // iframes, objects and embeds
	Graphics int // svg and canvas
	Media    int // audio and video
	Images   int // pictures that are not data: URLs (not fetched)
	Links    int // links whose address is not http, https, mailto or in-page
	Layout   int // elements with a position, float, column or margin of their own
}

// Total is the number of skipped elements.
func (s Skipped) Total() int {
	return s.Scripts + s.Styles + s.Forms + s.Frames + s.Graphics + s.Media + s.Images + s.Links + s.Layout
}

type reader struct {
	doc     docfile.Document
	skipped Skipped
	cells   int
	pre     bool
}

// Import reads an HTML page.
func Import(data []byte) (docfile.Document, error) {
	d, _, err := ImportReport(data)
	return d, err
}

// Inspect counts what Import would leave out.
func Inspect(data []byte) (Skipped, error) {
	_, s, err := ImportReport(data)
	return s, err
}

// ImportReport reads a page and says what it left out.
func ImportReport(data []byte) (docfile.Document, Skipped, error) {
	if len(data) > MaxBytes {
		return docfile.Document{}, Skipped{}, ErrTooLarge
	}
	root, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return docfile.Document{}, Skipped{}, err
	}
	r := &reader{}
	if t := find(root, atom.Title); t != nil {
		r.doc.Title = strings.TrimSpace(collapse(text(t)))
	}
	if h := find(root, atom.Head); h != nil {
		r.element(h, docfile.Format{}, 0, 0) // counts the scripts and styles; reads nothing
	}
	body := find(root, atom.Body)
	if body == nil {
		body = root
	}
	// vertical text: the page (the html or the body element) is set in a vertical writing mode
	for _, n := range []*html.Node{find(root, atom.Html), body} {
		if n != nil && verticalStyle(attr(n, "style")) {
			l := r.doc.PageLayout()
			l.Vertical = true
			r.doc.SetPageLayout(l)
			break
		}
	}
	r.blocks(body, docfile.Format{}, 0, 0)
	r.trimEnd()
	return r.doc, r.skipped, nil
}

// verticalStyle says whether an inline style sets a vertical writing mode (vertical-rl, vertical-lr, or the old
// tb-rl and tb-lr).
func verticalStyle(style string) bool {
	for _, decl := range strings.Split(style, ";") {
		k, v, ok := strings.Cut(decl, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "writing-mode") {
			v = strings.ToLower(strings.TrimSpace(v))
			return strings.HasPrefix(v, "vertical") || strings.HasPrefix(v, "tb-")
		}
	}
	return false
}

func find(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := find(c, a); f != nil {
			return f
		}
	}
	return nil
}

func attr(n *html.Node, k string) string {
	for _, a := range n.Attr {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}

func text(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style) {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

var spaceRE = regexp.MustCompile(`[ \t\r\n\f]+`)

func collapse(s string) string { return spaceRE.ReplaceAllString(s, " ") }

func (r *reader) add(it docfile.Item) { r.doc.Content = append(r.doc.Content, it) }

func (r *reader) text(s string, f docfile.Format) {
	if s != "" {
		r.add(docfile.Item{Chars: &docfile.Chars{Text: s, Format: f}})
	}
}

// endLine ends the paragraph being written, once.
func (r *reader) endLine() {
	if c := r.doc.Content; len(c) > 0 {
		if l := c[len(c)-1]; l.Chars != nil && strings.HasSuffix(l.Chars.Text, "\n") {
			return
		}
	}
	r.text("\n", docfile.Format{})
}

func isBlock(a atom.Atom) bool {
	switch a {
	case atom.P, atom.Div, atom.Section, atom.Article, atom.Aside, atom.Header, atom.Footer, atom.Main, atom.Nav, atom.Address,
		atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.Ul, atom.Ol, atom.Li, atom.Blockquote, atom.Pre, atom.Hr,
		atom.Table, atom.Figure, atom.Figcaption, atom.Dl, atom.Dt, atom.Dd, atom.Details, atom.Summary, atom.Fieldset:
		return true
	}
	return false
}

// blocks reads the children of n, which are blocks and inline runs mixed.
func (r *reader) blocks(n *html.Node, f docfile.Format, depth, level int) {
	if depth > maxDepth {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			r.inlineText(c.Data, f)
		case html.ElementNode:
			r.element(c, f, depth+1, level)
		}
	}
}

func (r *reader) inlineText(s string, f docfile.Format) {
	if r.pre {
		r.text(s, f)
		return
	}
	s = collapse(s)
	if c := r.doc.Content; s == " " || strings.HasPrefix(s, " ") {
		// no space at the start of a line
		if len(c) == 0 || c[len(c)-1].Chars != nil && strings.HasSuffix(c[len(c)-1].Chars.Text, "\n") || c[len(c)-1].Para != nil {
			s = strings.TrimLeft(s, " ")
		}
	}
	r.text(s, f)
}

func (r *reader) element(n *html.Node, f docfile.Format, depth, level int) {
	if l := attr(n, "lang"); l != "" && canvas.ValidLang(l) && l != f.Lang {
		f.Lang = l
	}
	f = r.styled(n, f)
	switch n.DataAtom {
	case atom.Script, atom.Noscript:
		r.skipped.Scripts++
		return
	case atom.Style, atom.Link:
		if n.DataAtom == atom.Style || strings.EqualFold(attr(n, "rel"), "stylesheet") {
			r.skipped.Styles++
		}
		return
	case atom.Head:
		for c := n.FirstChild; c != nil; c = c.NextSibling { // the scripts and styles of the page are counted, not read
			if c.Type == html.ElementNode {
				r.element(c, f, depth+1, level)
			}
		}
		return
	case atom.Title, atom.Meta, atom.Template:
		return
	case atom.Form, atom.Input, atom.Button, atom.Select, atom.Textarea:
		r.skipped.Forms++
		return
	case atom.Iframe, atom.Object, atom.Embed, atom.Frame, atom.Frameset:
		r.skipped.Frames++
		return
	case atom.Svg, atom.Canvas:
		r.skipped.Graphics++
		return
	case atom.Audio, atom.Video:
		r.skipped.Media++
		return
	case atom.Br:
		r.endLine()
		return
	case atom.Hr:
		r.endLine()
		r.add(docfile.Item{Rule: &docfile.RuleItem{}})
		return
	case atom.Img:
		r.image(n)
		return
	case atom.Table:
		r.endLine()
		r.table(n)
		return
	case atom.Ul, atom.Ol:
		r.endLine()
		r.list(n, f, depth, level, n.DataAtom == atom.Ol)
		return
	case atom.Pre:
		r.endLine()
		g := f
		g.FontFamily = "monospace"
		r.pre = true
		r.blocks(n, g, depth, level)
		r.pre = false
		r.endLine()
		return
	case atom.Blockquote:
		r.endLine()
		r.add(docfile.Item{Para: &docfile.Para{QuoteBar: "#9ca3af"}})
		r.blocks(n, f, depth, level)
		r.endLine()
		return
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		r.endLine()
		f.Scale, f.FontWeight = headingScales[n.DataAtom-atom.H1], 700
		r.blocks(n, f, depth, level)
		r.endLine()
		return
	case atom.Strong, atom.B:
		f.FontWeight = 700
	case atom.Em, atom.I, atom.Cite, atom.Dfn, atom.Var:
		f.FontStyle = "italic"
	case atom.U, atom.Ins:
		f.Underline = true
	case atom.S, atom.Strike, atom.Del:
		f.Strike = true
	case atom.Sup:
		f.Superscript = true
	case atom.Sub:
		f.Subscript = true
	case atom.Mark:
		f.Background = "#ffff00"
	case atom.Code, atom.Kbd, atom.Samp, atom.Tt:
		f.FontFamily = "monospace"
	case atom.A:
		if href := attr(n, "href"); href != "" {
			if ok := safeLink(href); ok {
				f.Link = href
			} else {
				r.skipped.Links++
			}
		}
	}
	if isBlock(n.DataAtom) {
		r.endLine()
		r.blocks(n, f, depth, level)
		r.endLine()
		return
	}
	r.blocks(n, f, depth, level)
}

// safeLink allows the addresses a document may carry.
func safeLink(h string) bool {
	if strings.HasPrefix(h, "#") {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(h))
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
		return true
	case "":
		return false // a relative address points at nothing once the page is gone
	}
	return false
}

var (
	colorRE  = regexp.MustCompile(`(?i)(?:^|;)\s*color\s*:\s*(#[0-9a-f]{6}|#[0-9a-f]{3})\s*(?:;|$)`)
	bgRE     = regexp.MustCompile(`(?i)background(?:-color)?\s*:\s*(#[0-9a-f]{6}|#[0-9a-f]{3})\s*(?:;|$)`)
	layoutRE = regexp.MustCompile(`(?i)(?:position|float|column-count|columns)\s*:`)
)

func hex6(c string) string {
	c = strings.ToLower(c)
	if len(c) == 4 {
		return "#" + strings.Repeat(string(c[1]), 2) + strings.Repeat(string(c[2]), 2) + strings.Repeat(string(c[3]), 2)
	}
	return c
}

// styled applies the colours of an element's style attribute and counts layout it leaves out.
func (r *reader) styled(n *html.Node, f docfile.Format) docfile.Format {
	st := attr(n, "style")
	if st == "" {
		return f
	}
	if m := colorRE.FindStringSubmatch(st); m != nil {
		f.Color = hex6(m[1])
	}
	if m := bgRE.FindStringSubmatch(st); m != nil {
		f.Background = hex6(m[1])
	}
	if layoutRE.MatchString(st) {
		r.skipped.Layout++
	}
	return f
}

func (r *reader) image(n *html.Node) {
	src := strings.TrimSpace(attr(n, "src"))
	if !strings.HasPrefix(strings.ToLower(src), "data:image/") {
		r.skipped.Images++
		return
	}
	img := &docfile.ImageItem{URL: src, Alt: strings.TrimSpace(attr(n, "alt"))}
	if img.Alt == "" && attr(n, "role") == "presentation" {
		img.Decorative = true
	}
	r.add(docfile.Item{Image: img})
}

func (r *reader) list(n *html.Node, f docfile.Format, depth, level int, ordered bool) {
	kind := 1
	if ordered {
		kind = 2
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.DataAtom != atom.Li {
			continue
		}
		r.endLine()
		r.add(docfile.Item{Para: &docfile.Para{List: docfile.List{Kind: kind, Level: level}}})
		g := f
		if l := attr(c, "lang"); l != "" && canvas.ValidLang(l) {
			g.Lang = l
		}
		g = r.styled(c, g)
		// the item's own text first, the lists inside it after
		for k := c.FirstChild; k != nil; k = k.NextSibling {
			if k.Type == html.ElementNode && (k.DataAtom == atom.Ul || k.DataAtom == atom.Ol) {
				continue
			}
			switch k.Type {
			case html.TextNode:
				r.inlineText(k.Data, g)
			case html.ElementNode:
				r.element(k, g, depth+1, level)
			}
		}
		r.endLine()
		for k := c.FirstChild; k != nil; k = k.NextSibling {
			if k.Type == html.ElementNode && (k.DataAtom == atom.Ul || k.DataAtom == atom.Ol) {
				r.list(k, g, depth+1, level+1, k.DataAtom == atom.Ol)
			}
		}
	}
}

type tcell struct {
	text    string
	colSpan int
	rowSpan int
}

func (r *reader) table(tbl *html.Node) {
	var rows [][]tcell
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.DataAtom {
			case atom.Thead, atom.Tbody, atom.Tfoot:
				walk(c)
			case atom.Tr:
				var row []tcell
				for d := c.FirstChild; d != nil; d = d.NextSibling {
					if d.Type == html.ElementNode && (d.DataAtom == atom.Td || d.DataAtom == atom.Th) && r.cells < maxCells {
						r.cells++
						row = append(row, tcell{text: strings.TrimSpace(collapse(text(d))), colSpan: atoi(attr(d, "colspan")), rowSpan: atoi(attr(d, "rowspan"))})
					}
				}
				rows = append(rows, row)
			}
		}
	}
	walk(tbl)
	cols := 0
	for _, row := range rows {
		w := 0
		for _, c := range row {
			w += c.colSpan
		}
		cols = max(cols, min(w, maxCols))
	}
	if len(rows) == 0 || cols == 0 {
		return
	}
	out := &docfile.TableItem{}
	cells := make([][]string, len(rows))
	taken := make([][]bool, len(rows))
	for i := range cells {
		cells[i], taken[i] = make([]string, cols), make([]bool, cols)
	}
	for ri, row := range rows {
		col := 0
		for _, c := range row {
			for col < cols && taken[ri][col] {
				col++
			}
			if col >= cols {
				break
			}
			cs := min(c.colSpan, cols-col)
			rs := min(c.rowSpan, len(rows)-ri)
			cells[ri][col] = c.text
			for dr := 0; dr < rs; dr++ {
				for dc := 0; dc < cs; dc++ {
					taken[ri+dr][col+dc] = true
				}
			}
			if cs > 1 || rs > 1 {
				out.Merges = append(out.Merges, docfile.TableMerge{Row: ri, Col: col, Rows: rs, Cols: cs})
			}
			col += cs
		}
	}
	for _, row := range cells {
		out.Rows = append(out.Rows, docfile.TableRow{Cells: row})
	}
	r.add(docfile.Item{Table: out})
}

func atoi(s string) int {
	n := 0
	for _, ch := range strings.TrimSpace(s) {
		if ch < '0' || ch > '9' {
			return 1
		}
		n = n*10 + int(ch-'0')
		if n > maxCols {
			return maxCols
		}
	}
	if n < 1 {
		return 1
	}
	return n
}

// trimEnd drops the empty lines the last block leaves.
func (r *reader) trimEnd() {
	c := r.doc.Content
	for len(c) > 0 {
		if last := c[len(c)-1]; last.Chars != nil && strings.TrimSpace(last.Chars.Text) == "" {
			c = c[:len(c)-1]
			continue
		}
		break
	}
	r.doc.Content = c
}
