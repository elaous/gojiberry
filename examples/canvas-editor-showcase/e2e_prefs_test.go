//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// Display preferences kept in the browser are applied at start: text size, spacing, contrast and motion
// change the page's computed styles, and none set leaves the defaults.
func TestDisplayPreferencesAreApplied(t *testing.T) {
	site := e2etest.NewSite(t)
	d := site.OpenURL(t, "/index.html")
	d.WaitSelector("canvas")
	size := func() float64 {
		var f float64
		d.Eval(`parseFloat(getComputedStyle(document.documentElement).fontSize)`, &f)
		return f
	}
	base := size()
	d.OK(!d.Bool(`document.documentElement.hasAttribute('data-gj-text')`), "no preference: no attribute")

	d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify({text: 'large', spacing: 'wide', contrast: 'more', motion: 'reduce'}))`, nil)
	d.Reload()
	d.WaitSelector("canvas")
	d.OK(size() == base*1.25, "text size is 125%")
	d.OK(d.Str(`getComputedStyle(document.body).letterSpacing`) != "normal", "wide spacing sets the letter spacing")
	d.OK(d.Str(`getComputedStyle(document.documentElement).getPropertyValue('--gj-text').trim()`) == "#000000", "more contrast darkens the text")
	d.OK(d.Str(`getComputedStyle(document.documentElement).getPropertyValue('--gj-border').trim()`) == "#4b5563", "and the borders")

	d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify({text: 'bogus', contrast: 'x'}))`, nil)
	d.Reload()
	d.WaitSelector("canvas")
	d.OK(size() == base && !d.Bool(`document.documentElement.hasAttribute('data-gj-text') || document.documentElement.hasAttribute('data-gj-contrast')`), "unknown values are ignored")
	d.NoErrors()
}

// The View menu's display preferences change the page at once, are kept for the next visit, and
// reset to the defaults.
func TestDisplayPreferencesMenu(t *testing.T) {
	site := e2etest.NewSite(t)
	d := site.OpenURL(t, "/index.html")
	d.WaitSelector("canvas")
	choose := func(item string) {
		d.Click(`button.gj-menubar-button:has-text("View")`)
		d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Display preferences")`)
		d.Click(`.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("` + item + `")`)
	}
	root := func(attr string) string {
		return d.Str(`document.documentElement.getAttribute('` + attr + `') || ''`)
	}

	choose("Text size: 150%")
	d.WaitFor("the size", `document.documentElement.getAttribute('data-gj-text') === 'larger'`)
	choose("More contrast")
	d.WaitFor("the contrast", `document.documentElement.getAttribute('data-gj-contrast') === 'more'`)
	choose("Reduce motion")
	choose("Wide text spacing")
	d.WaitFor("the spacing", `document.documentElement.getAttribute('data-gj-spacing') === 'wide'`)
	d.OK(root("data-gj-motion") == "reduce", "motion is reduced")

	d.Reload()
	d.WaitSelector("canvas")
	d.OK(root("data-gj-text") == "larger" && root("data-gj-contrast") == "more", "the choices are kept for the next visit")

	d.Click(`button.gj-menubar-button:has-text("View")`)
	d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Display preferences")`)
	d.OK(d.Has(`.gj-menubar-panel li.gj-menu-item[aria-checked="true"]:has-text("More contrast")`) && d.Has(`.gj-menubar-panel li.gj-menu-item[aria-checked="true"]:has-text("Text size: 150%")`), "the menu shows what is on")
	d.Click(`.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("Text size: default")`)
	choose("Reset to defaults")
	d.WaitFor("reset", `!document.documentElement.hasAttribute('data-gj-text') && !document.documentElement.hasAttribute('data-gj-contrast') && !document.documentElement.hasAttribute('data-gj-motion')`)
	d.NoErrors()
}

// Large targets make what can be pressed at least 44 pixels each way, and the distraction-free mode hides
// the toolbar, the side panel and the status bar; the menus stay, and turning either off brings it back.
func TestLargeTargetsAndDistractionFreeMode(t *testing.T) {
	site := e2etest.NewSite(t)
	d := site.OpenURL(t, "/index.html")
	d.WaitSelector(".gj-docview-sidebar button")
	small := func() float64 {
		var f float64
		d.Eval(`Math.min(...Array.from(document.querySelectorAll('.gj-docview-sidebar button')).map((b) => b.getBoundingClientRect().height))`, &f)
		return f
	}
	before := small()
	d.OK(before > 0 && before < 44, "buttons are small by default")
	d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify({targets: 'large'}))`, nil)
	d.Reload()
	d.WaitSelector(".gj-docview-sidebar button")
	d.OK(small() >= 44, "large targets: the smallest side-panel button is now "+d.Str(`String(Math.min(...Array.from(document.querySelectorAll('.gj-docview-sidebar button')).map((b) => b.getBoundingClientRect().height)))`))

	d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify({focus: 'on'}))`, nil)
	d.Reload()
	d.WaitSelector(".gj-docview-sidebar button")
	for _, sel := range []string{".gj-docview-toolbar", ".gj-docview-sidebar", ".gj-docview-status"} {
		d.OK(d.Str(`getComputedStyle(document.querySelector('`+sel+`')).display`) == "none", sel+" is hidden")
	}
	d.OK(d.Has(`button.gj-menubar-button:has-text("View")`), "the menus stay")
	// and the menu brings it back
	d.Click(`button.gj-menubar-button:has-text("View")`)
	d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Display preferences")`)
	d.OK(d.Has(`.gj-menubar-panel li.gj-menu-item[aria-checked="true"]:has-text("Distraction-free mode")`), "the menu shows it is on")
	d.Click(`.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("Distraction-free mode")`)
	d.WaitFor("back", `!document.documentElement.hasAttribute('data-gj-focus') && getComputedStyle(document.querySelector('.gj-docview-sidebar')).display !== 'none'`)
	d.NoErrors()
}
