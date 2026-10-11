package pdftext_test

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/pdftext"
)

func TestImportReadsWhatThePDFExporterWrote(t *testing.T) {
	c := func(s string) docfile.Item { return docfile.Item{Chars: &docfile.Chars{Text: s}} }
	data, err := export.PDF{}.Export(export.FromDocument(docfile.Document{Content: []docfile.Item{
		c("Gojiberry editor exports to PDF.\nA second paragraph about canvases."),
	}}))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := pdftext.Import(data)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, it := range doc.Content {
		if it.Chars != nil {
			sb.WriteString(it.Chars.Text)
		}
	}
	text := strings.Join(strings.Fields(sb.String()), "")
	if !strings.Contains(text, "Gojiberryeditorexportstopdf") && !strings.Contains(strings.ToLower(text), "gojiberryeditorexportstopdf") {
		t.Errorf("text %q", sb.String())
	}
	if !strings.Contains(text, "canvases") {
		t.Errorf("second paragraph missing: %q", sb.String())
	}
}

func TestImportRejectsOtherData(t *testing.T) {
	if _, err := pdftext.Import([]byte("not a pdf")); err == nil {
		t.Error("expected an error")
	}
}

func TestExtractBytesJoinsEveryPage(t *testing.T) {
	c := func(s string) docfile.Item { return docfile.Item{Chars: &docfile.Chars{Text: s}} }
	// enough lines to run onto a second page
	var long strings.Builder
	for i := 0; i < 120; i++ {
		long.WriteString("Line of text number that fills the page\n")
	}
	data, err := export.PDF{}.Export(export.FromDocument(docfile.Document{Content: []docfile.Item{c("First words here.\n"), c(long.String()), c("Final words there.")}}))
	if err != nil {
		t.Fatal(err)
	}
	pages, err := pdftext.ExtractPagesBytes(data)
	if err != nil || len(pages) < 2 {
		t.Fatalf("%d pages, %v", len(pages), err)
	}
	all, err := pdftext.ExtractBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(all), "")
	if !strings.Contains(flat, "Firstwordshere") || !strings.Contains(flat, "Finalwordsthere") {
		t.Errorf("the text of the first and last pages: %q...", all[:min(len(all), 80)])
	}
	if got := len(strings.Join(pages, "")); got != len(all) {
		t.Errorf("ExtractBytes is the pages joined: %d against %d", len(all), got)
	}
	if _, err := pdftext.ExtractBytes([]byte("not a pdf")); err == nil {
		t.Error("not a PDF is an error")
	}
}
