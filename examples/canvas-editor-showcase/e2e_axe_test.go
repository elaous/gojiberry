//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// axeDisabled lists the rules the showcase does not enforce yet, each with why. A rule is added here
// only with a roadmap entry that tracks fixing it.
var axeDisabled = []string{
	// The panels' titles are h4 (the components' own level) under a page whose headings are in the
	// screen-reader copy of the document; the levels skip. Roadmap item 48, "heading levels".
	"heading-order",
}

// TestNoAccessibilityViolationsAxe runs axe-core over the page in the states a person meets: the
// document, every tab of the side panel, every menu open, and the find bar. A new violation fails
// the build. axe finds what a tool can (roles, names, labels, contrast, landmarks, ids); it does not
// replace using the page with a keyboard and a screen reader.
func TestNoAccessibilityViolationsAxe(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	check := func(state string) {
		t.Helper()
		for _, v := range d.Axe("", axeDisabled...) {
			t.Errorf("%s: %s", state, v)
		}
	}
	check("the document")
	for _, tab := range []string{"find", "save", "people", "language"} {
		if d.Has(`button[data-tab="` + tab + `"]`) {
			d.Click(`button[data-tab="` + tab + `"]`)
			check("the " + tab + " tab")
		}
	}
	names := d.Strs(`Array.from(document.querySelectorAll('button.gj-menubar-button')).map(b => b.textContent.trim())`)
	for _, name := range names {
		if name == "" || strings.ContainsAny(name, `"\`) {
			continue
		}
		d.Click(`button.gj-menubar-button:has-text("` + name + `")`)
		d.WaitFor("the "+name+" menu to open", `!!document.querySelector('.gj-menubar-panel, [role="menu"]')`)
		check("the " + name + " menu")
		d.Press("Escape")
		d.OK(!d.Has(".gj-menubar-panel"), "Escape closes the "+name+" menu")
	}
	d.Focus("#input")
	d.Press("Control+f")
	d.WaitSelector(".gj-find-input")
	check("the find bar")
}

// The menus work from the keyboard: Enter on a focused entry chooses it, as a click does, and
// closes the menu.
func TestMenuEntryChosenWithTheKeyboard(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button.gj-menubar-button:has-text("File")`)
	d.WaitSelector(".gj-menubar-panel")
	d.Focus(`.gj-menubar-panel li.gj-menu-item:has(.gj-menu-label:text-is("Page setup…"))`)
	d.Press("Enter")
	d.WaitSelector(".gj-pagesetup-title")
	d.OK(!d.Has(".gj-menubar-panel"), "choosing closes the menu")
}
