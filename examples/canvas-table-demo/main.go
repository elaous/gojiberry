//go:build js && wasm

// Command canvas-table-demo shows canvas.TableView: an Obsidian-style
// table drawn entirely on a <canvas>. Hover it to reveal the "+" bars
// on the right edge and bottom edge.
package main

import (
	"strconv"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/domrt/browser"
)

func main() {
	doc := browser.Document()
	view := canvas.NewTableView(canvas.NewTableFrom([][]string{
		{"Name", "Role", "Status"},
		{"Ada", "Engineer", "Active"},
		{"Grace", "Admiral", "Retired"},
	}), nil)
	view.OnChange = func(t *canvas.Table) {
		doc.GetElementByID("state").SetTextContent(
			"rows=" + strconv.Itoa(t.Rows()) + " cols=" + strconv.Itoa(t.Cols()))
	}
	if err := view.Bind(doc.GetElementByID("table"), doc.GetElementByID("input")); err != nil {
		doc.GetElementByID("state").SetTextContent("error: " + err.Error())
	}
	select {}
}
