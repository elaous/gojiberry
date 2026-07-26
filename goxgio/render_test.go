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

// TestOnFiresForANodeWithNoAttrHandler proves Renderer.On's ID-based
// fallback actually fires against a node carrying no Attr handler of
// its own -- the exact shape every real OnDesign editor/ui/*.gox
// component takes, since none of them use gox.On (interactivity is
// wired externally by ID instead, matching main.gox's own
// ed.on(id, event, fn)); gio-counter-demo's Counter, by contrast,
// always sets an Attr handler and so never exercises this path. Uses
// the same real-click mechanism (Renderer.Click, Gio's own
// widget.Clickable.Click()) every other button-click test in this file
// already relies on.
func TestOnFiresForANodeWithNoAttrHandler(t *testing.T) {
	w, err := headless.NewWindow(60, 30)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	node := gox.El("button", []gox.Attr{gox.A("id", "close")}, gox.Text("×"))

	var fired bool
	renderer.On("close", "click", func(gox.Event) { fired = true })

	frame := func() {
		var ops op.Ops
		var router input.Router
		gtx := layout.Context{
			Ops:         &ops,
			Source:      router.Source(),
			Constraints: layout.Exact(image.Pt(60, 30)),
		}
		renderer.Layout(gtx, th, node)
		router.Frame(gtx.Ops)
		if err := w.Frame(&ops); err != nil {
			t.Fatalf("Frame: %v", err)
		}
	}

	frame()
	if fired {
		t.Fatal("handler fired before any click")
	}

	if !renderer.Click("close") {
		t.Fatal(`Click("close"): no clickable registered for that id after the first Layout`)
	}
	frame()
	if !fired {
		t.Fatal("Renderer.On's registered handler never fired after a real click on an id-only node with no Attr handler of its own")
	}
}

// TestOnLosesToANodesOwnAttrHandler confirms On is purely additive: a
// node that DOES carry its own Attr handler (gox.On, the mechanism
// gio-counter-demo's Counter uses) keeps using it, unaffected by an
// On registration for the same id -- so adding ID-based delegation
// couldn't have changed gio-counter-demo's own existing behavior.
func TestOnLosesToANodesOwnAttrHandler(t *testing.T) {
	w, err := headless.NewWindow(60, 30)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	var attrFired, registeredFired bool
	node := gox.El("button", []gox.Attr{
		gox.A("id", "dec"),
		gox.On("click", func(gox.Event) { attrFired = true }),
	}, gox.Text("-"))
	renderer.On("dec", "click", func(gox.Event) { registeredFired = true })

	frame := func() {
		var ops op.Ops
		var router input.Router
		gtx := layout.Context{
			Ops:         &ops,
			Source:      router.Source(),
			Constraints: layout.Exact(image.Pt(60, 30)),
		}
		renderer.Layout(gtx, th, node)
		router.Frame(gtx.Ops)
		if err := w.Frame(&ops); err != nil {
			t.Fatalf("Frame: %v", err)
		}
	}

	frame()
	if !renderer.Click("dec") {
		t.Fatal(`Click("dec"): no clickable registered for that id after the first Layout`)
	}
	frame()

	if !attrFired {
		t.Fatal("the node's own Attr handler never fired")
	}
	if registeredFired {
		t.Fatal("the On-registered handler fired even though the node has its own Attr handler, which should always win")
	}
}

// TestFocusFiresOnChangeWithoutAnyPointerClick proves Renderer.Focus's
// programmatic focus actually works: typing into a text input right
// after Focus, with no pointer.Event dispatched at all -- unlike
// TestTextInputHandlesRealClickFocusAndTyping, which drives a real
// click at the widget's own on-screen position first. Focus exists
// precisely for callers that can't easily compute that position (an
// input buried inside a larger tree, not laid out in isolation), so
// this test's whole point is that a real key.EditEvent still lands
// correctly without one.
func TestFocusFiresOnChangeWithoutAnyPointerClick(t *testing.T) {
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

	renderer.Layout(gtx, th, node)
	router.Frame(gtx.Ops)
	if err := w.Frame(&ops); err != nil {
		t.Fatalf("Frame: %v", err)
	}

	if !renderer.Focus("name") {
		t.Fatal(`Focus("name"): no editor registered for that id after the first Layout`)
	}
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

	if !fired {
		t.Fatal("onChange never fired from a typed key.EditEvent after Focus, with no pointer click at all")
	}
	if lastValue != "hi" {
		t.Fatalf("onChange value = %q, want \"hi\"", lastValue)
	}
}

// TestToggleFiresOnChangeWithoutAnyPointerClick proves Renderer.Toggle
// actually flips a checkbox and fires its onChange, with no
// pointer.Event dispatched at all -- widget.Bool, unlike
// widget.Clickable, exposes no public Click-equivalent shortcut of its
// own, so Toggle exists specifically to give a checkbox the same
// coordinate-free interaction Click already gives a button/select.
func TestToggleFiresOnChangeWithoutAnyPointerClick(t *testing.T) {
	w, err := headless.NewWindow(60, 30)
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

	frame := func() {
		var ops op.Ops
		var router input.Router
		gtx := layout.Context{
			Ops:         &ops,
			Source:      router.Source(),
			Constraints: layout.Exact(image.Pt(60, 30)),
		}
		renderer.Layout(gtx, th, node)
		router.Frame(gtx.Ops)
		if err := w.Frame(&ops); err != nil {
			t.Fatalf("Frame: %v", err)
		}
	}

	frame()
	if fired {
		t.Fatal("onChange fired before any interaction")
	}

	if !renderer.Toggle("agree") {
		t.Fatal(`Toggle("agree"): no checkbox registered for that id after the first Layout`)
	}
	frame()

	if !fired {
		t.Fatal("onChange never fired from Toggle, with no pointer click at all")
	}
	if !lastChecked {
		t.Fatalf("onChange checked = %v, want true (checkbox starts unchecked, one Toggle should check it)", lastChecked)
	}
}

// TestSpanWithElementChildrenLaysOutThoseChildren covers a real bug
// found while wiring OnDesign's own toolbar.gox on native: a <span>
// (or <p>) whose children include a real element, not just text --
// toolbar.gox's own "font-picker-wrap" span wraps a button plus a
// hidden div, exactly this shape -- used to render as an empty
// material.Label (textOf only concatenates KindText children, silently
// dropping element children), so nothing inside such a span/p was ever
// laid out at all: a button entirely inside one would never register a
// Clickable, and Renderer.Click would report it as never having
// existed. A span/p with only text children (the overwhelmingly common
// case -- every other span in this codebase) still renders as a plain
// Label, unaffected.
func TestSpanWithElementChildrenLaysOutThoseChildren(t *testing.T) {
	w, err := headless.NewWindow(120, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	node := gox.El("span", []gox.Attr{gox.A("class", "wrap")},
		gox.El("button", []gox.Attr{gox.A("id", "inner")}, gox.Text("Click me")),
	)

	var ops op.Ops
	var router input.Router
	gtx := layout.Context{
		Ops:         &ops,
		Source:      router.Source(),
		Constraints: layout.Constraints{Max: image.Pt(120, 60)},
	}
	dims := renderer.Layout(gtx, th, node)
	router.Frame(gtx.Ops)
	if err := w.Frame(&ops); err != nil {
		t.Fatalf("Frame: %v", err)
	}

	if dims.Size.X == 0 || dims.Size.Y == 0 {
		t.Fatalf("Dimensions = %+v, want non-zero: the button inside the span should have laid out to some real size", dims)
	}
	if !renderer.Click("inner") {
		t.Fatal(`Click("inner"): no clickable registered for the button nested inside the span -- it was never laid out`)
	}
}
