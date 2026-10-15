package export

import (
	"bytes"
	"compress/zlib"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/elaous/gojiberry/lang"
	"github.com/signintech/gopdf"
)

// Tagged PDF. gopdf draws text and images but writes no structure, so the exporter marks what it draws
// and a second pass turns the marks into a tagged PDF (ISO 32000-1 14.7, the base of PDF/UA):
//
//  1. While drawing, pdfMarkBegin and pdfMarkEnd put a marker into the page's content stream. The marker
//     is a dash-pattern operator whose array carries numbers no real dash pattern uses
//     ("[7777 kind id a b] 0 d"); gopdf writes the array as given and nothing else in this package
//     sets a dash pattern.
//  2. tagPDF reads the finished file, replaces each marker by "/Tag <</MCID n>> BDC" or "EMC", builds
//     a structure tree (Document, headings, paragraphs, lists, tables, figures) from the marks, and
//     appends it as an incremental update: new versions of the catalog, the pages and their content
//     streams, plus the structure objects, a new cross-reference section and trailer. The original bytes
//     are not moved.
//
// Links, languages and nesting:
//
//   - A link run is its own marked piece (a Link element); the link annotation gopdf adds is found in
//     the page's /Annots in the same order, gets a /StructParent key and a /Contents, and the Link
//     element holds the text and an /OBJR to the annotation, so the annotation is in the tree.
//   - A run whose Format.Lang differs from the document's is a Span element (a Link element for a link)
//     with its own /Lang; the document's language stays on the catalog. Table cells do the same: their text
//     is split into Span and Link elements inside the TH or TD.
//   - A list item at a deeper Block.Level puts its L inside the LI of the item before it.
//
// pdfua.Check shows what is there; a full audit with veraPDF or PAC has not been run.

var objHeadRE = regexp.MustCompile(`\n(\d+) 0 obj\n`)

// cidFontObjects returns the number and the dictionary of every font object of subtype CIDFontType2.
func cidFontObjects(raw []byte) (nums []int, bodies [][]byte) {
	at := 0
	for {
		i := bytes.Index(raw[at:], []byte("/Subtype /CIDFontType2"))
		if i < 0 {
			return
		}
		i += at
		at = i + 1
		start := bytes.LastIndex(raw[:i], []byte("\nendobj"))
		head := objHeadRE.FindSubmatchIndex(raw[max(start, 0) : i+1])
		end := bytes.Index(raw[i:], []byte("endobj"))
		if head == nil || end < 0 {
			continue
		}
		num, _ := strconv.Atoi(string(raw[max(start, 0)+head[2] : max(start, 0)+head[3]]))
		nums = append(nums, num)
		bodies = append(bodies, raw[max(start, 0)+head[1]:i+end])
	}
}

const (
	markBegin    = 7777
	markEnd      = 7778
	markArtifact = 7779
)

// The kinds of marked content, which are also the structure types they become.
const (
	tagP = iota + 1
	tagH1
	tagH2
	tagH3
	tagQuote
	tagLbl
	tagLBody
	tagFigure
	tagCaption
	tagTH
	tagTD
	tagLink // a = the run's language (index+1 in pdfRunLangs, 0 for none)
	tagSpan // a = the same
	tagH4
	tagH5
	tagH6
)

var tagNames = map[int]string{
	tagP: "P", tagH1: "H1", tagH2: "H2", tagH3: "H3", tagQuote: "BlockQuote", tagLbl: "Lbl", tagLBody: "LBody",
	tagFigure: "Figure", tagCaption: "Caption", tagTH: "TH", tagTD: "TD", tagLink: "Link", tagSpan: "Span", tagH4: "H4", tagH5: "H5", tagH6: "H6",
}

// pdfTag says what the next marked piece of content is. id names the structure element (all the pieces
// with the same kind and id belong to one element); a and b carry what the kind needs: for a list item
// a is 1 when numbered and b the nesting level, for a table cell a is the table (the index of its first
// cell) and b the row*65536+column. A Link or Span piece belongs to the block piece opened before it
// and has no id.
type pdfTag struct{ kind, id, a, b int }

func pdfMarkBegin(pdf *gopdf.GoPdf, t pdfTag) {
	pdf.SetCustomLineType([]float64{markBegin, float64(t.kind), float64(t.id), float64(t.a), float64(t.b)}, 0)
}

func pdfMarkEnd(pdf *gopdf.GoPdf) { pdf.SetCustomLineType([]float64{markEnd}, 0) }

// pdfMarkArtifact starts content that is not part of the document's content (a table border); end it
// with pdfMarkEnd.
func pdfMarkArtifact(pdf *gopdf.GoPdf) { pdf.SetCustomLineType([]float64{markArtifact}, 0) }

var markRE = regexp.MustCompile(`\[(7777|7778|7779)\.00((?: \d+\.00){0,4})\] 0\.00 d\n`)

// piece is one marked stretch of content: which element it belongs to and where it is.
type piece struct{ page, mcid int }

// kid is one child of an element in reading order: a piece of marked content or an inner element (a
// Link or Span inside a paragraph).
type kid struct {
	p     piece
	child *elem
}

type elem struct {
	tag    pdfTag
	pieces []piece
	kids   []kid
	node   *node
	// For a Link: the annotation object, its /StructParent key and its /URI as a PDF string.
	annot, key int
	href       string
}

type node struct {
	typ      string
	elem     *elem
	children []*node
	obj      int
	parent   *node
	numbered bool // a list: its items are numbered
}

// object is the text of one PDF object in the file.
func objectText(raw []byte, n int) (string, bool) {
	head := []byte("\n" + strconv.Itoa(n) + " 0 obj\n")
	i := bytes.Index(raw, head)
	if i < 0 {
		return "", false
	}
	i += len(head)
	j := bytes.Index(raw[i:], []byte("\nendobj"))
	if j < 0 {
		return "", false
	}
	return string(raw[i : i+j]), true
}

var (
	kidsRE     = regexp.MustCompile(`/Kids\s*\[([^\]]*)\]`)
	refRE      = regexp.MustCompile(`(\d+) 0 R`)
	contentsRE = regexp.MustCompile(`/Contents\s+(\d+) 0 R`)
	sizeRE     = regexp.MustCompile(`/Size (\d+)`)
	startRE    = regexp.MustCompile(`startxref\n(\d+)\n`)
	annotsRE   = regexp.MustCompile(`/Annots\s*\[([^\]]*)\]`)
	uriRE      = regexp.MustCompile(`/URI\s*(\((?:\\.|[^\\)])*\))`)
)

// streamOf returns the data of a stream object and the dictionary text before it.
func streamOf(body string) (dict string, data []byte, ok bool) {
	i := bytes.Index([]byte(body), []byte("stream\n"))
	if i < 0 {
		return "", nil, false
	}
	dict = body[:i]
	rest := body[i+len("stream\n"):]
	k := bytes.LastIndex([]byte(rest), []byte("\nendstream"))
	if k < 0 {
		return "", nil, false
	}
	return dict, []byte(rest[:k]), true
}

func inflate(b []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, 256<<20))
}

func deflate(b []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

// pdfTextString writes s as a PDF text string (UTF-16BE with a byte order mark, in hex).
func pdfTextString(s string) string {
	u := utf16.Encode([]rune(s))
	out := []byte{'<', 'F', 'E', 'F', 'F'}
	for _, c := range u {
		out = append(out, fmt.Sprintf("%04X", c)...)
	}
	return string(append(out, '>'))
}

// tagPDF turns the markers in the finished file raw into a tagged PDF.
func tagPDF(raw []byte, snap Snapshot) ([]byte, error) {
	pagesObj, ok := objectText(raw, 2)
	if !ok {
		return nil, fmt.Errorf("export: tagging the PDF: no page tree")
	}
	km := kidsRE.FindStringSubmatch(pagesObj)
	if km == nil {
		return nil, fmt.Errorf("export: tagging the PDF: no page list")
	}
	var pageObjs []int
	for _, m := range refRE.FindAllStringSubmatch(km[1], -1) {
		n, _ := strconv.Atoi(m[1])
		pageObjs = append(pageObjs, n)
	}
	sm := sizeRE.FindSubmatch(raw[bytes.LastIndex(raw, []byte("trailer")):])
	if sm == nil {
		return nil, fmt.Errorf("export: tagging the PDF: no trailer size")
	}
	size, _ := strconv.Atoi(string(sm[1]))
	next := size
	langs := pdfRunLangs(snap)
	nextKey := len(pageObjs) // /StructParent keys of annotations follow the pages' keys
	var linkElems []*elem

	var elems []*elem
	byKey := map[[2]int]*elem{}
	type update struct {
		n    int
		body []byte
	}
	var updates []update

	for pi, pn := range pageObjs {
		pageText, ok := objectText(raw, pn)
		if !ok {
			return nil, fmt.Errorf("export: tagging the PDF: page object %d missing", pn)
		}
		cm := contentsRE.FindStringSubmatch(pageText)
		if cm == nil {
			continue // an empty page has nothing to tag
		}
		cn, _ := strconv.Atoi(cm[1])
		cbody, ok := objectText(raw, cn)
		if !ok {
			return nil, fmt.Errorf("export: tagging the PDF: content object %d missing", cn)
		}
		_, data, ok := streamOf(cbody)
		if !ok {
			return nil, fmt.Errorf("export: tagging the PDF: content object %d has no stream", cn)
		}
		content, err := inflate(data)
		if err != nil {
			return nil, fmt.Errorf("export: tagging the PDF: content of page %d: %w", pi, err)
		}
		mcid := 0
		var out bytes.Buffer
		last := 0
		var current, outer *elem
		var pageLinks []*elem
		for _, loc := range markRE.FindAllSubmatchIndex(content, -1) {
			out.Write(content[last:loc[0]])
			last = loc[1]
			code := string(content[loc[2]:loc[3]])
			switch code {
			case "7778":
				out.WriteString("EMC\n")
				current = nil
			case "7779":
				out.WriteString("/Artifact BMC\n")
			default:
				var nums [4]int
				i := 0
				for _, f := range bytes.Fields(content[loc[4]:loc[5]]) {
					if i < 4 {
						v, _ := strconv.ParseFloat(string(f), 64)
						nums[i] = int(v)
						i++
					}
				}
				t := pdfTag{kind: nums[0], id: nums[1], a: nums[2], b: nums[3]}
				name, known := tagNames[t.kind]
				if !known {
					return nil, fmt.Errorf("export: tagging the PDF: unknown marker kind %d", t.kind)
				}
				if t.kind == tagLink || t.kind == tagSpan {
					if outer == nil {
						return nil, fmt.Errorf("export: tagging the PDF: a %s outside any block", name)
					}
					current = &elem{tag: t}
					outer.kids = append(outer.kids, kid{child: current})
					if t.kind == tagLink {
						pageLinks = append(pageLinks, current)
					}
				} else {
					key := [2]int{t.kind, t.id}
					current = byKey[key]
					if current == nil {
						current = &elem{tag: t}
						byKey[key] = current
						elems = append(elems, current)
					}
					outer = current
				}
				current.pieces = append(current.pieces, piece{pi, mcid})
				current.kids = append(current.kids, kid{p: piece{pi, mcid}})
				fmt.Fprintf(&out, "/%s <</MCID %d>> BDC\n", name, mcid)
				mcid++
			}
		}
		out.Write(content[last:])
		packed := deflate(out.Bytes())
		var body bytes.Buffer
		fmt.Fprintf(&body, "<<\n/Filter/FlateDecode/Length %d\n>>\nstream\n", len(packed))
		body.Write(packed)
		body.WriteString("\nendstream")
		updates = append(updates, update{cn, body.Bytes()})
		// The page gets a key into the parent tree.
		pageTrim := bytes.TrimRight([]byte(pageText), " \n\t")
		if !bytes.HasSuffix(pageTrim, []byte(">>")) {
			return nil, fmt.Errorf("export: tagging the PDF: page object %d has an unexpected shape", pn)
		}
		page := append(append([]byte{}, pageTrim[:len(pageTrim)-2]...), []byte(fmt.Sprintf("  /StructParents %d /Tabs /S\n>>", pi))...)
		updates = append(updates, update{pn, page})

		// The link annotations gopdf added to this page, in the order the links were drawn.
		if len(pageLinks) > 0 {
			am := annotsRE.FindStringSubmatch(pageText)
			var annots []int
			if am != nil {
				for _, m := range refRE.FindAllStringSubmatch(am[1], -1) {
					n, _ := strconv.Atoi(m[1])
					if at, ok := objectText(raw, n); ok && strings.Contains(at, "/Subtype /Link") {
						annots = append(annots, n)
					}
				}
			}
			if len(annots) != len(pageLinks) {
				return nil, fmt.Errorf("export: tagging the PDF: page %d has %d link annotations for %d links", pi, len(annots), len(pageLinks))
			}
			for i, l := range pageLinks {
				at, _ := objectText(raw, annots[i])
				atTrim := bytes.TrimRight([]byte(at), " \n\t")
				if !bytes.HasSuffix(atTrim, []byte(">>")) {
					return nil, fmt.Errorf("export: tagging the PDF: annotation %d has an unexpected shape", annots[i])
				}
				l.annot, l.key, l.href = annots[i], nextKey, "(Footnote)" // an internal link has no /URI: it goes to a footnote or back
				nextKey++
				if um := uriRE.FindStringSubmatch(at); um != nil {
					l.href = um[1]
				}
				body := append(append([]byte{}, atTrim[:len(atTrim)-2]...), []byte(fmt.Sprintf(" /F 4 /StructParent %d /Contents %s>>", l.key, l.href))...)
				updates = append(updates, update{annots[i], body})
				linkElems = append(linkElems, l)
			}
		}
	}
	if len(elems) == 0 {
		return raw, nil // nothing was drawn that needs a tag
	}

	// The tree.
	doc := &node{typ: "Document"}
	var mk func(typ string, e *elem, parent *node) *node
	mk = func(typ string, e *elem, parent *node) *node {
		n := &node{typ: typ, elem: e, parent: parent}
		e.node = n
		for _, k := range e.kids {
			if k.child != nil {
				n.children = append(n.children, mk(tagNames[k.child.tag.kind], k.child, n))
			}
		}
		return n
	}
	// Open lists, outermost first: a deeper item goes into the item before it.
	type openList struct {
		l        *node
		level    int
		numbered int
		item     *node
	}
	var lists []*openList
	var table, row *node
	itemID := -1
	rowIdx, tableID := -1, -1
	for _, e := range elems {
		t := e.tag
		switch t.kind {
		case tagLbl, tagLBody:
			table, row, tableID, rowIdx = nil, nil, -1, -1
			if t.id != itemID {
				itemID = t.id
				for len(lists) > 0 && lists[len(lists)-1].level > t.b {
					lists = lists[:len(lists)-1]
				}
				if n := len(lists); n > 0 && lists[n-1].level == t.b && lists[n-1].numbered != t.a {
					lists = lists[:n-1] // a bulleted list followed by a numbered one at the same level
				}
				if n := len(lists); n == 0 || lists[n-1].level < t.b {
					parent := doc
					if n > 0 {
						// A nested list is inside the body of the item before it, not beside it.
						parent = lists[n-1].item
						for i := len(parent.children) - 1; i >= 0; i-- {
							if parent.children[i].typ == "LBody" {
								parent = parent.children[i]
								break
							}
						}
					}
					l := &node{typ: "L", parent: parent, numbered: t.a == 1}
					parent.children = append(parent.children, l)
					lists = append(lists, &openList{l: l, level: t.b, numbered: t.a})
				}
				top := lists[len(lists)-1]
				top.item = &node{typ: "LI", parent: top.l}
				top.l.children = append(top.l.children, top.item)
			}
			li := lists[len(lists)-1].item
			li.children = append(li.children, mk(tagNames[t.kind], e, li))
		case tagTH, tagTD:
			lists, itemID = nil, -1
			if table == nil || tableID != t.a {
				table = &node{typ: "Table", parent: doc}
				doc.children = append(doc.children, table)
				tableID, rowIdx, row = t.a, -1, nil
			}
			if r := t.b >> 16; row == nil || r != rowIdx {
				row = &node{typ: "TR", parent: table}
				table.children = append(table.children, row)
				rowIdx = r
			}
			row.children = append(row.children, mk(tagNames[t.kind], e, row))
		default:
			lists, itemID, table, row, tableID, rowIdx = nil, -1, nil, nil, -1, -1
			doc.children = append(doc.children, mk(tagNames[t.kind], e, doc))
		}
	}

	rootObj := next
	next++
	var all []*node
	var number func(n *node)
	number = func(n *node) {
		n.obj = next
		next++
		all = append(all, n)
		for _, c := range n.children {
			number(c)
		}
	}
	number(doc)

	// Which element holds each marked piece, for the parent tree.
	parents := make([][]int, len(pageObjs))
	for _, n := range all {
		if n.elem == nil {
			continue
		}
		for _, p := range n.elem.pieces {
			for len(parents[p.page]) <= p.mcid {
				parents[p.page] = append(parents[p.page], 0)
			}
			parents[p.page][p.mcid] = n.obj
		}
	}

	for _, n := range all {
		var b bytes.Buffer
		parent := rootObj
		if n.parent != nil {
			parent = n.parent.obj
		}
		fmt.Fprintf(&b, "<< /Type /StructElem /S /%s /P %d 0 R", n.typ, parent)
		if n.elem != nil && (n.elem.tag.kind == tagLink || n.elem.tag.kind == tagSpan) && n.elem.tag.a > 0 && n.elem.tag.a <= len(langs) {
			fmt.Fprintf(&b, " /Lang %s", pdfTextString(langs[n.elem.tag.a-1]))
		}
		if n.elem != nil && len(n.elem.pieces) > 0 {
			// Its own content and inner elements, in reading order.
			fmt.Fprintf(&b, " /Pg %d 0 R /K [", pageObjs[n.elem.pieces[0].page])
			for _, k := range n.elem.kids {
				if k.child != nil {
					fmt.Fprintf(&b, " %d 0 R", k.child.node.obj)
				} else {
					fmt.Fprintf(&b, " << /Type /MCR /Pg %d 0 R /MCID %d >>", pageObjs[k.p.page], k.p.mcid)
				}
			}
			for _, c := range n.children {
				if c.elem == nil { // a list nested in this item's body
					fmt.Fprintf(&b, " %d 0 R", c.obj)
				}
			}
			if n.elem.annot != 0 {
				fmt.Fprintf(&b, " << /Type /OBJR /Obj %d 0 R /Pg %d 0 R >>", n.elem.annot, pageObjs[n.elem.pieces[0].page])
			}
			b.WriteString(" ]")
			if n.elem.annot != 0 {
				fmt.Fprintf(&b, " /Alt %s", n.elem.href)
			}
		} else if len(n.children) > 0 {
			b.WriteString(" /K [")
			for _, c := range n.children {
				fmt.Fprintf(&b, " %d 0 R", c.obj)
			}
			b.WriteString(" ]")
		}
		if n.elem != nil && n.elem.tag.kind == tagFigure && n.elem.tag.id < len(snap.Blocks) {
			fmt.Fprintf(&b, " /Alt %s", pdfTextString(snap.Blocks[n.elem.tag.id].ImageAlt))
		}
		switch n.typ {
		case "TH": // every header cell is in the first row, so it heads a column
			b.WriteString(" /A << /O /Table /Scope /Column >>")
		case "L":
			if n.numbered {
				b.WriteString(" /A << /O /List /ListNumbering /Decimal >>")
			} else {
				b.WriteString(" /A << /O /List /ListNumbering /Disc >>")
			}
		}
		b.WriteString(" >>")
		updates = append(updates, update{n.obj, b.Bytes()})
	}
	var root bytes.Buffer
	fmt.Fprintf(&root, "<< /Type /StructTreeRoot /K %d 0 R /ParentTree << /Nums [", doc.obj)
	for pi, refs := range parents {
		fmt.Fprintf(&root, " %d [", pi)
		for _, r := range refs {
			if r == 0 {
				root.WriteString(" null")
			} else {
				fmt.Fprintf(&root, " %d 0 R", r)
			}
		}
		root.WriteString(" ]")
	}
	for _, l := range linkElems {
		fmt.Fprintf(&root, " %d %d 0 R", l.key, l.node.obj)
	}
	fmt.Fprintf(&root, " ] >> /ParentTreeNextKey %d >>", nextKey)
	updates = append(updates, update{rootObj, root.Bytes()})

	// The XMP metadata that says the file follows PDF/UA-1 and what it is called.
	xmp := xmpPacket(snap)
	metaObj := next
	next++
	updates = append(updates, update{metaObj, []byte(fmt.Sprintf("<< /Type /Metadata /Subtype /XML /Length %d >>\nstream\n%s\nendstream", len(xmp), xmp))})

	// A CIDFontType2 says how its CIDs map to glyphs (PDF/UA, ISO 14289-1 7.21.3.2). The writer leaves the
	// entry out, which means Identity; say so, with the font object otherwise as it is.
	cidNums, cidBodies := cidFontObjects(raw)
	for k, num := range cidNums {
		body := bytes.TrimRight(cidBodies[k], " \n\t")
		if bytes.Contains(body, []byte("/CIDToGIDMap")) || !bytes.HasSuffix(body, []byte(">>")) {
			continue
		}
		fixed := append(append([]byte{}, body[:len(body)-2]...), []byte("/CIDToGIDMap /Identity\n>>")...)
		updates = append(updates, update{num, fixed})
	}

	// The catalog says the file is tagged.
	cat, ok := objectText(raw, 1)
	if !ok {
		return nil, fmt.Errorf("export: tagging the PDF: no catalog")
	}
	catTrim := bytes.TrimRight([]byte(cat), " \n\t")
	if !bytes.HasSuffix(catTrim, []byte(">>")) {
		return nil, fmt.Errorf("export: tagging the PDF: the catalog has an unexpected shape")
	}
	var nc bytes.Buffer
	nc.Write(catTrim[:len(catTrim)-2])
	fmt.Fprintf(&nc, "  /MarkInfo << /Marked true >>\n  /StructTreeRoot %d 0 R\n  /ViewerPreferences << /DisplayDocTitle true >>\n  /Metadata %d 0 R\n", rootObj, metaObj)
	if snap.Lang != "" && lang.ValidTag(snap.Lang) {
		fmt.Fprintf(&nc, "  /Lang %s\n", pdfTextString(snap.Lang))
	}
	nc.WriteString(">>")
	updates = append(updates, update{1, nc.Bytes()})

	// The incremental update: objects, then a cross-reference section for just those.
	prev := startRE.FindSubmatch(raw[bytes.LastIndex(raw, []byte("startxref")):])
	if prev == nil {
		return nil, fmt.Errorf("export: tagging the PDF: no startxref")
	}
	trailerAt := bytes.LastIndex(raw, []byte("trailer"))
	trailer := string(raw[trailerAt+len("trailer") : bytes.LastIndex(raw, []byte("startxref"))])
	sort.Slice(updates, func(i, j int) bool { return updates[i].n < updates[j].n })
	var out bytes.Buffer
	out.Write(raw)
	if !bytes.HasSuffix(raw, []byte("\n")) {
		out.WriteString("\n")
	}
	offsets := map[int]int{}
	for _, u := range updates {
		offsets[u.n] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n", u.n)
		out.Write(u.body)
		out.WriteString("\nendobj\n")
	}
	xrefAt := out.Len()
	out.WriteString("xref\n0 1\n0000000000 65535 f \n")
	for _, u := range updates {
		fmt.Fprintf(&out, "%d 1\n%010d 00000 n \n", u.n, offsets[u.n])
	}
	newTrailer := sizeRE.ReplaceAllString(trailer, fmt.Sprintf("/Size %d", next))
	k := bytes.LastIndex([]byte(newTrailer), []byte(">>"))
	if k < 0 {
		return nil, fmt.Errorf("export: tagging the PDF: the trailer has an unexpected shape")
	}
	newTrailer = newTrailer[:k] + "/Prev " + string(prev[1]) + "\n>>"
	fmt.Fprintf(&out, "trailer%s\nstartxref\n%d\n%%%%EOF\n", newTrailer, xrefAt)
	return out.Bytes(), nil
}

// xmpPacket is the XMP metadata of the file: the PDF/UA-1 identifier (ISO 14289-1), the title and the
// language of the document.
func xmpPacket(snap Snapshot) string {
	esc := func(s string) string {
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	title := snap.Title
	if title == "" {
		title = "Untitled"
	}
	var b strings.Builder
	b.WriteString("<?xpacket begin=\"\uFEFF\" id=\"W5M0MpCehiHzreSzNTczkc9d\"?>\n")
	b.WriteString("<x:xmpmeta xmlns:x=\"adobe:ns:meta/\"><rdf:RDF xmlns:rdf=\"http://www.w3.org/1999/02/22-rdf-syntax-ns#\">\n")
	b.WriteString("<rdf:Description rdf:about=\"\" xmlns:dc=\"http://purl.org/dc/elements/1.1/\" xmlns:pdfuaid=\"http://www.aiim.org/pdfua/ns/id/\">\n")
	b.WriteString("<pdfuaid:part>1</pdfuaid:part>\n")
	b.WriteString("<dc:title><rdf:Alt><rdf:li xml:lang=\"x-default\">" + esc(title) + "</rdf:li></rdf:Alt></dc:title>\n")
	if snap.Lang != "" && lang.ValidTag(snap.Lang) {
		b.WriteString("<dc:language><rdf:Bag><rdf:li>" + esc(snap.Lang) + "</rdf:li></rdf:Bag></dc:language>\n")
	}
	b.WriteString("</rdf:Description></rdf:RDF></x:xmpmeta>\n<?xpacket end=\"w\"?>")
	return b.String()
}
