package docfile

import (
	"encoding/json"
	"slices"

	"github.com/elaous/gojiberry/reference"
)

const (
	SourcesKey        = "reference.sources"
	ReferenceStyleKey = "reference.style"
)

func (d Document) Sources() []reference.Source {
	v, ok := d.Get(SourcesKey)
	if !ok || v == "" {
		return nil
	}
	var s []reference.Source
	if json.Unmarshal([]byte(v), &s) != nil {
		return nil
	}
	return s
}

func (d Document) ReferenceStyle() reference.Style {
	v, _ := d.Get(ReferenceStyleKey)
	for _, s := range reference.Styles {
		if string(s.Style) == v {
			return s.Style
		}
	}
	return reference.APA
}

func (d *Document) SetSources(style reference.Style, sources []reference.Source) {
	if len(sources) == 0 {
		d.Meta = slices.DeleteFunc(slices.Clone(d.Meta), func(m MetaEntry) bool { return m.Key == SourcesKey || m.Key == ReferenceStyleKey })
		return
	}
	b, err := json.Marshal(sources)
	if err != nil {
		return
	}
	d.Set(SourcesKey, string(b))
	d.Set(ReferenceStyleKey, string(style))
}
