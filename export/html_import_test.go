package export

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/transfer"
)

func TestHTMLWeWriteIsReadBack(t *testing.T) {
	snap := textSnapshot(
		Block{Kind: Heading1, Segments: []Segment{{Text: "Title"}}},
		Block{Kind: Paragraph, Segments: []Segment{{Text: "see "}, {Text: "here", Format: Format{Href: "https://example.org"}}, {Text: " and "}, {Text: "bold", Format: Format{Bold: true}}}},
		Block{Kind: BulletItem, Segments: []Segment{{Text: "one"}}},
		Block{Kind: BulletItem, Level: 1, Segments: []Segment{{Text: "inner"}}},
	)
	data, err := HTML{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	r := transfer.Default()
	Register(r)
	doc, err := r.Import("html", "page.html", data)
	if err != nil {
		t.Fatal(err)
	}
	var txt strings.Builder
	var link, bold, nested bool
	for _, it := range doc.Content {
		if c := it.Chars; c != nil {
			txt.WriteString(c.Text)
			link = link || c.Format.Link == "https://example.org"
			bold = bold || (c.Text == "bold" && c.Format.FontWeight == 700)
		}
		nested = nested || (it.Para != nil && it.Para.List.Level == 1)
	}
	for _, want := range []string{"Title", "see here and bold", "one", "inner"} {
		if !strings.Contains(txt.String(), want) {
			t.Errorf("text lacks %q: %q", want, txt.String())
		}
	}
	if !link || !bold || !nested {
		t.Errorf("link %v bold %v nested list %v", link, bold, nested)
	}
}

func TestImportFidelityOfAPage(t *testing.T) {
	rep := ImportFidelity("html", []byte(`<p>x</p><script>1</script><iframe></iframe><img src="https://a.example/p.png">`))
	if !rep.Known {
		t.Fatal("html import reports are known")
	}
	got := map[Feature]int{}
	for _, l := range rep.Losses {
		got[l.Feature] = l.Count
	}
	if got[FeatScript] != 1 || got[FeatFrame] != 1 || got[FeatRemoteImage] != 1 {
		t.Errorf("losses: %+v", rep.Losses)
	}
}

func TestMarkdownImportFidelityNamesWhatIsLeftOut(t *testing.T) {
	md := "# T\n\n![far](https://example.org/a.png) and <b>bold</b>.\n\n| A | **B** |\n|---|---|\n| `c` | [d](https://x.org) |\n\n```\n![in code](https://example.org/b.png)\n```\n"
	r := ImportFidelity("markdown", []byte(md))
	if !r.Known {
		t.Fatal("known for Markdown")
	}
	got := map[Feature]int{}
	for _, l := range r.Losses {
		got[l.Feature] = l.Count
	}
	if got[FeatRemoteImage] != 1 || got[FeatCellFormat] != 3 || got[FeatRawHTML] != 1 {
		t.Fatalf("one remote picture (the one in code is not read), three cells with marks, one line of HTML: %v", got)
	}
	if r := ImportFidelity("markdown", []byte("# Plain\n\nJust words.\n")); !r.Known || len(r.Losses) != 0 {
		t.Fatalf("a plain file loses nothing: %+v", r)
	}
}
