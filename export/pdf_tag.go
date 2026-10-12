package export

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"unicode/utf16"

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
// Not covered: link annotations are not in the tree (a checker such as veraPDF reports them), and lists
// are not nested (a nested item is a sibling item). pdfua.Check shows what is there.

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
)

var tagNames = map[int]string{
	tagP: "P", tagH1: "H1", tagH2: "H2", tagH3: "H3", tagQuote: "BlockQuote", tagLbl: "Lbl", tagLBody: "LBody",
	tagFigure: "Figure", tagCaption: "Caption", tagTH: "TH", tagTD: "TD",
}

// pdfTag says what the next marked piece of content is. id names the structure element (all the pieces
// with the same kind and id belong to one element); a and b carry what the kind needs: for a list item
// a is 1 when numbered, for a table cell a is the table (the index of its first cell) and b the
// row*65536+column.
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

type elem struct {
	tag    pdfTag
	pieces []piece
	obj    int // the object number, set when the tree is written
}

type node struct {
	typ      string
	elem     *elem
	children []*node
	obj      int
	parent   *node
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
		var current *elem
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
				key := [2]int{t.kind, t.id}
				current = byKey[key]
				if current == nil {
					current = &elem{tag: t}
					byKey[key] = current
					elems = append(elems, current)
				}
				current.pieces = append(current.pieces, piece{pi, mcid})
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
		page := append(append([]byte{}, pageTrim[:len(pageTrim)-2]...), []byte(fmt.Sprintf("  /StructParents %d\n>>", pi))...)
		updates = append(updates, update{pn, page})
	}
	if len(elems) == 0 {
		return raw, nil // nothing was drawn that needs a tag
	}

	// The tree.
	doc := &node{typ: "Document"}
	var list, table, row *node
	var itemID = -1
	var rowIdx, tableID = -1, -1
	for _, e := range elems {
		t := e.tag
		switch t.kind {
		case tagLbl, tagLBody:
			table, row, tableID, rowIdx = nil, nil, -1, -1
			if list == nil || (t.kind == tagLbl && list.elem != nil && (list.elem.tag.a != t.a)) {
				list = &node{typ: "L", parent: doc, elem: &elem{tag: pdfTag{a: t.a}}}
				doc.children = append(doc.children, list)
				itemID = -1
			}
			if t.id != itemID {
				li := &node{typ: "LI", parent: list}
				list.children = append(list.children, li)
				itemID = t.id
			}
			li := list.children[len(list.children)-1]
			li.children = append(li.children, &node{typ: tagNames[t.kind], elem: e, parent: li})
		case tagTH, tagTD:
			list, itemID = nil, -1
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
			row.children = append(row.children, &node{typ: tagNames[t.kind], elem: e, parent: row})
		default:
			list, itemID, table, row, tableID, rowIdx = nil, -1, nil, nil, -1, -1
			doc.children = append(doc.children, &node{typ: tagNames[t.kind], elem: e, parent: doc})
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
		if n.elem == nil || len(n.children) > 0 {
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
		if len(n.children) > 0 {
			b.WriteString(" /K [")
			for _, c := range n.children {
				fmt.Fprintf(&b, " %d 0 R", c.obj)
			}
			b.WriteString(" ]")
		} else if n.elem != nil && len(n.elem.pieces) > 0 {
			fmt.Fprintf(&b, " /Pg %d 0 R /K [", pageObjs[n.elem.pieces[0].page])
			for _, p := range n.elem.pieces {
				fmt.Fprintf(&b, " << /Type /MCR /Pg %d 0 R /MCID %d >>", pageObjs[p.page], p.mcid)
			}
			b.WriteString(" ]")
		}
		if n.elem != nil && n.elem.tag.kind == tagFigure && n.elem.tag.id < len(snap.Blocks) {
			fmt.Fprintf(&b, " /Alt %s", pdfTextString(snap.Blocks[n.elem.tag.id].ImageAlt))
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
	fmt.Fprintf(&root, " ] >> /ParentTreeNextKey %d >>", len(pageObjs))
	updates = append(updates, update{rootObj, root.Bytes()})

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
	fmt.Fprintf(&nc, "  /MarkInfo << /Marked true >>\n  /StructTreeRoot %d 0 R\n  /ViewerPreferences << /DisplayDocTitle true >>\n", rootObj)
	if snap.Lang != "" {
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
