// Package i18n translates an interface: message catalogs by language, a
// lookup that falls back sensibly (es-MX to es to the default to the English the
// code already has), and a match of the browser's preferred languages against
// the ones available.
//
// A message id names a place in the interface ("saver.save"); the code passes the
// English text with it, so an interface works with no catalog at all and a
// missing translation shows English, never an id.
//
//	cat := i18n.Default()                // es, fr and de for the stock components
//	text := cat.Text("fr", "saver.save", "Save")    // "Enregistrer"
//
// Plural and ordering differences are handled the plain way: a message may hold
// fmt verbs (%d, %s, %[2]s) and the catalog's text for a language puts them
// where that language wants them.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed packs/*.json
var packs embed.FS

// Catalog holds the messages of each language.
type Catalog struct {
	mu   sync.RWMutex
	msgs map[string]map[string]string
	// Fallback is the language tried when the one asked for lacks a message
	// ("" for none: the caller's English default is used).
	Fallback string
}

// New returns an empty catalog.
func New() *Catalog { return &Catalog{msgs: map[string]map[string]string{}} }

// Add merges messages for a language ("fr", "pt-BR"); later calls win.
func (c *Catalog) Add(lang string, messages map[string]string) {
	lang = normalize(lang)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.msgs[lang] == nil {
		c.msgs[lang] = map[string]string{}
	}
	for id, m := range messages {
		c.msgs[lang][id] = m
	}
}

// AddJSON reads {"fr": {"id": "text"}, "de": {...}} and adds each language.
func (c *Catalog) AddJSON(data []byte) error {
	var all map[string]map[string]string
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	for lang, m := range all {
		c.Add(lang, m)
	}
	return nil
}

// Default returns a catalog with the stock components' messages in the
// languages this package ships (see Languages).
func Default() *Catalog {
	c := New()
	entries, _ := packs.ReadDir("packs")
	for _, e := range entries {
		if b, err := packs.ReadFile("packs/" + e.Name()); err == nil {
			_ = c.AddJSON(b)
		}
	}
	return c
}

// Languages lists the languages with at least one message, sorted.
func (c *Catalog) Languages() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.msgs))
	for l := range c.msgs {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

func normalize(lang string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(lang), "_", "-"))
}

// base is the language without its region ("pt-br" is "pt").
func base(lang string) string {
	b, _, _ := strings.Cut(lang, "-")
	return b
}

// Lookup returns the message for id in lang, trying the region ("pt-BR"), the
// language ("pt") and Fallback; ok is false if none has it.
func (c *Catalog) Lookup(lang, id string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	lang = normalize(lang)
	for _, l := range []string{lang, base(lang), c.Fallback} {
		if l == "" {
			continue
		}
		if m, ok := c.msgs[l][id]; ok {
			return m, true
		}
	}
	return "", false
}

// Text returns the message for id in lang with args put in (if there are any),
// or def, the English text the code has, when the catalog has none.
func (c *Catalog) Text(lang, id, def string, args ...any) string {
	m, ok := c.Lookup(lang, id)
	if !ok {
		m = def
	}
	if len(args) > 0 {
		return fmt.Sprintf(m, args...)
	}
	return m
}

// Match picks the best of the available languages for the languages a person
// prefers (most wanted first, as a browser's navigator.languages lists them): an
// exact match, else the same language in another region. It returns fallback when
// nothing fits.
func Match(preferred, available []string, fallback string) string {
	have := map[string]string{}
	for _, a := range available {
		have[normalize(a)] = a
	}
	for _, p := range preferred {
		p = normalize(p)
		if a, ok := have[p]; ok {
			return a
		}
		if a, ok := have[base(p)]; ok {
			return a
		}
		for _, a := range available {
			if base(normalize(a)) == base(p) {
				return a
			}
		}
	}
	return fallback
}

// Direction is "rtl" for the languages written right to left and "ltr" for the
// rest, for a page's dir attribute.
func Direction(lang string) string {
	switch base(normalize(lang)) {
	case "ar", "he", "fa", "ur", "yi", "ps", "sd", "dv":
		return "rtl"
	}
	return "ltr"
}
