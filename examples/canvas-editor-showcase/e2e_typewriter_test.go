//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// With typewriter scrolling on, the line being typed stays in the middle of the view and the page moves under it:
// moving down a few lines scrolls the page although the caret was still well inside the view. Off, the page stays
// until the caret reaches the edge.
func TestTypewriterScrolling(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	down := func(n int) {
		for range n {
			d.Press("ArrowDown")
		}
	}
	down(8)
	d.OK(d.State().Num("scrollY") == 0, "off: eight lines down the caret is still in view and the page stays")

	d.Click(`button[data-tab="prefs"]`)
	d.WaitSelector(".gj-prefs-search")
	d.Fill(".gj-prefs-search", "typewriter")
	d.Dispatch(".gj-prefs-search", "input")
	d.WaitFor("one preference", `document.querySelector('.gj-prefs-count').textContent === '1 preference'`)
	d.Click(`.gj-prefs .gj-checkbox-input`)
	d.WaitFor("kept", `JSON.parse(localStorage.getItem('showcase:a11y')).typewriter === 'on'`)

	d.Focus("#input")
	down(14)
	y := d.State().Num("scrollY")
	d.OK(y > 0, "on: the page has moved under the caret")
	d.Type("x") // typing on the line does not move the page
	d.OK(d.State().Num("scrollY") == y, "typing on a line keeps the page where it is")
	d.NoErrors()
}
