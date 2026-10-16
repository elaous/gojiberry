//go:build !(js && wasm)

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestEndToEnd drives the demo in headless Chromium: view mode is read-only; edit
// mode lets you type, Tab between cells, add columns/rows with the "+" bars, and
// use the column and table handle menus. It asserts on the JSON the demo mirrors
// into #state, plus a few canvas pixels.
func TestEndToEnd(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	nums := func(path string) []float64 {
		var v []float64
		d.Eval(`JSON.parse(document.getElementById('state').textContent).`+path, &v)
		return v
	}
	mid := func(edges []float64, i int) float64 { return (edges[i] + edges[i+1]) / 2 }

	s := d.State()
	d.OK(s.Str("mode") == "view", "starts in view mode")
	d.OK(d.Is("s.cells.length === 3 && s.cells[0].join() === 'Name,Role,Status'"), "initial cells")

	// View mode: clicks and typing do nothing.
	d.CanvasClick(50, 50)
	d.Type("zzz")
	d.OK(d.Is("s.active === null && s.cells[0][0] === 'Name'"), "view mode ignores clicks and typing")

	d.ToggleMode()
	d.OK(d.State().Str("mode") == "edit", "toggle enters edit mode")

	// Type into a cell, then Tab on.
	d.CanvasClick(50, 50)
	d.Press("End")
	d.Type("!")
	d.OK(d.Is("s.cells[0][0] === 'Name!' && s.active.join() === '0,0'"), "typing edits the active cell")
	d.Press("Tab")
	d.OK(d.Is("s.active.join() === '0,1'"), "Tab moves to the next cell")
	d.Press("Escape")

	// A formula: the cell keeps its text and the table shows the value.
	d.CanvasClick(50, mid(nums("rowEdges"), 1))
	d.Press("Home")
	for range 3 {
		d.Press("Delete")
	}
	d.Type("=6*7")
	d.Press("Escape")
	d.OK(d.Is("s.cells[1][0] === '=6*7' && s.shown[1][0] === '42' && s.shown[1][1] === 'Engineer'"), "a formula cell shows its value and keeps its text")

	// "+" bars.
	size := nums("size")
	w := size[0]
	d.CanvasClick(w-11, mid(nums("rowEdges"), 0)+30) // add-column bar
	d.OK(d.Is("s.cells[0].length === 4 && s.active.join() === '0,3'"), `"+" on the right adds a column`)
	d.CanvasClick(40, nums("size")[1]-11) // add-row bar
	d.OK(d.Is("s.cells.length === 4 && s.active.join() === '3,0'"), `"+" at the bottom adds a row`)

	// Column handle: selects the column and opens a menu.
	colEdges := nums("colEdges")
	colX := mid(colEdges, 1)
	d.CanvasClick(colX, 24)
	d.OK(d.Is("s.selection.kind === 'column' && s.selection.index === 1 && s.menuOpen"), "column grip selects it and opens the menu")
	grip := d.Pixel(int(colX+0.5)-10, 24) // the pill, left of its dots
	d.OK(grip[0] == 0x7c && grip[1] == 0x5c && grip[2] == 0xff, "selected column grip is drawn in the accent color")

	// "Insert column left" is the menu's first item.
	d.CanvasClick(colEdges[1]+20, 32+14)
	d.OK(d.Is("s.cells[0].length === 5 && s.cells[0][1] === '' && s.cells[0][2] === 'Role'"), "menu: insert column left")
	d.OK(!d.State().Bool("menuOpen"), "menu closes after choosing")

	// Delete that column again: re-open and pick "Delete column" (last item).
	colEdges = nums("colEdges")
	d.CanvasClick(mid(colEdges, 1), 24)
	colEdges = nums("colEdges")
	d.CanvasClick(colEdges[1]+20, 32+28*6+9*3+14)
	d.OK(d.Is("s.cells[0].length === 4 && s.cells[0][1] === 'Role'"), "menu: delete column")

	// Table handle -> a table style: the menu lists the styles after "Wrap text around" (28 px an item, 9 for a
	// divider); "Blue header, banded" is the fourth, and dresses the first row in blue.
	d.CanvasClick(24, 24)
	d.OK(d.Is("s.selection.kind === 'table' && s.menuOpen"), "the table handle opens the menu with the styles in it")
	d.CanvasClick(40, 32+28*4+9*3+28*3+14)
	d.OK(!d.State().Bool("menuOpen"), "choosing a style closes the menu")
	head := d.Pixel(60, 44)
	d.OK(head[0] == 0x25 && head[1] == 0x63 && head[2] == 0xeb, fmt.Sprintf("the header row is blue after the style: %#x", head))
	if dir := os.Getenv("GOJIBERRY_FEATURE_SHOTS"); dir != "" { // the picture of docs/features.md (make feature-shots)
		d.CanvasClick(24, 24)
		d.ScreenshotOf("#stage", filepath.Join(dir, "table-styles.webp"))
		d.Press("Escape")
	}

	// Table handle -> Clear contents.
	d.CanvasClick(24, 24)
	d.OK(d.Is("s.selection.kind === 'table' && s.menuOpen"), "corner icon selects the table")
	d.CanvasClick(40, 32+28*2+9+14)
	d.OK(d.Is("s.cells.every((r) => r.every((c) => c === ''))"), "menu: clear contents")

	// Back to view mode hides the editing UI.
	d.ToggleMode()
	d.OK(d.Is("s.mode === 'view' && s.selection.kind === 'none' && !s.menuOpen"), "view mode drops selection and menu")
	d.ToggleMode() // back to editing
	// Paste: a Markdown table from the clipboard fills the cells from the active one and grows the table.
	d.CanvasClick(50, mid(nums("rowEdges"), 0))
	d.Eval(`(() => { const dt = new DataTransfer(); dt.setData('text/plain', '| P | Q |\n|---|---|\n| 7 | =A2*6 |');
		document.getElementById('input').dispatchEvent(new ClipboardEvent('paste', {clipboardData: dt, bubbles: true, cancelable: true})) })()`, nil)
	d.OK(d.Is("s.cells[0][0] === 'P' && s.cells[1][0] === '7' && s.cells[1][1] === '=A2*6' && s.shown[1][1] === '42'"), "a pasted Markdown table fills the cells and its formula works")

	d.NoErrors()
}
