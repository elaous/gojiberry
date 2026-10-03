// Package fonts finds fonts: a Repository lists the font families some
// source offers -- the web-safe set, a directory of font files on disk, a
// gojiberry fontserver, the fonts installed on the user's own machine --
// and Multi merges several into one catalog for a font selector. Nothing
// here depends on the browser or the canvas, so it works (and is tested)
// natively; BrowserLocal, the one browser-only repository, is in
// browser_js.go.
package fonts

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Variant is one weight/style cut of a Font.
type Variant struct {
	Weight int    `json:"weight"` // CSS weight, 100-900
	Italic bool   `json:"italic"`
	Style  string `json:"style,omitempty"` // the cut's own name, e.g. "Bold Italic"
	// URL is where the font file can be fetched, for repositories that
	// serve files; empty for fonts already installed on the machine,
	// which CSS can use by name alone.
	URL string `json:"url,omitempty"`
	// Key is where a Library keeps its downloaded copy of the file in its
	// store; empty until the font has been used.
	Key string `json:"key,omitempty"`
}

// Font is a family and the cuts a repository has of it.
type Font struct {
	Family   string    `json:"family"`
	Source   string    `json:"source"`             // the repository's name
	Category string    `json:"category,omitempty"` // serif, sans-serif, monospace, display, handwriting
	Variants []Variant `json:"variants"`
}

// Repository is a source of fonts.
type Repository interface {
	// Name identifies the repository in a font selector ("Google
	// Fonts", "This device").
	Name() string
	// Fonts lists the families on offer.
	Fonts(ctx context.Context) ([]Font, error)
}

// --- static ----------------------------------------------------------

type static struct {
	name  string
	fonts []Font
}

// Static is a repository with a fixed list of fonts, each marked with
// the repository's name as its Source.
func Static(name string, fonts ...Font) Repository {
	out := make([]Font, len(fonts))
	for i, f := range fonts {
		f.Source = name
		out[i] = f
	}
	return &static{name: name, fonts: out}
}

func (s *static) Name() string                          { return s.name }
func (s *static) Fonts(context.Context) ([]Font, error) { return append([]Font(nil), s.fonts...), nil }

// WebSafe is the families every browser can be expected to have (or map
// to something close), plus CSS's generic families.
func WebSafe() Repository {
	regular := []Variant{{Weight: 400}, {Weight: 400, Italic: true}, {Weight: 700}, {Weight: 700, Italic: true}}
	f := func(family, category string) Font { return Font{Family: family, Category: category, Variants: regular} }
	return Static("Web safe",
		f("Arial", "sans-serif"), f("Helvetica", "sans-serif"), f("Verdana", "sans-serif"),
		f("Tahoma", "sans-serif"), f("Trebuchet MS", "sans-serif"),
		f("Times New Roman", "serif"), f("Georgia", "serif"), f("Garamond", "serif"),
		f("Courier New", "monospace"), f("Brush Script MT", "handwriting"), f("Impact", "display"),
		f("sans-serif", "sans-serif"), f("serif", "serif"), f("monospace", "monospace"),
		f("cursive", "handwriting"), f("fantasy", "display"), f("system-ui", "sans-serif"),
	)
}

// --- combining -------------------------------------------------------

type multi struct{ repos []Repository }

// Multi merges repositories into one catalog sorted by family name.
// A family offered by several repositories appears once, with the
// union of their variants and the first repository's Source. If some
// repositories fail, Fonts still returns what the others offered,
// together with an error describing the failures.
func Multi(repos ...Repository) Repository { return &multi{repos: repos} }

func (m *multi) Name() string {
	names := make([]string, len(m.repos))
	for i, r := range m.repos {
		names[i] = r.Name()
	}
	return strings.Join(names, " + ")
}

func (m *multi) Fonts(ctx context.Context) ([]Font, error) {
	var errs []error
	byFamily := map[string]*Font{}
	var order []string
	for _, r := range m.repos {
		fonts, err := r.Fonts(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.Name(), err))
		}
		for _, f := range fonts {
			key := strings.ToLower(f.Family)
			have, ok := byFamily[key]
			if !ok {
				c := f
				c.Variants = append([]Variant(nil), f.Variants...)
				byFamily[key] = &c
				order = append(order, key)
				continue
			}
			if have.Category == "" {
				have.Category = f.Category
			}
			for _, v := range f.Variants {
				i := variantIndex(have.Variants, v)
				switch {
				case i < 0:
					have.Variants = append(have.Variants, v)
				case have.Variants[i].URL == "" && v.URL != "":
					// An earlier source only knew the cut by name (a
					// web-safe font, say); this one can say where to get it.
					have.Variants[i].URL = v.URL
				}
			}
		}
	}
	out := make([]Font, 0, len(order))
	for _, k := range order {
		f := *byFamily[k]
		sortVariants(f.Variants)
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Family) < strings.ToLower(out[j].Family) })
	return out, errors.Join(errs...)
}

func variantIndex(vs []Variant, v Variant) int {
	for i, x := range vs {
		if x.Weight == v.Weight && x.Italic == v.Italic {
			return i
		}
	}
	return -1
}

func sortVariants(vs []Variant) {
	sort.SliceStable(vs, func(i, j int) bool {
		if vs[i].Weight != vs[j].Weight {
			return vs[i].Weight < vs[j].Weight
		}
		return !vs[i].Italic && vs[j].Italic
	})
}

type cached struct {
	repo Repository
	ttl  time.Duration

	mu      sync.Mutex
	fonts   []Font
	fetched time.Time
	ok      bool
}

// Cached remembers repo's answer for ttl, and keeps serving the last good
// answer if a refresh fails (reporting the error alongside it).
func Cached(repo Repository, ttl time.Duration) Repository { return &cached{repo: repo, ttl: ttl} }

func (c *cached) Name() string { return c.repo.Name() }

func (c *cached) Fonts(ctx context.Context) ([]Font, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ok && time.Since(c.fetched) < c.ttl {
		return append([]Font(nil), c.fonts...), nil
	}
	fonts, err := c.repo.Fonts(ctx)
	if err != nil && c.ok {
		return append([]Font(nil), c.fonts...), err // stale beats nothing
	}
	if err == nil {
		c.fonts, c.fetched, c.ok = fonts, time.Now(), true
	}
	return fonts, err
}

// --- searching and CSS ------------------------------------------------

// Search returns the fonts whose family contains query (case-insensitive,
// every space-separated word must match), best matches first: names
// starting with the query, then the rest, each in the order given.
func Search(fonts []Font, query string) []Font {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return append([]Font(nil), fonts...)
	}
	var prefix, rest []Font
	for _, f := range fonts {
		name := strings.ToLower(f.Family)
		match := true
		for _, w := range words {
			match = match && strings.Contains(name, w)
		}
		switch {
		case !match:
		case strings.HasPrefix(name, words[0]):
			prefix = append(prefix, f)
		default:
			rest = append(rest, f)
		}
	}
	return append(prefix, rest...)
}

// Sources returns the distinct Source names in fonts, in first-seen order.
func Sources(fonts []Font) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range fonts {
		if f.Source != "" && !seen[f.Source] {
			seen[f.Source] = true
			out = append(out, f.Source)
		}
	}
	return out
}

// FromSource returns the fonts from one source ("" means all).
func FromSource(fonts []Font, source string) []Font {
	if source == "" {
		return fonts
	}
	var out []Font
	for _, f := range fonts {
		if f.Source == source {
			out = append(out, f)
		}
	}
	return out
}

// Find returns the font with the given family (case-insensitive).
func Find(fonts []Font, family string) (Font, bool) {
	for _, f := range fonts {
		if strings.EqualFold(f.Family, family) {
			return f, true
		}
	}
	return Font{}, false
}

var genericFamilies = map[string]bool{
	"serif": true, "sans-serif": true, "monospace": true, "cursive": true, "fantasy": true,
	"system-ui": true, "ui-serif": true, "ui-sans-serif": true, "ui-monospace": true,
}

// CSSFamily returns the font-family value for f: the family quoted
// (unless it is a CSS generic like "serif") followed by a fallback
// generic chosen from its Category.
func CSSFamily(f Font) string {
	name := f.Family
	if !genericFamilies[strings.ToLower(name)] {
		name = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name) + `"`
	}
	fallback := map[string]string{
		"serif": "serif", "sans-serif": "sans-serif", "monospace": "monospace",
		"handwriting": "cursive", "display": "fantasy",
	}[f.Category]
	if fallback == "" || strings.EqualFold(fallback, f.Family) {
		return name
	}
	return name + ", " + fallback
}

// FontFaceCSS returns @font-face rules for every variant that has a URL,
// so the browser can load them; fonts already on the machine need none.
func FontFaceCSS(fonts []Font) string {
	var b strings.Builder
	for _, f := range fonts {
		for _, v := range f.Variants {
			if v.URL == "" {
				continue
			}
			style := "normal"
			if v.Italic {
				style = "italic"
			}
			fmt.Fprintf(&b, "@font-face{font-family:%q;font-weight:%d;font-style:%s;font-display:swap;src:url(%q)}\n",
				f.Family, max(v.Weight, 100), style, v.URL)
		}
	}
	return b.String()
}

// Closest returns the variant of f nearest the requested weight and
// italic-ness (italic matters most), or false if f has none.
func (f Font) Closest(weight int, italic bool) (Variant, bool) {
	best, bestScore, found := Variant{}, 1<<30, false
	for _, v := range f.Variants {
		score := abs(v.Weight - weight)
		if v.Italic != italic {
			score += 10000
		}
		if score < bestScore {
			best, bestScore, found = v, score, true
		}
	}
	return best, found
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// parseVariantKey parses Google's variant keys: "regular", "italic",
// "700", "700italic".
func parseVariantKey(k string) (weight int, italic bool, ok bool) {
	switch k {
	case "regular":
		return 400, false, true
	case "italic":
		return 400, true, true
	}
	k, italic = strings.CutSuffix(k, "italic")
	w, err := strconv.Atoi(k)
	if err != nil || w < 1 || w > 1000 {
		return 0, false, false
	}
	return w, italic, true
}

// escapePath percent-escapes each segment of a slash-separated path.
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}
