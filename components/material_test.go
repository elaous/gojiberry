package components

import (
	"context"
	"strings"
	"testing"
)

func TestMaterialDrawsAMaskedSpanAndKnowsItsNames(t *testing.T) {
	html := render(t, Material("format_bold"))
	for _, want := range []string{"<span", `class="gj-material"`, `data-material="format_bold"`, `aria-hidden="true"`, "mask-image:url(data:image/svg+xml,", "viewBox=%270%20-960%20960%20960%27", "%3Cpath%20d=%27M"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in %s", want, html)
		}
	}
	if strings.Contains(html, "<svg") {
		t.Error("an <svg> element would not draw when built client-side")
	}
	if got := render(t, Material("no-such-icon")); strings.Contains(got, "gj-material") {
		t.Errorf("an unknown name draws nothing: %s", got)
	}
	if !HasMaterial("link") || HasMaterial("nope") || len(MaterialNames()) < 80 {
		t.Error("names")
	}
	for cmd, name := range commandMaterial {
		if !HasMaterial(name) {
			t.Errorf("command %q: no symbol %q", cmd, name)
		}
	}
	for class, name := range docIcons {
		if !HasMaterial(name) {
			t.Errorf("%s: no symbol %q", class, name)
		}
	}
	for _, name := range MaterialNames() {
		if p := materialPaths[name]; !strings.HasPrefix(p, "M") && !strings.HasPrefix(p, "m") || len(p) < 20 || strings.ContainsAny(p, "'\"<>#%()") {
			t.Errorf("%s: odd path %q", name, p)
		}
	}
}

func TestMaterialsAppearInMenusAndButtons(t *testing.T) {
	menu := render(t, Menu(MenuProps{Items: []MenuItem{{Label: "Bold", Icon: "format_bold"}, {Label: "Plain"}, {Label: "Odd", Icon: "nope"}}}))
	if strings.Count(menu, "gj-menu-icon") != 1 || !strings.Contains(menu, `data-material="format_bold"`) {
		t.Errorf("only an item with a known symbol gets one: %s", menu)
	}
	list := render(t, DocList(DocListProps{Docs: []Doc{{ID: "x", Name: "a"}}, Pending: &Pending{Inline: true},
		Open: func(_ context.Context, _ string) error { return nil }, Link: func(string) string { return "u" }, CopyLink: func(string) {}}))
	for _, name := range []string{"description", "link"} {
		if !strings.Contains(list, `data-material="`+name+`"`) {
			t.Errorf("no %s symbol in the document row", name)
		}
	}
	if got := CommandMaterial("bold"); got != "format_bold" || CommandMaterial("zzz") != "" {
		t.Error(got)
	}
}
