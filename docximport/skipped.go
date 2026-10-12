package docximport

import (
	"archive/zip"
	"bytes"
	"strings"
)

// Skipped counts what a .docx file holds that Import does not bring into the document: the elements it has no
// place for. Import never fails on them, it leaves them out; Inspect says how many there were, so a person is
// told before the file is trusted as a copy of the original (the import side of the fidelity report).
//
// Where the content of an element is text that Import does read in another way (a field's shown result, the
// text of a text box's neighbours) only the element's own job is counted, not the text.
type Skipped struct {
	Comments    int // comments in word/comments.xml (their text and authors are not read)
	HeaderFoots int // headers and footers that hold text
	TextBoxes   int // text boxes: their text is not read
	Drawings    int // shapes, charts and diagrams that are not pictures
	Equations   int // Office Math equations (their text is not read)
	Endnotes    int // endnote references (only footnotes are read)
	Fields      int // fields (page number, cross reference, contents): their last shown text stays, the field is gone
	Bookmarks   int // bookmarks (links to one are kept as "#name", the place they point at is not)
	Columns     int // sections laid out in columns
	Layout      int // paragraphs with an indent or spacing of their own
}

// Total is the number of skipped elements.
func (s Skipped) Total() int {
	return s.Comments + s.HeaderFoots + s.TextBoxes + s.Drawings + s.Equations + s.Endnotes + s.Fields + s.Bookmarks + s.Columns + s.Layout
}

// Inspect counts what Import would leave out of a .docx file. It reads the same parts under the same limits, and
// returns ErrNotDocx for what is not a Word document.
func Inspect(data []byte) (Skipped, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Skipped{}, ErrNotDocx
	}
	r := &reader{files: map[string]*zip.File{}, rels: map[string]string{}, styles: map[string]string{}, numbers: map[string]map[int]bool{}, notes: map[string]string{}, seenNotes: map[string]bool{}, pics: map[string]string{}}
	for _, f := range zr.File {
		r.files[f.Name] = f
	}
	body := r.xml("word/document.xml")
	if body == nil || body.name != "document" {
		return Skipped{}, ErrNotDocx
	}
	var s Skipped
	if c := r.xml("word/comments.xml"); c != nil {
		s.Comments = len(c.children("comment"))
	}
	for name := range r.files {
		if !strings.HasPrefix(name, "word/header") && !strings.HasPrefix(name, "word/footer") || !strings.HasSuffix(name, ".xml") {
			continue
		}
		if h := r.xml(name); h != nil && strings.TrimSpace(plainText(h)) != "" {
			s.HeaderFoots++
		}
	}
	s.scan(body)
	return s, nil
}

// scan counts the elements of one part.
func (s *Skipped) scan(n *node) {
	for _, k := range n.kids {
		switch k.name {
		case "drawing", "pict":
			switch {
			case k.find("txbxContent") != nil:
				s.TextBoxes++
			case k.find("blip") == nil && k.find("imagedata") == nil:
				s.Drawings++
			}
			continue // what is inside a shape is not the document's text
		case "oMath", "oMathPara":
			s.Equations++
			continue
		case "endnoteReference":
			s.Endnotes++
		case "fldSimple":
			s.Fields++
		case "fldChar":
			if k.attr("fldCharType") == "begin" {
				s.Fields++
			}
		case "bookmarkStart":
			if !strings.HasPrefix(k.attr("name"), "_") { // Word's own _GoBack and _Toc marks
				s.Bookmarks++
			}
		case "cols":
			if v := k.attr("num"); v != "" && v != "1" && v != "0" {
				s.Columns++
			}
		case "pPr":
			if k.child("ind") != nil || k.child("spacing") != nil {
				s.Layout++
			}
		}
		s.scan(k)
	}
}
