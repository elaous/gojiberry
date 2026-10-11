//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// Word suggestions: Ctrl+Space lists the words that continue the one being typed, the list is an ARIA
// listbox the input element controls (the focus never leaves the input), Down moves the highlight,
// Enter puts the word in place (and types no new line), Escape closes the list, and typing narrows it.
func TestWordSuggestions(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Home")
	d.Type("zebra zebra zeppelin ze ")
	d.Press("ArrowLeft") // the caret is at the end of "ze", before the space

	d.Press("Control+Space")
	d.WaitSelector(`#suggestions[role="listbox"]`)
	d.OK(d.Count(`#suggestions [role="option"]`) == 2, "two words continue 'ze'")
	d.OK(strings.TrimSpace(d.Text(`#suggestions [aria-selected="true"]`)) == "zebra", "the most used word is highlighted first")
	d.OK(d.Bool(`document.activeElement.id === 'input'`), "the focus stays in the input")
	d.OK(d.Str(`document.getElementById('input').getAttribute('aria-expanded')`) == "true" &&
		d.Str(`document.getElementById('input').getAttribute('aria-activedescendant')`) == "suggestions-opt-0", "the input names the highlighted option")
	d.OK(strings.Contains(d.Text(".gj-announcer"), "2 suggestions"), "the count is announced: "+d.Text(".gj-announcer"))

	d.Press("ArrowDown")
	d.WaitFor("the second option", `document.getElementById('input').getAttribute('aria-activedescendant') === 'suggestions-opt-1'`)
	d.Press("Escape")
	d.WaitFor("the list closing", `!document.getElementById('suggestions')`)
	d.OK(!d.Bool(`document.getElementById('input').hasAttribute('aria-expanded')`), "and the input is no longer expanded")

	d.Press("Control+Space")
	d.WaitSelector(`#suggestions`)
	d.Type("p") // narrows: 'zep' has one candidate
	d.WaitFor("narrowing", `document.querySelectorAll('#suggestions [role=option]').length === 1`)
	lines := strings.Count(d.State().Str("text"), "\n")
	d.Press("Enter")
	d.Wait("s.text.includes('zebra zebra zeppelin zeppelin ')")
	d.OK(strings.Count(d.State().Str("text"), "\n") == lines, "Enter accepted the word and typed no new line")
	d.WaitFor("the list closing", `!document.getElementById('suggestions')`)
	d.NoErrors()
}
