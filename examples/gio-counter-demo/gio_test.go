package main

import (
	"bytes"
	"image"
	"testing"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget/material"

	"github.com/elaous/gojiberry/examples/gio-counter-demo/goxgio"
	"github.com/elaous/gojiberry/examples/gio-counter-demo/shared"
	"github.com/elaous/gojiberry/gox"
)

// TestCounterRendersAndClicksThroughRealGio drives the exact same
// shared.Counter component and goxgio.Renderer that main.go's run
// mounts into a real desktop window, but through Gio's own headless
// GPU backend (gioui.org/gpu/headless) instead of app.Window, so it
// runs without a display server or compositor: each frame is a real
// (software-rasterized where no GPU is present) rendered image read
// back pixel-for-pixel, and the "+" button's handler is driven by a
// real widget.Clickable click via Renderer.Click, not a direct Go call
// to OnInc bypassing the renderer entirely.
func TestCounterRendersAndClicksThroughRealGio(t *testing.T) {
	w, err := headless.NewWindow(300, 80)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := goxgio.New()
	count := 0

	frame := func() *image.RGBA {
		var ops op.Ops
		var router input.Router
		gtx := layout.Context{
			Ops:         &ops,
			Source:      router.Source(),
			Constraints: layout.Exact(image.Pt(300, 80)),
		}
		node := shared.Counter(shared.CounterProps{
			Count: count,
			OnInc: func(gox.Event) { count++ },
			OnDec: func(gox.Event) { count-- },
		})
		renderer.Layout(gtx, th, node)
		router.Frame(gtx.Ops)
		if err := w.Frame(&ops); err != nil {
			t.Fatalf("Frame: %v", err)
		}
		img := image.NewRGBA(image.Rect(0, 0, 300, 80))
		if err := w.Screenshot(img); err != nil {
			t.Fatalf("Screenshot: %v", err)
		}
		return img
	}

	// Two consecutive frames of unchanged state should screenshot
	// identically -- establishes a stable baseline and rules out
	// nondeterministic rendering before attributing any later
	// difference to the click.
	frame()
	before := frame()

	if !renderer.Click("inc") {
		t.Fatal("no clickable registered for the inc button's id -- Renderer never laid out a button identified \"inc\"")
	}
	// Clicked() fires (and count updates) synchronously the next time
	// Layout walks a tree containing "inc", but Gio's headless backend
	// settles a newly-rasterized glyph (here, the "1" that wasn't in
	// the glyph atlas before) one frame late: the very next Screenshot
	// still shows the pre-click pixels even though count has already
	// changed. Render once to let the click and the new glyph enter the
	// atlas, then again to read back the settled result -- confirmed by
	// hand: three same-state frames in a row are pixel-identical, but
	// the frame immediately after a state change never is until this
	// second render.
	frame()
	after := frame()

	if count != 1 {
		t.Fatalf("count = %d, want 1: the inc button's real onClick handler should have fired through Renderer.Click, the same path a real pointer click drives", count)
	}
	if bytes.Equal(before.Pix, after.Pix) {
		t.Fatal("frame pixels identical before/after the click: the re-rendered \"1\" label never actually got painted")
	}

	if !renderer.Click("dec") {
		t.Fatal("no clickable registered for the dec button's id")
	}
	frame()
	settled := frame()
	if count != 0 {
		t.Fatalf("count = %d, want 0 after a dec click", count)
	}
	if bytes.Equal(after.Pix, settled.Pix) {
		t.Fatal("frame pixels identical before/after the dec click: the re-rendered \"0\" label never actually got painted")
	}
}
