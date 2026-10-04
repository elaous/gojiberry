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
