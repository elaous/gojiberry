// Package cssgen turns a bounded, real slice of CSS -- :root/body.dark
// custom-property tokens and flat per-class rules -- into a real
// map[string]xangogio.ClassStyle, for exactly a caller-given list of
// class names. It is the build-time generator half of xangogio's own
// class-driven styling (see Renderer.Styles/ClassStyle's own doc
// comments for the runtime half); this package produces the table,
// it doesn't apply it.
//
// It is deliberately not a general CSS parser. Two narrowing
// assumptions, both confirmed by hand against OnDesign's own real
// index.html (466 lines, no @media/@keyframes/nesting anywhere in
// it) rather than assumed:
//
//   - Only flat, non-nested "selector { declarations }" rules are
//     recognized. There is no cascade/specificity resolution beyond
//     "later rule for the same bare class wins per field" (source
//     order, the same rule real CSS uses for equal-specificity
//     single-class selectors) -- no @media, no nesting, no
//     descendant/child/sibling combinators, no compound selectors
//     (".a.b"), no pseudo-classes (":hover", ":disabled" included --
//     ClassStyle has no separate hover/disabled variant of its own
//     yet, so there would be nothing correct to do with one even if
//     it were parsed). Any selector matching a wanted class name that
//     isn't a bare ".name" is reported as a Warning and skipped, not
//     silently misapplied to the base style.
//   - Only five declarations have any Gio equivalent at all: padding,
//     background, border (shorthand), border-color, border-width,
//     border-radius. Every other real CSS property a matched rule
//     sets (color, font-*, display, position, cursor, box-shadow,
//     transition, ...) is reported as a Warning, not silently
//     dropped -- there is simply no ClassStyle field for it yet.
package cssgen

import (
	"fmt"
	"image/color"
	"math"
	"regexp"
	"strconv"
	"strings"

	"gioui.org/unit"

	"github.com/elaous/gojiberry/xangogio"
)

// Tokens is a resolved --custom-property table for one theme, keyed by
// name without its leading "--" (e.g. "panel-bg" for --panel-bg).
type Tokens map[string]string

// Warning documents one CSS declaration or selector this generator
// recognized syntactically but didn't (or couldn't precisely)
// translate into a ClassStyle field -- returned instead of silently
// skipped, so a caller can decide whether an approximation is
// acceptable or a rule needs a human to look at it by hand. Property
// is empty for a selector-level warning (an unsupported selector
// shape matching a wanted class name).
type Warning struct {
	Class    string
	Property string
	Value    string
	Reason   string
}

func (w Warning) String() string {
	if w.Property == "" {
		return fmt.Sprintf("%s: %s", w.Class, w.Reason)
	}
	return fmt.Sprintf("%s: %s: %q: %s", w.Class, w.Property, w.Value, w.Reason)
}

// ParseTokens extracts custom properties from css's own `:root { --x:
// v; }` block as Light, and its `body.dark { --x: v; }` block
// (OnDesign's own dark-theme override selector, toggled by adding
// "dark" to <body>'s own class list) layered on top of Light as Dark
// -- only a subset of --custom-properties actually differs between
// the two in practice, so Dark starts as a full copy of Light and only
// overrides what body.dark itself redeclares, the same cascade
// OnDesign's real page relies on. Either return value is empty (not
// nil) if the corresponding selector block isn't present in css at
// all.
func ParseTokens(css string) (light, dark Tokens) {
	css = stripComments(css)
	light = Tokens{}
	for _, r := range findRules(css) {
		if r.selector != ":root" {
			continue
		}
		for name, val := range customProps(r.decls) {
			light[name] = val
		}
	}
	dark = Tokens{}
	for k, v := range light {
		dark[k] = v
	}
	for _, r := range findRules(css) {
		if r.selector != "body.dark" {
			continue
		}
		for name, val := range customProps(r.decls) {
			dark[name] = val
		}
	}
	return light, dark
}

// Generate parses css for exactly the rules matching classNames (a
// bare ".name" selector, see this package's own doc comment for
// exactly what's supported), resolving var(--x) values against
// tokens, and returns one xangogio.ClassStyle per class name that had at
// least one recognized declaration, plus every Warning encountered
// along the way (an unsupported selector shape, an unsupported
// property, or a value this generator's own narrow parsers couldn't
// make sense of). A class name with no matching rule at all, or whose
// only rule contained nothing recognized, simply doesn't appear in the
// returned map -- Renderer.Styles already treats a missing entry as a
// no-op, so there's no need for an explicit zero-value placeholder.
func Generate(css string, classNames []string, tokens Tokens) (map[string]xangogio.ClassStyle, []Warning) {
	css = stripComments(css)
	wanted := make(map[string]bool, len(classNames))
	for _, c := range classNames {
		wanted[c] = true
	}

	styles := make(map[string]xangogio.ClassStyle)
	var warnings []Warning

	for _, r := range findRules(css) {
		for _, sel := range strings.Split(r.selector, ",") {
			sel = strings.TrimSpace(sel)
			if !strings.HasPrefix(sel, ".") {
				continue
			}
			name := baseClassOf(sel)
			if !wanted[name] {
				continue
			}
			if sel != "."+name {
				warnings = append(warnings, Warning{
					Class:  name,
					Reason: fmt.Sprintf("selector %q is not a bare .class rule (compound selectors, pseudo-classes, and combinators aren't supported)", sel),
				})
				continue
			}

			style, had := styles[name]
			for _, d := range declsOf(r.decls) {
				if _, warn := applyDecl(&style, name, d.prop, d.val, tokens); warn != (Warning{}) {
					warnings = append(warnings, warn)
				}
			}
			if !had && style == (xangogio.ClassStyle{}) {
				// Every declaration in this rule was unrecognized; a
				// zero ClassStyle is indistinguishable from "no rule
				// at all" to Renderer.Styles, so leave it out of the
				// map rather than adding a misleadingly-present but
				// empty entry.
				continue
			}
			styles[name] = style
		}
	}
	return styles, warnings
}

// applyDecl tries to translate one CSS declaration into style's own
// fields, reporting ok=false with a Warning for an unrecognized
// property or an unparseable value, or ok=true with a zero Warning for
// a clean translation, or ok=true with a non-zero Warning for a
// translation that's only an approximation (documented per case
// below).
func applyDecl(style *xangogio.ClassStyle, class, prop, val string, tokens Tokens) (ok bool, warn Warning) {
	switch prop {
	case "padding":
		y, x, ok, approx := parsePadding(val)
		if !ok {
			return false, Warning{Class: class, Property: prop, Value: val, Reason: "couldn't parse padding length(s)"}
		}
		style.PaddingY, style.PaddingX = y, x
		if approx {
			return true, Warning{Class: class, Property: prop, Value: val, Reason: "3-4 value padding shorthand approximated using the first (top, right) pair -- ClassStyle has no separate top/bottom/left/right"}
		}
		return true, Warning{}
	case "background":
		c, ok := parseColorValue(val, tokens)
		if !ok {
			return false, Warning{Class: class, Property: prop, Value: val, Reason: "unsupported color value (only hex/rgb()/rgba()/var()/none/transparent are recognized)"}
		}
		style.Background = c
		return true, Warning{}
	case "border":
		w, c, ok := parseBorder(val, tokens)
		if !ok {
			return false, Warning{Class: class, Property: prop, Value: val, Reason: "couldn't parse border shorthand"}
		}
		style.BorderWidth = w
		style.BorderColor = c
		return true, Warning{}
	case "border-color":
		c, ok := parseColorValue(val, tokens)
		if !ok {
			return false, Warning{Class: class, Property: prop, Value: val, Reason: "unsupported color value"}
		}
		style.BorderColor = c
		return true, Warning{}
	case "border-width":
		l, ok := parseLength(val)
		if !ok {
			return false, Warning{Class: class, Property: prop, Value: val, Reason: "couldn't parse length"}
		}
		style.BorderWidth = l
		return true, Warning{}
	case "border-radius":
		l, ok := parseLength(val)
		if !ok {
			return false, Warning{Class: class, Property: prop, Value: val, Reason: "couldn't parse length"}
		}
		style.CornerRadius = l
		return true, Warning{}
	default:
		return false, Warning{Class: class, Property: prop, Value: val, Reason: "property has no Gio equivalent modeled in ClassStyle yet"}
	}
}

// rule is one flat "selector { declarations }" block.
type rule struct {
	selector string
	decls    string
}

var (
	commentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

func stripComments(css string) string {
	return commentRE.ReplaceAllString(css, "")
}

// baseClassOf returns the leading class name from a "." selector,
// stripping anything from the first pseudo-class/compound-class/
// combinator marker onward (":", a second ".", a space, ">", "+",
// "~", "["). This lets a selector like ".find-toggle:disabled" or
// ".find-toggle.active" still be recognized as "about" find-toggle --
// matched against wanted, then rejected-and-warned for not being a
// bare rule -- rather than silently falling through the wanted lookup
// unrecognized just because its full text isn't an exact class name.
func baseClassOf(sel string) string {
	name := strings.TrimPrefix(sel, ".")
	if i := strings.IndexAny(name, ":. >+~["); i >= 0 {
		name = name[:i]
	}
	return name
}

// findRules extracts every flat rule in css, in source order. Block-less
// at-rules (@import, @charset, ...) are skipped, and so are at-rules with a
// block (@media, @supports, @keyframes, ...) together with everything inside
// them: nested rules are not supported, and applying a @media body as if it
// were unconditional would override the base styles.
func findRules(css string) []rule {
	var rules []rule
	i := 0
	for i < len(css) {
		// The prelude runs to the next '{', ';' or '}'.
		j := strings.IndexAny(css[i:], "{;}")
		if j < 0 {
			break
		}
		j += i
		prelude := strings.TrimSpace(css[i:j])
		switch css[j] {
		case ';', '}': // a block-less at-rule, or a stray brace: nothing to keep
			i = j + 1
			continue
		}
		// css[j] == '{': find the matching '}'.
		depth, k := 1, j+1
		for k < len(css) && depth > 0 {
			switch css[k] {
			case '{':
				depth++
			case '}':
				depth--
			}
			k++
		}
		if depth > 0 { // unterminated block
			break
		}
		body := css[j+1 : k-1]
		i = k
		if strings.HasPrefix(prelude, "@") || prelude == "" || strings.ContainsAny(body, "{}") {
			continue
		}
		rules = append(rules, rule{selector: prelude, decls: body})
	}
	return rules
}

// decl is one "prop: value" declaration.
type decl struct{ prop, val string }

// declsOf splits a "prop: value; prop2: value2" declaration block into its
// declarations in source order (CSS lets a later declaration override an
// earlier one, so the order matters), tolerating a trailing/missing final
// semicolon.
func declsOf(decls string) []decl {
	var out []decl
	for _, d := range strings.Split(decls, ";") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		i := strings.IndexByte(d, ':')
		if i < 0 {
			continue
		}
		out = append(out, decl{strings.TrimSpace(d[:i]), strings.TrimSpace(d[i+1:])})
	}
	return out
}

// customProps is declsOf's own counterpart for a :root/body.dark
// block: only "--name: value" declarations, keyed without the leading
// "--".
func customProps(decls string) map[string]string {
	out := map[string]string{}
	for _, d := range declsOf(decls) {
		if name, ok := strings.CutPrefix(d.prop, "--"); ok {
			out[name] = d.val
		}
	}
	return out
}

var lengthRE = regexp.MustCompile(`^(-?[0-9]*\.?[0-9]+)(px|em)?$`)

// parseLength parses a single CSS length. "em" is approximated at 16
// px/em -- this app's own body font-size is never overridden in
// index.html's CSS, so that's the real browser default, not a guess --
// and a bare unitless number is only accepted for zero (the one case
// real CSS itself allows a unitless length).
func parseLength(s string) (unit.Dp, bool) {
	s = strings.TrimSpace(s)
	m := lengthRE.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	switch m[2] {
	case "":
		if n != 0 {
			return 0, false
		}
		return 0, true
	case "px":
		return unit.Dp(n), true
	case "em":
		return unit.Dp(n * 16), true
	}
	return 0, false
}

// parsePadding parses the CSS `padding` shorthand's 1-4 space-
// separated length values into (Y, X), the shape ClassStyle itself
// supports. 1 value means both axes equal; 2 means (Y, X) exactly,
// CSS's own meaning for that form -- no approximation needed for
// either. 3 (top, horizontal, bottom) or 4 (top, right, bottom, left)
// values can't be represented exactly by a Y/X-only type, so approx
// reports that the first (top, right) pair was used as an
// approximation rather than the true asymmetric value.
func parsePadding(value string) (y, x unit.Dp, ok, approx bool) {
	fields := strings.Fields(value)
	lens := make([]unit.Dp, 0, len(fields))
	for _, f := range fields {
		l, ok := parseLength(f)
		if !ok {
			return 0, 0, false, false
		}
		lens = append(lens, l)
	}
	switch len(lens) {
	case 1:
		return lens[0], lens[0], true, false
	case 2:
		return lens[0], lens[1], true, false
	case 3, 4:
		return lens[0], lens[1], true, true
	default:
		return 0, 0, false, false
	}
}

var (
	hex6RE = regexp.MustCompile(`^#([0-9a-fA-F]{6})$`)
	hex3RE = regexp.MustCompile(`^#([0-9a-fA-F]{3})$`)
	rgbRE  = regexp.MustCompile(`^rgba?\(\s*([0-9]+)\s*,\s*([0-9]+)\s*,\s*([0-9]+)\s*(?:,\s*([0-9.]+)\s*)?\)$`)
	varRE  = regexp.MustCompile(`^var\(--([\w-]+)\)$`)
)

// parseColorValue recognizes #hex (3 or 6 digit), rgb()/rgba(), a
// var(--token) reference resolved against tokens (recursively, in
// case a token's own value is itself one of these forms), and
// "none"/"transparent" (a real, intentional zero color, not a failure
// -- the zero color.NRGBA already means fully transparent black).
// Anything else (a named color like "red", a gradient, ...) reports
// ok=false.
func parseColorValue(s string, tokens Tokens) (color.NRGBA, bool) {
	return parseColor(s, tokens, 0)
}

// maxVarDepth bounds var() chains, so a token that refers to itself (directly
// or through others) is reported as unparseable instead of recursing forever.
const maxVarDepth = 16

func parseColor(s string, tokens Tokens, depth int) (color.NRGBA, bool) {
	s = strings.TrimSpace(s)
	if s == "none" || s == "transparent" {
		return color.NRGBA{}, true
	}
	if m := varRE.FindStringSubmatch(s); m != nil {
		v, ok := tokens[m[1]]
		if !ok || depth >= maxVarDepth {
			return color.NRGBA{}, false
		}
		return parseColor(v, tokens, depth+1)
	}
	if m := hex6RE.FindStringSubmatch(s); m != nil {
		r, _ := strconv.ParseUint(m[1][0:2], 16, 8)
		g, _ := strconv.ParseUint(m[1][2:4], 16, 8)
		b, _ := strconv.ParseUint(m[1][4:6], 16, 8)
		return color.NRGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255}, true
	}
	if m := hex3RE.FindStringSubmatch(s); m != nil {
		r, _ := strconv.ParseUint(strings.Repeat(string(m[1][0]), 2), 16, 8)
		g, _ := strconv.ParseUint(strings.Repeat(string(m[1][1]), 2), 16, 8)
		b, _ := strconv.ParseUint(strings.Repeat(string(m[1][2]), 2), 16, 8)
		return color.NRGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255}, true
	}
	if m := rgbRE.FindStringSubmatch(s); m != nil {
		r, _ := strconv.Atoi(m[1])
		g, _ := strconv.Atoi(m[2])
		b, _ := strconv.Atoi(m[3])
		a := 255
		if m[4] != "" {
			af, _ := strconv.ParseFloat(m[4], 64)
			a = int(math.Round(min(max(af, 0), 1) * 255))
		}
		clamp := func(v int) uint8 { return uint8(min(max(v, 0), 255)) }
		return color.NRGBA{R: clamp(r), G: clamp(g), B: clamp(b), A: uint8(a)}, true
	}
	return color.NRGBA{}, false
}

// parseBorder parses the CSS `border` shorthand (any order/subset of a
// length, a style keyword, and a color -- e.g. "1px solid
// var(--panel-border)"), ignoring the style keyword entirely (Gio only
// draws a plain stroke, there's no dashed/dotted/double equivalent).
// "none" is a real, intentional zero border, not a failure. ok is
// false only if the value contained neither a recognized length nor a
// recognized color at all -- a border-style keyword alone
// ("border: none" aside) isn't enough to conclude anything.
func parseBorder(value string, tokens Tokens) (unit.Dp, color.NRGBA, bool) {
	value = strings.TrimSpace(value)
	if value == "none" {
		return 0, color.NRGBA{}, true
	}
	var width unit.Dp
	var col color.NRGBA
	var any bool
	for _, f := range strings.Fields(value) {
		if l, ok := parseLength(f); ok {
			width = l
			any = true
			continue
		}
		if c, ok := parseColorValue(f, tokens); ok {
			col = c
			any = true
			continue
		}
		// Anything else here is assumed to be the border-style
		// keyword (solid, dashed, ...) and simply ignored.
	}
	return width, col, any
}
