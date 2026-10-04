//go:build !(js && wasm)

package main

import (
	"fmt"
	"math"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestEndToEnd drives the demo in headless Chromium: font metrics change the
// layout, columns flow text across, and in edit mode the gap grip can be dragged
// to resize neighbouring columns (view mode can't).
func TestEndToEnd(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	near := func(a, b, eps float64) bool { return math.Abs(a-b) < eps }

	s := d.State()
	d.OK(s.Str("mode") == "view" && d.Is("s.columns.length === 1"), "starts in view mode with one column")
	d.OK(d.Is("s.lines.length > 3 && !s.overflow"), "text is laid out and fits")
	ink := d.Int(`(() => {
		const px = document.getElementById('stage').getContext('2d').getImageData(30, 40, 840, 60).data;
		let dark = 0;
		for (let i = 0; i < px.length; i += 4) if (px[i] < 100 && px[i + 1] < 100) dark++;
		return dark;
	})()`)
	d.OK(ink > 200, fmt.Sprintf("text is actually painted on the canvas (%d dark pixels)", ink))

	// Font metrics feed the layout.
	linesBefore := d.Int(`JSON.parse(document.getElementById('state').textContent).lines.length`)
	d.Select("#weight", "700")
	d.Select("#style", "italic")
	d.Select("#family", "monospace")
	d.Fill("#size", "24")
	d.Dispatch("#size", "change")
	s = d.State()
	d.OK(s.Str("font") == "italic 700 24px monospace", "font string reflects family/size/weight/style: "+s.Str("font"))
	d.OK(d.Int(`JSON.parse(document.getElementById('state').textContent).lines.length`) > linesBefore, "bigger text wraps onto more lines")
	d.Fill("#size", "16")
	d.Dispatch("#size", "change")
	d.Fill("#leading", "40")
	d.Dispatch("#leading", "change")
	d.OK(d.Is("Math.abs(s.lines[1].Y - s.lines[0].Y - 40) < 1e-6"), "line height is honoured")
	d.Select("#weight", "400")
	d.Select("#style", "normal")
	d.Select("#family", "sans-serif")
	d.Fill("#leading", "22")
	d.Dispatch("#leading", "change")

	// Columns.
	d.Select("#cols", "3")
	s = d.State()
	d.OK(d.Is("s.columns.length === 3 && Math.abs(s.columns.reduce((x, y) => x + y, 0) + 2 * s.gap - 840) < 1e-6"), "three equal columns fill the page")
	d.OK(d.Is("new Set(s.lines.map((l) => l.Col)).size > 1"), "text flows into later columns")
	cols := func() []float64 {
		var c []float64
		d.Eval(`JSON.parse(document.getElementById('state').textContent).columns`, &c)
		return c
	}
	gapCenter := 30 + cols()[0] + s.Num("gap")/2

	// View mode: the grip isn't there.
	d.CanvasDrag(gapCenter, 30, gapCenter+40, 30)
	c := cols()
	d.OK(near(c[0], c[1], 1e-6), "view mode ignores gap drags")

	d.ToggleMode()
	widths := c
	d.CanvasDrag(gapCenter, 30, gapCenter+40, 30)
	c = cols()
	d.OK(near(c[0], widths[0]+40, 1.5) && near(c[1], widths[1]-40, 1.5), fmt.Sprint("dragging the grip resizes the two neighbours: ", c))
	d.OK(near(c[0]+c[1], widths[0]+widths[1], 1e-6) && c[2] == widths[2], "combined width and the third column are unchanged")
	// The 1px guide straddles two pixel columns, so it is anti-aliased.
	guide := d.Pixel(int(gapCenter+40), 200)
	d.OK(guide[0] < 250 && guide[2] > guide[0], fmt.Sprint("the gap guide line is drawn in edit mode: ", guide))

	d.CanvasDrag(gapCenter+40, 30, gapCenter+2000, 30)
	d.OK(cols()[1] == 40, "dragging far right stops at the minimum column width")
	d.NoErrors()
}
