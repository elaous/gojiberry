package tokens

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/a11ycheck"
)

// The colour names every set must define.
const (
	Text       = "text"        // body text
	Background = "background"  // the page
	Muted      = "muted-text"  // secondary text on the page
	Link       = "link"        // links on the page
	Accent     = "accent"      // buttons and highlights (a background)
	AccentText = "accent-text" // text on Accent
	Danger     = "danger"      // errors, as text on the page
)

var pairs = []struct{ fg, bg string }{
	{Text, Background}, {Muted, Background}, {Link, Background}, {AccentText, Accent}, {Danger, Background},
}

const (
	minRatio    = 4.5 // WCAG 2 success criterion 1.4.3
	minBaseSize = 16.0
)

// Set is one theme.
type Set struct {
	Name      string            `json:"name"`
	Colors    map[string]string `json:"colors"`
	TypeScale []float64         `json:"typeScale"`
	// Spacing is the spacing steps in px, smallest first.
	Spacing []float64 `json:"spacing"`
}

type Problem struct {
	// Field is where: "colors.text", "typeScale", "spacing".
	Field   string
	Message string
}

// Default is a set that passes Validate.
func Default() Set {
	return Set{
		Name: "default",
		Colors: map[string]string{
			Text: "#1a1a1a", Background: "#ffffff", Muted: "#595959", Link: "#0b5cad",
			Accent: "#0b5cad", AccentText: "#ffffff", Danger: "#b00020",
		},
		TypeScale: []float64{16, 18, 20, 24, 30, 36},
		Spacing:   []float64{4, 8, 12, 16, 24, 32},
	}
}

func (s Set) Validate() []Problem {
	var out []Problem
	add := func(field, format string, a ...any) { out = append(out, Problem{field, fmt.Sprintf(format, a...)}) }

	required := []string{Text, Background, Muted, Link, Accent, AccentText, Danger}
	for _, name := range required {
		v, ok := s.Colors[name]
		if !ok || strings.TrimSpace(v) == "" {
			add("colors."+name, "The colour %q is missing.", name)
			continue
		}
		if _, ok := a11ycheck.Contrast(v, "#000000"); !ok {
			add("colors."+name, "%q is not a colour that can be read (use #rrggbb, #rgb or rgb(r, g, b)).", v)
		}
	}
	var others []string
	for name := range s.Colors {
		if !slicesContains(required, name) {
			others = append(others, name)
		}
	}
	sort.Strings(others)
	for _, name := range others {
		if _, ok := a11ycheck.Contrast(s.Colors[name], "#000000"); !ok {
			add("colors."+name, "%q is not a colour that can be read.", s.Colors[name])
		}
	}
	for _, p := range pairs {
		ratio, ok := a11ycheck.Contrast(s.Colors[p.fg], s.Colors[p.bg])
		if ok && ratio < minRatio {
			add("colors."+p.fg, "%s on %s has a contrast of %.1f:1; %.1f:1 is needed to read it comfortably.", p.fg, p.bg, ratio, minRatio)
		}
	}

	switch {
	case len(s.TypeScale) == 0:
		add("typeScale", "The type scale has no sizes.")
	case s.TypeScale[0] < minBaseSize:
		add("typeScale", "The body size is %.0fpx; it should be at least %.0fpx.", s.TypeScale[0], minBaseSize)
	}
	if !ascending(s.TypeScale) {
		add("typeScale", "The sizes must grow from one step to the next.")
	}
	if len(s.Spacing) == 0 {
		add("spacing", "The spacing scale has no steps.")
	} else if s.Spacing[0] <= 0 || !ascending(s.Spacing) {
		add("spacing", "The steps must be above zero and grow from one to the next.")
	}
	return out
}

func (s Set) CSS() (string, error) {
	if ps := s.Validate(); len(ps) > 0 {
		return "", fmt.Errorf("tokens: the set has %d problem(s), the first: %s", len(ps), ps[0].Message)
	}
	var b strings.Builder
	b.WriteString(":root{")
	names := make([]string, 0, len(s.Colors))
	for name := range s.Colors {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "--gj-color-%s:%s;", cssName(name), strings.TrimSpace(s.Colors[name]))
	}
	for i, v := range s.TypeScale {
		fmt.Fprintf(&b, "--gj-font-%d:%gpx;", i+1, v)
	}
	for i, v := range s.Spacing {
		fmt.Fprintf(&b, "--gj-space-%d:%gpx;", i+1, v)
	}
	b.WriteString("}")
	return b.String(), nil
}

func cssName(n string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(n) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

func ascending(v []float64) bool {
	for i := 1; i < len(v); i++ {
		if v[i] <= v[i-1] {
			return false
		}
	}
	return true
}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func FromCSS(css string) (s Set, ok bool) {
	body, found := strings.CutPrefix(strings.TrimSpace(css), ":root{")
	if !found {
		return Set{}, false
	}
	body, found = strings.CutSuffix(body, "}")
	if !found {
		return Set{}, false
	}
	s = Set{Name: "document", Colors: map[string]string{}}
	var fonts, spaces = map[int]float64{}, map[int]float64{}
	for _, decl := range strings.Split(body, ";") {
		name, val, found := strings.Cut(decl, ":")
		if !found {
			continue
		}
		name, val = strings.TrimSpace(name), strings.TrimSpace(val)
		switch {
		case strings.HasPrefix(name, "--gj-color-"):
			s.Colors[strings.TrimPrefix(name, "--gj-color-")] = val
		case strings.HasPrefix(name, "--gj-font-"):
			if i, n, ok := step(strings.TrimPrefix(name, "--gj-font-"), val); ok {
				fonts[i] = n
			}
		case strings.HasPrefix(name, "--gj-space-"):
			if i, n, ok := step(strings.TrimPrefix(name, "--gj-space-"), val); ok {
				spaces[i] = n
			}
		}
	}
	if len(s.Colors) == 0 && len(fonts) == 0 && len(spaces) == 0 {
		return Set{}, false
	}
	s.TypeScale, s.Spacing = ordered(fonts), ordered(spaces)
	return s, true
}

func step(idx, val string) (int, float64, bool) {
	i, err := strconv.Atoi(idx)
	if err != nil || i < 1 {
		return 0, 0, false
	}
	n, err := strconv.ParseFloat(strings.TrimSuffix(val, "px"), 64)
	return i, n, err == nil
}

func ordered(m map[int]float64) []float64 {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	out := make([]float64, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}
