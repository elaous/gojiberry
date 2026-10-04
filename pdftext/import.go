package pdftext

import (
	"strings"

	"github.com/elaous/gojiberry/docfile"
)

// Import reads a PDF's text as a document: every page's lines as paragraphs,
// with a page break between pages. Only text is read (no layout, pictures or
// fonts), and a PDF that is a scan has none.
func Import(data []byte) (docfile.Document, error) {
	pages, err := ExtractPagesBytes(data)
	if err != nil {
		return docfile.Document{}, err
	}
	var doc docfile.Document
	for i, p := range pages {
		if i > 0 {
			doc.Content = append(doc.Content, docfile.Item{Break: &docfile.Break{Kind: 1}})
		}
		p = strings.ReplaceAll(strings.ReplaceAll(p, "\r\n", "\n"), "\r", "\n")
		var lines []string
		for _, l := range strings.Split(p, "\n") {
			lines = append(lines, strings.TrimRight(l, " \t"))
		}
		text := strings.TrimRight(strings.Join(lines, "\n"), "\n")
		if text != "" {
			doc.Content = append(doc.Content, docfile.Item{Chars: &docfile.Chars{Text: text + "\n"}})
		}
	}
	return doc, nil
}
