package export

import (
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docximport"
	"github.com/elaous/gojiberry/htmlimport"
	"github.com/elaous/gojiberry/odtimport"
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
	// media says the format embeds audio and video (HTML); the others write a clip as text.
	media bool
}

func (f formatExporter) Format() transfer.Format { return f.format }
func (f formatExporter) Export(d docfile.Document) ([]byte, error) {
	if f.media {
		return f.e.Export(FromDocumentWithMedia(d))
	}
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

type htmlImporter struct{}

func (htmlImporter) Format() transfer.Format {
	return transfer.Format{Name: "html", Label: "Web page (HTML)", Exts: []string{".html", ".htm"}, MIME: "text/html"}
}
func (htmlImporter) Import(b []byte) (docfile.Document, error) { return htmlimport.Import(b) }

type odtImporter struct{}

func (odtImporter) Format() transfer.Format {
	return transfer.Format{Name: "odt", Label: "OpenDocument text (.odt)", Exts: []string{".odt"}, MIME: "application/vnd.oasis.opendocument.text"}
}
func (odtImporter) Import(b []byte) (docfile.Document, error) { return odtimport.Import(b) }

type pdfImporter struct{}

func (pdfImporter) Format() transfer.Format {
	return transfer.Format{Name: "pdf", Label: "PDF (text only)", Exts: []string{".pdf"}, MIME: "application/pdf"}
}
func (pdfImporter) Import(b []byte) (docfile.Document, error) { return pdftext.Import(b) }

// acceptedExporter writes the document as it would be with every suggestion accepted, for the exporters that
// have no way to show a suggestion: text proposed to be removed must not reach a file that cannot strike it.
type acceptedExporter struct{ transfer.Exporter }

func (a acceptedExporter) Export(d docfile.Document) ([]byte, error) {
	return a.Exporter.Export(acceptedView(d).MediaAsText().EquationsAsText())
}

// RegisterAccepted makes the Markdown and plain-text exporters of transfer.Default write the document as it
// would be with every suggestion accepted (Register does it too). It pulls in none of the heavy formats, for a
// program that does those elsewhere (package export/remote).
func RegisterAccepted(r *transfer.Registry) {
	for _, e := range r.Exporters() { // Markdown and plain text of transfer.Default read the text as it is
		if n := e.Format().Name; n == "markdown" || n == "text" {
			r.AddExporter(acceptedExporter{e})
		}
	}
}

// Register adds the formats this package handles to r. It writes HTML, RTF,
// EPUB, PDF, OpenDocument and Word (.docx), and reads Word, OpenDocument, HTML and PDF text. (Markdown and plain
// text are in transfer.Default; for braille see package braille.)
func Register(r *transfer.Registry) {
	RegisterAccepted(r)
	for _, f := range []formatExporter{
		{transfer.Format{Name: "html", Label: "Web page (HTML)", Exts: []string{".html"}, MIME: "text/html"}, HTML{}, true, true},
		{transfer.Format{Name: "rtf", Label: "Rich Text Format", Exts: []string{".rtf"}, MIME: "application/rtf"}, RTF{}, false, false},
		{transfer.Format{Name: "epub", Label: "E-book (EPUB)", Exts: []string{".epub"}, MIME: "application/epub+zip"}, EPUB{}, true, false},
		{transfer.Format{Name: "pdf", Label: "PDF", Exts: []string{".pdf"}, MIME: "application/pdf"}, PDF{}, false, false},
		{transfer.Format{Name: "odt", Label: "OpenDocument text (.odt)", Exts: []string{".odt"}, MIME: "application/vnd.oasis.opendocument.text"}, ODT{}, false, false},
		{transfer.Format{Name: "docx", Label: "Word (.docx)", Exts: []string{".docx"}, MIME: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}, DOCX{}, true, false},
	} {
		r.AddExporter(f)
	}
	r.AddImporter(docxImporter{})
	r.AddImporter(htmlImporter{})
	r.AddImporter(odtImporter{})
	r.AddImporter(pdfImporter{})
}
