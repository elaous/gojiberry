package export

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

const testTheme = ":root{--gj-color-text:#202040;--gj-color-background:#fffbe6;--gj-color-link:#0a6e2d;--gj-font-1:18px;}"

func themedDoc() docfile.Document {
	d := docfile.Document{Title: "T", Content: []docfile.Item{{Chars: &docfile.Chars{Text: "hello world\n"}}}}
	d.SetTheme(testTheme)
	return d
}

func zipText(t *testing.T, data []byte, name string) string {
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
	t.Fatalf("no %s in the archive", name)
	return ""
}

func TestThemeReadsColoursAndSize(t *testing.T) {
	th := themeOf(Snapshot{Theme: testTheme})
	if th.text != "202040" || th.background != "FFFBE6" || th.link != "0A6E2D" || th.bodyPt != 13.5 {
		t.Errorf("%+v", th)
	}
	if th := themeOf(Snapshot{Theme: ":root{--gj-color-text:red;}</style>"}); th != (docTheme{}) {
		t.Errorf("an unsafe theme was read: %+v", th)
	}
	if hexColour("rgb(1, 2, 255)") != "0102FF" || hexColour("#abc") != "AABBCC" || hexColour("red") != "" || hexColour("#12") != "" {
		t.Error("hexColour")
	}
}

func TestDOCXTakesTheDocumentsTheme(t *testing.T) {
	d := themedDoc()
	d.Content[0].Chars.Format = docfile.Format{Link: "https://example.org"}
	out, err := DOCX{}.Export(FromDocument(d))
	if err != nil {
		t.Fatal(err)
	}
	styles := zipText(t, out, "word/styles.xml")
	if !strings.Contains(styles, `<w:color w:val="202040"/><w:sz w:val="27"/>`) {
		t.Errorf("styles lack the text colour and size: %s", styles[:400])
	}
	doc := zipText(t, out, "word/document.xml")
	if !strings.Contains(doc, `<w:background w:color="FFFBE6"/>`) || !strings.Contains(doc, `<w:color w:val="0A6E2D"/>`) {
		t.Errorf("document lacks the page or link colour")
	}
	if !strings.Contains(zipText(t, out, "word/settings.xml"), "displayBackgroundShape") {
		t.Error("the background is not shown without the setting")
	}
	// no theme: as before (no settings part, Word's blue for links)
	plain := themedDoc()
	plain.SetTheme("")
	plain.Content[0].Chars.Format = docfile.Format{Link: "https://example.org"}
	out, _ = DOCX{}.Export(FromDocument(plain))
	doc = zipText(t, out, "word/document.xml")
	if strings.Contains(doc, "w:background") || !strings.Contains(doc, `w:val="0563C1"`) {
		t.Error("a document without a theme changed")
	}
}

func TestODTRTFPDFEPUBTakeTheDocumentsTheme(t *testing.T) {
	d := themedDoc()
	d.Content[0].Chars.Format = docfile.Format{Link: "https://example.org"}
	snap := FromDocument(d)

	odt, err := ODT{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	styles := zipText(t, odt, "styles.xml")
	for _, want := range []string{`fo:font-size="13.5pt"`, `fo:color="#202040"`, `fo:background-color="#fffbe6"`, `style:name="Internet_20_link"`, `fo:color="#0a6e2d"`} {
		if !strings.Contains(styles, want) {
			t.Errorf("ODT styles lack %q", want)
		}
	}
	if !strings.Contains(zipText(t, odt, "content.xml"), `text:style-name="Internet_20_link"`) {
		t.Error("ODT links do not use the link style")
	}

	rtf, err := RTF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(rtf); !strings.Contains(s, `{\colortbl;\red32\green32\blue64;\red10\green110\blue45;}`) || !strings.Contains(s, `\fs27\cf1`) || !strings.Contains(s, `\fldrslt \cf2 `) {
		t.Errorf("RTF: %.300s", s)
	}

	pdf, err := PDF{}.Export(snap)
	if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("pdf: %v", err)
	}
	plain := themedDoc()
	plain.SetTheme("")
	pdf0, _ := PDF{}.Export(FromDocument(plain))
	if bytes.Equal(pdf, pdf0) {
		t.Error("the PDF is the same with and without a theme")
	}

	epub, err := EPUB{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if x := zipText(t, epub, "OEBPS/content.xhtml"); !strings.Contains(x, "--gj-color-text:#202040") {
		t.Error("the EPUB page lacks the theme")
	}
}
