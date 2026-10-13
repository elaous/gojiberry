// Package odtimport reads an OpenDocument text file (.odt) into a docfile.Document.
//
// LibreOffice, Collabora, Google Docs (File > Download > OpenDocument) and many public offices write this
// format. It is read directly from the package:
//
//   - paragraphs and headings (by outline level), quotes and code (by their style), alignment is not kept
//   - bullet and numbered lists with their nesting
//   - runs: bold, italic, underline, strike-through, super and subscript, and the language of a run
//   - hyperlinks (http, https, mailto and in-page only), tabs, spaces and line breaks
//   - tables (the text of each cell, spans, and the fill of a cell)
//   - pictures (embedded in the package, as data: URLs, with their size and alternative text)
//   - footnotes, the title and the language of the document
//
// Not read: comments, headers and footers, text boxes and other drawings, charts, tracked changes (the text
// is read as it stands), fields (the last shown text stays), bookmarks, columns, and paragraph spacing,
// borders and fonts. Inspect counts these, so a person can be told what an import leaves out. The package
// is untrusted input: sizes, nesting and the number of elements are limited.
package odtimport

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
)

// ErrNotODT is returned for data that is not an OpenDocument text file.
var ErrNotODT = errors.New("odtimport: not an .odt file")

const (
	maxTotalBytes = 256 << 20
	maxImageBytes = 64 << 20
	maxImages     = 200
	maxDepth      = 256
	maxElements   = 2_000_000
	maxCols       = 256
	maxCells      = 200_000
	pxPerIn       = 96.0
)

var headingScales = [5]float64{2, 1.6, 1.35, 1.2, 1.05}

// node is an element with its children and the text between them in order ("#text" nodes).
type node struct {
	name  string
	attrs map[string]string
	kids  []*node
	text  string
}

func (n *node) attr(k string) string { return n.attrs[k] }

func (n *node) child(name string) *node {
	for _, k := range n.kids {
		if k.name == name {
			return k
		}
	}
	return nil
}

func (n *node) walk(f func(*node) bool) {
	if !f(n) {
		return
	}
	for _, k := range n.kids {
		k.walk(f)
	}
}

func parseXML(data []byte) (*node, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	var stack []*node
	var root *node
	elements := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if elements++; elements > maxElements || len(stack) >= maxDepth {
				return nil, errors.New("XML too large or too deeply nested")
			}
			n := &node{name: t.Name.Local, attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.attrs[a.Name.Local] = a.Value
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.kids = append(p.kids, n)
			} else {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.kids = append(p.kids, &node{name: "#text", text: string(t)})
			}
		}
	}
	if root == nil {
		return nil, errors.New("empty XML")
	}
	return root, nil
}

// props are the text and paragraph properties of a style that matter here.
type props struct {
	bold, italic, underline, strike bool
	pos                             string // "super", "sub" or ""
	lang                            string
	mono                            bool
	quote                           bool
	background                      string
}

type reader struct {
	files    map[string]*zip.File
	styles   map[string]*props // style name -> its own properties
	parents  map[string]string // style name -> parent style name
	ordered  map[string]bool   // list style name -> its first level is numbered
	defLang  string
	doc      docfile.Document
	read     int64
	images   int
	imgBytes int
	cells    int
	notes    int
	pics     map[string]string
	skipped  Skipped
}

// Import reads an .odt file.
func Import(data []byte) (docfile.Document, error) {
	d, _, err := ImportReport(data)
	return d, err
}

// Inspect counts what Import would leave out of an .odt file.
func Inspect(data []byte) (Skipped, error) {
	_, s, err := ImportReport(data)
	return s, err
}

func newReader(data []byte) (*reader, *node, *node, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, nil, ErrNotODT
	}
	r := &reader{files: map[string]*zip.File{}, styles: map[string]*props{}, parents: map[string]string{}, ordered: map[string]bool{}, pics: map[string]string{}}
	for _, f := range zr.File {
		r.files[f.Name] = f
	}
	if mt, err := r.readFile("mimetype"); err != nil || strings.TrimSpace(string(mt)) != "application/vnd.oasis.opendocument.text" {
		return nil, nil, nil, ErrNotODT
	}
	content := r.xml("content.xml")
	if content == nil || content.name != "document-content" {
		return nil, nil, nil, ErrNotODT
	}
	return r, content, r.xml("styles.xml"), nil
}

func (r *reader) readFile(name string) ([]byte, error) {
	f := r.files[name]
	if f == nil {
		return nil, errors.New("missing")
	}
	if r.read += int64(f.UncompressedSize64); r.read > maxTotalBytes || f.UncompressedSize64 > maxTotalBytes {
		return nil, errors.New("the file is too large")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(io.LimitReader(rc, maxTotalBytes+1))
}

func (r *reader) xml(name string) *node {
	b, err := r.readFile(name)
	if err != nil {
		return nil
	}
	n, err := parseXML(b)
	if err != nil {
		return nil
	}
	return n
}

// ImportReport reads an .odt file and says what it left out.
func ImportReport(data []byte) (docfile.Document, Skipped, error) {
	r, content, styles, err := newReader(data)
	if err != nil {
		return docfile.Document{}, Skipped{}, err
	}
	if styles != nil {
		r.loadStyles(styles)
	}
	r.loadStyles(content)
	if meta := r.xml("meta.xml"); meta != nil {
		meta.walk(func(n *node) bool {
			switch n.name {
			case "title":
				r.doc.Title = strings.TrimSpace(textOf(n))
			}
			return true
		})
	}
	r.countSkipped(content, styles)
	if body := content.child("body"); body != nil {
		if t := body.child("text"); t != nil {
			r.blocks(t.kids, props{}, 0)
		}
	}
	r.trimEnd()
	return r.doc, r.skipped, nil
}

func textOf(n *node) string {
	var sb strings.Builder
	n.walk(func(k *node) bool {
		if k.name == "#text" {
			sb.WriteString(k.text)
		}
		return true
	})
	return sb.String()
}

func (r *reader) loadStyles(root *node) {
	root.walk(func(n *node) bool {
		switch n.name {
		case "default-style":
			if n.attr("family") == "paragraph" {
				if tp := n.child("text-properties"); tp != nil {
					r.defLang = tp.attr("language")
				}
			}
		case "style":
			name := n.attr("name")
			if name == "" {
				return true
			}
			p := &props{}
			if tp := n.child("text-properties"); tp != nil {
				p.bold = tp.attr("font-weight") == "bold" || tp.attr("font-weight") == "700"
				p.italic = tp.attr("font-style") == "italic" || tp.attr("font-style") == "oblique"
				p.underline = v(tp.attr("text-underline-style")) != ""
				p.strike = v(tp.attr("text-line-through-style")) != ""
				switch pos := tp.attr("text-position"); {
				case strings.HasPrefix(pos, "super"):
					p.pos = "super"
				case strings.HasPrefix(pos, "sub"):
					p.pos = "sub"
				}
				if l := tp.attr("language"); l != "" {
					p.lang = strings.ToLower(l)
					if c := tp.attr("country"); c != "" && c != "none" {
						p.lang += "-" + strings.ToUpper(c)
					}
				}
				p.mono = strings.Contains(strings.ToLower(tp.attr("font-family")), "mono") || strings.Contains(strings.ToLower(tp.attr("font-name")), "mono") || strings.Contains(strings.ToLower(tp.attr("font-name")), "courier")
			}
			if pp := n.child("paragraph-properties"); pp != nil {
				p.quote = pp.attr("border-left") != ""
			}
			if cp := n.child("table-cell-properties"); cp != nil {
				if bg := cp.attr("background-color"); strings.HasPrefix(bg, "#") && len(bg) == 7 {
					p.background = strings.ToLower(bg)
				}
			}
			r.styles[name] = p
			if pn := n.attr("parent-style-name"); pn != "" {
				r.parents[name] = pn
			}
		case "list-style":
			if lv := n.kids; len(lv) > 0 {
				for _, k := range lv {
					if k.name == "list-level-style-number" {
						r.ordered[n.attr("name")] = true
					}
					if k.name == "list-level-style-bullet" || k.name == "list-level-style-number" || k.name == "list-level-style-image" {
						break
					}
				}
			}
		}
		return true
	})
}

func v(s string) string {
	if s == "none" {
		return ""
	}
	return s
}

// resolve is the properties of a style with the ones it inherits from its parents.
func (r *reader) resolve(name string) props {
	var out props
	seen := 0
	for n := name; n != "" && seen < 16; n, seen = r.parents[n], seen+1 {
		p := r.styles[n]
		if p == nil {
			continue
		}
		out.bold = out.bold || p.bold
		out.italic = out.italic || p.italic
		out.underline = out.underline || p.underline
		out.strike = out.strike || p.strike
		out.mono = out.mono || p.mono
		out.quote = out.quote || p.quote
		if out.pos == "" {
			out.pos = p.pos
		}
		if out.lang == "" {
			out.lang = p.lang
		}
		if out.background == "" {
			out.background = p.background
		}
	}
	return out
}

func (r *reader) add(it docfile.Item) { r.doc.Content = append(r.doc.Content, it) }

func (r *reader) text(s string, f docfile.Format) {
	if s != "" {
		r.add(docfile.Item{Chars: &docfile.Chars{Text: s, Format: f}})
	}
}

func (r *reader) format(base docfile.Format, p props) docfile.Format {
	f := base
	if p.bold {
		f.FontWeight = 700
	}
	if p.italic {
		f.FontStyle = "italic"
	}
	if p.underline {
		f.Underline = true
	}
	if p.strike {
		f.Strike = true
	}
	switch p.pos {
	case "super":
		f.Superscript = true
	case "sub":
		f.Subscript = true
	}
	if p.mono {
		f.FontFamily = "monospace"
	}
	if p.lang != "" && !strings.EqualFold(strings.SplitN(p.lang, "-", 2)[0], r.defLang) && canvas.ValidLang(p.lang) {
		f.Lang = p.lang
	}
	return f
}

// blocks reads block-level children.
func (r *reader) blocks(kids []*node, inherit props, depth int) {
	if depth > maxDepth {
		return
	}
	for _, k := range kids {
		switch k.name {
		case "p":
			r.paragraph(k, 0, nil)
		case "h":
			lvl, _ := strconv.Atoi(k.attr("outline-level"))
			r.paragraph(k, max(lvl, 1), nil)
		case "list":
			r.list(k, 0, "", depth+1)
		case "table":
			r.table(k)
		case "section", "index-body", "table-of-content", "index-title", "alphabetical-index":
			r.blocks(k.kids, inherit, depth+1)
		}
	}
}

func (r *reader) paragraph(p *node, heading int, list *docfile.List) {
	st := r.resolve(p.attr("style-name"))
	if list != nil {
		r.add(docfile.Item{Para: &docfile.Para{List: *list}})
	} else if st.quote {
		r.add(docfile.Item{Para: &docfile.Para{QuoteBar: "#9ca3af"}})
	}
	f := docfile.Format{}
	if heading > 0 {
		f.Scale, f.FontWeight = headingScales[min(heading, 5)-1], 700
	}
	if st.mono || strings.Contains(strings.ToLower(p.attr("style-name")), "preformatted") {
		f.FontFamily = "monospace"
	}
	r.inline(p.kids, f, 0)
	r.endLine()
}

func (r *reader) endLine() { r.text("\n", docfile.Format{}) }

// inline reads the children of a paragraph, span or link.
func (r *reader) inline(kids []*node, f docfile.Format, depth int) {
	if depth > maxDepth {
		return
	}
	for _, k := range kids {
		switch k.name {
		case "#text":
			r.text(collapseXMLSpace(k.text), f)
		case "s":
			c, _ := strconv.Atoi(k.attr("c"))
			if k.attr("c") == "" {
				c = 1
			}
			r.text(strings.Repeat(" ", min(max(c, 0), 1000)), f)
		case "tab":
			r.text("\t", f)
		case "line-break":
			r.text("\n", f)
		case "span":
			r.inline(k.kids, r.format(f, r.resolve(k.attr("style-name"))), depth+1)
		case "a":
			g := f
			if h := strings.TrimSpace(k.attr("href")); safeLink(h) {
				g.Link = h
			} else if h != "" {
				r.skipped.Links++
			}
			r.inline(k.kids, g, depth+1)
		case "note":
			r.note(k, f)
		case "frame":
			r.frame(k)
		case "soft-page-break", "bookmark", "bookmark-start", "bookmark-end":
			if strings.HasPrefix(k.name, "bookmark") {
				r.skipped.Bookmarks++
			}
		case "date", "time", "page-number", "page-count", "title", "subject", "author-name", "file-name":
			r.skipped.Fields++
			r.inline(k.kids, f, depth+1)
		case "annotation", "annotation-end":
			// counted by countSkipped
		case "change-start", "change-end", "change":
		}
	}
}

// collapseXMLSpace is the text of a text node: ODF collapses a run of white space to one space, and
// white space made only of line ends and indentation is not text.
func collapseXMLSpace(s string) string {
	var sb strings.Builder
	space := false
	for _, ch := range s {
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			space = true
			continue
		}
		if space {
			sb.WriteByte(' ')
			space = false
		}
		sb.WriteRune(ch)
	}
	if space {
		sb.WriteByte(' ')
	}
	return sb.String()
}

func safeLink(h string) bool {
	if strings.HasPrefix(h, "#") {
		return true
	}
	l := strings.ToLower(h)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "mailto:")
}

func (r *reader) list(l *node, level int, style string, depth int) {
	if depth > maxDepth {
		return
	}
	if s := l.attr("style-name"); s != "" {
		style = s
	}
	kind := 1
	if r.ordered[style] {
		kind = 2
	}
	for _, it := range l.kids {
		if it.name != "list-item" && it.name != "list-header" {
			continue
		}
		for _, k := range it.kids {
			switch k.name {
			case "p", "h":
				lv := docfile.List{Kind: kind, Level: level}
				r.paragraph(k, 0, &lv)
			case "list":
				r.list(k, level+1, style, depth+1)
			}
		}
	}
}

func (r *reader) note(n *node, f docfile.Format) {
	body := n.child("note-body")
	if body == nil || n.attr("note-class") != "footnote" {
		r.skipped.Endnotes++
		return
	}
	r.notes++
	id := strconv.Itoa(r.notes)
	r.add(docfile.Item{Footnote: &docfile.FootnoteMark{ID: id}})
	r.doc.Footnotes = append(r.doc.Footnotes, docfile.Footnote{ID: id, Text: strings.TrimSpace(collapseXMLSpace(textOf(body)))})
}

func lengthPx(s string) float64 {
	s = strings.TrimSpace(s)
	for _, u := range []struct {
		suffix string
		px     float64
	}{{"in", pxPerIn}, {"cm", pxPerIn / 2.54}, {"mm", pxPerIn / 25.4}, {"pt", pxPerIn / 72}, {"px", 1}} {
		if strings.HasSuffix(s, u.suffix) {
			if f, err := strconv.ParseFloat(strings.TrimSuffix(s, u.suffix), 64); err == nil && f >= 0 && f < 1e5 {
				return f * u.px
			}
		}
	}
	return 0
}

func (r *reader) frame(fr *node) {
	img := fr.child("image")
	if img == nil || r.images >= maxImages {
		if img == nil && (fr.child("text-box") != nil) {
			r.skipped.TextBoxes++
		} else if img == nil {
			r.skipped.Drawings++
		}
		return
	}
	href := img.attr("href")
	mime := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".svg": "image/svg+xml", ".bmp": "image/bmp", ".webp": "image/webp"}[strings.ToLower(path.Ext(href))]
	if href == "" || strings.Contains(href, "..") || mime == "" || r.files[href] == nil {
		return
	}
	url, ok := r.pics[href]
	if !ok {
		data, err := r.readFile(href)
		if err != nil || r.imgBytes+len(data) > maxImageBytes {
			return
		}
		r.imgBytes += len(data)
		url = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		r.pics[href] = url
	}
	it := &docfile.ImageItem{URL: url, Width: lengthPx(fr.attr("width")), Height: lengthPx(fr.attr("height"))}
	if t := fr.child("title"); t != nil {
		it.Alt = strings.TrimSpace(textOf(t))
	}
	if it.Alt == "" {
		if d := fr.child("desc"); d != nil {
			it.Alt = strings.TrimSpace(textOf(d))
		}
	}
	r.images++
	r.add(docfile.Item{Image: it})
}

func (r *reader) table(t *node) {
	var rows [][]cell
	cols := 0
	var walk func(*node, int)
	walk = func(n *node, depth int) {
		if depth > maxDepth {
			return
		}
		for _, k := range n.kids {
			switch k.name {
			case "table-header-rows", "table-rows", "table-row-group":
				walk(k, depth+1)
			case "table-row":
				var row []cell
				rep := 1
				if x, err := strconv.Atoi(k.attr("number-rows-repeated")); err == nil && x > 1 {
					rep = min(x, 1000)
				}
				for _, c := range k.kids {
					if c.name != "table-cell" && c.name != "covered-table-cell" {
						continue
					}
					if r.cells++; r.cells > maxCells {
						continue
					}
					cr := 1
					if x, err := strconv.Atoi(c.attr("number-columns-repeated")); err == nil && x > 1 {
						cr = min(x, maxCols)
					}
					ce := cell{covered: c.name == "covered-table-cell", colSpan: atoi(c.attr("number-columns-spanned")), rowSpan: atoi(c.attr("number-rows-spanned")), style: c.attr("style-name")}
					if !ce.covered {
						var parts []string
						for _, p := range c.kids {
							if p.name == "p" || p.name == "h" {
								parts = append(parts, strings.TrimSpace(collapseXMLSpace(textOf(p))))
							}
						}
						ce.text = strings.Join(parts, "\n")
					}
					for range cr {
						row = append(row, ce)
					}
				}
				for range rep {
					rows = append(rows, row)
				}
			}
		}
	}
	walk(t, 0)
	for _, row := range rows {
		cols = max(cols, min(len(row), maxCols))
	}
	if len(rows) == 0 || cols == 0 {
		return
	}
	out := &docfile.TableItem{}
	for ri, row := range rows {
		cs := make([]string, cols)
		for ci, c := range row {
			if ci >= cols || c.covered {
				continue
			}
			cs[ci] = c.text
			if c.colSpan > 1 || c.rowSpan > 1 {
				out.Merges = append(out.Merges, docfile.TableMerge{Row: ri, Col: ci, Rows: min(c.rowSpan, len(rows)-ri), Cols: min(c.colSpan, cols-ci)})
			}
			if bg := r.resolve(c.style).background; bg != "" {
				out.Styles = append(out.Styles, docfile.TableCellStyle{Row: ri, Col: ci, Background: bg})
			}
		}
		out.Rows = append(out.Rows, docfile.TableRow{Cells: cs})
	}
	r.add(docfile.Item{Table: out})
}

type cell struct {
	text             string
	colSpan, rowSpan int
	covered          bool
	style            string
}

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 {
		return 1
	}
	return min(n, maxCols)
}

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
