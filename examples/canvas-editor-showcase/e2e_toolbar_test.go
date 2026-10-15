//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// The formatting toolbar: icon buttons with names and shortcuts, one stop in the tab order with the arrow keys
// between the buttons, disabled where the command cannot run, and bold, italic and the like that say whether they are
// on at the caret and run the same command as the key.
func TestFormattingToolbarRunsCommandsAndSaysWhatIsOn(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.WaitSelector("#format-toolbar")
	d.OK(d.Attr("#format-toolbar", "role") == "toolbar" && d.Attr("#format-toolbar", "aria-label") == "Formatting", "it is a labelled toolbar")
	d.OK(d.Bool(`document.querySelector('#format-toolbar [data-cmd="bold"]').disabled`), "in view mode the formatting buttons are off")
	d.OK(d.Attr(`#format-toolbar [data-cmd="bold"]`, "title") == "Bold (Ctrl+B)", "the name and the shortcut are in the tooltip: "+d.Attr(`#format-toolbar [data-cmd="bold"]`, "title"))
	d.OK(d.Attr(`#format-toolbar [data-cmd="bold"]`, "aria-keyshortcuts") == "Ctrl+B", "and announced")

	d.ToggleMode()
	d.WaitFor("the buttons on", `!document.querySelector('#format-toolbar [data-cmd="bold"]').disabled`)
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("End")
	for range 4 {
		d.Press("Shift+ArrowLeft")
	}
	d.OK(d.Attr(`#format-toolbar [data-cmd="bold"]`, "aria-pressed") == "false", "bold is not on")
	d.Click(`#format-toolbar [data-cmd="bold"]`)
	d.WaitFor("bold is on", `document.querySelector('#format-toolbar [data-cmd="bold"]').getAttribute('aria-pressed') === 'true'`)
	d.OK(d.Bool(`document.querySelector('#format-toolbar [data-cmd="bold"]').classList.contains('gj-icontoolbar-on')`), "and it looks on")
	d.Click(`#format-toolbar [data-cmd="bold"]`)
	d.WaitFor("bold is off again", `document.querySelector('#format-toolbar [data-cmd="bold"]').getAttribute('aria-pressed') === 'false'`)

	// the keyboard: one stop in the tab order, arrows between the buttons, Home and End to the ends
	d.OK(d.Count(`#format-toolbar button[tabindex="0"]`) == 1, "one tab stop")
	d.Eval(`document.querySelector('#format-toolbar button[tabindex="0"]').focus()`, nil)
	first := d.Str(`document.activeElement.getAttribute('data-cmd')`)
	d.Press("ArrowRight")
	second := d.Str(`document.activeElement.getAttribute('data-cmd')`)
	d.OK(first != "" && second != "" && first != second, "the arrow key moves along the toolbar: "+first+" then "+second)
	d.Press("End")
	d.OK(d.Str(`document.activeElement.getAttribute('data-cmd')`) == "comment.add", "End goes to the last button: "+d.Str(`document.activeElement.getAttribute('data-cmd')`))
	d.Press("Home")
	d.OK(d.Str(`document.activeElement.id`) != "" && strings.HasPrefix(d.Str(`document.activeElement.id`), "format-toolbar-"), "Home goes to the first button")
	d.Press("Tab")
	d.OK(!d.Bool(`document.getElementById('format-toolbar').contains(document.activeElement)`), "Tab leaves the toolbar")

	// a viewer cannot format, and can still comment
	d.Click(`#roles button[data-role="viewer"]`)
	d.WaitFor("formatting off for a viewer", `document.querySelector('#format-toolbar [data-cmd="bold"]').disabled`)
	d.OK(!d.Bool(`document.querySelector('#format-toolbar [data-cmd="comment.add"]').disabled`), "a viewer can comment")

	for _, v := range d.Axe("#format-toolbar", axeDisabled...) {
		t.Errorf("the toolbar: %s", v)
	}
	d.NoErrors()
}
