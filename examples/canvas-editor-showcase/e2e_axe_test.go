//go:build !(js && wasm)

package main

import (
	"net/url"
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

// The arrow keys move through the menus: along the bar (taking an open menu along), into a menu and
// among its entries, into a submenu and back out, and Escape puts the focus back on the menu's button.
func TestMenusAreOperatedWithTheArrowKeys(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	active := func() string {
		return d.Str(`(document.activeElement.textContent || '').trim().split('\n')[0].replace(/\s+/g, ' ').slice(0, 40)`)
	}
	// Down on a closed menu button opens the menu and lands on its first entry
	d.Focus(`button.gj-menubar-button:has-text("File")`)
	d.Press("ArrowDown")
	d.WaitSelector(".gj-menubar-panel")
	d.WaitFor("the first entry", `document.activeElement.classList.contains('gj-menu-item')`)
	first := active()
	d.Press("ArrowDown")
	d.OK(active() != first && d.Bool(`document.activeElement.classList.contains('gj-menu-item')`), "Down moves to the next entry: "+active())
	d.Press("ArrowUp")
	d.OK(active() == first, "Up moves back: "+active())
	d.Press("ArrowUp")
	last := active()
	d.OK(last != first, "Up from the first wraps to the last: "+last)
	d.Press("Home")
	d.OK(active() == first, "Home: "+active())
	d.Press("End")
	d.OK(active() == last, "End: "+active())

	// Escape closes the menu and puts the focus back on its button
	d.Press("Escape")
	d.OK(!d.Has(".gj-menubar-panel") && active() == "File", "Escape returns to the File button: "+active())

	// Right and Left move along the bar; with a menu open they take it along
	d.Press("ArrowDown")
	d.WaitSelector(".gj-menubar-panel")
	d.Focus(`button.gj-menubar-button:has-text("File")`)
	d.Press("ArrowRight")
	d.WaitFor("Edit open", `document.querySelector('.gj-menubar-entry-open > .gj-menubar-button').textContent.trim() === 'Edit'`)
	d.OK(active() == "Edit", "Right moves to the next button: "+active())
	d.Press("ArrowLeft")
	d.WaitFor("File open", `document.querySelector('.gj-menubar-entry-open > .gj-menubar-button').textContent.trim() === 'File'`)
	d.Press("Escape")

	// a submenu: Right goes in, Left comes back out
	d.Click(`button.gj-menubar-button:has-text("View")`)
	d.WaitSelector(".gj-menubar-panel")
	d.Focus(`.gj-menubar-panel li.gj-menu-item-sub:has(.gj-menu-label:text-is("Interface language"))`)
	d.Press("ArrowRight")
	d.OK(d.Bool(`!!document.activeElement.closest('.gj-menu-item-sub > .gj-menu')`), "Right goes into the submenu: "+active())
	d.Press("ArrowLeft")
	d.OK(strings.HasPrefix(active(), "Interface language"), "Left comes back to the entry: "+active())
}

// The side panel's tabs are a row for the keyboard: Tab enters it once (on the selected tab), the arrow
// keys, Home and End move along it and open the tab they reach.
func TestSidePanelTabsAreOperatedWithTheArrowKeys(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	tabOf := func() string { return d.Str(`document.activeElement.getAttribute('data-tab') || ''`) }
	selected := func() string {
		return d.Str(`Array.from(document.querySelectorAll('[role="tab"][aria-selected="true"]')).map(b => b.getAttribute('data-tab')).join(',')`)
	}
	d.OK(d.Int(`document.querySelectorAll('[role="tablist"] [role="tab"][tabindex="0"]').length`) == 1, "only one tab is in the Tab order")
	d.OK(selected() == "format", "Format is the tab shown first: "+selected())
	d.Focus(`button[data-tab="format"]`)
	d.Press("ArrowRight")
	d.OK(tabOf() == "styles" && selected() == "styles", "Right opens the next tab: "+tabOf()+" "+selected())
	d.Press("End")
	d.OK(tabOf() == "transfer" && selected() == "transfer", "End: "+tabOf())
	d.Press("ArrowRight")
	d.OK(tabOf() == "format" && selected() == "format", "Right from the last wraps to the first: "+tabOf())
	d.Press("ArrowLeft")
	d.OK(tabOf() == "transfer", "Left from the first wraps to the last: "+tabOf())
	d.Press("Home")
	d.OK(tabOf() == "format" && selected() == "format", "Home: "+tabOf())
	d.OK(d.Int(`document.querySelectorAll('[role="tablist"] [role="tab"][tabindex="0"]').length`) == 1, "still only one in the Tab order")
}

// A dialog works from the keyboard: it takes the focus, Tab and Shift+Tab stay inside it, Escape closes it
// and the focus goes back to what opened it.
func TestDialogFocusIsTrappedAndReturned(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.OpenURL(t, "/index.html?docstore="+url.QueryEscape(site.URL))
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-offer-review")
	inDialog := func() bool {
		return d.Bool(`!!document.activeElement.closest('[role="alertdialog"]')`)
	}
	d.Focus("#dir-offer-review")
	d.Press("Enter")
	d.WaitSelector(`[role="alertdialog"]`)
	d.WaitFor("the focus in the dialog", `!!document.activeElement.closest('[role="alertdialog"]')`)
	for i := 0; i < 6; i++ { // more presses than the dialog has buttons: it wraps
		d.Press("Tab")
		d.OK(inDialog(), "Tab stays inside the dialog")
	}
	for i := 0; i < 4; i++ {
		d.Press("Shift+Tab")
		d.OK(inDialog(), "Shift+Tab stays inside the dialog")
	}
	d.Press("Escape")
	d.OK(!d.Has(`[role="alertdialog"]`), "Escape closes the dialog")
	d.WaitFor("the focus back", `document.activeElement && document.activeElement.id === 'dir-offer-review'`)
}
