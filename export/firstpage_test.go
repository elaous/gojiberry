package export

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/pdftext"
)

func TestFirstPageLayoutRoundTripsThroughMeta(t *testing.T) {
	var d docfile.Document
	d.SetPageLayout(docfile.PageLayout{Header: "H", Footer: "F", FirstDiffers: true, FirstHeader: "FH"})
	got := d.PageLayout()
	if !got.FirstDiffers || got.FirstHeader != "FH" || got.FirstFooter != "" || got.Header != "H" {
		t.Fatalf("layout = %+v", got)
	}
	d.SetPageLayout(docfile.PageLayout{Header: "H", FirstHeader: "ignored"})
	if got := d.PageLayout(); got.FirstDiffers || got.FirstHeader != "" {
		t.Errorf("without the switch the first page has no layout of its own: %+v", got)
	}
	if _, ok := d.Get(docfile.PageFirstHead); ok {
		t.Error("the first page's header was left in Meta")
	}
}

func TestPDFFirstPageHasItsOwnHeaderAndFooter(t *testing.T) {
	snap := longSnapshot(3)
	snap.Header, snap.Footer = "Running", "Page {page} of {pages}"
	snap.FirstDiffers, snap.FirstHeader = true, "Title page"
	data, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := pdftext.ExtractPagesBytes(data)
	if err != nil || len(pages) < 3 {
		t.Fatalf("pages %d, %v", len(pages), err)
	}
	if !strings.Contains(pages[0], "Title page") || strings.Contains(pages[0], "Running") || strings.Contains(pages[0], "Page 1 of") {
		t.Errorf("first page: %q", pages[0])
	}
	for i, txt := range pages[1:] {
		if !strings.Contains(txt, "Running") || strings.Contains(txt, "Title page") {
			t.Errorf("page %d: %q", i+2, txt)
		}
	}
}

func TestHTMLFirstPageRule(t *testing.T) {
	snap := longSnapshot(1)
	snap.Header = "Running"
	snap.FirstDiffers, snap.FirstFooter = true, "Cover"
	out, err := HTML{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	i := strings.Index(s, "@page :first{")
	if i < 0 {
		t.Fatalf("no first page rule in %s", s[:min(len(s), 1500)])
	}
	rule := s[i : i+strings.Index(s[i:], "}\n")]
	if !strings.Contains(rule, "@top-center{content:none}") || !strings.Contains(rule, "Cover") || strings.Contains(rule, "Running") {
		t.Errorf("rule = %s", rule)
	}
	snap.FirstDiffers = false
	out, _ = HTML{}.Export(snap)
	if strings.Contains(string(out), "@page :first") {
		t.Error("the rule is written without the switch")
	}
}

func TestDOCXFirstPageHeaderAndTitlePage(t *testing.T) {
	snap := longSnapshot(1)
	snap.Header = "Running"
	snap.FirstDiffers, snap.FirstHeader = true, "Cover"
	data, err := DOCX{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	p := docxParts(t, data)
	doc := p["word/document.xml"]
	if !strings.Contains(doc, `<w:titlePg/>`) || !strings.Contains(doc, `<w:headerReference w:type="first"`) || strings.Contains(doc, `<w:footerReference w:type="first"`) {
		t.Errorf("section = %s", doc[len(doc)-600:])
	}
	if !strings.Contains(p["word/header2.xml"], "Cover") || !strings.Contains(p["word/header1.xml"], "Running") {
		t.Errorf("headers = %s | %s", p["word/header1.xml"], p["word/header2.xml"])
	}
	snap.FirstDiffers = false
	data, _ = DOCX{}.Export(snap)
	if strings.Contains(docxParts(t, data)["word/document.xml"], "titlePg") {
		t.Error("titlePg without the switch")
	}
}

func TestRTFFirstPageHeaderAndTitlePage(t *testing.T) {
	snap := longSnapshot(1)
	snap.Header = "Running"
	snap.FirstDiffers, snap.FirstHeader = true, "Cover"
	out, err := RTF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`\titlepg`, `{\headerf`, "Cover", "Running"} {
		if !strings.Contains(s, want) {
			t.Errorf("RTF lacks %q", want)
		}
	}
	if strings.Contains(s, `{\footerf`) {
		t.Error("an empty first footer was written")
	}
}
