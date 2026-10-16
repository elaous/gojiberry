package docfile

import (
	"encoding/json"
	"slices"

	"github.com/elaous/gojiberry/reference"
)

// Meta keys for the sources a document cites and the style it cites them in.
const (
	SourcesKey        = "reference.sources"
	ReferenceStyleKey = "reference.style"
)

// Sources returns the sources kept with the document, if they can be read.
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

// ReferenceStyle returns the style the document cites in; APA when none was chosen.
func (d Document) ReferenceStyle() reference.Style {
	v, _ := d.Get(ReferenceStyleKey)
	for _, s := range reference.Styles {
		if string(s.Style) == v {
			return s.Style
		}
	}
	return reference.APA
}

// SetSources keeps the sources and the style with the document; no sources removes both.
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
