//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/xango/domrt/browser"
)

func main() {
	doc := browser.Document()
	table := canvas.NewTableFrom([][]string{
		{"Name", "Role", "Status"},
		{"Ada", "Engineer", "Active"},
		{"Grace", "Admiral", "Retired"},
	})
	view := canvas.NewTableView(table, nil)
	stage := canvas.NewStage(nil, 0, 0, view)
	redraw, err := stage.Bind(doc.GetElementByID("stage"), doc.GetElementByID("input"))
	if err != nil {
		doc.GetElementByID("state").SetTextContent(`{"error":` + jsonString(err.Error()) + `}`)
		return
	}

	stage.OnDraw = func() {
		type sel struct {
			Kind  string `json:"kind"`
			Index int    `json:"index"`
		}
		kind := [...]string{"none", "column", "row", "table"}[view.Selection().Kind]
		st := map[string]any{
			"mode":      view.Mode().String(),
			"cells":     table.Cells,
			"shown":     table.Values(),
			"selection": sel{kind, view.Selection().Index},
			"menuOpen":  view.Menu().IsOpen(),
			"colEdges":  view.ColumnEdges(),
			"rowEdges":  view.RowEdges(),
			"active":    nil,
		}
		if r, c, ok := view.Active(); ok {
			st["active"] = []int{r, c}
		}
		w, h := view.Size()
		st["size"] = []float64{w, h}
		b, _ := json.Marshal(st)
		doc.GetElementByID("state").SetTextContent(string(b))
	}

	btn := doc.GetElementByID("mode")
	btn.AddEventListener("click", js.FuncOf(func(js.Value, []js.Value) any {
		if view.Mode() == canvas.ModeView {
			view.SetMode(canvas.ModeEdit)
		} else {
			view.SetMode(canvas.ModeView)
		}
		btn.SetTextContent("Mode: " + view.Mode().String() + " (click to switch)")
		redraw()
		return nil
	}))
	redraw()
	select {}
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }
