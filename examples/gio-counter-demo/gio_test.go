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

	"github.com/elaous/gojiberry/examples/gio-counter-demo/shared"
	"github.com/elaous/gojiberry/xango"
	"github.com/elaous/gojiberry/xango/gio"
)

func TestCounterRendersAndClicksThroughRealGio(t *testing.T) {
	w, err := headless.NewWindow(300, 80)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := gio.New()
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
			OnInc: func(xango.Event) { count++ },
			OnDec: func(xango.Event) { count-- },
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

	frame()
	before := frame()

	if !renderer.Click("inc") {
		t.Fatal("no clickable registered for the inc button's id -- Renderer never laid out a button identified \"inc\"")
	}
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
