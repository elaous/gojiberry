package docximport

import (
	"archive/zip"
	"bytes"
	"strings"
)

// Skipped counts what a .docx holds that Import leaves out.
type Skipped struct {
	HeaderFoots int // headers and footers that hold text
	TextBoxes   int // text boxes: their text is not read
	Drawings    int // shapes, charts and diagrams that are not pictures
	Endnotes    int // endnote references (only footnotes are read)
	Fields      int
	Bookmarks   int
	Columns     int // sections laid out in columns
	Layout      int // paragraphs with an indent or spacing of their own
}

// Total is the number of skipped elements.
func (s Skipped) Total() int {
	return s.HeaderFoots + s.TextBoxes + s.Drawings + s.Endnotes + s.Fields + s.Bookmarks + s.Columns + s.Layout
}

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
			continue
		case "oMath", "oMathPara":
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
