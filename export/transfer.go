package export

import (
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docximport"
	"github.com/elaous/gojiberry/htmlimport"
	"github.com/elaous/gojiberry/odtimport"
	"github.com/elaous/gojiberry/pdftext"
	"github.com/elaous/gojiberry/transfer"
)

type formatExporter struct {
	format  transfer.Format
	e       Exporter
	tracked bool
	media   bool
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

// text proposed for removal must not reach a file
type acceptedExporter struct{ transfer.Exporter }

func (a acceptedExporter) Export(d docfile.Document) ([]byte, error) {
	return a.Exporter.Export(acceptedView(d).MediaAsText().EquationsAsText().FormFieldsAsText())
}

func RegisterAccepted(r *transfer.Registry) {
	for _, e := range r.Exporters() {
		if n := e.Format().Name; n == "markdown" || n == "text" {
			r.AddExporter(acceptedExporter{e})
		}
	}
}

// Register adds this package's formats to r.
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
