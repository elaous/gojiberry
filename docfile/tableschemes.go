package docfile

import (
	"encoding/json"
	"slices"

	"github.com/elaous/gojiberry/canvas"
)

// TableSchemesKey is the Meta key the table styles a person defined are kept under.
const TableSchemesKey = "table.schemes"

// TableSchemes returns the table styles kept with the document (only the valid ones).
func (d Document) TableSchemes() []canvas.TableScheme {
	v, ok := d.Get(TableSchemesKey)
	if !ok || v == "" {
		return nil
	}
	var list []canvas.TableScheme
	if json.Unmarshal([]byte(v), &list) != nil {
		return nil
	}
	var out []canvas.TableScheme
	for _, s := range list {
		if s.Valid() == nil && len(out) < canvas.MaxCustomSchemes {
			out = append(out, s)
		}
	}
	return out
}

// SetTableSchemes keeps the table styles with the document; none removes the entry.
func (d *Document) SetTableSchemes(list []canvas.TableScheme) {
	if len(list) == 0 {
		d.Meta = slices.DeleteFunc(slices.Clone(d.Meta), func(m MetaEntry) bool { return m.Key == TableSchemesKey })
		return
	}
	if b, err := json.Marshal(list); err == nil {
		d.Set(TableSchemesKey, string(b))
	}
}
