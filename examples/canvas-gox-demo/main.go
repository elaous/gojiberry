//go:build js && wasm

// Command canvas-gox-demo draws a canvas from a gox tree (app.gox): a
// TextFlow over several pages whose header, footer and page number come
// from a render prop. Buttons change the state (mode, page count, text)
// and re-render the tree; the canvas components are updated in place.
// #state mirrors the result as JSON (the e2e test reads it).
package main

import (
	"encoding/json"
	"strconv"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
	cgox "github.com/elaous/gojiberry/canvas/gox"
	"github.com/elaous/gojiberry/domrt/browser"
)

func main() {
	doc := browser.Document()
	state := State{Source: lorem, Pages: 3}

	stage := canvas.NewStage(nil, 0, 0)
	redraw, err := stage.Bind(doc.GetElementByID("stage"), doc.GetElementByID("input"))
	if err != nil {
		doc.GetElementByID("state").SetTextContent(`{"error":"` + err.Error() + `"}`)
		return
	}
	root := cgox.NewRoot(stage)
	root.Redraw = redraw

	stage.OnDraw = func() {
		ps := root.Find(cgox.TagPage, 0).(*canvas.PageSet)
		flow := root.Find(cgox.TagTextFlow, 0).(*canvas.TextFlow)
		headers := []string{}
		numbers := []string{}
		for i := range ps.Pages {
			h := ""
			if hf := ps.ResolvedHeader(i); hf != nil {
				h = hf.Text
			}
			headers = append(headers, h)
			numbers = append(numbers, ps.PageNumberText(i))
		}
		b, _ := json.Marshal(map[string]any{
			"mode":     flow.Mode().String(),
			"pages":    len(ps.Pages),
			"headers":  headers,
			"numbers":  numbers,
			"overflow": flow.Layout().Overflow,
			"lines":    len(flow.Layout().Lines),
		})
		doc.GetElementByID("state").SetTextContent(string(b))
	}

	render := func() {
		if err := root.Render(Doc(state)); err != nil {
			doc.GetElementByID("state").SetTextContent(`{"error":"` + err.Error() + `"}`)
			return
		}
		stage.Draw()
	}
	render()

	on := func(id, event string, fn func(js.Value)) {
		doc.GetElementByID(id).Call("addEventListener", event, js.FuncOf(func(_ js.Value, a []js.Value) any {
			fn(a[0])
			return nil
		}))
	}
	on("mode", "click", func(js.Value) {
		if state.Mode == canvas.ModeView {
			state.Mode = canvas.ModeEdit
		} else {
			state.Mode = canvas.ModeView
		}
		doc.GetElementByID("mode").SetTextContent("Mode: " + state.Mode.String() + " (click to switch)")
		render()
	})
	on("pages", "change", func(js.Value) {
		n, _ := strconv.Atoi(doc.GetElementByID("pages").Get("value").String())
		state.Pages = max(n, 1)
		render()
	})
	on("source", "input", func(js.Value) {
		state.Source = doc.GetElementByID("source").Get("value").String()
		render()
	})
	doc.GetElementByID("source").Set("value", lorem)
	select {}
}
