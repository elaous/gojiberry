//go:build experimental || exp_textformats

package transfer

import (
	"encoding/json"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/textimport"
)

// textFormat reads one of the text formats of package textimport as it is, for reading.
type textFormat struct {
	kind, label, mime string
	exts              []string
}

func (t textFormat) Format() Format {
	return Format{Name: t.kind, Label: t.label, Exts: t.exts, MIME: t.mime}
}
func (t textFormat) Import(b []byte) (docfile.Document, error) { return textimport.Import(t.kind, b) }

// TextFormats are the text formats opened as they are (reading only, no export): the ones with a file extension of their
// own. A .json file is read by the JSON codec of the editor's own files, or as data when it is not one (see jsonFile).
var TextFormats = []Importer{
	textFormat{textimport.JSONL, "JSON lines (.jsonl)", "application/x-ndjson", []string{".jsonl", ".ndjson"}},
	textFormat{textimport.JSON5, "JSON5 (.json5)", "application/json5", []string{".json5"}},
	textFormat{textimport.CSV, "Comma-separated values (.csv), as a table", "text/csv", []string{".csv"}},
	textFormat{textimport.TSV, "Tab-separated values (.tsv), as a table", "text/tab-separated-values", []string{".tsv", ".tab"}},
	textFormat{textimport.YAML, "YAML (.yaml, .yml)", "application/yaml", []string{".yaml", ".yml"}},
	textFormat{textimport.TOML, "TOML (.toml)", "application/toml", []string{".toml"}},
	textFormat{textimport.INI, "Settings file (.ini, .cfg, .conf, .properties)", "text/plain", []string{".ini", ".cfg", ".conf", ".properties"}},
	textFormat{textimport.Log, "Log file (.log)", "text/plain", []string{".log"}},
}

// jsonFile reads a .json file as one of the editor's own documents when it is one (it has the keys of a document), and as
// data otherwise, indented, as a code block.
type jsonFile struct{ Importer }

func (j jsonFile) Import(b []byte) (docfile.Document, error) {
	var keys map[string]json.RawMessage
	if json.Unmarshal(b, &keys) == nil {
		for _, k := range []string{"content", "title", "styles", "fonts", "meta", "comments", "footnotes", "history", "updated"} {
			if _, ok := keys[k]; ok {
				return j.Importer.Import(b)
			}
		}
	}
	if d, err := textimport.Import(textimport.JSON, b); err == nil {
		return d, nil
	}
	return j.Importer.Import(b)
}

// registerTextFormats adds the importers of the text formats (build tag exp_textformats, or experimental).
func registerTextFormats(r *Registry) {
	for _, f := range TextFormats {
		r.AddImporter(f)
	}
}

// wrapJSON makes a .json file a document or data, see jsonFile.
func wrapJSON(i Importer) Importer { return jsonFile{i} }
