//go:build js && wasm

package main

import (
	"strconv"

	"github.com/elaous/gojiberry/canvas"
	cxango "github.com/elaous/gojiberry/canvas/xango"
	"github.com/elaous/gojiberry/domrt/browser"
	"github.com/elaous/gojiberry/xango"
)

func main() {
	doc := browser.Document()
	v, err := cxango.NewView(doc.GetElementByID("host"), 480, 160)
	if err != nil {
		doc.GetElementByID("state").SetTextContent("error: " + err.Error())
		return
	}
	cells := [][]string{{"Task", "Done"}, {"Write", "70"}, {"Review", "30"}}
	table := cxango.Table(cxango.TableProps{
		Cells: cells, X: 10, Y: 10,
		RenderCell: func(p canvas.Painter, row, col int, text string, x, y, w, h float64) bool {
			if row == 0 || col != 1 {
				return false
			}
			n, _ := strconv.Atoi(text)
			p.SetFillStyle("#00aa00")
			p.FillRect(x+4, y+4, (w-8)*float64(n)/100, h-8)
			return true
		},
	})
	text := "Task: Write, done 70%. Task: Review, done 30%."
	if err := v.Render(cxango.Canvas(cxango.CanvasProps{Width: 480, Height: 160, Children: []xango.Node{table}}), text); err != nil {
		doc.GetElementByID("state").SetTextContent("error: " + err.Error())
		return
	}
	doc.GetElementByID("state").SetTextContent("ready")
}
