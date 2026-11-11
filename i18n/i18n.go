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
	mu       sync.RWMutex
	msgs     map[string]map[string]string
	Fallback string
}

// New returns an empty catalog.
func New() *Catalog { return &Catalog{msgs: map[string]map[string]string{}} }

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

func base(lang string) string {
	b, _, _ := strings.Cut(lang, "-")
	return b
}

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

func Direction(lang string) string {
	switch base(normalize(lang)) {
	case "ar", "he", "fa", "ur", "yi", "ps", "sd", "dv":
		return "rtl"
	}
	return "ltr"
}
