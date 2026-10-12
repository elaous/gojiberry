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
	// tracked says the format can show suggested changes (as insertions and deletions); the others
	// write the document as it would be with every suggestion accepted.
	tracked bool
}

func (f formatExporter) Format() transfer.Format { return f.format }
func (f formatExporter) Export(d docfile.Document) ([]byte, error) {
	if f.tracked {
		return f.e.Export(FromDocumentTracked(d))
	}
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

// acceptedExporter writes the document as it would be with every suggestion accepted, for the exporters that
// have no way to show a suggestion: text proposed to be removed must not reach a file that cannot strike it.
type acceptedExporter struct{ transfer.Exporter }

func (a acceptedExporter) Export(d docfile.Document) ([]byte, error) {
	return a.Exporter.Export(acceptedView(d))
}

// Register adds the formats this package handles to r. It writes HTML, RTF,
// EPUB, PDF and Word (.docx), and reads Word and PDF text. (Markdown and plain
// text are in transfer.Default; for braille see package braille.)
func Register(r *transfer.Registry) {
	for _, e := range r.Exporters() { // Markdown and plain text of transfer.Default read the text as it is
		if n := e.Format().Name; n == "markdown" || n == "text" {
			r.AddExporter(acceptedExporter{e})
		}
	}
	for _, f := range []formatExporter{
		{transfer.Format{Name: "html", Label: "Web page (HTML)", Exts: []string{".html"}, MIME: "text/html"}, HTML{}, true},
		{transfer.Format{Name: "rtf", Label: "Rich Text Format", Exts: []string{".rtf"}, MIME: "application/rtf"}, RTF{}, false},
		{transfer.Format{Name: "epub", Label: "E-book (EPUB)", Exts: []string{".epub"}, MIME: "application/epub+zip"}, EPUB{}, true},
		{transfer.Format{Name: "pdf", Label: "PDF", Exts: []string{".pdf"}, MIME: "application/pdf"}, PDF{}, false},
		{transfer.Format{Name: "docx", Label: "Word (.docx)", Exts: []string{".docx"}, MIME: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}, DOCX{}, true},
	} {
		r.AddExporter(f)
	}
	r.AddImporter(docxImporter{})
	r.AddImporter(pdfImporter{})
}
