package cssgen

import (
	"image/color"
	"strings"
	"testing"
)

// realCSS is copied verbatim from OnDesign's own editor/index.html (as
// of the commit this test was written against) -- the :root/body.dark
// token block in full, plus a representative slice of real class
// rules covering every case this package's own doc comment claims to
// handle: plain padding/background/border/border-radius, a var(--x)
// background, an rgba() background, a literal-hex border, "none"
// background/border, an unsupported property mixed into an otherwise
// recognized rule (proving unrelated properties don't interfere with
// each other), a :hover/.active/:disabled variant of a class that
// should be skipped-and-warned rather than misapplied to the base
// rule, and a class whose only declaration is entirely unsupported
// (proving it's correctly left out of the result rather than added as
// an empty entry). Deliberately not synthetic/simplified CSS -- the
// whole point of testing against this is proving the parser handles
// real syntax, not an idealized version of it.
const realCSS = `
    :root {
      --bg: #fff;
      --text: #111827;
      --muted: #6b7280;
      --canvas-wrap-bg: rgb(229, 231, 235);
      --panel-bg: #fff;
      --panel-border: #d1d5db;
      --panel-divider: #e5e7eb;
      --hover-bg: #f3f4f6;
      --active-bg: #e0e7ff;
    }
    body.dark {
      --bg: #1a1d24;
      --text: #e5e7eb;
      --muted: #9ca3af;
      --canvas-wrap-bg: #0f1115;
      --panel-bg: #262b36;
      --panel-border: #3f4654;
      --panel-divider: #3f4654;
      --hover-bg: #333a48;
      --active-bg: #3730a3;
    }
    .font-picker { position: absolute; top: 100%; left: 0; z-index: 30; background: var(--panel-bg); border: 1px solid var(--panel-border); border-radius: 4px; box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15); width: 260px; padding: 0.5em; }
    .font-picker-item { padding: 0.35em 0.5em; cursor: pointer; border-radius: 3px; font-size: 1.05em; }
    .font-picker-item:hover { background: var(--hover-bg); }
    .find-toggle { padding: 0.25em 0.45em; font-size: 0.8em; font-weight: bold; border: 1px solid var(--panel-border); border-radius: 3px; background: var(--bg); color: var(--text); cursor: pointer; }
    .find-toggle.active { background: var(--active-bg); }
    .find-toggle:disabled { opacity: 0.4; cursor: not-allowed; }
    .style-editor { position: fixed; inset: 0; z-index: 20; background: rgba(0, 0, 0, 0.35); display: flex; justify-content: center; align-items: flex-start; padding-top: 3em; }
    .style-editor-inner { position: relative; background: var(--panel-bg); color: var(--text); border-radius: 6px; box-shadow: 0 8px 24px rgba(0, 0, 0, 0.25); width: 480px; max-width: 90vw; max-height: 80vh; display: flex; overflow: hidden; }
    .outline-level-1 { font-weight: bold; }
    .style-editor-closebtn { position: absolute; top: 0.4em; right: 0.5em; border: none; background: none; font-size: 1.3em; line-height: 1; cursor: pointer; color: var(--muted); padding: 0.2em 0.4em; }
    .style-editor-closebtn:hover { color: var(--text); }
`

func TestParseTokensMatchesRealLightAndDarkValues(t *testing.T) {
	light, dark := ParseTokens(realCSS)

	wantLight := Tokens{
		"bg": "#fff", "text": "#111827", "muted": "#6b7280",
		"canvas-wrap-bg": "rgb(229, 231, 235)", "panel-bg": "#fff",
		"panel-border": "#d1d5db", "panel-divider": "#e5e7eb",
		"hover-bg": "#f3f4f6", "active-bg": "#e0e7ff",
	}
	for k, v := range wantLight {
		if light[k] != v {
			t.Errorf("light[%q] = %q, want %q", k, light[k], v)
		}
	}
	if len(light) != len(wantLight) {
		t.Errorf("light has %d tokens, want %d: %+v", len(light), len(wantLight), light)
	}

	wantDark := Tokens{
		"bg": "#1a1d24", "text": "#e5e7eb", "muted": "#9ca3af",
		"canvas-wrap-bg": "#0f1115", "panel-bg": "#262b36",
		"panel-border": "#3f4654", "panel-divider": "#3f4654",
		"hover-bg": "#333a48", "active-bg": "#3730a3",
	}
	for k, v := range wantDark {
		if dark[k] != v {
			t.Errorf("dark[%q] = %q, want %q", k, dark[k], v)
		}
	}
}

func TestGenerateAgainstRealClassRules(t *testing.T) {
	light, _ := ParseTokens(realCSS)
	classes := []string{
		"font-picker", "font-picker-item", "find-toggle",
		"style-editor", "style-editor-inner", "outline-level-1",
		"style-editor-closebtn", "no-such-class",
	}
	styles, warnings := Generate(realCSS, classes, light)

	t.Run("font-picker: padding/background/border/border-radius all recognized", func(t *testing.T) {
		s, ok := styles["font-picker"]
		if !ok {
			t.Fatal("no style generated for .font-picker")
		}
		if s.PaddingY != 8 || s.PaddingX != 8 {
			t.Errorf("PaddingY/X = %v/%v, want 8/8 (0.5em @ 16px/em)", s.PaddingY, s.PaddingX)
		}
		if s.Background != (color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) {
			t.Errorf("Background = %+v, want white (var(--panel-bg) resolved)", s.Background)
		}
		if s.BorderWidth != 1 {
			t.Errorf("BorderWidth = %v, want 1", s.BorderWidth)
		}
		if s.BorderColor != (color.NRGBA{R: 0xd1, G: 0xd5, B: 0xdb, A: 0xff}) {
			t.Errorf("BorderColor = %+v, want #d1d5db (var(--panel-border) resolved)", s.BorderColor)
		}
		if s.CornerRadius != 4 {
			t.Errorf("CornerRadius = %v, want 4", s.CornerRadius)
		}
	})

	t.Run("font-picker-item: 2-value padding exact, hover rule skipped not merged", func(t *testing.T) {
		s, ok := styles["font-picker-item"]
		if !ok {
			t.Fatal("no style generated for .font-picker-item")
		}
		if s.PaddingY != 5.6 || s.PaddingX != 8 {
			t.Errorf("PaddingY/X = %v/%v, want 5.6/8 (0.35em/0.5em @ 16px/em)", s.PaddingY, s.PaddingX)
		}
		if s.CornerRadius != 3 {
			t.Errorf("CornerRadius = %v, want 3", s.CornerRadius)
		}
		// The :hover variant's own "background: var(--hover-bg)" must
		// NOT leak into the base style -- that would make the base
		// rule look permanently hovered.
		if s.Background != (color.NRGBA{}) {
			t.Errorf("Background = %+v, want zero value: the :hover rule's background must not merge into the base style", s.Background)
		}
	})

	t.Run("find-toggle: background var(--bg), .active/:disabled variants skipped", func(t *testing.T) {
		s, ok := styles["find-toggle"]
		if !ok {
			t.Fatal("no style generated for .find-toggle")
		}
		if s.Background != (color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) {
			t.Errorf("Background = %+v, want white (var(--bg) resolved)", s.Background)
		}
		if s.BorderWidth != 1 || s.CornerRadius != 3 {
			t.Errorf("BorderWidth/CornerRadius = %v/%v, want 1/3", s.BorderWidth, s.CornerRadius)
		}
	})

	t.Run("style-editor: rgba() background parsed with real alpha", func(t *testing.T) {
		s, ok := styles["style-editor"]
		if !ok {
			t.Fatal("no style generated for .style-editor")
		}
		// 0.35*255 = 89.25, truncated to 89 by parseColorValue's own
		// int(af*255) conversion -- matched here, not recomputed as a
		// constant expression, to avoid a compile-time non-integer
		// constant conversion error.
		want := color.NRGBA{R: 0, G: 0, B: 0, A: 89}
		if s.Background != want {
			t.Errorf("Background = %+v, want %+v (rgba(0, 0, 0, 0.35))", s.Background, want)
		}
	})

	t.Run("style-editor-closebtn: border:none/background:none recognized, real padding still applied", func(t *testing.T) {
		s, ok := styles["style-editor-closebtn"]
		if !ok {
			t.Fatal("no style generated for .style-editor-closebtn")
		}
		if s.BorderWidth != 0 || s.BorderColor != (color.NRGBA{}) {
			t.Errorf("border = %v/%+v, want zero (border: none)", s.BorderWidth, s.BorderColor)
		}
		if s.Background != (color.NRGBA{}) {
			t.Errorf("Background = %+v, want zero (background: none)", s.Background)
		}
		if s.PaddingY != 3.2 || s.PaddingX != 6.4 {
			t.Errorf("PaddingY/X = %v/%v, want 3.2/6.4 (0.2em/0.4em @ 16px/em)", s.PaddingY, s.PaddingX)
		}
	})

	t.Run("outline-level-1: entirely-unsupported rule produces no entry", func(t *testing.T) {
		if s, ok := styles["outline-level-1"]; ok {
			t.Errorf(".outline-level-1 got a style entry %+v, want none: its only declaration (font-weight) has no ClassStyle field", s)
		}
	})

	t.Run("no-such-class: absent from CSS entirely produces no entry and no warning", func(t *testing.T) {
		if _, ok := styles["no-such-class"]; ok {
			t.Error(".no-such-class got a style entry, want none: it has no rule in realCSS at all")
		}
	})

	// Every real warning-worthy shape actually got flagged: the three
	// pseudo/compound selectors that must not be silently merged
	// (.font-picker-item:hover, .find-toggle.active,
	// .find-toggle:disabled, .style-editor-closebtn:hover -- four, not
	// three), and at least one genuinely unsupported property
	// (font-size, appearing on several of these rules).
	wantSelectorWarnings := map[string]bool{
		".font-picker-item:hover":      false,
		".find-toggle.active":          false,
		".find-toggle:disabled":        false,
		".style-editor-closebtn:hover": false,
	}
	sawUnsupportedProperty := false
	for _, w := range warnings {
		for sel := range wantSelectorWarnings {
			if w.Property == "" && strings.Contains(w.Reason, sel) {
				wantSelectorWarnings[sel] = true
			}
		}
		if w.Property == "font-size" {
			sawUnsupportedProperty = true
		}
	}
	for sel, saw := range wantSelectorWarnings {
		if !saw {
			t.Errorf("no selector warning seen for %q among %d warnings", sel, len(warnings))
		}
	}
	if !sawUnsupportedProperty {
		t.Error("no warning seen for the unsupported font-size property, which appears on several matched rules")
	}
	if len(warnings) == 0 {
		t.Fatal("no warnings at all: realCSS deliberately includes several unsupported shapes")
	}
}

func TestParseLengthAndParseColorValueEdgeCases(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"0", 0, true},
		{"4px", 4, true},
		{"0.5em", 8, true},
		{"1em", 16, true},
		{"3", 0, false}, // unitless nonzero: real CSS doesn't allow this for a length
		{"50%", 0, false},
		{"1vw", 0, false},
	}
	for _, c := range cases {
		got, ok := parseLength(c.in)
		if ok != c.ok {
			t.Errorf("parseLength(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && float64(got) != c.want {
			t.Errorf("parseLength(%q) = %v, want %v", c.in, got, c.want)
		}
	}

	tokens := Tokens{"panel-bg": "#abcdef"}
	colorCases := []struct {
		in   string
		want color.NRGBA
		ok   bool
	}{
		{"#abcdef", color.NRGBA{R: 0xab, G: 0xcd, B: 0xef, A: 0xff}, true},
		{"#fff", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, true},
		{"rgb(1, 2, 3)", color.NRGBA{R: 1, G: 2, B: 3, A: 0xff}, true},
		{"var(--panel-bg)", color.NRGBA{R: 0xab, G: 0xcd, B: 0xef, A: 0xff}, true},
		{"var(--missing)", color.NRGBA{}, false},
		{"red", color.NRGBA{}, false},
		{"linear-gradient(red, blue)", color.NRGBA{}, false},
	}
	for _, c := range colorCases {
		got, ok := parseColorValue(c.in, tokens)
		if ok != c.ok {
			t.Errorf("parseColorValue(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("parseColorValue(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestAtRulesDoNotSwallowOrLeakRules(t *testing.T) {
	css := "@charset \"utf-8\";\n@import \"x.css\";\n.a{background:#fff}\n" +
		"@media (prefers-color-scheme: dark) { .a { background: #000 } .b { background: #111 } }\n" +
		".c{background:#eee}"
	styles, _ := Generate(css, []string{"a", "b", "c"}, nil)
	if got := styles["a"].Background; got != (color.NRGBA{255, 255, 255, 255}) {
		t.Errorf(".a = %+v: the @media override must not apply (and the @import must not eat the rule)", got)
	}
	if _, ok := styles["b"]; ok {
		t.Error(".b exists only inside @media and must not be generated")
	}
	if _, ok := styles["c"]; !ok {
		t.Error(".c after an at-rule block was lost")
	}
	light, _ := ParseTokens("@charset \"utf-8\";\n:root{--a:#fff}")
	if light["a"] != "#fff" {
		t.Errorf("tokens after an at-rule: %v", light)
	}
}

func TestLaterDeclarationWins(t *testing.T) {
	for i := 0; i < 50; i++ { // used to depend on map iteration order
		styles, _ := Generate(".a{border:1px solid #f00; border-color:#00f}", []string{"a"}, nil)
		if got := styles["a"].BorderColor; got != (color.NRGBA{0, 0, 255, 255}) {
			t.Fatalf("run %d: border colour %+v, want the later border-color (blue)", i, got)
		}
	}
}

func TestSelfReferencingTokenTerminates(t *testing.T) {
	for _, tokens := range []Tokens{{"x": "var(--x)"}, {"a": "var(--b)", "b": "var(--a)"}} {
		if _, ok := parseColorValue("var(--x)", tokens); ok {
			t.Errorf("%v resolved to a colour", tokens)
		}
		if _, ok := parseColorValue("var(--a)", tokens); ok {
			t.Errorf("%v resolved to a colour", tokens)
		}
	}
}

func TestRGBIsClamped(t *testing.T) {
	got, ok := parseColorValue("rgba(300, 0, 0, 1.5)", nil)
	if !ok || got != (color.NRGBA{255, 0, 0, 255}) {
		t.Fatalf("got %+v ok=%v, want {255 0 0 255}", got, ok)
	}
}
