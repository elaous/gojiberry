//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestCanvasViewInAPage(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.WaitFor("the view", `document.getElementById('state').textContent === 'ready'`)
	d.OK(d.Count(".gj-canvas-view canvas") == 1, "the canvas is in the page")
	d.OK(strings.Contains(d.Text(".gj-canvas-view .gj-sr-only"), "Review, done 30%"), "the text copy is beside it: "+d.Text(".gj-canvas-view .gj-sr-only"))
	green := d.Int(`(() => {
		const px = document.querySelector('.gj-canvas-view canvas').getContext('2d').getImageData(0, 0, 480, 160).data;
		let n = 0;
		for (let i = 0; i < px.length; i += 4) if (px[i] < 20 && px[i + 1] > 150 && px[i + 2] < 20) n++;
		return n;
	})()`)
	d.OK(green > 300, "the bars of the render prop are painted on the canvas")
	d.NoErrors()
}
