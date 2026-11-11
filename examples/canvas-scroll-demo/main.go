//go:build js && wasm

package main

import (
	"encoding/json"
	"strconv"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/domrt/browser"
)

func main() {
	doc := browser.Document()

	rows := make([][]string, 40)
	for r := range rows {
		rows[r] = make([]string, 12)
		for c := range rows[r] {
			rows[r][c] = "r" + strconv.Itoa(r) + "c" + strconv.Itoa(c)
		}
	}
	rows[0] = []string{"Name", "Role", "Status", "Team", "City", "Year", "A", "B", "C", "D", "E", "F"}
	table := canvas.NewTableFrom(rows)
	tv := canvas.NewTableView(table, nil)

	sv := canvas.NewScrollView(20, 20, 420, 300)
	sv.Add(tv, 0, 0) // sized below, once there is a painter to measure with
	stage := canvas.NewStage(nil, 0, 0, sv)
	redraw, err := stage.Bind(doc.GetElementByID("stage"), doc.GetElementByID("input"))
	if err != nil {
		doc.GetElementByID("state").SetTextContent(`{"error":"` + err.Error() + `"}`)
		return
	}

	w, h := tv.Size()
	sv.SetContentSize(w+20, h+20)

	stage.OnDraw = func() {
		cw, ch := sv.ContentSize()
		st := map[string]any{
			"mode": tv.Mode().String(), "scrollX": sv.ScrollX, "scrollY": sv.ScrollY,
			"lockH": sv.LockH, "lockV": sv.LockV, "content": []float64{cw, ch},
			"viewport": []float64{sv.X, sv.Y, sv.Width, sv.Height},
			"active":   nil, "colEdges": tv.ColumnEdges(), "rowEdges": tv.RowEdges(),
			"cell00": table.Cell(0, 0),
		}
		if r, c, ok := tv.Active(); ok {
			st["active"] = []int{r, c}
			st["activeText"] = table.Cell(r, c)
		}
		b, _ := json.Marshal(st)
		doc.GetElementByID("state").SetTextContent(string(b))
	}

	on := func(id, event string, fn func()) {
		doc.GetElementByID(id).AddEventListener(event, js.FuncOf(func(js.Value, []js.Value) any { fn(); return nil }))
	}
	checked := func(id string) bool { return doc.GetElementByID(id).Get("checked").Bool() }
	on("lockv", "change", func() { sv.LockV = checked("lockv"); redraw() })
	on("lockh", "change", func() { sv.LockH = checked("lockh"); redraw() })
	btn := doc.GetElementByID("mode")
	on("mode", "click", func() {
		m := canvas.ModeEdit
		if tv.Mode() == canvas.ModeEdit {
			m = canvas.ModeView
		}
		sv.SetContentMode(m)
		btn.SetTextContent("Mode: " + m.String() + " (click to switch)")
		redraw()
	})
	redraw()
	select {}
}
