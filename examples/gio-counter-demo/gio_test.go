package main

import (
	"bytes"
	"image"
	"image/color"
	"testing"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
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

// TestCheckboxHandlesARealPointerClick drives goxgio's new
// type=checkbox support through a real pointer.Event press/release pair
// dispatched via a real input.Router -- widget.Bool, unlike
// widget.Clickable, has no exported Click() method for a shortcut
// programmatic click (see layoutInput's own doc comment), so this is
// Gio's own widget package test convention for Bool (see
// gioui.org/widget's TestBool) rather than a Renderer-level Click/Toggle
// helper. The click position is the exact center of the Dimensions
// Layout itself returned for the checkbox, not a guessed coordinate, so
// this can't pass by accident of a lucky click radius.
func TestCheckboxHandlesARealPointerClick(t *testing.T) {
	w, err := headless.NewWindow(60, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := goxgio.New()
	var fired bool
	var lastChecked bool
	node := gox.El("input", []gox.Attr{
		gox.A("type", "checkbox"),
		gox.A("id", "agree"),
		gox.On("change", func(e gox.Event) {
			fired = true
			lastChecked = e.Checked
		}),
	})

	var router input.Router
	var ops op.Ops
	gtx := layout.Context{
		Ops:         &ops,
		Source:      router.Source(),
		Constraints: layout.Exact(image.Pt(60, 60)),
	}

	dims := renderer.Layout(gtx, th, node)
	router.Frame(gtx.Ops)
	if err := w.Frame(&ops); err != nil {
		t.Fatalf("Frame: %v", err)
	}
	before := image.NewRGBA(image.Rect(0, 0, 60, 60))
	if err := w.Screenshot(before); err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if fired {
		t.Fatal("onChange fired before any click")
	}

	center := f32.Pt(float32(dims.Size.X)/2, float32(dims.Size.Y)/2)
	router.Queue(
		pointer.Event{Source: pointer.Touch, Kind: pointer.Press, Position: center},
		pointer.Event{Source: pointer.Touch, Kind: pointer.Release, Position: center},
	)

	gtx.Reset()
	renderer.Layout(gtx, th, node)
	router.Frame(gtx.Ops)
	if err := w.Frame(&ops); err != nil {
		t.Fatalf("Frame: %v", err)
	}
	after := image.NewRGBA(image.Rect(0, 0, 60, 60))
	if err := w.Screenshot(after); err != nil {
		t.Fatalf("Screenshot: %v", err)
	}

	if !fired {
		t.Fatal("onChange never fired from a real pointer click on the checkbox")
	}
	if !lastChecked {
		t.Fatal("onChange fired with Checked=false, want true after checking a previously-unchecked box")
	}
	if bytes.Equal(before.Pix, after.Pix) {
		t.Fatal("frame pixels identical before/after the click: the checked state never actually got painted")
	}
}

// TestTextInputHandlesRealClickFocusAndTyping drives goxgio's new
// type=text support through a real pointer click (to focus the
// widget.Editor, exactly as a real user clicking into a text field
// would -- Editor.processPointerEvent does the focusing itself, this
// test does nothing to force it) followed by a real key.EditEvent (the
// same event Gio's own widget package tests, e.g.
// TestEditorDimensions, use to simulate real typed input), rather than
// calling widget.Editor.SetText directly, which would bypass both the
// click-to-focus path and the Update-driven onChange firing in
// layoutInput entirely.
func TestTextInputHandlesRealClickFocusAndTyping(t *testing.T) {
	w, err := headless.NewWindow(200, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := goxgio.New()
	var fired bool
	var lastValue string
	node := gox.El("input", []gox.Attr{
		gox.A("type", "text"),
		gox.A("id", "name"),
		gox.On("change", func(e gox.Event) {
			fired = true
			lastValue = gox.EventValue[string](e)
		}),
	})

	var router input.Router
	var ops op.Ops
	gtx := layout.Context{
		Ops:         &ops,
		Source:      router.Source(),
		Constraints: layout.Exact(image.Pt(200, 60)),
	}

	dims := renderer.Layout(gtx, th, node)
	router.Frame(gtx.Ops)
	if err := w.Frame(&ops); err != nil {
		t.Fatalf("Frame: %v", err)
	}
	before := image.NewRGBA(image.Rect(0, 0, 200, 60))
	if err := w.Screenshot(before); err != nil {
		t.Fatalf("Screenshot: %v", err)
	}

	center := f32.Pt(float32(dims.Size.X)/2, float32(dims.Size.Y)/2)
	router.Queue(
		pointer.Event{Source: pointer.Mouse, Kind: pointer.Press, Position: center, Buttons: pointer.ButtonPrimary},
		pointer.Event{Source: pointer.Mouse, Kind: pointer.Release, Position: center, Buttons: pointer.ButtonPrimary},
	)
	gtx.Reset()
	renderer.Layout(gtx, th, node)
	router.Frame(gtx.Ops)

	router.Queue(key.EditEvent{Text: "hi"})
	gtx.Reset()
	renderer.Layout(gtx, th, node)
	router.Frame(gtx.Ops)
	if err := w.Frame(&ops); err != nil {
		t.Fatalf("Frame: %v", err)
	}
	after := image.NewRGBA(image.Rect(0, 0, 200, 60))
	if err := w.Screenshot(after); err != nil {
		t.Fatalf("Screenshot: %v", err)
	}

	if !fired {
		t.Fatal("onChange never fired from a real typed key.EditEvent on the text input")
	}
	if lastValue != "hi" {
		t.Fatalf("onChange value = %q, want \"hi\"", lastValue)
	}
	if bytes.Equal(before.Pix, after.Pix) {
		t.Fatal("frame pixels identical before/after typing: the typed text never actually got painted")
	}
}

// TestClassStyleAppliesBackgroundAndPadding drives goxgio's new
// Renderer.Styles support (the runtime half of mapping a bounded CSS
// class vocabulary onto real Gio painting -- see the mobile-backend
// ROADMAP entry for the fuller two-part design) through a real
// headless-GPU render: a div tagged with a class mapped to a
// background color renders visibly different pixels than the exact
// same tree with Renderer.Styles left nil, and the specific color
// actually painted matches what ClassStyle asked for -- not just "some
// pixel changed."
func TestClassStyleAppliesBackgroundAndPadding(t *testing.T) {
	w, err := headless.NewWindow(120, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	node := gox.El("div", []gox.Attr{gox.A("class", "panel")},
		gox.El("span", nil, gox.Text("hi")),
	)

	frame := func(styles map[string]goxgio.ClassStyle) *image.RGBA {
		renderer := goxgio.New()
		renderer.Styles = styles
		var ops op.Ops
		var router input.Router
		gtx := layout.Context{
			Ops:         &ops,
			Source:      router.Source(),
			Constraints: layout.Exact(image.Pt(120, 60)),
		}
		renderer.Layout(gtx, th, node)
		router.Frame(gtx.Ops)
		if err := w.Frame(&ops); err != nil {
			t.Fatalf("Frame: %v", err)
		}
		img := image.NewRGBA(image.Rect(0, 0, 120, 60))
		if err := w.Screenshot(img); err != nil {
			t.Fatalf("Screenshot: %v", err)
		}
		return img
	}

	unstyled := frame(nil)

	want := color.NRGBA{R: 220, G: 20, B: 20, A: 255}
	styled := frame(map[string]goxgio.ClassStyle{
		"panel": {PaddingY: 8, PaddingX: 8, Background: want},
	})

	if bytes.Equal(unstyled.Pix, styled.Pix) {
		t.Fatal("frame pixels identical styled vs unstyled: ClassStyle had no visible effect")
	}

	// The div starts at the frame's own (0,0); a point a few px in on
	// both axes is well inside the 8dp padding, clear of the "hi"
	// label's own text glyphs, so it should be pure painted background.
	got := styled.At(3, 3)
	r, g, b, a := got.RGBA()
	if uint8(r>>8) != want.R || uint8(g>>8) != want.G || uint8(b>>8) != want.B || uint8(a>>8) != want.A {
		t.Fatalf("pixel at (3,3) = %v, want background color %v", got, want)
	}
}
