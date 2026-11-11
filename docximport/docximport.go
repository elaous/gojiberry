// Package docximport reads a Word (.docx) file into a document.
package docximport

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
)

// ErrNotDocx is returned for data that is not a Word file.
var ErrNotDocx = errors.New("docximport: not a .docx file")

const (
	relNS     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	emuPerPx  = 9525
	maxImages = 200
	// Limits on what one file may cost to read.
	maxTotalBytes = 256 << 20 // all entries read, together
	maxImageBytes = 64 << 20  // all pictures kept, together
	maxDepth      = 256       // element nesting
	maxElements   = 2_000_000
	maxCols       = 256
	maxCells      = 200_000
)

// headingScales are the sizes the canvas gives headings.
var headingScales = [5]float64{2, 1.6, 1.35, 1.2, 1.05}

// node is a parsed XML element, with namespaces dropped.
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

func (n *node) children(name string) []*node {
	var out []*node
	for _, k := range n.kids {
		if k.name == name {
			out = append(out, k)
		}
	}
	return out
}

// find returns the first descendant called name.
func (n *node) find(name string) *node {
	for _, k := range n.kids {
		if k.name == name {
			return k
		}
		if f := k.find(name); f != nil {
			return f
		}
	}
	return nil
}

func parseXML(data []byte) (*node, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
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
				if a.Name.Space == relNS {
					n.attrs["r:"+a.Name.Local] = a.Value
				} else {
					n.attrs[a.Name.Local] = a.Value
				}
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
				stack[len(stack)-1].text += string(t)
			}
		}
	}
	if root == nil {
		return nil, errors.New("empty XML")
	}
	return root, nil
}

// reader holds what is needed while one document is read.
type reader struct {
	files   map[string]*zip.File
	rels    map[string]string       // relationship id -> target (resolved against word/)
	styles  map[string]string       // style id -> lower-case style name
	numbers map[string]map[int]bool // numId -> level -> ordered?
	notes   map[string]string       // footnote id -> text
	defLang string                  // the language of the default run properties

	doc    docfile.Document
	images int

	bytesRead    int64 // bytes read so far
	imageBytes   int
	cells        int
	comments     map[string]bool // imported comment IDs
	openComments []string        // the comment ranges open at this point of the body, outermost first
	seenNotes    map[string]bool
	pics         map[string]string // picture target -> data URL
}

func (r *reader) read(name string) ([]byte, error) {
	f, ok := r.files[name]
	if !ok {
		return nil, fmt.Errorf("missing %s", name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	left := maxTotalBytes - r.bytesRead
	if left <= 0 {
		return nil, errors.New("docx too large")
	}
	b, err := io.ReadAll(io.LimitReader(rc, min(64<<20, left)))
	r.bytesRead += int64(len(b))
	return b, err
}

func (r *reader) xml(name string) *node {
	b, err := r.read(name)
	if err != nil {
		return nil
	}
	n, err := parseXML(b)
	if err != nil {
		return nil
	}
	return n
}

// Import converts the bytes of a .docx file.
func Import(data []byte) (docfile.Document, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return docfile.Document{}, ErrNotDocx
	}
	r := &reader{files: map[string]*zip.File{}, rels: map[string]string{}, styles: map[string]string{}, numbers: map[string]map[int]bool{}, notes: map[string]string{}, seenNotes: map[string]bool{}, pics: map[string]string{}}
	for _, f := range zr.File {
		r.files[f.Name] = f
	}
	body := r.xml("word/document.xml")
	if body == nil || body.name != "document" {
		return docfile.Document{}, ErrNotDocx
	}
	r.loadRels()
	r.loadStyles()
	r.loadNumbering()
	r.loadNotes()
	r.loadComments()
	if core := r.xml("docProps/core.xml"); core != nil {
		if t := core.find("title"); t != nil {
			r.doc.Title = strings.TrimSpace(t.text)
		}
	}
	if b := body.child("body"); b != nil {
		r.blocks(b.kids)
		if sp := b.child("sectPr"); sp != nil {
			if td := sp.child("textDirection"); td != nil && strings.HasPrefix(td.attr("val"), "tbRl") {
				l := r.doc.PageLayout()
				l.Vertical = true
				r.doc.SetPageLayout(l)
			}
		}
	}
	r.trimEnd()
	return r.doc, nil
}

func (r *reader) loadRels() {
	rels := r.xml("word/_rels/document.xml.rels")
	if rels == nil {
		return
	}
	for _, rel := range rels.children("Relationship") {
		target := rel.attr("Target")
		if rel.attr("TargetMode") != "External" && !strings.HasPrefix(target, "/") {
			target = path.Join("word", target)
		} else if strings.HasPrefix(target, "/") && rel.attr("TargetMode") != "External" {
			target = strings.TrimPrefix(target, "/")
		}
		r.rels[rel.attr("Id")] = target
	}
}

func (r *reader) loadStyles() {
	st := r.xml("word/styles.xml")
	if st == nil {
		return
	}
	for _, s := range st.children("style") {
		if name := s.child("name"); name != nil {
			r.styles[s.attr("styleId")] = strings.ToLower(name.attr("val"))
		}
	}
	if dd := st.child("docDefaults"); dd != nil {
		if rp := dd.child("rPrDefault"); rp != nil {
			if rpr := rp.child("rPr"); rpr != nil {
				if l := rpr.child("lang"); l != nil && l.attr("val") != "" {
					r.defLang = l.attr("val")
					r.doc.SetLanguage(r.defLang)
				}
			}
		}
	}
}

func (r *reader) loadNumbering() {
	nb := r.xml("word/numbering.xml")
	if nb == nil {
		return
	}
	abstract := map[string]map[int]bool{}
	for _, a := range nb.children("abstractNum") {
		levels := map[int]bool{}
		for _, l := range a.children("lvl") {
			ilvl, _ := strconv.Atoi(l.attr("ilvl"))
			if f := l.child("numFmt"); f != nil {
				levels[ilvl] = f.attr("val") != "bullet" && f.attr("val") != "none"
			}
		}
		abstract[a.attr("abstractNumId")] = levels
	}
	for _, n := range nb.children("num") {
		if a := n.child("abstractNumId"); a != nil {
			r.numbers[n.attr("numId")] = abstract[a.attr("val")]
		}
	}
}

func (r *reader) loadNotes() {
	fn := r.xml("word/footnotes.xml")
	if fn == nil {
		return
	}
	for _, n := range fn.children("footnote") {
		id := n.attr("id")
		if id == "" || id == "0" || id == "-1" || n.attr("type") != "" {
			continue
		}
		var parts []string
		for _, p := range n.children("p") {
			parts = append(parts, strings.TrimSpace(plainText(p)))
		}
		r.notes[id] = strings.TrimSpace(strings.Join(parts, "\n"))
	}
}

// plainText is the text of a paragraph, ignoring its formatting.
func plainText(n *node) string {
	var sb strings.Builder
	var walk func(*node)
	walk = func(n *node) {
		switch n.name {
		case "t":
			sb.WriteString(n.text)
		case "tab":
			sb.WriteByte('\t')
		case "br":
			sb.WriteByte(' ')
		}
		for _, k := range n.kids {
			walk(k)
		}
	}
	walk(n)
	return sb.String()
}

func (r *reader) add(it docfile.Item) { r.doc.Content = append(r.doc.Content, it) }

func (r *reader) text(s string, f docfile.Format) {
	if s != "" {
		r.add(docfile.Item{Chars: &docfile.Chars{Text: s, Format: f}})
	}
}

// blocks converts body-level elements.
func (r *reader) blocks(kids []*node) {
	for _, k := range kids {
		switch k.name {
		case "p":
			r.paragraph(k)
		case "tbl":
			r.table(k)
		case "commentRangeStart":
			r.openComment(k.attr("id"))
		case "commentRangeEnd":
			r.closeComment(k.attr("id"))
		case "sdt":
			if c := k.child("sdtContent"); c != nil {
				r.blocks(c.kids)
			}
		}
	}
}

func (r *reader) table(tbl *node) {
	type cell struct {
		text          string
		colSpan       int // the cell's own width in columns
		style         docfile.TableCellStyle
		hasStyle      bool
		restart, cont bool // starts / continues a vertical merge
	}
	var grid [][]cell
	cols := 0
	for _, tr := range tbl.children("tr") {
		var row []cell
		for _, tc := range tr.children("tc") {
			if r.cells++; r.cells > maxCells {
				break
			}
			c := cell{colSpan: 1}
			var parts []string
			for _, p := range tc.find2("p") {
				if t := strings.TrimSpace(plainText(p)); t != "" {
					parts = append(parts, t)
				}
			}
			c.text = strings.Join(parts, " ")
			if pr := tc.child("tcPr"); pr != nil {
				if g := pr.child("gridSpan"); g != nil {
					if n, err := strconv.Atoi(g.attr("val")); err == nil && n > 1 && n < 64 {
						c.colSpan = n
					}
				}
				if vm := pr.child("vMerge"); vm != nil {
					if vm.attr("val") == "restart" {
						c.restart = true
					} else {
						c.cont = true
					}
				}
				if sh := pr.child("shd"); sh != nil {
					if fill := sh.attr("fill"); fill != "" && fill != "auto" && len(fill) == 6 {
						c.style.Background, c.hasStyle = "#"+strings.ToLower(fill), true
					}
				}
			}
			if p := tc.find("p"); p != nil {
				if ppr := p.child("pPr"); ppr != nil {
					if jc := ppr.child("jc"); jc != nil {
						switch jc.attr("val") {
						case "center":
							c.style.Align, c.hasStyle = 1, true
						case "right", "end":
							c.style.Align, c.hasStyle = 2, true
						}
					}
				}
				if allBold(p) {
					c.style.Bold, c.hasStyle = true, true
				}
			}
			row = append(row, c)
		}
		grid = append(grid, row)
	}
	if len(grid) == 0 {
		return
	}
	// Place the cells on the column grid.
	type placed struct {
		cell
		col int
	}
	var rows [][]placed
	for _, row := range grid {
		var pr []placed
		col := 0
		for _, c := range row {
			pr = append(pr, placed{c, col})
			col += c.colSpan
		}
		cols = min(max(cols, col), maxCols)
		rows = append(rows, pr)
	}
	out := &docfile.TableItem{}
	cells := make([][]string, len(rows))
	for i := range cells {
		cells[i] = make([]string, cols)
	}
	owner := map[int]int{} // column -> row of the cell a vertical merge there started in
	merges := map[[2]int]*docfile.TableMerge{}
	for ri, pr := range rows {
		for _, p := range pr {
			c := p.col
			if c >= cols {
				continue
			}
			p.colSpan = min(p.colSpan, cols-c)
			switch {
			case p.cont:
				if or, ok := owner[c]; ok {
					merges[[2]int{or, c}].Rows = ri - or + 1
					continue
				}
				fallthrough
			default:
				delete(owner, c)
				cells[ri][c] = p.text
				if p.hasStyle {
					out.Styles = append(out.Styles, at(p.style, ri, c))
				}
				if p.colSpan > 1 || p.restart {
					m := &docfile.TableMerge{Row: ri, Col: c, Rows: 1, Cols: p.colSpan}
					merges[[2]int{ri, c}] = m
					if p.restart {
						owner[c] = ri
					}
				}
			}
		}
	}
	for ri := range cells {
		out.Rows = append(out.Rows, docfile.TableRow{Cells: cells[ri]})
	}
	for _, m := range merges {
		if m.Rows > 1 || m.Cols > 1 {
			out.Merges = append(out.Merges, *m)
		}
	}
	sort.Slice(out.Merges, func(i, j int) bool {
		a, b := out.Merges[i], out.Merges[j]
		return a.Row < b.Row || a.Row == b.Row && a.Col < b.Col
	})
	r.add(docfile.Item{Table: out})
}

func allBold(p *node) bool {
	n := 0
	for _, run := range p.children("r") {
		if strings.TrimSpace(plainText(run)) == "" {
			continue
		}
		n++
		if !on(run.child("rPr").childOrNil("b")) {
			return false
		}
	}
	return n > 0
}

// childOrNil is child that tolerates a nil receiver.
func (n *node) childOrNil(name string) *node {
	if n == nil {
		return nil
	}
	return n.child(name)
}

// find2 returns every descendant called name.
func (n *node) find2(name string) []*node {
	var out []*node
	for _, k := range n.kids {
		if k.name == name {
			out = append(out, k)
		} else {
			out = append(out, k.find2(name)...)
		}
	}
	return out
}

// at gives the style a place in the table.
func at(st docfile.TableCellStyle, row, col int) docfile.TableCellStyle {
	st.Row, st.Col = row, col
	return st
}

type paraInfo struct {
	heading int
	quote   bool
	code    bool
	list    docfile.List
	align   int
}

func (r *reader) paragraphInfo(p *node) paraInfo {
	var info paraInfo
	ppr := p.child("pPr")
	if ppr == nil {
		return info
	}
	if st := ppr.child("pStyle"); st != nil {
		name := r.styles[st.attr("val")]
		if name == "" {
			name = strings.ToLower(st.attr("val"))
		}
		switch {
		case name == "title":
			info.heading = 1
		case strings.HasPrefix(name, "heading "), strings.HasPrefix(name, "heading"):
			if d := strings.TrimSpace(strings.TrimPrefix(name, "heading")); len(d) > 0 && d[0] >= '1' && d[0] <= '9' {
				info.heading = int(d[0] - '0')
			}
		case strings.Contains(name, "quote"):
			info.quote = true
		case strings.Contains(name, "macro text"), strings.Contains(name, "source code"), name == "code", strings.Contains(name, "preformatted"):
			info.code = true
		case strings.Contains(name, "list bullet"):
			info.list = docfile.List{Kind: 1}
		case strings.Contains(name, "list number"):
			info.list = docfile.List{Kind: 2}
		}
	}
	if num := ppr.child("numPr"); num != nil {
		numID, ilvl := "", 0
		if n := num.child("numId"); n != nil {
			numID = n.attr("val")
		}
		if l := num.child("ilvl"); l != nil {
			ilvl, _ = strconv.Atoi(l.attr("val"))
		}
		if numID != "" && numID != "0" {
			ordered := info.list.Kind == 2
			if levels, ok := r.numbers[numID]; ok {
				if o, ok := levels[ilvl]; ok {
					ordered = o
				}
			}
			info.list = docfile.List{Kind: 1, Level: ilvl}
			if ordered {
				info.list.Kind = 2
			}
		}
	}
	if jc := ppr.child("jc"); jc != nil {
		switch jc.attr("val") {
		case "center":
			info.align = 1
		case "right", "end":
			info.align = 2
		}
	}
	return info
}

func (r *reader) paragraph(p *node) {
	info := r.paragraphInfo(p)
	if info.list.Kind != 0 || info.quote || info.align != 0 {
		para := docfile.Para{Align: info.align, List: info.list}
		if info.quote {
			para.QuoteBar = "#9ca3af"
		}
		r.add(docfile.Item{Para: &para})
	}
	r.runs(p.kids, docfile.Format{}, info)
	if c := r.doc.Content; len(c) > 0 && c[len(c)-1].Equation != nil {
		return
	}
	r.text("\n", docfile.Format{})
}

// runs converts a paragraph's children: runs, hyperlinks and fields.
func (r *reader) runs(kids []*node, inherit docfile.Format, info paraInfo) {
	var lastDel *canvas.ProposalMark // a deletion just read, which an insertion right after it by the same person completes
	for _, k := range kids {
		if k.name != "del" && k.name != "ins" {
			lastDel = nil
		}
		switch k.name {
		case "del", "ins":
			// A tracked change becomes a suggestion.
			kind := canvas.ProposalInsert
			if k.name == "del" {
				kind = canvas.ProposalDelete
			}
			t := time.Unix(0, 0)
			if d, err := time.Parse(time.RFC3339, k.attr("date")); err == nil {
				t = d
			}
			m := canvas.ProposalMark{Kind: kind, ID: "w" + k.attr("id"), Author: k.attr("author"), Time: t}
			if kind == canvas.ProposalInsert && lastDel != nil && lastDel.Author == m.Author && lastDel.Time.Equal(m.Time) {
				m.ID = lastDel.ID
			}
			f := inherit
			f.Proposal = m.String()
			r.runs(k.kids, f, info)
			if kind == canvas.ProposalDelete {
				mm := m
				lastDel = &mm
			} else {
				lastDel = nil
			}
			continue
		case "r":
			if rpr := k.child("rPr"); rpr != nil && rpr.child("vanish") != nil {
				if c := r.doc.Content; len(c) > 0 && c[len(c)-1].Equation != nil {
					if alt := strings.TrimSpace(plainText(k)); alt != c[len(c)-1].Equation.Formula {
						c[len(c)-1].Equation.Alt = alt
					}
					continue
				}
			}
			r.run(k, inherit, info)
		case "hyperlink":
			f := inherit
			if id := k.attr("r:id"); id != "" {
				f.Link = r.rels[id]
			} else if a := k.attr("anchor"); a != "" {
				f.Link = "#" + a
			}
			r.runs(k.kids, f, info)
		case "commentRangeStart":
			r.openComment(k.attr("id"))
		case "commentRangeEnd":
			r.closeComment(k.attr("id"))
		case "smartTag", "sdt", "sdtContent", "fldSimple":
			r.runs(k.kids, inherit, info)
		case "oMathPara":
			r.equation(k, false)
		case "oMath":
			r.equation(k, textAround(kids, k))
		}
	}
}

// textAround says the paragraph's children hold visible text.
func textAround(kids []*node, except *node) bool {
	for _, k := range kids {
		if k == except || k.name != "r" {
			continue
		}
		if rpr := k.child("rPr"); rpr != nil && rpr.child("vanish") != nil {
			continue
		}
		if strings.TrimSpace(plainText(k)) != "" {
			return true
		}
	}
	return false
}

// equation reads an Office Math equation into an equation item.
func (r *reader) equation(m *node, inline bool) {
	formula := linearFormula(m)
	if formula == "" {
		return
	}
	if inline {
		r.add(docfile.Item{Equation: &docfile.EquationItem{Formula: formula, Inline: true}})
		return
	}
	if c := r.doc.Content; len(c) > 0 {
		if last := c[len(c)-1].Chars; last != nil && last.Text != "" && !strings.HasSuffix(last.Text, "\n") {
			r.text("\n", docfile.Format{}) // the equation starts a line of its own
		}
	}
	r.add(docfile.Item{Equation: &docfile.EquationItem{Formula: formula}})
}

func on(n *node) bool {
	if n == nil {
		return false
	}
	switch n.attr("val") {
	case "0", "false", "off", "none":
		return false
	}
	return true
}

func (r *reader) runFormat(rpr *node, inherit docfile.Format, info paraInfo) docfile.Format {
	f := inherit
	if info.heading > 0 && info.heading <= len(headingScales) {
		f.Scale, f.FontWeight = headingScales[info.heading-1], 700
	}
	if info.code {
		f.FontFamily = "monospace"
	}
	if rpr == nil {
		return f
	}
	if on(rpr.child("b")) {
		f.FontWeight = 700
	}
	if on(rpr.child("i")) {
		f.FontStyle = "italic"
	}
	if u := rpr.child("u"); u != nil && on(u) {
		f.Underline = true
	}
	if on(rpr.child("strike")) || on(rpr.child("dstrike")) {
		f.Strike = true
	}
	if v := rpr.child("vertAlign"); v != nil {
		switch v.attr("val") {
		case "superscript":
			f.Superscript = true
		case "subscript":
			f.Subscript = true
		}
	}
	if c := rpr.child("color"); c != nil {
		if v := c.attr("val"); v != "" && v != "auto" && len(v) == 6 {
			f.Color = "#" + strings.ToLower(v)
		}
	}
	if h := rpr.child("highlight"); h != nil {
		if bg, ok := highlights[h.attr("val")]; ok {
			f.Background = bg
		}
	}
	if sz := rpr.child("sz"); sz != nil {
		if half, err := strconv.Atoi(sz.attr("val")); err == nil && half > 0 && info.heading == 0 {
			f.FontSize = float64(half) / 2 * 96 / 72
		}
	}
	if l := rpr.child("lang"); l != nil {
		if v := l.attr("val"); v != "" && v != r.defLang && canvas.ValidLang(v) {
			f.Lang = v
		}
	}
	if fonts := rpr.child("rFonts"); fonts != nil {
		name := fonts.attr("ascii")
		switch strings.ToLower(name) {
		case "courier new", "courier", "consolas", "menlo", "monaco", "lucida console", "source code pro":
			f.FontFamily = "monospace"
		}
	}
	return f
}

var highlights = map[string]string{
	"yellow": "#ffff00", "green": "#00ff00", "cyan": "#00ffff", "magenta": "#ff00ff", "blue": "#0000ff",
	"red": "#ff0000", "lightGray": "#d3d3d3", "darkYellow": "#808000",
}

func (r *reader) run(run *node, inherit docfile.Format, info paraInfo) {
	f := r.runFormat(run.child("rPr"), inherit, info)
	if id := r.currentComment(); id != "" {
		f.Comment = id
	}
	var sb strings.Builder
	flush := func() {
		r.text(sb.String(), f)
		sb.Reset()
	}
	for _, k := range run.kids {
		switch k.name {
		case "t", "delText":
			sb.WriteString(k.text)
		case "tab":
			sb.WriteByte('\t')
		case "br", "cr":
			if k.attr("type") == "page" {
				flush()
				r.add(docfile.Item{Break: &docfile.Break{Kind: 1}})
			} else {
				sb.WriteString(" ")
			}
		case "footnoteReference":
			flush()
			if id := k.attr("id"); r.notes[id] != "" {
				r.add(docfile.Item{Footnote: &docfile.FootnoteMark{ID: id}})
				if !r.seenNotes[id] {
					r.seenNotes[id] = true
					r.doc.Footnotes = append(r.doc.Footnotes, docfile.Footnote{ID: id, Text: r.notes[id]})
				}
			}
		case "drawing", "pict":
			flush()
			r.picture(k)
		}
	}
	flush()
}

func (r *reader) picture(d *node) {
	blip := d.find("blip")
	if blip == nil || r.images >= maxImages {
		return
	}
	target := r.rels[blip.attr("r:embed")]
	if target == "" {
		return
	}
	mime := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".svg": "image/svg+xml", ".bmp": "image/bmp", ".webp": "image/webp"}[strings.ToLower(path.Ext(target))]
	if mime == "" {
		return
	}
	url, ok := r.pics[target]
	if !ok {
		data, err := r.read(target)
		if err != nil || r.imageBytes+len(data) > maxImageBytes {
			return
		}
		r.imageBytes += len(data)
		url = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		r.pics[target] = url
	}
	img := &docfile.ImageItem{URL: url}
	if dp := d.find("docPr"); dp != nil { // the picture's description: its alternative text
		img.Alt = strings.TrimSpace(dp.attr("descr"))
	}
	if ext := d.find("extent"); ext != nil {
		cx, _ := strconv.ParseFloat(ext.attr("cx"), 64)
		cy, _ := strconv.ParseFloat(ext.attr("cy"), 64)
		img.Width, img.Height = cx/emuPerPx, cy/emuPerPx
	}
	r.images++
	r.add(docfile.Item{Image: img})
}

// trimEnd drops the empty line the last paragraph leaves.
func (r *reader) trimEnd() {
	c := r.doc.Content
	for len(c) > 0 {
		last := c[len(c)-1]
		if last.Chars != nil && last.Chars.Text == "\n" {
			c = c[:len(c)-1]
			continue
		}
		break
	}
	r.doc.Content = c
}
