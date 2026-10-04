//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestColumnCommands drives the column commands from the Edit menu: a new
// region of columns starts at the caret's paragraph, and Undo takes it away.
func TestColumnCommands(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.OK(d.State().Int("columns") == 0, "the text starts in the base layout")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	text := d.State().Str("text")
	d.Click(`button.gj-menubar-button:has-text("Edit")`)
	d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Columns")`)
	d.Click(`.gj-menubar-panel li:has-text("Two columns"):not(:has-text("wide")):not(:has-text("narrow"))`)
	d.Wait("s.columns === 2")
	d.OK(d.State().Str("text") == text, "Two columns sets two columns from the caret and leaves the text alone")
	d.Focus("#input")
	d.Press("Control+z")
	d.Wait("s.columns === 0")
	d.OK(d.State().Str("text") == text, "Undo takes the columns away again")
	d.NoErrors()
}
