package export

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"testing"

	"github.com/dslipak/pdf"
	"github.com/elaous/gojiberry/pdfua"
)

func taggedSnapshot(t *testing.T) Snapshot {
	t.Helper()
	var png1 bytes.Buffer
	if err := png.Encode(&png1, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	src := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png1.Bytes())
	seg := func(s string) []Segment { return []Segment{{Text: s}} }
	blocks := []Block{
		{Kind: Heading1, Segments: seg("Title")},
		{Kind: Paragraph, Segments: seg("Body text.")},
		{Kind: BulletItem, Segments: seg("one")},
		{Kind: BulletItem, Segments: seg("two")},
		{Kind: Image, ImageSrc: src, ImageAlt: "A square", ImageW: 20, ImageH: 20},
		{Kind: Image, ImageSrc: src, ImageDecorative: true, ImageW: 20, ImageH: 20},
	}
	for r := 0; r < 2; r++ {
		for c := 0; c < 2; c++ {
			blocks = append(blocks, Block{Kind: TableCell, Segments: seg("cell"), TableRow: r, TableCol: c, TableRows: 2, TableCols: 2, TableColSpan: 1, TableRowSpan: 1})
		}
	}
	return Snapshot{Title: "Doc", Lang: "en", Blocks: blocks, PageWidth: 794, PageHeight: 1123, MarginLeft: 40, MarginTop: 40, MarginRight: 40, MarginBottom: 40}
}

func TestPDFExportIsTagged(t *testing.T) {
	data, err := PDF{}.Export(taggedSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := pdfua.Check(data); err != nil || len(got) != 0 {
		t.Fatalf("pdfua: %v, %v", got, err)
	}
	if bytes.Contains(data, []byte("7777.00")) {
		// The markers are compressed in the content; check the text of the structure instead.
		t.Log("markers are inside compressed streams")
	}
	// The text is still readable, so tagging did not damage the page content.
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	pr, err := r.GetPlainText()
	if err != nil {
		t.Fatal(err)
	}
	txt, _ := bytes.NewBuffer(nil), 0
	if _, err := txt.ReadFrom(pr); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Title", "Body text.", "one", "two", "cell"} {
		if !bytes.Contains(txt.Bytes(), []byte(want)) {
			t.Errorf("text %q lost: %q", want, txt.String())
		}
	}
}

func TestStructureHasListAndTable(t *testing.T) {
	data, err := PDF{}.Export(taggedSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/S /H1", "/S /P", "/S /L ", "/S /LI", "/S /Lbl", "/S /LBody", "/S /Table", "/S /TR", "/S /TH", "/S /TD", "/S /Figure", "/Alt <FEFF"} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("structure has no %q", want)
		}
	}
	if bytes.Count(data, []byte("/S /Figure")) != 1 {
		t.Error("the decorative image should be an artifact, not a figure")
	}
}

func TestTaggingSpansPages(t *testing.T) {
	snap := Snapshot{Title: "Long", Lang: "de", PageWidth: 794, PageHeight: 400, MarginLeft: 40, MarginTop: 40, MarginRight: 40, MarginBottom: 40}
	for i := 0; i < 80; i++ {
		snap.Blocks = append(snap.Blocks, Block{Kind: Paragraph, Segments: []Segment{{Text: "Line of text that is long enough to be a paragraph."}}})
	}
	data, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := pdfua.Check(data); err != nil || len(got) != 0 {
		t.Fatalf("pdfua: %v, %v", got, err)
	}
	if n := bytes.Count(data, []byte("/StructParents")); n < 2 {
		t.Errorf("%d pages carry a parent key, want several", n)
	}
}
