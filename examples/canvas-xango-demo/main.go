//go:build js && wasm

package main

import (
	"encoding/json"
	"strconv"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
	cxango "github.com/elaous/gojiberry/canvas/xango"
	"github.com/elaous/xango/domrt/browser"
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
	root := cxango.NewRoot(stage)
	root.Redraw = redraw

	stage.OnDraw = func() {
		ps := root.Find(cxango.TagPage, 0).(*canvas.PageSet)
		flow := root.Find(cxango.TagTextFlow, 0).(*canvas.TextFlow)
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
