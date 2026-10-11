package export

import (
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docximport"
	"github.com/elaous/gojiberry/pdftext"
	"github.com/elaous/gojiberry/transfer"
)

// formatExporter lets an Exporter (which reads a Snapshot) be registered with
// a transfer.Registry (which hands over the saved document).
type formatExporter struct {
	format transfer.Format
	e      Exporter
}

func (f formatExporter) Format() transfer.Format { return f.format }
func (f formatExporter) Export(d docfile.Document) ([]byte, error) {
	return f.e.Export(FromDocument(d))
}

// docxImporter and pdfImporter read the formats docximport and pdftext do.
type docxImporter struct{}

func (docxImporter) Format() transfer.Format {
	return transfer.Format{Name: "docx", Label: "Word (.docx), Google Docs download", Exts: []string{".docx"}, MIME: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}
}
func (docxImporter) Import(b []byte) (docfile.Document, error) { return docximport.Import(b) }

type pdfImporter struct{}

func (pdfImporter) Format() transfer.Format {
	return transfer.Format{Name: "pdf", Label: "PDF (text only)", Exts: []string{".pdf"}, MIME: "application/pdf"}
}
func (pdfImporter) Import(b []byte) (docfile.Document, error) { return pdftext.Import(b) }

// Register adds the formats this package handles to r. It writes HTML, RTF,
// EPUB, PDF and Word (.docx), and reads Word and PDF text. (Markdown and plain
// text are in transfer.Default; for braille see package braille.)
func Register(r *transfer.Registry) {
	for _, f := range []formatExporter{
		{transfer.Format{Name: "html", Label: "Web page (HTML)", Exts: []string{".html"}, MIME: "text/html"}, HTML{}},
		{transfer.Format{Name: "rtf", Label: "Rich Text Format", Exts: []string{".rtf"}, MIME: "application/rtf"}, RTF{}},
		{transfer.Format{Name: "epub", Label: "E-book (EPUB)", Exts: []string{".epub"}, MIME: "application/epub+zip"}, EPUB{}},
		{transfer.Format{Name: "pdf", Label: "PDF", Exts: []string{".pdf"}, MIME: "application/pdf"}, PDF{}},
		{transfer.Format{Name: "docx", Label: "Word (.docx)", Exts: []string{".docx"}, MIME: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}, DOCX{}},
	} {
		r.AddExporter(f)
	}
	r.AddImporter(docxImporter{})
	r.AddImporter(pdfImporter{})
}
