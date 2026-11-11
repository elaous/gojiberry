package docfile

import (
	"encoding/json"
	"slices"
)

// ImportKey is the Meta key for the import report.
const ImportKey = "import.report"

// ImportLoss is one thing an import left out.
type ImportLoss struct {
	Feature   string `json:"feature"`
	Treatment string `json:"treatment"`
	Note      string `json:"note,omitempty"`
	Count     int    `json:"count,omitempty"`
}

// ImportReport is what bringing a file in left out.
type ImportReport struct {
	// Name is the file that was imported.
	Name   string       `json:"name"`
	Known  bool         `json:"known,omitempty"`
	Losses []ImportLoss `json:"losses,omitempty"`
}

// ImportReport returns the report kept with the document.
func (d Document) ImportReport() (ImportReport, bool) {
	v, ok := d.Get(ImportKey)
	if !ok || v == "" {
		return ImportReport{}, false
	}
	var r ImportReport
	if json.Unmarshal([]byte(v), &r) != nil || r.Name == "" {
		return ImportReport{}, false
	}
	return r, true
}

// SetImportReport keeps the report with the document.
func (d *Document) SetImportReport(r *ImportReport) {
	if r == nil {
		d.Meta = slices.DeleteFunc(slices.Clone(d.Meta), func(m MetaEntry) bool { return m.Key == ImportKey })
		return
	}
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	d.Set(ImportKey, string(b))
}
