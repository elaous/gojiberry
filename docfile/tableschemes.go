package docfile

import (
	"encoding/json"
	"slices"

	"github.com/elaous/gojiberry/canvas"
)

const TableSchemesKey = "table.schemes"

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

func (d *Document) SetTableSchemes(list []canvas.TableScheme) {
	if len(list) == 0 {
		d.Meta = slices.DeleteFunc(slices.Clone(d.Meta), func(m MetaEntry) bool { return m.Key == TableSchemesKey })
		return
	}
	if b, err := json.Marshal(list); err == nil {
		d.Set(TableSchemesKey, string(b))
	}
}
