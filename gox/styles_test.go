package gox

import (
	"strings"
	"testing"
)

func resetStyles() {
	stylesMu.Lock()
	styles, styleIDs = nil, map[string]bool{}
	stylesMu.Unlock()
}

func TestRegisterStyle(t *testing.T) {
	resetStyles()
	defer resetStyles()
	if got, _ := RenderString(Styles()); got != "" {
		t.Fatalf("no styles registered: %q", got)
	}
	RegisterStyle("a/one.css", "a{color:red}")
	RegisterStyle("a/two.css", "b{color:blue}")
	RegisterStyle("a/one.css", "ignored") // same ID: no-op
	got := RegisteredStyles()
	if len(got) != 2 || got[0].ID != "a/one.css" || got[0].CSS != "a{color:red}" || got[1].ID != "a/two.css" {
		t.Fatalf("styles = %+v", got)
	}
	html, err := RenderString(Styles())
	if err != nil {
		t.Fatal(err)
	}
	want := `<style data-gox-style="a/one.css">a{color:red}</style><style data-gox-style="a/two.css">b{color:blue}</style>`
	if html != want {
		t.Errorf("Styles() = %q", html)
	}
}

func TestDocumentIncludesRegisteredStyles(t *testing.T) {
	resetStyles()
	defer resetStyles()
	RegisterStyle("a/one.css", "a{}")
	doc := Document{Title: "t", Head: []Node{Style("b{}")}, Body: Text("x")}
	html, err := RenderString(doc.Render())
	if err != nil {
		t.Fatal(err)
	}
	i, j := strings.Index(html, "a{}"), strings.Index(html, "b{}")
	if i < 0 || j < 0 || i > j {
		t.Errorf("registered styles come first, then Head: %s", html)
	}
	doc.NoStyles = true
	if html, _ = RenderString(doc.Render()); strings.Contains(html, "a{}") {
		t.Errorf("NoStyles: %s", html)
	}
}

func TestDocumentStylesPerPage(t *testing.T) {
	resetStyles()
	defer resetStyles()
	RegisterStyle("m/components/base.css", "BASE{}")
	RegisterStyle("m/components/button.css", "BUTTON{}")
	RegisterStyle("m/other/chip.css", "CHIP{}")
	render := func(only []string) string {
		html, err := RenderString(Document{Styles: only, Body: Text("x")}.Render())
		if err != nil {
			t.Fatal(err)
		}
		return html
	}
	if h := render(nil); !strings.Contains(h, "BASE{}") || !strings.Contains(h, "CHIP{}") {
		t.Errorf("nil means all: %s", h)
	}
	if h := render([]string{}); strings.Contains(h, "{}") {
		t.Errorf("an empty list means none: %s", h)
	}
	h := render([]string{"components/base.css", "chip.css"})
	if !strings.Contains(h, "BASE{}") || !strings.Contains(h, "CHIP{}") || strings.Contains(h, "BUTTON{}") {
		t.Errorf("by the end of the ID: %s", h)
	}
	if h := render([]string{"m/components/button.css"}); !strings.Contains(h, "BUTTON{}") || strings.Contains(h, "BASE{}") {
		t.Errorf("by full ID: %s", h)
	}
	if h := render([]string{"tton.css"}); strings.Contains(h, "BUTTON{}") {
		t.Error("only whole path segments match")
	}
}
