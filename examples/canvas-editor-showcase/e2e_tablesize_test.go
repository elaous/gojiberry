//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// Every table in the text has the grips of an editable table (column letters above, row numbers at the left); dragging
// the edge of a column grip or of a row grip makes that column wider or that row taller, and the size is saved.
func TestDraggingATableGripResizesAColumnAndARow(t *testing.T) {
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
	d.CanvasDrag(325, 33, 365, 33) // the edge between the two columns, on the column grips
	d.CanvasDrag(228, 79, 228, 99) // the edge under the first row, on the row grips
	d.Click(`button[data-tab="transfer"]`)
	_, js := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="json"]`) })
	flat := strings.Join(strings.Fields(string(js)), "")
	d.OK(strings.Contains(flat, `"colWidths":[120,0]`) && strings.Contains(flat, `"rowHeights":[52,0]`), "the sizes are in the saved document: "+string(js))
	d.NoErrors()
}
