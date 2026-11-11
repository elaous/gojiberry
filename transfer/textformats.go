//go:build experimental || exp_textformats

package transfer

import (
	"encoding/json"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/textimport"
)

type textFormat struct {
	kind, label, mime string
	exts              []string
}

func (t textFormat) Format() Format {
	return Format{Name: t.kind, Label: t.label, Exts: t.exts, MIME: t.mime}
}
func (t textFormat) Import(b []byte) (docfile.Document, error) { return textimport.Import(t.kind, b) }

var TextFormats = []Importer{
	textFormat{textimport.JSONL, "JSON lines (.jsonl)", "application/x-ndjson", []string{".jsonl", ".ndjson"}},
	textFormat{textimport.JSON5, "JSON5 (.json5)", "application/json5", []string{".json5"}},
	textFormat{textimport.CSV, "Comma-separated values (.csv), as a table", "text/csv", []string{".csv"}},
	textFormat{textimport.TSV, "Tab-separated values (.tsv), as a table", "text/tab-separated-values", []string{".tsv", ".tab"}},
	textFormat{textimport.YAML, "YAML (.yaml, .yml)", "application/yaml", []string{".yaml", ".yml"}},
	textFormat{textimport.TOML, "TOML (.toml)", "application/toml", []string{".toml"}},
	textFormat{textimport.INI, "Settings file (.ini, .cfg, .conf, .properties)", "text/plain", []string{".ini", ".cfg", ".conf", ".properties"}},
	textFormat{textimport.Log, "Log file (.log)", "text/plain", []string{".log"}},
	textFormat{textimport.XML, "XML (.xml)", "application/xml", []string{".xml"}},
	// by name, never extension: a .html file is a page
	textFormat{textimport.HTMLSource, "HTML as text (its source, not rendered)", "text/html", nil},
	textFormat{textimport.Text, "Any text file, as it is", "text/plain", nil},
}

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

func registerTextFormats(r *Registry) {
	for _, f := range TextFormats {
		r.AddImporter(f)
	}
}

func wrapJSON(i Importer) Importer { return jsonFile{i} }
