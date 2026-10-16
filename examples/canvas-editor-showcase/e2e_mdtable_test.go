//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// Three typed lines (a header row, the row of dashes, one row of cells with as many columns) become a table when
// Enter ends the third; fewer cells in the row leave the text as it is. A cell that starts with = is a formula.
func TestTypingAMarkdownTableMakesATable(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	tables := func() int {
		return strings.Count(d.Str(`document.getElementById('a11y-text').innerHTML`), "<table")
	}
	d.Focus("#input")
	d.Press("Control+a")
	d.Press("Delete")
	before := tables()
	for _, line := range []string{"| Qty | Total |", "|---|---|"} {
		d.Type(line)
		d.Press("Enter")
	}
	d.Type("| 3 | =A2*2 |")
	d.OK(tables() == before, "no table while the line is being typed")
	d.Press("Enter")
	d.WaitFor("one more table in the reading copy", `(document.getElementById('a11y-text').innerHTML.match(/<table/g) || []).length === `+e2etest.J(before+1))
	html := d.Str(`document.getElementById('a11y-text').innerHTML`)
	d.OK(strings.Contains(html, "Qty") && strings.Contains(html, ">6<"), "the header and the computed formula are in it: "+html)
	d.OK(!strings.Contains(d.Str(`JSON.parse(document.getElementById("state").textContent).text`), "|---|"), "the typed lines are gone")
	d.NoErrors()
}
