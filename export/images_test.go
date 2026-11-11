package export

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

func pngDataURL(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 40), G: uint8(y * 40), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestDOCXEmbedsADataURLPicture(t *testing.T) {
	snap := Snapshot{Blocks: []Block{
		{Kind: Image, ImageSrc: pngDataURL(t, 4, 3), ImageAlt: "A gradient", ImageCaption: "Fig. 1"},
		{Kind: Image, ImageSrc: pngDataURL(t, 2, 2), ImageW: 5000, ImageH: 5000},
		{Kind: Image, ImageSrc: "https://example.com/not-a-data-url.png", ImageCaption: "Remote"},
		{Kind: Image, ImageSrc: "data:image/webp;base64,AAAA", ImageCaption: "Unsupported"},
		{Kind: Image, ImageSrc: "data:image/png;base64,!!!notbase64", ImageCaption: "Broken"},
	}}
	out, err := DOCX{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	media := 0
	var doc string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "word/media/") {
			media++
		}
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			var b bytes.Buffer
			_, _ = b.ReadFrom(rc)
			_ = rc.Close()
			doc = b.String()
		}
	}
	if media != 2 {
		t.Errorf("%d pictures embedded, want the two PNGs", media)
	}
	if strings.Count(doc, "<w:drawing>") != 2 {
		t.Errorf("%d drawings in the document, want 2", strings.Count(doc, "<w:drawing>"))
	}
}

func TestPDFDrawsADataURLPictureAndSurvivesABrokenOne(t *testing.T) {
	snap := Snapshot{Blocks: []Block{
		{Kind: Paragraph, Segments: seg("Before")},
		{Kind: Image, ImageSrc: pngDataURL(t, 6, 6), ImageW: 120, ImageH: 120, ImageCaption: "Fig"},
		{Kind: Image, ImageSrc: "data:image/png;base64,!!!", ImageCaption: "Broken"},
		{Kind: Image, ImageSrc: "no-comma-at-all"},
	}}
	out, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) || !bytes.Contains(out, []byte("/Image")) {
		t.Errorf("a PDF with an image object: %q...", out[:min(len(out), 12)])
	}
}

func TestDecodeDataURLImage(t *testing.T) {
	img, err := decodeDataURLImage(pngDataURL(t, 3, 2))
	if err != nil || img.Bounds().Dx() != 3 || img.Bounds().Dy() != 2 {
		t.Fatalf("%v %v", img, err)
	}
	raw := strings.TrimPrefix(pngDataURL(t, 2, 2), "data:image/png;base64,")
	if _, err := decodeDataURLImage(raw); err != nil {
		t.Errorf("bare base64: %v", err)
	}
	if _, err := decodeDataURLImage("data:image/png;base64,@@@"); err == nil {
		t.Error("bad base64")
	}
	if _, err := decodeDataURLImage("data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not an image"))); err == nil {
		t.Error("not an image")
	}
}

func TestOwnerOfAndHeadingLevelAndRawCode(t *testing.T) {
	cells := []Block{
		{TableColSpan: 2, TableRowSpan: 1}, {TableCovered: true},
		{TableColSpan: 1, TableRowSpan: 1}, {TableColSpan: 1, TableRowSpan: 1},
	}
	if got := ownerOf(cells, 2, 2, 0, 1); got != (cellPos{0, 0}) {
		t.Errorf("covered cell owner %+v", got)
	}
	if got := ownerOf(cells, 2, 2, 1, 1); got != (cellPos{1, 1}) {
		t.Errorf("an ordinary cell owns itself: %+v", got)
	}
	if HeadingLevel(nil, "Heading 2") != 2 || HeadingLevel(nil, "Title") != 1 || HeadingLevel(nil, "Normal") != 0 {
		t.Error("HeadingLevel by style")
	}
	if HeadingLevel([]docfile.Chars{}, "") != 0 {
		t.Error("no runs, no style: not a heading")
	}
	if got := rawCodeText([]Segment{{Text: "a"}, {SoftBreak: true}, {Text: "b"}}); got != "a"+string(softBreakRune)+"b" {
		t.Errorf("rawCodeText %q", got)
	}
}

func TestALegendIsWrittenUnderThePictureInEveryFormat(t *testing.T) {
	d := docfile.Document{Content: []docfile.Item{
		{Chars: &docfile.Chars{Text: "Before\n"}},
		{Image: &docfile.ImageItem{URL: pngDataURL(t, 4, 3), Width: 40, Height: 30, Alt: "A gradient", Legend: "Figure 1: the gradient"}},
		{Chars: &docfile.Chars{Text: "\nAfter\n"}},
	}}
	snap := FromDocument(d)
	img, leg := -1, -1
	for i, b := range snap.Blocks {
		if b.Kind == Image {
			img = i
		}
		if len(b.Segments) == 1 && b.Segments[0].Text == "Figure 1: the gradient" {
			leg = i
			if !b.Segments[0].Format.Italic {
				t.Error("the legend is in italics")
			}
		}
	}
	if img < 0 || leg != img+1 {
		t.Fatalf("the legend follows the picture: image block %d, legend block %d", img, leg)
	}
	for _, format := range []string{"html", "rtf"} {
		if out := string(rawOne(t, registry(), format, d)); !strings.Contains(out, "Figure 1: the gradient") {
			t.Errorf("%s does not carry the legend:\n%.300s", format, out)
		}
	}
	for format, part := range map[string]string{"docx": "word/document.xml", "odt": "content.xml"} {
		raw := rawOne(t, registry(), format, d)
		zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, f := range zr.File {
			if f.Name != part {
				continue
			}
			rc, _ := f.Open()
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(rc)
			rc.Close()
			found = strings.Contains(buf.String(), "Figure 1: the gradient")
		}
		if !found {
			t.Errorf("%s does not carry the legend in %s", format, part)
		}
	}
}

func TestTableFormulasAreWrittenAsTheirValues(t *testing.T) {
	d := docfile.Document{Content: []docfile.Item{{Table: &docfile.TableItem{Rows: []docfile.TableRow{
		{Cells: []string{"Qty", "Price", "Total"}},
		{Cells: []string{"3", "1.5", "=A2*B2"}},
		{Cells: []string{"", "", "=C2/0"}},
	}}}}}
	var found []string
	for _, b := range FromDocument(d).Blocks {
		if b.Kind == TableCell {
			for _, s := range b.Segments {
				found = append(found, s.Text)
			}
		}
	}
	got := strings.Join(found, "|")
	if !strings.Contains(got, "4.5") || !strings.Contains(got, "#DIV/0!") || strings.Contains(got, "=") {
		t.Fatalf("%s", got)
	}
}

func TestImageCreditTravelsInWordAndODTProperties(t *testing.T) {
	doc := docfile.Document{Content: []docfile.Item{{Image: &docfile.ImageItem{
		URL: tinyPNG, Alt: "A dot", Credit: "Dot by Ann, CC BY 4.0, via Openverse", SourceURL: "https://example.org/dot"}}}}
	snap := FromDocument(doc)
	want := "Dot by Ann, CC BY 4.0, via Openverse - https://example.org/dot"
	docx, err := DOCX{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if x := string(unzipEntry(t, docx, "word/document.xml")); !strings.Contains(x, `title="`+want+`"`) {
		t.Errorf("docx lacks the credit:\n%s", x)
	}
	odt, err := ODT{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if x := string(unzipEntry(t, odt, "content.xml")); !strings.Contains(x, "<svg:desc>"+want+"</svg:desc>") {
		t.Errorf("odt lacks the credit:\n%s", x)
	}
}
