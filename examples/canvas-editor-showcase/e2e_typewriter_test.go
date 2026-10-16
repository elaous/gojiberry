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

// With the carriage on, the page slides left as the line grows and back right (and a line up) on Enter, and runs past
// its left edge at the start of a line, as the paper of a typewriter does.
func TestTypewriterCarriageSlidesThePageSideways(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.Click(`button[data-tab="prefs"]`)
	d.WaitSelector(".gj-prefs-search")
	d.Fill(".gj-prefs-search", "typewriter")
	d.Dispatch(".gj-prefs-search", "input")
	d.WaitFor("two preferences", `document.querySelector('.gj-prefs-count').textContent === '2 preferences'`)
	d.Eval(`document.querySelectorAll('.gj-prefs .gj-checkbox-input').forEach(i => i.click())`, nil)
	d.WaitFor("kept", `JSON.parse(localStorage.getItem('showcase:a11y')).typewriterx === 'on'`)
	d.Click(`button[data-tab="format"]`)
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Control+a")
	d.Press("Delete")
	d.Type("A line of typed text")
	x1 := d.State().Num("scrollX")
	d.Type(", and then some more words to type")
	x2 := d.State().Num("scrollX")
	d.OK(x2 > x1, "the page slid left as the line grew")
	y1 := d.State().Num("scrollY")
	d.Press("Enter")
	x3 := d.State().Num("scrollX")
	d.OK(x3 < x2 && x3 < 0, "on the return the page slid back right, past its left edge")
	d.OK(d.State().Num("scrollY") > y1, "and the page rolled up a line")
	d.NoErrors()
}
