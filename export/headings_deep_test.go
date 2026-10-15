package export

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/pdfua"
)

// Headings of level 4 to 6 keep their level in every format that has headings.
func TestDeepHeadingsKeepTheirLevel(t *testing.T) {
	doc := docfile.Document{Title: "Deep", Content: []docfile.Item{
		{Para: &docfile.Para{Style: "Heading 1"}}, {Chars: &docfile.Chars{Text: "One\n"}},
		{Para: &docfile.Para{Style: "Heading 2"}}, {Chars: &docfile.Chars{Text: "Two\n"}},
		{Para: &docfile.Para{Style: "Heading 3"}}, {Chars: &docfile.Chars{Text: "Three\n"}},
		{Para: &docfile.Para{Style: "Heading 4"}}, {Chars: &docfile.Chars{Text: "Four\n"}},
		{Para: &docfile.Para{Style: "Heading 5"}}, {Chars: &docfile.Chars{Text: "Five\n"}},
		{Para: &docfile.Para{Style: "Heading 6"}}, {Chars: &docfile.Chars{Text: "Six\n"}},
	}}
	doc.SetLanguage("en")
	snap := FromDocument(doc)
	var levels []int
	for _, b := range snap.Blocks {
		if l := b.HeadingLevel(); l > 0 {
			levels = append(levels, l)
		}
	}
	if len(levels) != 6 || levels[3] != 4 || levels[4] != 5 || levels[5] != 6 {
		t.Fatalf("levels %v", levels)
	}
	h, err := HTML{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<h4>Four</h4>", "<h5>Five</h5>", "<h6>Six</h6>"} {
		if !strings.Contains(string(h), want) {
			t.Errorf("HTML lacks %s", want)
		}
	}
	md, _ := Markdown{}.Export(snap)
	for _, want := range []string{"#### Four", "##### Five", "###### Six"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("Markdown lacks %q:\n%s", want, md)
		}
	}
	dx, _ := DOCX{}.Export(snap)
	body, styles := zipText(t, dx, "word/document.xml"), zipText(t, dx, "word/styles.xml")
	for _, n := range []string{"4", "5", "6"} {
		if !strings.Contains(body, `<w:pStyle w:val="Heading`+n+`"/>`) || !strings.Contains(styles, `w:styleId="Heading`+n+`"`) {
			t.Errorf("DOCX: Heading%s style used %v, defined %v", n, strings.Contains(body, `"Heading`+n+`"`), strings.Contains(styles, `w:styleId="Heading`+n+`"`))
		}
	}
	od, _ := ODT{}.Export(snap)
	content := zipText(t, od, "content.xml")
	if !strings.Contains(content, `text:outline-level="6"`) || !strings.Contains(zipText(t, od, "styles.xml"), `Heading_20_6`) {
		t.Error("ODT lacks level 6")
	}
	pdf, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := pdfua.Check(pdf); err != nil || len(got) != 0 {
		t.Errorf("pdfua: %v %v", got, err)
	}
	shape := structureShape(t, pdf)
	for _, want := range []string{"H4", "H5", "H6"} {
		if !strings.Contains(shape, want) {
			t.Errorf("PDF structure lacks %s: %s", want, shape)
		}
	}
	outline := snap.Outline()
	if len(outline) != 6 || outline[5].Level != 6 {
		t.Errorf("the outline: %+v", outline)
	}
}
