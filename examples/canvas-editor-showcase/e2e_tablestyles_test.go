//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

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
	d.Click(`button[data-tab="styles"]`)
	d.WaitSelector("#tstyle-panel")
	d.OK(!d.Has("#tstyle-hint"), "the caret is below the table, so the table is the one in hand")

	d.Click(`#tstyle-panel button[data-style="Blue header"]`)
	d.WaitFor("it is marked", `document.querySelector('#tstyle-panel button[data-style="Blue header"]').getAttribute('aria-pressed') === 'true'`)

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

	d.Click(`button[data-tab="transfer"]`)
	_, js := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="json"]`) })
	d.OK(strings.Contains(string(js), "table.schemes") && strings.Contains(string(js), "Sunrise"), "the style is kept with the document")
	d.NoErrors()
}
