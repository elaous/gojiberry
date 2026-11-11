//go:build js && wasm

package main

import (
	"encoding/base64"
	"encoding/json"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/domrt/browser"
)

const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="240" height="160">
<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">
<stop offset="0" stop-color="#ff7a59"/><stop offset="1" stop-color="#3b2bd9"/></linearGradient></defs>
<rect width="240" height="160" fill="url(#g)"/>
<circle cx="80" cy="60" r="34" fill="#ffd23f"/>
<path d="M0 160 L90 90 L150 140 L190 110 L240 160 Z" fill="#1b1b3a"/></svg>`

const lorem = "A picture can sit inside running text. With text wrap turned on, the lines on either side of it are shortened so that nothing runs underneath the picture, and the text below it resumes at full width. " +
	"Drag the picture left or right, crop it with the clip tool or change its opacity, and the paragraph reflows while you do it. " +
	"Columns work too: the picture pushes text aside in whichever column it overlaps, choosing the wider side of the column for the text to use.\n\n" +
	"The positions you drag to snap to multiples of five pixels, which keeps layouts tidy without a grid. " +
	"The wrap margin leaves a little air between the picture and the words. Turn wrap off again and the text returns to ignoring the picture altogether, flowing straight underneath it."

func main() {
	doc := browser.Document()
	flow := canvas.NewTextFlow(lorem+"\n\n"+lorem, 20, 50, 860, 480)
	flow.Gap = 30
	flow.SetColumnCount(2)

	stage := canvas.NewStage(nil, 0, 0, flow)
	redraw, err := stage.Bind(doc.GetElementByID("stage"), doc.GetElementByID("input"))
	if err != nil {
		doc.GetElementByID("state").SetTextContent(`{"error":"` + err.Error() + `"}`)
		return
	}

	var img *canvas.Image
	report := func() {
		lay := flow.Layout()
		type line struct {
			X, Y, W float64
			Col     int
		}
		lines := make([]line, len(lay.Lines))
		for i, l := range lay.Lines {
			lines[i] = line{l.X, l.Y, l.W, l.Col}
		}
		vx, vy, vw, vh := img.Visible()
		tool := [...]string{"move", "clip", "alpha"}[img.Tool()]
		b, _ := json.Marshal(map[string]any{
			"mode": img.Mode().String(), "tool": tool, "wrap": img.Wrap, "alpha": img.Alpha,
			"x": img.X, "y": img.Y, "clip": img.Clip,
			"visible": []float64{vx, vy, vw, vh},
			"lines":   lines,
		})
		doc.GetElementByID("state").SetTextContent(string(b))
	}

	canvas.LoadImage("data:image/svg+xml;base64,"+base64.StdEncoding.EncodeToString([]byte(svg)),
		func(src *canvas.HTMLImage) {
			img = canvas.NewImage(src, 60, 110)
			img.BoundsW = stage.Width
			img.SetFlow(flow)
			stage.Add(img)
			stage.OnDraw = report
			redraw()
		}, func() {
			doc.GetElementByID("state").SetTextContent(`{"error":"image failed to load"}`)
		})

	btn := doc.GetElementByID("mode")
	btn.AddEventListener("click", js.FuncOf(func(js.Value, []js.Value) any {
		if img == nil {
			return nil
		}
		m := canvas.ModeEdit
		if img.Mode() == canvas.ModeEdit {
			m = canvas.ModeView
		}
		stage.SetMode(m)
		btn.SetTextContent("Mode: " + m.String() + " (click to switch)")
		redraw()
		return nil
	}))
	select {}
}
