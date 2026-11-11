package cssgen

import (
	"fmt"
	"image/color"
	"math"
	"regexp"
	"strconv"
	"strings"

	"gioui.org/unit"

	"github.com/elaous/gojiberry/xango/gio"
)

type Tokens map[string]string

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

func Generate(css string, classNames []string, tokens Tokens) (map[string]gio.ClassStyle, []Warning) {
	css = stripComments(css)
	wanted := make(map[string]bool, len(classNames))
	for _, c := range classNames {
		wanted[c] = true
	}

	styles := make(map[string]gio.ClassStyle)
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
			if !had && style == (gio.ClassStyle{}) {
				continue
			}
			styles[name] = style
		}
	}
	return styles, warnings
}

func applyDecl(style *gio.ClassStyle, class, prop, val string, tokens Tokens) (ok bool, warn Warning) {
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

func baseClassOf(sel string) string {
	name := strings.TrimPrefix(sel, ".")
	if i := strings.IndexAny(name, ":. >+~["); i >= 0 {
		name = name[:i]
	}
	return name
}

func findRules(css string) []rule {
	var rules []rule
	i := 0
	for i < len(css) {
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

type decl struct{ prop, val string }

// order kept: a later declaration overrides an earlier one
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

// em approximated at 16px: body font-size is never overridden
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

func parseColorValue(s string, tokens Tokens) (color.NRGBA, bool) {
	return parseColor(s, tokens, 0)
}

// a self-referring token is reported unparseable, not recursed
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
	}
	return width, col, any
}
