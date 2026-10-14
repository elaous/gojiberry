package components

import (
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/tokens"
	"github.com/elaous/gojiberry/xango"
)

// ThemeEditorProps configures ThemeEditor.
type ThemeEditorProps struct {
	// Set is the theme being edited. The editor does not keep it: it shows Set, and calls OnChange with
	// the edited copy (a copy, so the caller's own map is never changed under it).
	Set tokens.Set
	// OnChange is called as the person types, with the set whether or not it is valid; Problems of the
	// set are shown at once, and the caller decides what to apply (only a set without problems).
	OnChange func(tokens.Set)
	// OnReset puts the default theme back; the button is left out when nil.
	OnReset func()
}

// themeColors are the colours the editor offers, in the order they are listed, with their words.
var themeColors = []struct{ name, id, def string }{
	{tokens.Text, "theme.text", "Text"},
	{tokens.Background, "theme.background", "Page background"},
	{tokens.Muted, "theme.muted", "Secondary text"},
	{tokens.Link, "theme.link", "Links"},
	{tokens.Accent, "theme.accent", "Accent (buttons)"},
	{tokens.AccentText, "theme.accentText", "Text on the accent"},
	{tokens.Danger, "theme.danger", "Errors"},
}

// ThemeEditor edits a design-token set (package tokens): the colours as text fields with a swatch, the type
// scale and the spacing as lists of numbers. It checks the set as it is typed: what is wrong is listed
// under the fields in plain words (a text colour under the contrast of 4.5:1, a body size under 16px), the
// field that has the problem says so (aria-invalid, described by the list), and a sample shows how the
// colours read together. Nothing is applied here; the caller applies a set that has no problems.
func ThemeEditor(p ThemeEditorProps) xango.Node {
	problems := p.Set.Validate()
	byField := map[string][]string{}
	for _, pr := range problems {
		byField[pr.Field] = append(byField[pr.Field], pr.Message)
	}
	edit := func(f func(*tokens.Set)) {
		if p.OnChange == nil {
			return
		}
		s := p.Set
		s.Colors = make(map[string]string, len(p.Set.Colors))
		for k, v := range p.Set.Colors {
			s.Colors[k] = v
		}
		s.TypeScale = append([]float64(nil), p.Set.TypeScale...)
		s.Spacing = append([]float64(nil), p.Set.Spacing...)
		f(&s)
		p.OnChange(s)
	}
	field := func(id, label, value, problemField string, on func(string)) xango.Node {
		attrs := []xango.Attr{xango.A("id", id), xango.A("type", "text"), xango.A("value", value), xango.A("spellcheck", "false"),
			xango.A("autocomplete", "off"), xango.A("class", "gj-theme-input"), xango.A("data-field", problemField),
			xango.On("input", func(ev xango.Event) { on(xango.EventValue[string](ev)) })}
		if len(byField[problemField]) > 0 {
			attrs = append(attrs, xango.A("aria-invalid", "true"), xango.A("aria-describedby", "gj-theme-problems"))
		}
		return xango.El("label", []xango.Attr{xango.A("class", "gj-theme-row"), xango.A("for", id)},
			xango.El("span", []xango.Attr{xango.A("class", "gj-theme-label")}, xango.Text(label)),
			xango.El("input", attrs))
	}
	var rows []xango.Node
	for _, c := range themeColors {
		c := c
		v := p.Set.Colors[c.name]
		row := field("gj-theme-"+c.name, tr(c.id, c.def), v, "colors."+c.name, func(s string) {
			edit(func(t *tokens.Set) { t.Colors[c.name] = s })
		})
		swatch := xango.El("span", []xango.Attr{xango.A("class", "gj-theme-swatch"), xango.A("aria-hidden", "true"), xango.A("style", "background:"+safeCSSColour(v))})
		rows = append(rows, xango.El("div", []xango.Attr{xango.A("class", "gj-theme-colour")}, row, swatch))
	}
	rows = append(rows,
		field("gj-theme-typeScale", tr("theme.typeScale", "Type sizes in px, smallest first"), numberList(p.Set.TypeScale), "typeScale", func(s string) {
			edit(func(t *tokens.Set) { t.TypeScale = parseNumberList(s) })
		}),
		field("gj-theme-spacing", tr("theme.spacing", "Spacing steps in px, smallest first"), numberList(p.Set.Spacing), "spacing", func(s string) {
			edit(func(t *tokens.Set) { t.Spacing = parseNumberList(s) })
		}))

	var status xango.Node
	if len(problems) == 0 {
		status = xango.El("p", []xango.Attr{xango.A("id", "gj-theme-problems"), xango.A("class", "gj-theme-ok"), xango.A("role", "status")},
			xango.Text(tr("theme.ok", "This theme can be read comfortably and is applied to HTML exports.")))
	} else {
		var items []xango.Node
		for _, pr := range problems {
			items = append(items, xango.El("li", nil, xango.Text(pr.Message)))
		}
		status = xango.El("div", []xango.Attr{xango.A("id", "gj-theme-problems"), xango.A("class", "gj-theme-problems"), xango.A("role", "status")},
			xango.El("p", nil, xango.Text(trn("theme.problems", "%d thing to fix before this theme is used:", "%d things to fix before this theme is used:", len(problems)))),
			xango.El("ul", nil, items...))
	}

	sample := xango.El("div", []xango.Attr{xango.A("class", "gj-theme-sample"), xango.A("aria-hidden", "true"),
		xango.A("style", "color:"+safeCSSColour(p.Set.Colors[tokens.Text])+";background:"+safeCSSColour(p.Set.Colors[tokens.Background])+";padding:0.6em")},
		xango.El("p", []xango.Attr{xango.A("style", "margin:0 0 0.3em")}, xango.Text(tr("theme.sampleText", "Sample text"))),
		xango.El("p", []xango.Attr{xango.A("style", "margin:0 0 0.3em;color:"+safeCSSColour(p.Set.Colors[tokens.Muted]))}, xango.Text(tr("theme.sampleMuted", "Secondary text"))),
		xango.El("p", []xango.Attr{xango.A("style", "margin:0;color:"+safeCSSColour(p.Set.Colors[tokens.Link])+";text-decoration:underline")}, xango.Text(tr("theme.sampleLink", "A link"))))

	children := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(tr("theme.title", "Theme"))),
		xango.El("p", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(tr("theme.intro", "Colours and sizes for this document's web page (HTML export). Text colours are checked as you type."))),
	}
	children = append(children, rows...)
	children = append(children, status, sample)
	if p.OnReset != nil {
		children = append(children, Button(ButtonProps{Label: tr("theme.reset", "Back to the default theme"), Variant: ButtonSecondary, OnClick: func(xango.Event) { p.OnReset() }}))
	}
	return xango.El("section", []xango.Attr{xango.A("class", "gj-theme"), xango.A("aria-label", tr("theme.title", "Theme"))}, children...)
}

// numberList writes sizes as "16, 18, 20".
func numberList(v []float64) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.FormatFloat(n, 'f', -1, 64)
	}
	return strings.Join(parts, ", ")
}

// parseNumberList reads "16, 18 20" back; what is not a number is left out, so the scale then reads as
// shorter or as growing wrongly, which Validate says.
func parseNumberList(s string) []float64 {
	var out []float64
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if n, err := strconv.ParseFloat(f, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// safeCSSColour passes a colour through to a style attribute only when it is a plain hex or rgb() value, so
// a field cannot write more CSS than a colour.
func safeCSSColour(c string) string {
	c = strings.TrimSpace(c)
	if c == "" {
		return "transparent"
	}
	for _, r := range c {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '#', r == '(', r == ')', r == ',', r == ' ', r == '.', r == '%':
		default:
			return "transparent"
		}
	}
	return c
}
