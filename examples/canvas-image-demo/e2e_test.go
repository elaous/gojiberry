//go:build !(js && wasm)

package main

import (
	"fmt"
	"math"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

const intrudes = `((lines, lineH, [x, y, w, h]) => lines.some((l) => l.Y < y + h && l.Y + lineH > y && l.X < x + w && l.X + l.W > x))`

const grown = `(([x, y, w, h]) => [x - 8, y - 8, w + 16, h + 16])`

func TestEndToEnd(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	visible := func() (v []float64) {
		d.Eval(`JSON.parse(document.getElementById('state').textContent).visible`, &v)
		return
	}
	x := func() float64 { return d.State().Num("x") }
	d.OK(d.Is("s.mode === 'view' && !s.wrap && s.alpha === 1 && s.x === 60"), "starts in view mode, opaque, not wrapping")
	d.OK(d.Is(intrudes+"(s.lines, 22, s.visible)"), "without wrap, text runs underneath the picture")

	d.CanvasDrag(150, 190, 200, 190)
	d.OK(x() == 60, "view mode ignores drags")

	d.ToggleMode()

	var v []float64
	clickBtn := func(i float64) { v = visible(); d.CanvasClick(v[0]+56*i+10, v[1]-17) }

	d.CanvasDrag(150, 190, 183, 190)
	d.OK(x() == 95 && math.Mod(x(), 5) == 0 && d.State().Num("y") == 110, fmt.Sprint("drag moves left/right and snaps to 5: x=", x()))
	d.CanvasDrag(x()+50, 190, x()+50+12, 260)
	d.OK(x() == 105 && d.State().Num("y") == 110, fmt.Sprint("vertical motion is ignored, 12px snaps to 10: x=", x()))

	clickBtn(3)
	d.OK(d.State().Bool("wrap"), "Wrap button turns wrapping on")
	d.OK(d.Is("!"+intrudes+"(s.lines, 22, "+grown+"(s.visible))"), "text no longer overlaps the picture")

	d.CanvasDrag(x()+100, 190, x()+100+200, 190)
	d.OK(x() == 305, fmt.Sprint("picture moved again: ", x()))
	d.OK(d.Is("!"+intrudes+"(s.lines, 22, "+grown+"(s.visible))"), "text still clear of the picture after moving it")

	v = visible()
	clickBtn(1)
	d.OK(d.State().Str("tool") == "clip", "Clip button selects the clip tool")
	vx, vy, vw, vh := v[0], v[1], v[2], v[3]
	d.CanvasDrag(vx, vy+vh/2, vx+47, vy+vh/2)
	d.OK(d.Is(fmt.Sprintf("s.clip.Left === 45 && s.visible[0] === %v && s.visible[2] === %v", vx+45, vw-45)), fmt.Sprint("left edge clipped, snapped to 5: ", d.State().Get("clip")))
	d.OK(d.Is("!"+intrudes+"(s.lines, 22, "+grown+"(s.visible))"), "text re-wraps around the cropped picture")
	v = visible()
	d.CanvasDrag(v[0]+v[2], v[1]+v[3]/2, v[0]+v[2]-92, v[1]+v[3]/2)
	d.OK(d.State().Num("clip.Right") == 90, fmt.Sprint("right edge clipped: ", d.State().Get("clip")))

	v = visible()
	probeX, probeY := int(v[0]+v[2]-10), int(v[1]+v[3]-10)
	clickBtn(2)
	before := d.Pixel(probeX, probeY)
	d.CanvasClick(v[0]+v[2]/2, v[1]+v[3]+14)
	s := d.State()
	d.OK(s.Str("tool") == "alpha" && math.Abs(s.Num("alpha")-0.5) < 0.051 && math.Round(s.Num("alpha")*20) == s.Num("alpha")*20, fmt.Sprint("slider sets alpha in 5% steps: ", s.Num("alpha")))
	after := d.Pixel(probeX, probeY)
	d.OK(after[0]+after[1]+after[2] > before[0]+before[1]+before[2], fmt.Sprint("half-transparent picture blends toward the page: ", before, " -> ", after))

	clickBtn(3)
	d.OK(!d.State().Bool("wrap") && d.Is(intrudes+"(s.lines, 22, s.visible)"), "Wrap off lets text run under the picture again")

	d.ToggleMode()
	x0 := x()
	v = visible()
	d.CanvasDrag(v[0]+20, v[1]+20, v[0]+120, v[1]+20)
	d.OK(d.State().Str("mode") == "view" && x() == x0, "view mode is read-only again")
	d.NoErrors()
}
