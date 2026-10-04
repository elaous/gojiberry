//go:build !(js && wasm)

package main

import (
	"fmt"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestEndToEnd drives the demo: the xango tree renders, its pages template gives
// per-page headers and numbers, state changes re-render in place, and a header
// edited on the canvas survives a re-render.
func TestEndToEnd(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	s := d.State()
	d.OK(s.Str("error") == "", "renders without error")
	d.OK(s.Int("pages") == 3, "three pages from the tree")
	d.OK(d.Is(`JSON.stringify(s.headers) === '["Gojiberry report","Gojiberry report","Appendix"]'`), fmt.Sprint("header inherits then changes on page 3: ", s.Get("headers")))
	d.OK(d.Is(`JSON.stringify(s.numbers) === '["","Page 2 of 3","Page 3 of 3"]'`), fmt.Sprint("page 1 has no number, later ones continue: ", s.Get("numbers")))
	ink := d.Int(`(() => {
		const px = document.getElementById('stage').getContext('2d').getImageData(170, 80, 560, 200).data;
		let dark = 0;
		for (let i = 0; i < px.length; i += 4) if (px[i] < 100 && px[i + 1] < 100) dark++;
		return dark;
	})()`)
	d.OK(ink > 200, fmt.Sprintf("text is painted on the canvas (%d dark pixels)", ink))

	// Re-render with a new page count.
	d.Fill("#pages", "4")
	d.Dispatch("#pages", "change")
	s = d.State()
	d.OK(s.Int("pages") == 4 && d.Is("s.numbers[3] === 'Page 4 of 4'"), fmt.Sprint("page count prop adds a sheet: ", s.Get("numbers")))

	// Edit a header on the canvas, then re-render: the edit stays.
	d.ToggleMode()
	d.OK(d.State().Str("mode") == "edit", "mode prop switches the components to edit")
	d.CanvasDblClick(450, 24+30)
	d.Type("!!")
	d.OK(d.Is("s.headers[0].endsWith('!!')"), fmt.Sprint("header edited on the canvas: ", d.State().Get("headers")))
	d.Fill("#source", "# Changed\n\nShort text.")
	d.OK(d.Is("s.headers[0].endsWith('!!')"), "edited header survives a re-render with new text")
	d.OK(d.State().Int("lines") < 10, fmt.Sprint("new source is laid out (", d.State().Get("lines"), " lines)"))
	d.NoErrors()
}
