package export

import (
	"regexp"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/pdftext"
	"github.com/elaous/gojiberry/pdfua"
)

func footnoteDoc() docfile.Document {
	return docfile.Document{
		Title: "Notes", Meta: []docfile.MetaEntry{{Key: docfile.LangKey, Value: "en"}},
		Content: []docfile.Item{
			{Chars: &docfile.Chars{Text: "First claim"}},
			{Footnote: &docfile.FootnoteMark{ID: "a"}},
			{Chars: &docfile.Chars{Text: " and a second one"}},
			{Footnote: &docfile.FootnoteMark{ID: "b"}},
			{Chars: &docfile.Chars{Text: ".\n"}},
		},
		Footnotes: []docfile.Footnote{{ID: "a", Text: "Source of the first."}, {ID: "b", Text: "Source of the second."}},
	}
}

func TestPDFFootnotesLinkBothWays(t *testing.T) {
	data, err := PDF{}.Export(FromDocument(footnoteDoc()))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if n := strings.Count(s, "/Subtype /Link") / 2; n != 4 { // each annotation is found twice: itself and its tag
		t.Errorf("%d link annotations, want 4 (a mark and a way back for each of two notes)", n)
	}
	if n := len(regexp.MustCompile(`/Dest`).FindAllString(s, -1)); n < 4 {
		t.Errorf("%d destinations, want 4 or more", n)
	}
	if got, err := pdfua.Check(data); err != nil || len(got) != 0 {
		t.Fatalf("pdfua: %v, %v", got, err)
	}
	txt, err := pdftext.ExtractPagesBytes(data)
	if err != nil || len(txt) == 0 || !strings.Contains(txt[0], "Source of the second.") {
		t.Fatalf("text: %q %v", txt, err)
	}
	plain, _ := PDF{}.Export(FromDocument(docfile.Document{Content: []docfile.Item{{Chars: &docfile.Chars{Text: "no notes\n"}}}}))
	if strings.Contains(string(plain), "/Dest") {
		t.Error("an internal link without a footnote")
	}
}
