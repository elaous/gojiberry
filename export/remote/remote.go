package remote

import (
	"encoding/json"
	"errors"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/transfer"
)

// The operations of the protocol.
const (
	OpImport         = "import"          // in: the file; out: the document as JSON
	OpExport         = "export"          // in: the document as JSON; out: the file
	OpFidelity       = "fidelity"        // in: the document as JSON; out: an export.Report as JSON
	OpImportFidelity = "import-fidelity" // in: the file; out: an export.Report as JSON
)

type Caller func(op, format string, in []byte) ([]byte, error)

// The formats the other program offers.
var (
	Exporters = []transfer.Format{
		{Name: "html", Label: "Web page (HTML)", Exts: []string{".html"}, MIME: "text/html"},
		{Name: "rtf", Label: "Rich Text Format", Exts: []string{".rtf"}, MIME: "application/rtf"},
		{Name: "epub", Label: "E-book (EPUB)", Exts: []string{".epub"}, MIME: "application/epub+zip"},
		{Name: "pdf", Label: "PDF", Exts: []string{".pdf"}, MIME: "application/pdf"},
		{Name: "odt", Label: "OpenDocument text (.odt)", Exts: []string{".odt"}, MIME: "application/vnd.oasis.opendocument.text"},
		{Name: "docx", Label: "Word (.docx)", Exts: []string{".docx"}, MIME: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	}
	Importers = []transfer.Format{
		{Name: "docx", Label: "Word (.docx), Google Docs download", Exts: []string{".docx"}, MIME: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{Name: "html", Label: "Web page (HTML)", Exts: []string{".html", ".htm"}, MIME: "text/html"},
		{Name: "odt", Label: "OpenDocument text (.odt)", Exts: []string{".odt"}, MIME: "application/vnd.oasis.opendocument.text"},
		{Name: "pdf", Label: "PDF (text only)", Exts: []string{".pdf"}, MIME: "application/pdf"},
	}
)

func Register(r *transfer.Registry, call Caller) {
	export.RegisterAccepted(r)
	for _, f := range Exporters {
		r.AddExporter(exporter{f, call})
	}
	for _, f := range Importers {
		r.AddImporter(importer{f, call})
	}
}

type exporter struct {
	f    transfer.Format
	call Caller
}

func (e exporter) Format() transfer.Format { return e.f }
func (e exporter) Export(d docfile.Document) ([]byte, error) {
	in, err := docfile.JSON.Marshal(d)
	if err != nil {
		return nil, err
	}
	return e.call(OpExport, e.f.Name, in)
}

type importer struct {
	f    transfer.Format
	call Caller
}

func (i importer) Format() transfer.Format { return i.f }
func (i importer) Import(data []byte) (docfile.Document, error) {
	out, err := i.call(OpImport, i.f.Name, data)
	if err != nil {
		return docfile.Document{}, err
	}
	return docfile.JSON.Unmarshal(out)
}

// Fidelity is export.Fidelity done by the other program.
func Fidelity(call Caller, format string, d docfile.Document) (export.Report, error) {
	in, err := docfile.JSON.Marshal(d)
	if err != nil {
		return export.Report{}, err
	}
	return report(call, OpFidelity, format, in)
}

// ImportFidelity is export.ImportFidelity done by the other program.
func ImportFidelity(call Caller, format string, data []byte) (export.Report, error) {
	return report(call, OpImportFidelity, format, data)
}

func report(call Caller, op, format string, in []byte) (export.Report, error) {
	out, err := call(op, format, in)
	if err != nil {
		return export.Report{}, err
	}
	var r export.Report
	if err := json.Unmarshal(out, &r); err != nil {
		return export.Report{}, errors.New("remote: the report could not be read")
	}
	return r, nil
}

func WordImport(call Caller) func([]byte) (docfile.Document, error) {
	return importer{f: Importers[0], call: call}.Import
}
