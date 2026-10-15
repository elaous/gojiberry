//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// Format > Vertical text lays the text out in columns: down the column is forward along the line, and the arrow
// to the left goes to the column after it.
func TestVerticalTextInTheEditor(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.Click(`button.gj-menubar-button:has-text("Format")`)
	d.Click(`.gj-menubar-panel li.gj-menu-item:has-text("Vertical text")`)
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Control+Home") // the start of the document; the body text that follows the heading is a long paragraph
	for range 17 {
		d.Press("ArrowDown")
	}
	caret := func() int {
		return d.Int(`JSON.parse(document.getElementById('state').textContent).cursors[0].Caret`)
	}
	start := caret()
	d.Press("ArrowDown")
	d.OK(caret() == start+1, "down the column goes to the next character")
	d.Press("ArrowUp")
	d.OK(caret() == start, "and up goes back")
	d.Press("ArrowLeft")
	d.OK(caret() > start+3, "left goes to the same place in the next column")
	d.Press("ArrowRight")
	d.OK(caret() == start, "and right back to the first")
	d.NoErrors()
}
