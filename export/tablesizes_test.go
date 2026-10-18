package export

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

// A column and a row a person sized keep their sizes in the formats that can say so.
func sizedTableDoc() docfile.Document {
	return docfile.Document{Title: "T", Content: []docfile.Item{
		{Table: &docfile.TableItem{
			Rows:       []docfile.TableRow{{Cells: []string{"Item", "Qty"}}, {Cells: []string{"Pens", "3"}}},
			ColWidths:  []float64{200, 0},
			RowHeights: []float64{0, 60},
		}},
		{Chars: &docfile.Chars{Text: "\n"}},
	}}
}

func zipFile(t *testing.T, data []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			return string(b)
		}
	}
	t.Fatalf("no %s", name)
	return ""
}

func TestSizedTableInHTML(t *testing.T) {
	out, err := HTML{}.Export(FromDocument(sizedTableDoc()))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `<th style="width:200px;">Item</th>`) || !strings.Contains(s, `height:60px;`) || strings.Contains(s, `<th>Qty</th>`) == false {
		t.Fatalf("html: %s", s)
	}
}

func TestSizedTableInWord(t *testing.T) {
	out, err := DOCX{}.Export(FromDocument(sizedTableDoc()))
	if err != nil {
		t.Fatal(err)
	}
	x := zipFile(t, out, "word/document.xml")
	if !strings.Contains(x, `<w:gridCol w:w="3000"/><w:gridCol w:w="2000"/>`) || !strings.Contains(x, `<w:tcW w:w="3000" w:type="dxa"/>`) || !strings.Contains(x, `<w:trHeight w:val="900" w:hRule="atLeast"/>`) {
		t.Fatalf("docx: %s", x)
	}
}

func TestSizedTableInOpenDocument(t *testing.T) {
	out, err := ODT{}.Export(FromDocument(sizedTableDoc()))
	if err != nil {
		t.Fatal(err)
	}
	c := zipFile(t, out, "content.xml")
	if !strings.Contains(c, `style:column-width="2.0833in"`) || !strings.Contains(c, `style:min-row-height="0.6250in"`) || !strings.Contains(c, `<table:table-column table:style-name="TableCol"/>`) {
		t.Fatalf("odt: %s", c)
	}
}

func TestSizedTableInRTFAndColumnShares(t *testing.T) {
	out, err := RTF{}.Export(FromDocument(sizedTableDoc()))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `\trrh900`) || !strings.Contains(s, `\cellx3000`) {
		t.Fatalf("rtf: %s", s)
	}
	cells := []Block{{TableCols: 3, TableColW: 100}, {TableCols: 3}, {TableCols: 3}}
	w := tableColumnWidths(cells, 300, 1)
	if w[0] != 100 || w[1] != 100 || w[2] != 100 {
		t.Fatalf("shares %v", w)
	}
	w = tableColumnWidths([]Block{{TableCols: 2, TableColW: 1000}, {TableCols: 2}}, 300, 1)
	if w[0]+w[1] != 300 || w[1] < 37 { // the sized column is scaled to leave the other a quarter of an even share
		t.Fatalf("scaled %v", w)
	}
}

func TestSizedTableInPDFAndEPUB(t *testing.T) {
	snap := FromDocument(sizedTableDoc())
	snap.PageWidth, snap.PageHeight, snap.MarginLeft, snap.MarginRight, snap.MarginTop, snap.MarginBottom = 816, 1056, 96, 96, 96, 96
	out, err := PDF{}.Export(snap)
	if err != nil || !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Fatalf("pdf: %v", err)
	}
	if _, err := (EPUB{}).Export(FromDocument(sizedTableDoc())); err != nil {
		t.Fatalf("epub: %v", err)
	}
}
