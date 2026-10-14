package export

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/pdftext"
)

func TestEvenPageLayoutRoundTripsThroughMeta(t *testing.T) {
	var d docfile.Document
	d.SetPageLayout(docfile.PageLayout{Header: "Odd", EvenDiffers: true, EvenHeader: "Even", EvenFooter: "{page}"})
	got := d.PageLayout()
	if !got.EvenDiffers || got.EvenHeader != "Even" || got.EvenFooter != "{page}" || got.Header != "Odd" {
		t.Fatalf("layout = %+v", got)
	}
	d.SetPageLayout(docfile.PageLayout{Header: "Odd", EvenHeader: "ignored"})
	if got := d.PageLayout(); got.EvenDiffers || got.EvenHeader != "" {
		t.Errorf("without the switch the even pages have no layout of their own: %+v", got)
	}
	if _, ok := d.Get(docfile.PageEvenHead); ok {
		t.Error("the even header was left in Meta")
	}
}

func TestPDFEvenAndOddPagesHaveTheirOwnHeaders(t *testing.T) {
	snap := longSnapshot(3)
	snap.Header = "Odd running"
	snap.EvenDiffers, snap.EvenHeader = true, "Even running"
	snap.FirstDiffers, snap.FirstHeader = true, "Cover"
	data, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := pdftext.ExtractPagesBytes(data)
	if err != nil || len(pages) < 4 {
		t.Fatalf("pages %d, %v", len(pages), err)
	}
	for i, txt := range pages {
		want, not := "Odd running", "Even running"
		switch {
		case i == 0:
			want, not = "Cover", "running"
		case (i+1)%2 == 0:
			want, not = "Even running", "Odd running"
		}
		if !strings.Contains(txt, want) || strings.Contains(txt, not) {
			t.Errorf("page %d: want %q and not %q in %q", i+1, want, not, txt)
		}
	}
}

func TestHTMLEvenPageRule(t *testing.T) {
	snap := longSnapshot(1)
	snap.Header = "Odd"
	snap.EvenDiffers, snap.EvenFooter = true, "Even"
	out, err := HTML{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	i := strings.Index(s, "@page :left{")
	if i < 0 {
		t.Fatalf("no rule for the even pages")
	}
	if rule := s[i : i+strings.Index(s[i:], "}\n")]; !strings.Contains(rule, "@top-center{content:none}") || !strings.Contains(rule, `"Even"`) || strings.Contains(rule, `"Odd"`) {
		t.Errorf("rule = %s", rule)
	}
}

func TestDOCXEvenAndOddHeaders(t *testing.T) {
	snap := longSnapshot(1)
	snap.Header = "Odd"
	snap.EvenDiffers, snap.EvenHeader = true, "Even"
	data, err := DOCX{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	p := docxParts(t, data)
	if !strings.Contains(p["word/settings.xml"], "<w:evenAndOddHeaders/>") {
		t.Errorf("settings = %s", p["word/settings.xml"])
	}
	if doc := p["word/document.xml"]; !strings.Contains(doc, `<w:headerReference w:type="even"`) || !strings.Contains(doc, `<w:headerReference w:type="default"`) {
		t.Errorf("section lacks the references")
	}
	if !strings.Contains(p["word/header3.xml"], "Even") || !strings.Contains(p["word/header1.xml"], "Odd") {
		t.Error("header parts")
	}
	snap.EvenDiffers = false
	data, _ = DOCX{}.Export(snap)
	if _, ok := docxParts(t, data)["word/settings.xml"]; ok {
		t.Error("settings without the switch")
	}
}

func TestRTFFacingPages(t *testing.T) {
	snap := longSnapshot(1)
	snap.Header = "Odd"
	snap.EvenDiffers, snap.EvenHeader = true, "Even"
	out, err := RTF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`\facingp`, `{\headerr`, `{\headerl`} {
		if !strings.Contains(s, want) {
			t.Errorf("RTF lacks %q", want)
		}
	}
}
