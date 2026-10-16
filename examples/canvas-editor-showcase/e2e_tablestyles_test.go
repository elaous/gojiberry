//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// A table is dressed in a style from the Styles tab; the style follows the rows when one is added; a style the person
// defines is offered and is kept with the document.
func TestTableStylesPickerDefineAndFollowRows(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Control+a")
	d.Press("Delete")
	for _, line := range []string{"| Item | Qty |", "|---|---|", "| a | 1 |", "| b | 2 |"} {
		d.Type(line)
		d.Press("Enter")
	}
	d.Press("Enter")
	// find a cell of the table by clicking down the first column
	found := false
	for y := 130; y < 420 && !found; y += 8 {
		d.CanvasClick(110, float64(y))
		found = strings.Contains(d.Str(`JSON.parse(document.getElementById('state').textContent).target`), "TableView")
	}
	d.OK(found, "a cell of the table was entered")
	d.Click(`button[data-tab="styles"]`)
	d.WaitSelector("#tstyle-panel")

	// a built-in style
	d.Click(`#tstyle-panel button[data-style="Blue header, banded"]`)
	d.WaitFor("it is marked", `document.querySelector('#tstyle-panel button[data-style="Blue header, banded"]').getAttribute('aria-pressed') === 'true'`)

	// a style of the person's own: needs a name and real colours, then it is offered
	d.Fill("#tstyle-name", "Sunrise")
	d.Fill("#tstyle-header", "orange")
	d.Click("#tstyle-save")
	d.WaitFor("refused", `document.getElementById('tstyle-message').textContent.includes('colours')`)
	d.Fill("#tstyle-header", "#c2410c")
	d.Fill("#tstyle-headertext", "#ffffff")
	d.Fill("#tstyle-band", "#ffedd5")
	d.Click("#tstyle-save")
	d.WaitSelector(`#tstyle-panel button[data-style="Sunrise"]`)
	d.Click(`#tstyle-panel button[data-style="Sunrise"]`)
	d.WaitFor("it is marked", `document.querySelector('#tstyle-panel button[data-style="Sunrise"]').getAttribute('aria-pressed') === 'true'`)

	// it goes into the saved document
	d.Click(`button[data-tab="transfer"]`)
	_, js := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="json"]`) })
	d.OK(strings.Contains(string(js), "table.schemes") && strings.Contains(string(js), "Sunrise"), "the style is kept with the document")
	d.NoErrors()
}
