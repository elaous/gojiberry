// Package tokens is the design-token set that themes the editor and its exports: named colours, a
// type scale and a spacing scale. A Set is checked when it is edited (Validate), not when somebody
// finds the page unreadable: the text colours must reach the WCAG 2 contrast of 4.5:1 on their
// backgrounds, the type scale must grow and not start below a readable size, and every colour must
// be one the rest of the system can read. CSS turns a valid set into custom properties.
package tokens

import (
	"fmt"
	"sort"
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

// pairs are the colour pairs that are read as text, each needing minRatio.
var pairs = []struct{ fg, bg string }{
	{Text, Background}, {Muted, Background}, {Link, Background}, {AccentText, Accent}, {Danger, Background},
}

const (
	minRatio    = 4.5 // WCAG 2 success criterion 1.4.3
	minBaseSize = 16.0
)

// Set is one theme.
type Set struct {
	Name string `json:"name"`
	// Colors maps a colour name to a CSS colour ("#rrggbb", "#rgb", "rgb(r, g, b)" or a basic name).
	Colors map[string]string `json:"colors"`
	// TypeScale is the font sizes in px, smallest first; the first is the body size.
	TypeScale []float64 `json:"typeScale"`
	// Spacing is the spacing steps in px, smallest first.
	Spacing []float64 `json:"spacing"`
}

// Problem is one thing wrong with a Set, in words a person can act on.
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

// Validate lists what is wrong with s, in a fixed order; none means the set is fit to apply.
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

// CSS writes the set as custom properties on :root, for the page and the exports to share:
// --gj-color-<name>, --gj-font-<step> (1-based, px) and --gj-space-<step>. A set with problems is
// refused so a theme that cannot be read never reaches a page.
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

// cssName keeps a token name to what is safe inside a custom property name, so a name from a stored
// file cannot end the declaration early.
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
