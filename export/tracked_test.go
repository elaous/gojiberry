package export

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/transfer"
)

// suggested is "the [brown]{red} fox": ana's replacement of "brown" by "red", and bo's removal of " runs".
func suggested() docfile.Document {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	del := canvas.ProposalMark{Kind: canvas.ProposalDelete, ID: "p1", Author: "ana@example.com", Time: at}.String()
	ins := canvas.ProposalMark{Kind: canvas.ProposalInsert, ID: "p1", Author: "ana@example.com", Time: at}.String()
	del2 := canvas.ProposalMark{Kind: canvas.ProposalDelete, ID: "p2", Author: `bo "the" <b>`, Time: at}.String()
	c := func(text, mark string) docfile.Item {
		return docfile.Item{Chars: &docfile.Chars{Text: text, Format: docfile.Format{Proposal: mark}}}
	}
	return docfile.Document{Title: "T", Content: []docfile.Item{c("the ", ""), c("brown", del), c("red", ins), c(" fox", ""), c(" runs", del2), c("\n", "")}}
}

func TestAcceptedViewHidesStruckText(t *testing.T) {
	snap := FromDocument(suggested())
	var got strings.Builder
	for _, b := range snap.Blocks {
		for _, s := range b.Segments {
			if s.Format.Change != NoChange {
				t.Errorf("an accepted view has no changes: %+v", s)
			}
			if s.SoftBreak {
				got.WriteString(" ")
			} else {
				got.WriteString(s.Text)
			}
		}
	}
	if got.String() != "the red fox" {
		t.Errorf("accepted view: %q", got.String())
	}
	// formats that cannot show a suggestion write the accepted text, and never the struck text
	for _, e := range []Exporter{PlainText{}, Markdown{}, RTF{}} {
		out, err := e.Export(snap)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), "brown") || strings.Contains(string(out), "runs") || !strings.Contains(string(out), "the") {
			t.Errorf("%T: %q", e, out)
		}
	}
	pdf, err := PDF{}.Export(snap)
	if err != nil || bytes.Contains(pdf, []byte("brown")) {
		t.Errorf("the PDF holds no struck text: %v", err)
	}
}

func TestHTMLAndDOCXKeepSuggestionsAsTrackedChanges(t *testing.T) {
	r := transfer.Default()
	Register(r)
	var htmlOut, docxOut []byte
	for _, e := range r.Exporters() {
		out, err := e.Export(suggested())
		if err != nil {
			t.Fatal(err)
		}
		switch e.Format().Name {
		case "html":
			htmlOut = out
		case "docx":
			docxOut = out
		}
	}
	html := string(htmlOut)
	for _, want := range []string{`<del data-author="ana@example.com" datetime="2026-03-04T05:06:07Z">brown</del>`, `<ins data-author="ana@example.com" datetime="2026-03-04T05:06:07Z">red</ins>`, `data-author="bo &quot;the&quot; &lt;b&gt;"`} {
		if !strings.Contains(html, want) {
			t.Errorf("html missing %q in\n%s", want, html)
		}
	}
	zr, err := zip.NewReader(bytes.NewReader(docxOut), int64(len(docxOut)))
	if err != nil {
		t.Fatal(err)
	}
	var doc string
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			var b bytes.Buffer
			_, _ = b.ReadFrom(rc)
			_ = rc.Close()
			doc = b.String()
		}
	}
	for _, want := range []string{`<w:del w:id="1" w:author="ana@example.com" w:date="2026-03-04T05:06:07Z"><w:r><w:delText xml:space="preserve">brown</w:delText></w:r></w:del>`,
		`<w:ins w:id="2" w:author="ana@example.com" w:date="2026-03-04T05:06:07Z"><w:r><w:t xml:space="preserve">red</w:t></w:r></w:ins>`, `w:author="bo &#34;the&#34; &lt;b&gt;"`} {
		if !strings.Contains(doc, want) {
			t.Errorf("docx missing %q in\n%s", want, doc)
		}
	}
	// a document with no suggestions is written as before
	plain := docfile.Document{Content: []docfile.Item{{Chars: &docfile.Chars{Text: "just text\n"}}}}
	out, _ := HTML{}.Export(FromDocumentTracked(plain))
	if strings.Contains(string(out), "<ins") || strings.Contains(string(out), "<del") {
		t.Error("no suggestions, no tracked markup")
	}
}
