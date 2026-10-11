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

// pngDataURL is a small opaque PNG as a data: URL.
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
		{Kind: Image, ImageSrc: pngDataURL(t, 2, 2), ImageW: 5000, ImageH: 5000}, // wider than the page: scaled down
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
	// without the "data:...," head the payload alone is accepted
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
	// a 2x2 table whose first cell spans both columns: (0,1) belongs to (0,0)
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
