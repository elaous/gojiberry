package components

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/fonts"
	"github.com/elaous/xango"
)

func byAttr(n xango.Node, name, value string) (xango.Node, bool) {
	for _, a := range n.Attrs {
		if a.Name == name && a.Value == value {
			return n, true
		}
	}
	for _, c := range n.Children {
		if found, ok := byAttr(c, name, value); ok {
			return found, true
		}
	}
	return xango.Node{}, false
}

func fire(t *testing.T, root xango.Node, attr, value, event string, payload any) {
	t.Helper()
	n, ok := byAttr(root, attr, value)
	if !ok {
		t.Fatalf("no element with %s=%q", attr, value)
	}
	for _, a := range n.Attrs {
		if a.Name == event && a.Handler != nil {
			a.Handler(xango.Event{Type: event, Value: payload})
			return
		}
	}
	t.Fatalf("%s=%q has no %q handler", attr, value, event)
}

var testFonts = []fonts.Font{
	{Family: "Open Sans", Source: "Google Fonts", Category: "sans-serif"},
	{Family: "Lora", Source: "Google Fonts", Category: "serif"},
	{Family: "Georgia", Source: "Web safe", Category: "serif"},
	{Family: "My Local", Source: "Local fonts"},
}

func TestFontSelectorClosed(t *testing.T) {
	html := render(t, FontSelector(FontSelectorProps{Fonts: testFonts, Selected: "Lora"}))
	mustContain(t, html, `class="gj-fontselector"`, `aria-haspopup="listbox"`, `aria-expanded="false"`,
		`>Lora<`, `style="font-family:&#34;Lora&#34;, serif"`)
	mustNotContain(t, html, "gj-fontselector-panel")

	html = render(t, FontSelector(FontSelectorProps{Fonts: testFonts, Placeholder: "Pick"}))
	mustContain(t, html, ">Pick<")
	html = render(t, FontSelector(FontSelectorProps{}))
	mustContain(t, html, ">Choose a font<")

	html = render(t, FontSelector(FontSelectorProps{Fonts: testFonts, Selected: "Unknown One"}))
	mustContain(t, html, ">Unknown One<", `font-family:&#34;Unknown One&#34;`)
}

func TestFontSelectorToggle(t *testing.T) {
	var got []bool
	n := FontSelector(FontSelectorProps{Fonts: testFonts, OnToggleOpen: func(o bool) { got = append(got, o) }})
	findHandlers(n, "click")[0](xango.Event{})
	n = FontSelector(FontSelectorProps{Fonts: testFonts, Open: true, OnToggleOpen: func(o bool) { got = append(got, o) }})
	findHandlers(n, "click")[0](xango.Event{})
	if fmt.Sprint(got) != "[true false]" {
		t.Fatalf("toggles = %v", got)
	}
	disabled := render(t, FontSelector(FontSelectorProps{Fonts: testFonts, Open: true, Disabled: true}))
	mustContain(t, disabled, `disabled="disabled"`)
	mustNotContain(t, disabled, "gj-fontselector-panel")
}

func TestFontSelectorPanelListsFontsInTheirOwnTypefaces(t *testing.T) {
	html := render(t, FontSelector(FontSelectorProps{Fonts: testFonts, Open: true, Selected: "Georgia", PreviewText: "Hamburgefonstiv"}))
	mustContain(t, html, "gj-fontselector-panel", `role="listbox"`, `type="search"`,
		`data-family="Open Sans"`, `data-family="Georgia"`, `data-family="My Local"`,
		`font-family:&#34;Open Sans&#34;, sans-serif`, ">Hamburgefonstiv<", ">Google Fonts<")
	mustContain(t, html, `aria-selected="true"`, "gj-fontselector-option-selected")
	mustContain(t, html, `data-source=""`, `data-source="Google Fonts"`, `data-source="Web safe"`, `data-source="Local fonts"`)
	if strings.Count(html, `role="option"`) != 4 {
		t.Fatalf("expected 4 options: %s", html)
	}
	mustContain(t, render(t, FontSelector(FontSelectorProps{Fonts: testFonts, Open: true})), "The quick brown fox")
}

func TestFontSelectorSearchAndSourceFilter(t *testing.T) {
	html := render(t, FontSelector(FontSelectorProps{Fonts: testFonts, Open: true, Query: "o"}))
	if strings.Count(html, `role="option"`) != 4 || strings.Index(html, `data-family="Open Sans"`) > strings.Index(html, `data-family="Lora"`) {
		t.Fatalf("prefix matches first: %s", html)
	}
	html = render(t, FontSelector(FontSelectorProps{Fonts: testFonts, Open: true, Query: "geo"}))
	if strings.Count(html, `role="option"`) != 1 {
		t.Fatalf("query filter: %s", html)
	}
	html = render(t, FontSelector(FontSelectorProps{Fonts: testFonts, Open: true, Source: "Google Fonts"}))
	if strings.Count(html, `role="option"`) != 2 {
		t.Fatalf("source filter: %s", html)
	}
	mustContain(t, html, `aria-pressed="true" data-source="Google Fonts"`)
	html = render(t, FontSelector(FontSelectorProps{Fonts: testFonts, Open: true, Query: "zzz"}))
	mustContain(t, html, "No fonts match")
	one := render(t, FontSelector(FontSelectorProps{Fonts: testFonts[:2], Open: true}))
	mustNotContain(t, one, "gj-fontselector-chip")
}

func TestFontSelectorCallbacks(t *testing.T) {
	var query, source string
	var picked fonts.Font
	n := FontSelector(FontSelectorProps{
		Fonts: testFonts, Open: true,
		OnQuery:  func(q string) { query = q },
		OnSource: func(s string) { source = s },
		OnSelect: func(f fonts.Font) { picked = f },
	})
	fire(t, n, "aria-label", "Search fonts", "input", "lor")
	fire(t, n, "data-source", "Web safe", "click", nil)
	fire(t, n, "data-family", "Lora", "click", nil)
	if query != "lor" || source != "Web safe" || picked.Family != "Lora" || picked.Source != "Google Fonts" {
		t.Fatalf("query=%q source=%q picked=%+v", query, source, picked)
	}
	fire(t, n, "data-source", "", "click", nil)
	if source != "" {
		t.Fatal("the All chip selects every source")
	}
}

func TestFontSelectorRecentAndLimit(t *testing.T) {
	many := make([]fonts.Font, 0, 30)
	for i := 0; i < 30; i++ {
		many = append(many, fonts.Font{Family: fmt.Sprintf("Font %02d", i), Source: "S"})
	}
	html := render(t, FontSelector(FontSelectorProps{Fonts: many, Open: true, Limit: 10, Recent: []string{"Font 07", "Missing", "Font 03"}}))
	mustContain(t, html, "gj-fontselector-group", ">Recent<", "20 more — keep typing to narrow the list")
	i7, i3, i0 := strings.Index(html, `data-family="Font 07"`), strings.Index(html, `data-family="Font 03"`), strings.Index(html, `data-family="Font 00"`)
	if i7 >= i3 || i3 >= i0 {
		t.Fatalf("recent order: %d %d %d", i7, i3, i0)
	}
	mustNotContain(t, render(t, FontSelector(FontSelectorProps{Fonts: many, Open: true, Query: "Font", Recent: []string{"Font 07"}})), ">Recent<")
	big := make([]fonts.Font, 150)
	for i := range big {
		big[i] = fonts.Font{Family: fmt.Sprintf("F%03d", i)}
	}
	mustContain(t, render(t, FontSelector(FontSelectorProps{Fonts: big, Open: true})), "50 more")
}

func edit(t *testing.T, props StyleEditorProps, attr, value, event string, payload any) canvas.Format {
	t.Helper()
	var got canvas.Format
	called := false
	props.OnChange = func(f canvas.Format) { got, called = f, true }
	fire(t, StyleEditor(props), attr, value, event, payload)
	if !called {
		t.Fatalf("%s=%q did not call OnChange", attr, value)
	}
	return got
}

func TestStyleEditorRendersTheFormatAndAPreview(t *testing.T) {
	html := render(t, StyleEditor(StyleEditorProps{
		Format:      canvas.Format{FontWeight: 700, FontStyle: canvas.StyleItalic, Underline: true, Color: "#aa0000", Background: "#ffff00", FontSize: 20},
		Base:        canvas.TextStyle{FontFamily: "Georgia", FontSize: 16, FontWeight: 400},
		PreviewText: "Sample",
	}))
	mustContain(t, html, "gj-styleeditor",
		`data-action="bold"`, `data-action="italic"`, `data-action="underline"`, `data-action="strike"`, `data-action="superscript"`, `data-action="subscript"`,
		`value="20"`, `value="#aa0000"`, `value="#ffff00"`, `>Sample<`,
		`data-preview="true"`, "font-family:Georgia;font-size:20px;font-weight:700;font-style:italic;color:#aa0000;background-color:#ffff00;text-decoration:underline;")
	for _, action := range []string{"bold", "italic", "underline"} {
		n, _ := byAttr(StyleEditor(StyleEditorProps{Format: canvas.Format{FontWeight: 700, FontStyle: canvas.StyleItalic, Underline: true}}), "data-action", action)
		pressed := false
		for _, a := range n.Attrs {
			pressed = pressed || (a.Name == "aria-pressed" && a.Value == "true")
		}
		if !pressed {
			t.Errorf("%s should be pressed", action)
		}
	}
	mustContain(t, render(t, StyleEditor(StyleEditorProps{})), "The quick brown fox jumps over the lazy dog", `value="16"`, `value="#000000"`, `value="#ffff00"`)
	mustContain(t, render(t, StyleEditor(StyleEditorProps{Disabled: true})), `disabled="disabled"`)
}

func TestStyleEditorToggles(t *testing.T) {
	base := canvas.TextStyle{FontSize: 16, FontWeight: 400}
	if f := edit(t, StyleEditorProps{Base: base}, "data-action", "bold", "click", nil); f.FontWeight != 700 {
		t.Fatalf("bold on: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: base, Format: canvas.Format{FontWeight: 700}}, "data-action", "bold", "click", nil); f.FontWeight != 400 {
		t.Fatalf("bold off: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: canvas.TextStyle{FontWeight: 700}}, "data-action", "bold", "click", nil); f.FontWeight != 400 {
		t.Fatalf("a bold base can be made regular: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: base}, "data-action", "italic", "click", nil); f.FontStyle != canvas.StyleItalic {
		t.Fatalf("italic on: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: base, Format: canvas.Format{FontStyle: canvas.StyleItalic}}, "data-action", "italic", "click", nil); f.FontStyle != canvas.StyleNormal {
		t.Fatalf("italic off: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: base}, "data-action", "underline", "click", nil); !f.Underline {
		t.Fatal("underline on")
	}
	if f := edit(t, StyleEditorProps{Base: base, Format: canvas.Format{Underline: true}}, "data-action", "underline", "click", nil); f.Underline {
		t.Fatal("underline off")
	}
	if f := edit(t, StyleEditorProps{Base: base}, "data-action", "strike", "click", nil); !f.Strike {
		t.Fatal("strike on")
	}
	if f := edit(t, StyleEditorProps{Base: base, Format: canvas.Format{Subscript: true}}, "data-action", "superscript", "click", nil); !f.Superscript || f.Subscript {
		t.Fatalf("super replaces sub: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: base, Format: canvas.Format{Superscript: true}}, "data-action", "subscript", "click", nil); !f.Subscript || f.Superscript {
		t.Fatalf("sub replaces super: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: base, Format: canvas.Format{Superscript: true}}, "data-action", "superscript", "click", nil); f.Superscript {
		t.Fatalf("super off: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: base, Format: canvas.Format{Color: "#123456"}}, "data-action", "bold", "click", nil); f.Color != "#123456" {
		t.Fatalf("bold dropped the color: %+v", f)
	}
}

func TestStyleEditorFields(t *testing.T) {
	base := canvas.TextStyle{FontSize: 16}
	if f := edit(t, StyleEditorProps{Base: base}, "data-field", "size", "input", " 24.5 "); f.FontSize != 24.5 {
		t.Fatalf("size: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: base, Format: canvas.Format{FontSize: 20}}, "data-field", "size", "input", ""); f.FontSize != 0 {
		t.Fatalf("an empty size inherits: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: base, Format: canvas.Format{FontSize: 20}}, "data-field", "size", "input", "abc"); f.FontSize != 20 {
		t.Fatalf("junk is ignored: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: base, Format: canvas.Format{Scale: 2}}, "data-field", "size", "input", "40"); f.FontSize != 20 {
		t.Fatalf("with Scale 2 the shown 40px means a 20px FontSize: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: base}, "data-field", "weight", "change", "600"); f.FontWeight != 600 {
		t.Fatalf("weight: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: base, Format: canvas.Format{FontWeight: 600}}, "data-field", "weight", "change", ""); f.FontWeight != 0 {
		t.Fatalf("weight inherit: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: base}, "data-field", "color", "input", "#ff0000"); f.Color != "#ff0000" {
		t.Fatalf("color: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: base}, "data-field", "background", "input", "#00ff00"); f.Background != "#00ff00" {
		t.Fatalf("background: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: base, Format: canvas.Format{Color: "#ff0000"}}, "data-clear", "color", "click", nil); f.Color != "" {
		t.Fatalf("clear color: %+v", f)
	}
	if f := edit(t, StyleEditorProps{Base: base, Format: canvas.Format{Background: "#00ff00"}}, "data-clear", "background", "click", nil); f.Background != "" {
		t.Fatalf("clear highlight: %+v", f)
	}
	html := render(t, StyleEditor(StyleEditorProps{Format: canvas.Format{FontWeight: 600}}))
	mustContain(t, html, `value="600" selected="selected"`)
}

func TestStyleEditorWithoutOnChangeIsInert(t *testing.T) {
	n := StyleEditor(StyleEditorProps{})
	for _, h := range findHandlers(n, "click") {
		h(xango.Event{})
	}
	for _, h := range findHandlers(n, "input") {
		h(xango.Event{Value: "5"})
	}
	for _, h := range findHandlers(n, "change") {
		h(xango.Event{Value: "5"})
	}
}

func TestStyleEditorFontPicker(t *testing.T) {
	var changed canvas.Format
	var pickedByUser string
	props := StyleEditorProps{
		Format:   canvas.Format{FontFamily: `"Open Sans", sans-serif`},
		OnChange: func(f canvas.Format) { changed = f },
		FontPicker: &FontSelectorProps{
			Fonts: testFonts, Open: true,
			OnSelect: func(f fonts.Font) { pickedByUser = f.Family },
		},
	}
	n := StyleEditor(props)
	html := render(t, n)
	mustContain(t, html, "gj-fontselector", `aria-selected="true" data-family="Open Sans"`)
	fire(t, n, "data-family", "Lora", "click", nil)
	if pickedByUser != "Lora" || changed.FontFamily != `"Lora", serif` {
		t.Fatalf("user=%q format=%+v: choosing a font calls both", pickedByUser, changed)
	}
	mustNotContain(t, render(t, StyleEditor(StyleEditorProps{})), "gj-fontselector")
	props.Disabled = true
	mustNotContain(t, render(t, StyleEditor(props)), "gj-fontselector-panel")
	props.Disabled = false
	props.FontPicker = &FontSelectorProps{Fonts: testFonts, Open: true}
	changed = canvas.Format{}
	fire(t, StyleEditor(props), "data-family", "Georgia", "click", nil)
	if changed.FontFamily != `"Georgia", serif` {
		t.Fatalf("format = %+v", changed)
	}
}

func TestPrimaryFamily(t *testing.T) {
	for in, want := range map[string]string{
		`"Open Sans", sans-serif`: "Open Sans", "Georgia, serif": "Georgia", `'Quo ted'`: "Quo ted",
		"": "", `"Odd\"Name\\"`: `Odd"Name\`, "sans-serif": "sans-serif",
	} {
		if got := primaryFamily(in); got != want {
			t.Errorf("primaryFamily(%q) = %q, want %q", in, got, want)
		}
	}
	if !isHexColor("#a1B2c3") || isHexColor("red") || isHexColor("#12345") || isHexColor("#12345g") {
		t.Fatal("isHexColor")
	}
	if roundTo(12.3456, 2) != 12.35 || maxFloat(1, 2) != 2 || maxFloat(3, 2) != 3 {
		t.Fatal("helpers")
	}
}
