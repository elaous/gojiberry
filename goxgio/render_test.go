package goxgio

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

	"github.com/elaous/gojiberry/gox"
)

// TestCheckboxHandlesARealPointerClick drives type=checkbox support
// through a real pointer.Event press/release pair dispatched via a
// real input.Router -- widget.Bool, unlike widget.Clickable, has no
// exported Click() method for a shortcut programmatic click (see
// layoutInput's own doc comment), so this is Gio's own widget package
// test convention for Bool (see gioui.org/widget's TestBool) rather
// than a Renderer-level Click/Toggle helper. The click position is the
// exact center of the Dimensions Layout itself returned for the
// checkbox, not a guessed coordinate, so this can't pass by accident
// of a lucky click radius.
func TestCheckboxHandlesARealPointerClick(t *testing.T) {
	w, err := headless.NewWindow(60, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
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

// TestTextInputHandlesRealClickFocusAndTyping drives type=text support
// through a real pointer click (to focus the widget.Editor, exactly as
// a real user clicking into a text field would -- Editor.
// processPointerEvent does the focusing itself, this test does nothing
// to force it) followed by a real key.EditEvent (the same event Gio's
// own widget package tests, e.g. TestEditorDimensions, use to simulate
// real typed input), rather than calling widget.Editor.SetText
// directly, which would bypass both the click-to-focus path and the
// Update-driven onChange firing in layoutInput entirely.
func TestTextInputHandlesRealClickFocusAndTyping(t *testing.T) {
	w, err := headless.NewWindow(200, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
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

// TestClassStyleAppliesBackgroundAndPadding drives Renderer.Styles (the
// runtime half of mapping a bounded CSS class vocabulary onto real Gio
// painting -- see the mobile-backend ROADMAP entry for the fuller
// two-part design) through a real headless-GPU render: a div tagged
// with a class mapped to a background color renders visibly different
// pixels than the exact same tree with Renderer.Styles left nil, and
// the specific color actually painted matches what ClassStyle asked
// for -- not just "some pixel changed."
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

	frame := func(styles map[string]ClassStyle) *image.RGBA {
		renderer := New()
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
	styled := frame(map[string]ClassStyle{
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

// TestSelectOpensAndFiresOnChangeOnRealClicks drives select/option
// support -- the one tag Gio's own widget/material has no native
// equivalent for, so goxgio has to build its own dropdown rather than
// wrap an existing widget -- through real clicks via Renderer.Click,
// the same "simple programmatic click" mechanism (Gio's own
// widget.Clickable.Click()) the original button test already
// established, not a bypass reaching into Renderer's own unexported
// state. The trigger and each option row share that exact mechanism
// because layoutSelect stores both in the same clickables map
// layoutButton already uses (an option row's own key is "<select
// id>\x00<option value>", documented on layoutSelect itself).
func TestSelectOpensAndFiresOnChangeOnRealClicks(t *testing.T) {
	w, err := headless.NewWindow(200, 160)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	var fired bool
	var lastValue string
	selected := "b"
	node := func() gox.Node {
		opt := func(value, label string) gox.Node {
			attrs := []gox.Attr{gox.A("value", value)}
			if value == selected {
				attrs = append(attrs, gox.A("selected", "selected"))
			}
			return gox.El("option", attrs, gox.Text(label))
		}
		return gox.El("select", []gox.Attr{
			gox.A("id", "color"),
			gox.On("change", func(e gox.Event) {
				fired = true
				lastValue = gox.EventValue[string](e)
			}),
		},
			opt("a", "A"), opt("b", "B"), opt("c", "C"),
		)
	}

	var router input.Router
	var ops op.Ops
	gtx := layout.Context{
		Ops:    &ops,
		Source: router.Source(),
		// Loose (Min zero, Max the window size), not layout.Exact --
		// this test compares the closed vs. open dropdown's own
		// Dimensions to confirm it actually grew, which an Exact
		// constraint would mask by force-filling the full window
		// height regardless of real content size.
		Constraints: layout.Constraints{Max: image.Pt(200, 160)},
	}

	closedDims := renderer.Layout(gtx, th, node())
	router.Frame(gtx.Ops)
	if err := w.Frame(&ops); err != nil {
		t.Fatalf("Frame: %v", err)
	}
	closed := image.NewRGBA(image.Rect(0, 0, 200, 160))
	if err := w.Screenshot(closed); err != nil {
		t.Fatalf("Screenshot: %v", err)
	}

	if !renderer.Click("color") {
		t.Fatal("no clickable registered for the select's own trigger id -- Renderer never laid out a select identified \"color\"")
	}
	gtx.Reset()
	openDims := renderer.Layout(gtx, th, node())
	router.Frame(gtx.Ops)
	if err := w.Frame(&ops); err != nil {
		t.Fatalf("Frame: %v", err)
	}
	open := image.NewRGBA(image.Rect(0, 0, 200, 160))
	if err := w.Screenshot(open); err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if bytes.Equal(closed.Pix, open.Pix) {
		t.Fatal("frame pixels identical closed vs open: clicking the trigger never expanded the dropdown")
	}
	if openDims.Size.Y <= closedDims.Size.Y {
		t.Fatalf("open dropdown height = %d, want taller than the closed trigger's own %d (three option rows plus the toggle button)", openDims.Size.Y, closedDims.Size.Y)
	}

	if fired {
		t.Fatal("onChange fired just from opening the dropdown, before any option was clicked")
	}
	if !renderer.Click("color\x00c") {
		t.Fatal("no clickable registered for option \"c\" -- Renderer never laid out an open dropdown row for it")
	}
	gtx.Reset()
	renderer.Layout(gtx, th, node())
	router.Frame(gtx.Ops)
	if err := w.Frame(&ops); err != nil {
		t.Fatalf("Frame: %v", err)
	}

	if !fired {
		t.Fatal("onChange never fired from a real click on the \"C\" option row")
	}
	if lastValue != "c" {
		t.Fatalf("onChange value = %q, want \"c\"", lastValue)
	}

	// selected is still "b" in the tree (this test's own node() closure
	// doesn't update it -- that's the caller's job in response to
	// onChange, per layoutSelect's own controlled-component doc
	// comment), so the dropdown should now report itself closed again
	// (selecting a row closes it) with the same "B ▾" trigger it
	// started with, back to the closed frame's own dimensions.
	gtx.Reset()
	afterDims := renderer.Layout(gtx, th, node())
	if afterDims != closedDims {
		t.Fatalf("dimensions after selecting an option = %+v, want %+v (dropdown should have closed again)", afterDims, closedDims)
	}
}
