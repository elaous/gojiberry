//go:build js && wasm

// Command canvas-flow-demo shows canvas.TextFlow: text flowed into a
// page of a given size with configurable font metrics (family, size,
// weight, style, line height) and 1-3 columns. In edit mode, drag the
// grip above a column gap to resize the neighbouring columns. #state
// mirrors the layout as JSON (the e2e test reads it).
package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/domrt/browser"
	"github.com/elaous/gojiberry/domrt/html"
)

const lorem = "Typography is the art and technique of arranging type to make written language legible, readable and appealing when displayed. " +
	"The arrangement of type involves selecting typefaces, point sizes, line lengths, line-spacing and letter-spacing, and adjusting the space between pairs of letters.\n\n" +
	"A text flow pours a string into a page of a given width and height. When the page has several columns, text fills the first column from top to bottom and continues at the top of the next one. " +
	"Switch to edit mode and drag the grip above a gap between two columns to give one column more room and the other less. " +
	"The page width never changes: what one column gains the other loses.\n\n" +
	"Later, formatting will be attached to individual characters and character ranges, the way a text editor does it, without changing how lines are broken into columns."

func main() {
	doc := browser.Document()
	flow := canvas.NewTextFlow(lorem, 30, 40, 840, 420)
	flow.Gap = 30
	flow.SetColumnCount(1)
	stage := canvas.NewStage(nil, 0, 0, flow)
	redraw, err := stage.Bind(doc.GetElementByID("stage"), doc.GetElementByID("input"))
	if err != nil {
		doc.GetElementByID("state").SetTextContent(`{"error":"` + err.Error() + `"}`)
		return
	}

	stage.OnDraw = func() {
		lay := flow.Layout()
		type line struct {
			X, Y, W float64
			Col     int
		}
		lines := make([]line, len(lay.Lines))
		for i, l := range lay.Lines {
			lines[i] = line{l.X, l.Y, l.W, l.Col}
		}
		b, _ := json.Marshal(map[string]any{
			"mode":     flow.Mode().String(),
			"columns":  flow.Columns,
			"gap":      flow.Gap,
			"font":     flow.Style.Font(),
			"overflow": lay.Overflow,
			"lines":    lines,
		})
		doc.GetElementByID("state").SetTextContent(string(b))
	}

	value := func(id string) string { return doc.GetElementByID(id).GetValue() }
	number := func(id string) float64 {
		f, _ := strconv.ParseFloat(strings.TrimSpace(value(id)), 64)
		return f
	}
	apply := func() {
		flow.Style.FontFamily = value("family")
		flow.Style.FontSize = number("size")
		flow.Style.FontWeight = int(number("weight"))
		flow.Style.FontStyle = value("style")
		flow.Style.LineHeight = number("leading")
		if n := int(number("cols")); n != len(flow.Columns) {
			flow.SetColumnCount(n)
		}
		redraw()
	}
	on := func(el *html.Element, event string, fn func()) {
		el.AddEventListener(event, js.FuncOf(func(js.Value, []js.Value) any { fn(); return nil }))
	}
	for _, id := range []string{"family", "size", "weight", "style", "leading", "cols"} {
		on(doc.GetElementByID(id), "change", apply)
	}
	btn := doc.GetElementByID("mode")
	on(btn, "click", func() {
		if flow.Mode() == canvas.ModeView {
			flow.SetMode(canvas.ModeEdit)
		} else {
			flow.SetMode(canvas.ModeView)
		}
		btn.SetTextContent("Mode: " + flow.Mode().String() + " (click to switch)")
		redraw()
	})
	redraw()
	select {}
}
