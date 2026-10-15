//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// Hebrew typed into a paragraph of its own is laid out right to left: the arrow keys move by what is seen
// (the left arrow goes to the next letter in the text), the copy for a screen reader says dir=auto for it, and
// the paragraph before it is as it was.
func TestRightToLeftParagraphInTheEditor(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Home")
	d.Press("Enter")
	d.Press("ArrowUp")
	d.Type("שלום עולם")
	caret := func() int {
		return d.Int(`JSON.parse(document.getElementById('state').textContent).cursors[0].Caret`)
	}
	end := caret()
	d.Press("Home")
	start := caret()
	d.OK(end-start == 9, "typed nine letters")
	d.Press("ArrowLeft")
	d.OK(caret() == start+1, "the left arrow in right-to-left text goes to the next letter")
	d.Press("ArrowRight")
	d.OK(caret() == start, "and the right arrow back")
	d.Press("End")
	d.Press("Shift+ArrowRight")
	d.OK(d.Int(`JSON.parse(document.getElementById('state').textContent).cursors[0].Caret`) == end-1, "shift and the right arrow select towards the start of the text")
	d.WaitFor("the copy marks the block", `!!document.querySelector('#a11y-text [dir="auto"]')`)
	d.NoErrors()
}
