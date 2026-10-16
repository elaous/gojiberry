//go:build !(js && wasm)

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestFeatureShots makes the pictures of docs/features.md: one screenshot of the editor per feature, each in the state
// the feature is shown in. It does nothing unless GOJIBERRY_FEATURE_SHOTS names the directory to write them to
// (`make feature-shots` sets it to docs/features/img); run it again when the editor looks different.
func TestFeatureShots(t *testing.T) {
	dir := os.Getenv("GOJIBERRY_FEATURE_SHOTS")
	if dir == "" {
		t.Skip("set GOJIBERRY_FEATURE_SHOTS to a directory to make the feature pictures")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Eval(`document.getElementById('state').style.display = 'none'`, nil) // the test page's JSON mirror is not part of the editor
	shot := func(name string) { d.ScreenshotView(filepath.Join(dir, name+".webp")) }
	of := func(selector, name string) { d.ScreenshotOf(selector, filepath.Join(dir, name+".webp")) }
	tab := func(id string) {
		d.Click(`button[data-tab="` + id + `"]`)
		d.WaitSelector(`button[data-tab="` + id + `"][aria-selected="true"], button[data-tab="` + id + `"]`)
	}

	// the editor itself, in view mode and then editing
	of(".gj-docview-desk", "editor-view")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	of(".gj-docview-desk", "editor-edit")

	// the side panel, one tab at a time
	for _, id := range []string{"format", "styles", "symbols", "keys", "comments", "review", "outline", "images", "find", "spell",
		"language", "stats", "share", "people", "migrate", "console", "public", "page", "save", "transfer", "speech", "privacy", "prefs"} {
		tab(id)
		of(".gj-docview-sidebar", "tab-"+id)
	}

	// states
	tab("check")
	runCheck(d)
	of(".gj-docview-sidebar", "tab-check")

	tab("format")
	d.CanvasClick(240, 164)
	d.Focus("#input")
	addFootnote(d, "A note at the foot of the page, with its number.")
	d.CanvasMove(450, 300)
	d.Wheel(0, 460) // down to the foot of the first page, where its notes are
	d.Sleep(300)
	of(".gj-docview-desk", "footnotes")
	d.Wheel(0, -5000)
	d.Sleep(200)

	run := func(label string) {
		d.CanvasClick(240, 164)
		d.Focus("#input")
		d.Click(`button.gj-menubar-button:has-text("Edit")`)
		d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Columns")`)
		shot("menu-columns")
		d.Click(`.gj-menubar-panel .gj-menu-label:text-is("` + label + `")`)
	}
	run("Two columns")
	d.Wait("s.columns === 2")
	of(".gj-docview-desk", "columns")

	d.Click(`button.gj-menubar-button:has-text("View")`)
	d.Click(`.gj-menubar-panel .gj-menu-label:text-is("Two-page spread")`)
	d.Wait("s.spread === true")
	of(".gj-docview-desk", "spread")

	// formulas typeset in the text: one on a line of its own, one in a sentence
	d.Click(`button.gj-menubar-button:has-text("View")`)
	d.Click(`.gj-menubar-panel .gj-menu-label:text-is("Two-page spread")`) // back to one column of pages
	d.Wait("s.spread === false")
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Control+a")
	d.Press("Delete")
	d.Type("The roots of a quadratic are given by ")
	tab("images")
	d.WaitSelector("#eq-insert")
	d.SetChecked("#eq-inline input", true)
	d.Fill("#eq-formula", "x = frac{-b + sqrt{b^2 - 4ac}}{2a}")
	d.Fill("#eq-alt", "x equals minus b plus the square root of b squared minus four a c, over two a")
	d.Click("#eq-insert")
	d.Wait("s.cursors && s.cursors.length > 0")
	d.Focus("#input")
	d.Type(", and the sum of squares is ")
	tab("images")
	d.SetChecked("#eq-inline input", true)
	d.Fill("#eq-formula", "sum_{i=1}^{n} x_i^2")
	d.Fill("#eq-alt", "the sum of x sub i squared, for i from one to n")
	d.Click("#eq-insert")
	d.Eval(`window.scrollTo(0, 0); document.querySelectorAll('*').forEach(e => { if (e.scrollTop > 0) e.scrollTop = 0 })`, nil)
	d.Sleep(500)
	of(".gj-docview-desk", "math")

	d.Click(`button.gj-menubar-button:has-text("Read aloud")`)
	shot("menu-read-aloud")
	d.NoErrors()
}
