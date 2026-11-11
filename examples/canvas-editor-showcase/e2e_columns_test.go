//go:build !(js && wasm)

package main

import (
	"fmt"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestColumnCommands(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.OK(d.State().Int("columns") == 0, "the text starts in the base layout")
	d.ToggleMode()
	text := d.State().Str("text")

	run := func(label string) {
		d.CanvasClick(240, 164)
		d.Focus("#input")
		d.Click(`button.gj-menubar-button:has-text("Edit")`)
		d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Columns")`)
		d.Click(`.gj-menubar-panel .gj-menu-label:text-is("` + label + `")`)
	}
	ratio := func() float64 { // the first column's share of the two
		var w []float64
		d.Eval(`JSON.parse(document.getElementById('state').textContent).columnConfig.widths || []`, &w)
		if len(w) != 2 {
			return 0
		}
		return w[0] / (w[0] + w[1])
	}

	for _, c := range []struct {
		label string
		count int
	}{{"Two columns", 2}, {"Three columns", 3}, {"Four columns", 4}, {"One column", 1}} {
		run(c.label)
		d.Wait(fmt.Sprintf("s.columns === %d", c.count))
		d.OK(d.State().Str("text") == text, c.label+" sets "+fmt.Sprint(c.count)+" and leaves the text alone")
	}

	run("Two columns, wide then narrow")
	d.Wait("s.columnConfig.widths && s.columnConfig.widths.length === 2 && s.columnConfig.widths[0] > s.columnConfig.widths[1]")
	d.OK(ratio() > 0.6, fmt.Sprintf("wide then narrow: the first column takes %.2f", ratio()))
	run("Reset column widths")
	d.Wait("s.columns === 2 && !(s.columnConfig.widths && s.columnConfig.widths.length)")
	d.OK(ratio() == 0, "Reset column widths gives the two columns equal widths again")
	run("Two columns, wide then narrow")
	d.Wait("s.columnConfig.widths && s.columnConfig.widths.length === 2")
	run("Two columns, narrow then wide")
	d.Wait("s.columnConfig.widths && s.columnConfig.widths.length === 2 && s.columnConfig.widths[0] < s.columnConfig.widths[1]")
	d.OK(ratio() < 0.4, fmt.Sprintf("narrow then wide: the first column takes %.2f", ratio()))

	gap := func() float64 { return d.State().Num("columnConfig.gap") }
	run("Column spacing: narrow")
	d.Wait("s.columnConfig.gap === 12")
	narrow := gap()
	run("Column spacing: wide")
	d.Wait("s.columnConfig.gap === 48")
	run("Column spacing: standard")
	d.Wait("s.columnConfig.gap !== 48 && s.columnConfig.gap !== 12")
	d.OK(narrow < gap() && gap() < 48, fmt.Sprintf("spacing narrow %.0f < standard %.0f < wide 48", narrow, gap()))

	d.Focus("#input")
	for range 20 {
		d.Press("Control+z")
	}
	d.Wait("s.columns === 0")
	d.OK(d.State().Str("text") == text, "Undo takes the columns away again")
	d.NoErrors()
}
