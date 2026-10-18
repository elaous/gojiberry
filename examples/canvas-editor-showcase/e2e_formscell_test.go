//go:build !(js && wasm) && (experimental || exp_forms)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// A table cell becomes a form field from the Forms tab (table, row and column); a press on the cell in the text opens it
// for filling in; the exports carry its words, and "Make the cell plain text again" undoes it.
func TestAFormFieldInATableCell(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Control+a")
	d.Press("Delete")
	for _, line := range []string{"| Item | Qty |", "|---|---|", "| Pens | 1 |"} {
		d.Type(line)
		d.Press("Enter")
	}
	d.Press("Enter")
	d.Click(`button[data-tab="forms"]`)
	d.WaitSelector("#forms-bar")
	d.SetChecked("#forms-in-cell input", true)
	d.WaitSelector("#forms-cell-row")
	d.Fill("#forms-cell-row", "2")
	d.Fill("#forms-cell-column", "2")
	d.Click("#forms-add-number")
	d.WaitSelector("#forms-editor")
	d.Fill("#forms-prop-label", "Quantity")
	d.Click("#forms-edit-done")
	d.WaitSelector("#forms-item-0")
	d.OK(d.Has("#forms-uncell-0"), "the field is in a table cell")

	// a press on the cell in the text opens it for filling in
	d.CanvasClick(340, 75) // the cell of the table (the canvas is scrolled sideways in the test window)
	d.WaitSelector("#forms-input")
	d.Fill("#forms-input", "7")
	d.Click("#forms-fill-done")

	// the pen at the top right of the table: formulas and fields as they are defined, and a press changes the field
	d.CanvasClick(384, 38) // the pen/eye at the table's top right (the table is wider once filled in)
	d.CanvasClick(340, 75)
	d.WaitSelector("#forms-editor")
	d.Click("#forms-edit-done")
	d.Click("#forms-see-all")
	d.CanvasClick(340, 75)
	d.WaitSelector("#forms-input")
	d.Click("#forms-fill-done")

	d.Click(`button[data-tab="transfer"]`)
	_, md := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="markdown"]`) })
	d.OK(strings.Contains(string(md), "Quantity: 7"), "the export has the words: "+string(md))

	d.Click(`button[data-tab="forms"]`)
	d.Click("#forms-uncell-0")
	d.WaitFor("it is plain text again", `!document.getElementById('forms-item-0')`)
	d.NoErrors()
}
