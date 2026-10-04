//go:build !(js && wasm)

package main

import (
	"fmt"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestEndToEnd drives the demo in headless Chromium: wheel and scrollbar
// scrolling, clipping to the viewport, the vertical/horizontal locks, and that a
// table inside a scrolled view still receives correctly mapped clicks in edit mode.
func TestEndToEnd(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	num := func(expr string) float64 {
		var v float64
		d.Eval(`(s => `+expr+`)(JSON.parse(document.getElementById('state').textContent))`, &v)
		return v
	}
	var vp []float64
	d.Eval(`JSON.parse(document.getElementById('state').textContent).viewport`, &vp)
	vx, vy, vw, vh := vp[0], vp[1], vp[2], vp[3]
	inX, inY := vx+200, vy+150
	alpha := func(x, y float64) int { return d.Pixel(int(x), int(y))[3] }

	d.OK(d.Is("s.scrollX === 0 && s.scrollY === 0 && s.content[0] > s.viewport[2] && s.content[1] > s.viewport[3]"), "content is bigger than the viewport")
	d.OK(alpha(vx+vw+8, vy+50) == 0 && alpha(vx+50, vy+vh+8) == 0, "content is clipped to the viewport (nothing painted outside it)")
	d.OK(alpha(vx+100, vy+100) != 0, "content is painted inside it")

	// Wheel scrolls both axes.
	d.CanvasMove(inX, inY)
	d.Wheel(0, 120)
	d.OK(d.Is("s.scrollY === 120 && s.scrollX === 0"), fmt.Sprint("wheel scrolls vertically: ", num("s.scrollY")))
	d.Wheel(90, 0)
	d.OK(d.Is("s.scrollX === 90"), fmt.Sprint("wheel scrolls horizontally: ", num("s.scrollX")))
	d.OK(d.Pixel(int(vx)+5, int(vy)+5)[3] != 0, "the scrolled content redraws")

	// Wheel outside the viewport does nothing to it.
	d.CanvasMove(vx+vw+30, vy+vh+15)
	d.Wheel(0, 100)
	d.OK(d.Is("s.scrollY === 120"), "wheel outside the viewport is ignored")
	d.CanvasMove(inX, inY)

	// Clamped at the end.
	d.Wheel(0, 100000)
	d.OK(d.Is("s.scrollY === s.content[1] - s.viewport[3]"), fmt.Sprint("vertical scroll clamps at the end: ", num("s.scrollY")))
	d.Wheel(0, -100000)
	d.Wheel(-100000, 0)
	d.OK(d.Is("s.scrollX === 0 && s.scrollY === 0"), "and at the start")

	// Locks.
	d.SetChecked("#lockv", true)
	d.CanvasMove(inX, inY)
	d.Wheel(60, 60)
	d.OK(d.Is("s.lockV && s.scrollY === 0 && s.scrollX === 60"), "vertical lock: only horizontal moved")
	d.SetChecked("#lockv", false)
	d.SetChecked("#lockh", true)
	d.CanvasMove(inX, inY) // clicking a checkbox moved the pointer
	d.Wheel(60, 60)
	d.OK(d.Is("s.lockH && s.scrollX === 60 && s.scrollY === 60"), "horizontal lock: only vertical moved")
	d.SetChecked("#lockh", false)
	d.CanvasMove(inX, inY)
	d.Wheel(-1000, -1000)

	// Drag the vertical scrollbar thumb (right edge, top of the track).
	thumbX := vx + vw - 5
	d.CanvasDrag(thumbX, vy+8, thumbX, vy+8+100)
	d.OK(num("s.scrollY") > 100, fmt.Sprint("dragging the scrollbar thumb scrolls: ", num("s.scrollY")))
	d.OK(d.Pixel(int(thumbX), int(vy)+8+100)[3] != 0, "the thumb is drawn")

	// Edit mode inside a scrolled view: click a cell and check it maps correctly.
	d.Wheel(-100000, -100000)
	d.ToggleMode()
	d.Wheel(0, 200)
	d.Wheel(150, 0)
	cx, cy := vx+120, vy+120 // viewport point
	d.OK(true, "scrolled")
	d.Eval(`window.__cell = (s => { const contentX = `+e2etest.J(cx-vx)+` + s.scrollX, contentY = `+e2etest.J(cy-vy)+` + s.scrollY;
		return [s.rowEdges.findIndex((e, i) => contentY >= e && contentY < s.rowEdges[i + 1]), s.colEdges.findIndex((e, i) => contentX >= e && contentX < s.colEdges[i + 1])]; })(JSON.parse(document.getElementById('state').textContent))`, nil)
	var want []int
	d.Eval(`window.__cell`, &want)
	d.CanvasClick(cx, cy)
	d.OK(d.Is(fmt.Sprintf("s.active && s.active[0] === %d && s.active[1] === %d", want[0], want[1])), fmt.Sprintf("click maps through the scroll offset: expected cell %d,%d, got %v", want[0], want[1], d.State().Get("active")))
	d.Type("Z")
	d.OK(d.Is("s.activeText.includes('Z')"), fmt.Sprint("typing still works inside the view: ", d.State().Get("activeText")))
	d.NoErrors()
}
