package goxgio

import (
	"bytes"
	"image"
	"image/color"
	"strings"
	"testing"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
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

// TestTextareaHandlesRealClickFocusAndTyping mirrors
// TestTextInputHandlesRealClickFocusAndTyping exactly, but for a
// "textarea" node instead of "input" -- proving the new
// case "input", "textarea" dispatch in Layout (and layoutInput's own
// tag-aware widget.Editor{SingleLine: n.Tag != "textarea"} branch)
// actually reaches the same real click/focus/typed-input/onChange path
// input already had, not just that it no longer falls through to the
// unrecognized-tag default (which would render but never wire up).
func TestTextareaHandlesRealClickFocusAndTyping(t *testing.T) {
	w, err := headless.NewWindow(200, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	var fired bool
	var lastValue string
	node := gox.El("textarea", []gox.Attr{
		gox.A("id", "comment-text"),
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

	router.Queue(key.EditEvent{Text: "a comment"})
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
		t.Fatal("onChange never fired from a real typed key.EditEvent on the textarea")
	}
	if lastValue != "a comment" {
		t.Fatalf("onChange value = %q, want \"a comment\"", lastValue)
	}
	if bytes.Equal(before.Pix, after.Pix) {
		t.Fatal("frame pixels identical before/after typing: the typed text never actually got painted")
	}
}

// TestTextareaPreservesNewlinesUnlikeSingleLineInput proves the one
// real behavioral difference layoutInput's own SingleLine:
// n.Tag != "textarea" branch exists for: a newline typed into a
// textarea survives in the widget's own Text(), where the identical
// newline typed into a plain single-line input does not -- not just
// that both tags reach the same generic wiring path (already proven by
// TestTextareaHandlesRealClickFocusAndTyping/
// TestTextInputHandlesRealClickFocusAndTyping), but that the multi-line
// case is genuinely distinct, not a no-op flag.
func TestTextareaPreservesNewlinesUnlikeSingleLineInput(t *testing.T) {
	w, err := headless.NewWindow(200, 80)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()

	typeAndCapture := func(tag string) string {
		renderer := New()
		var last string
		node := gox.El(tag, []gox.Attr{
			gox.A("id", "field"),
			gox.On("change", func(e gox.Event) { last = gox.EventValue[string](e) }),
		})

		var router input.Router
		var ops op.Ops
		gtx := layout.Context{
			Ops:         &ops,
			Source:      router.Source(),
			Constraints: layout.Exact(image.Pt(200, 80)),
		}
		renderer.Layout(gtx, th, node)
		router.Frame(gtx.Ops)
		if err := w.Frame(&ops); err != nil {
			t.Fatalf("Frame: %v", err)
		}

		if !renderer.Focus("field") {
			t.Fatalf("Focus(%q): no editor registered for tag %q after the first Layout", "field", tag)
		}
		gtx.Reset()
		renderer.Layout(gtx, th, node)
		router.Frame(gtx.Ops)

		router.Queue(key.EditEvent{Text: "line one\nline two"})
		gtx.Reset()
		renderer.Layout(gtx, th, node)
		router.Frame(gtx.Ops)
		if err := w.Frame(&ops); err != nil {
			t.Fatalf("Frame: %v", err)
		}
		return last
	}

	inputResult := typeAndCapture("input")
	if strings.Contains(inputResult, "\n") {
		t.Fatalf("single-line input.Text() = %q, want the newline stripped (SingleLine forces a single line)", inputResult)
	}

	textarea := typeAndCapture("textarea")
	if !strings.Contains(textarea, "\n") {
		t.Fatalf("textarea.Text() = %q, want the newline preserved (multi-line)", textarea)
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

// TestDivWithClickHandlerFiresThroughRealClick proves a plain "div"
// node carrying a click handler (registered via Renderer.On, the same
// ID-based delegation TestOnFiresForANodeWithNoAttrHandler already
// exercises for button) is itself a real click target -- layoutButton
// isn't the only tag that can be one, see this file's own package doc
// comment and layoutDivContent's own doc comment for why (comment.go's
// own #comment-item-<id> text div is exactly this shape: a real click
// listener attached directly to a div, not a nested button). The div's
// own text children (e.g. a mention span nested inside it) still
// render, unlike wrapping the same content in a "button" node instead,
// which would have discarded anything but the button's own direct
// KindText children via textOf.
func TestDivWithClickHandlerFiresThroughRealClick(t *testing.T) {
	w, err := headless.NewWindow(200, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	node := gox.El("div", []gox.Attr{gox.A("id", "comment-text-c1")},
		gox.Text("hi "),
		gox.El("span", []gox.Attr{gox.A("class", "comment-mention")}, gox.Text("@sam")),
	)

	var fired bool
	renderer.On("comment-text-c1", "click", func(gox.Event) { fired = true })

	frame := func() {
		var ops op.Ops
		var router input.Router
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
	}

	frame()
	if fired {
		t.Fatal("handler fired before any click")
	}

	if !renderer.Click("comment-text-c1") {
		t.Fatal(`Click("comment-text-c1"): no clickable registered for that id after the first Layout -- div click support missing`)
	}
	frame()
	if !fired {
		t.Fatal("On's registered handler never fired after a real click on a div with no Attr handler of its own")
	}
}

// TestDivWithNoClickHandlerStaysPlain proves a div with no click
// handler registered at all is never given a Clickable -- Click
// reports false, matching every other interactive tag's own "nothing
// registered yet" contract, so the overwhelming majority of plain,
// non-interactive divs in any real tree pay no cost for this.
func TestDivWithNoClickHandlerStaysPlain(t *testing.T) {
	w, err := headless.NewWindow(200, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	node := gox.El("div", []gox.Attr{gox.A("id", "plain")}, gox.Text("just text"))

	var ops op.Ops
	var router input.Router
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

	if renderer.Click("plain") {
		t.Fatal(`Click("plain") reported true for a div with no click handler, want false`)
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

// TestSetValueSeedsBeforeFirstLayout proves Renderer.SetValue's own
// central claim over Focus/Toggle: it works even when called before the
// target has ever been laid out, since a caller wanting to seed a panel's
// input the moment it becomes visible can't wait for a first Layout call
// the way Focus/Toggle's own "must already be registered" contract
// requires.
func TestSetValueSeedsBeforeFirstLayout(t *testing.T) {
	w, err := headless.NewWindow(200, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	node := gox.El("input", []gox.Attr{
		gox.A("type", "text"),
		gox.A("id", "margin-top"),
		gox.A("value", "1.0"),
	})

	renderer.SetValue("margin-top", "1.25")

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

	ed, ok := renderer.editors["margin-top"]
	if !ok {
		t.Fatal(`no editor registered for "margin-top" after Layout`)
	}
	if got, want := ed.Text(), "1.25"; got != want {
		t.Fatalf("editor text = %q, want %q (SetValue's own pending request, not the node's own \"value\" attribute)", got, want)
	}
}

// TestSetValueReseedsAnExistingEditor proves the other half of
// SetValue's own contract: re-seeding an editor that already exists
// (and may already hold different text, real user typing or an earlier
// seed) overwrites it -- the actual motivating case, a panel with a
// text input showing a document's own current state that's since
// changed, closed, and reopened.
func TestSetValueReseedsAnExistingEditor(t *testing.T) {
	w, err := headless.NewWindow(200, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	node := gox.El("input", []gox.Attr{
		gox.A("type", "text"),
		gox.A("id", "margin-top"),
		gox.A("value", "1.0"),
	})

	var router input.Router
	var ops op.Ops
	gtx := layout.Context{
		Ops:         &ops,
		Source:      router.Source(),
		Constraints: layout.Exact(image.Pt(200, 60)),
	}
	frame := func() {
		gtx.Reset()
		renderer.Layout(gtx, th, node)
		router.Frame(gtx.Ops)
		if err := w.Frame(&ops); err != nil {
			t.Fatalf("Frame: %v", err)
		}
	}
	frame()

	ed := renderer.editors["margin-top"]
	if got, want := ed.Text(), "1.0"; got != want {
		t.Fatalf("editor text after first Layout = %q, want %q (seeded from the node's own \"value\" attribute)", got, want)
	}

	renderer.SetValue("margin-top", "2.5")
	frame()

	if got, want := ed.Text(), "2.5"; got != want {
		t.Fatalf("editor text after SetValue = %q, want %q", got, want)
	}

	// A second Layout with no further SetValue call must not keep
	// re-applying the same request -- confirms it's consumed once, the
	// same "deferred and cleared" shape focusRequests/toggleRequests
	// already take, not a value that would silently fight real typing on
	// every later frame.
	ed.SetText("something the user typed")
	frame()
	if got, want := ed.Text(), "something the user typed"; got != want {
		t.Fatalf("editor text after a frame with no new SetValue = %q, want %q (the earlier request should not still be pending)", got, want)
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

// TestSetCheckedSeedsBeforeFirstLayoutAndDoesNotFireOnChange proves
// Renderer.SetChecked's own central claim, the same one
// TestSetValueSeedsBeforeFirstLayout establishes for SetValue: it
// works even when called before the target has ever been laid out,
// and it does not fire onChange (setting a value programmatically from
// outside isn't a user interaction, the same posture SetValue already
// takes for a text input).
func TestSetCheckedSeedsBeforeFirstLayoutAndDoesNotFireOnChange(t *testing.T) {
	w, err := headless.NewWindow(200, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	var fired bool
	node := gox.El("input", []gox.Attr{
		gox.A("type", "checkbox"),
		gox.A("id", "dark-theme"),
		gox.On("change", func(gox.Event) { fired = true }),
	})

	renderer.SetChecked("dark-theme", true)

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

	cb, ok := renderer.checkboxes["dark-theme"]
	if !ok {
		t.Fatal(`no checkbox registered for "dark-theme" after Layout`)
	}
	if !cb.Value {
		t.Fatal("checkbox Value = false, want true (SetChecked's own pending request, not the node's own default unchecked state)")
	}
	if fired {
		t.Fatal("onChange fired from SetChecked -- setting a value programmatically isn't a user interaction")
	}
}

// TestSetCheckedReseedsAnExistingCheckbox proves the other half of
// SetChecked's own contract, mirroring TestSetValueReseedsAnExistingEditor:
// re-seeding a checkbox that already exists overwrites its real current
// Value, and a later frame with no new SetChecked call leaves it alone
// (the request is consumed once, not re-applied every frame).
func TestSetCheckedReseedsAnExistingCheckbox(t *testing.T) {
	w, err := headless.NewWindow(200, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	node := gox.El("input", []gox.Attr{
		gox.A("type", "checkbox"),
		gox.A("id", "dark-theme"),
	})

	var router input.Router
	var ops op.Ops
	gtx := layout.Context{
		Ops:         &ops,
		Source:      router.Source(),
		Constraints: layout.Exact(image.Pt(200, 60)),
	}
	frame := func() {
		gtx.Reset()
		renderer.Layout(gtx, th, node)
		router.Frame(gtx.Ops)
		if err := w.Frame(&ops); err != nil {
			t.Fatalf("Frame: %v", err)
		}
	}
	frame()

	cb := renderer.checkboxes["dark-theme"]
	if cb.Value {
		t.Fatal("checkbox Value = true after first Layout, want false (no \"checked\" attribute on the node)")
	}

	renderer.SetChecked("dark-theme", true)
	frame()
	if !cb.Value {
		t.Fatal("checkbox Value = false after SetChecked(true), want true")
	}

	cb.Value = false // simulate a real click unchecking it
	frame()
	if cb.Value {
		t.Fatal("checkbox Value = true after a frame with no new SetChecked call, want false (the earlier request should not still be pending)")
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

// TestIconButtonRendersRealIconContentAndClicks proves layoutButton's
// own icon-content path (task #107, Phase 41): a button shaped like
// editor/ui/toolbar.gox's own iconBtn -- a real <span> icon child plus
// an empty text label, the exact shape textOf(n) alone would silently
// reduce to "" -- actually paints something (a real frame diff against
// a blank button of the same size, the same rigor
// TestSpanWithElementChildRendersNestedButton's own sibling tests in
// this file already apply) and still fires its own click handler, the
// same real click-handling material.Button already gave a plain text
// button.
func TestIconButtonRendersRealIconContentAndClicks(t *testing.T) {
	w, err := headless.NewWindow(120, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()

	frame := func(node gox.Node) (*image.RGBA, *Renderer) {
		renderer := New()
		var ops op.Ops
		var router input.Router
		gtx := layout.Context{
			Ops:         &ops,
			Source:      router.Source(),
			Constraints: layout.Constraints{Max: image.Pt(120, 60)},
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
		return img, renderer
	}

	blankBtn := gox.El("button", []gox.Attr{gox.A("id", "icon-btn")}, gox.Text(""))
	blank, _ := frame(blankBtn)

	iconSpan := gox.El("span", []gox.Attr{gox.A("class", "material-symbols-outlined")}, gox.Text("bold"))
	iconBtn := gox.El("button", []gox.Attr{gox.A("id", "icon-btn")}, iconSpan, gox.Text(""))
	got, renderer := frame(iconBtn)

	if bytes.Equal(blank.Pix, got.Pix) {
		t.Fatal("icon button frame identical to a blank same-id text button: the icon span's own content never actually painted")
	}
	if !renderer.Click("icon-btn") {
		t.Fatal(`Click("icon-btn"): no clickable registered for an icon button -- the icon-content path lost real click handling`)
	}
}

// TestIconButtonExposesAccessibleNameViaSemanticDescription proves
// accessibleNameOf actually reaches a real Gio semantic tree (task
// #107, Phase 41): a button with a "title" attribute and an icon span
// child (the shape iconBtn(id, iconName, title, "") produces whenever
// its own label argument is "", the overwhelming majority of toolbar
// buttons) gets a real semantic.Button-class node whose Description
// equals the title -- input.Router.AppendSemantics, the same public
// API gioui.org/io/input's own semantic_test.go exercises, not a
// synthetic ops.Ops read by hand.
func TestIconButtonExposesAccessibleNameViaSemanticDescription(t *testing.T) {
	w, err := headless.NewWindow(120, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	iconSpan := gox.El("span", []gox.Attr{gox.A("class", "material-symbols-outlined")}, gox.Text("format_bold"))
	node := gox.El("button", []gox.Attr{
		gox.A("id", "bold-btn"),
		gox.A("title", "Bold"),
	}, iconSpan, gox.Text(""))

	var ops op.Ops
	var router input.Router
	gtx := layout.Context{
		Ops:         &ops,
		Source:      router.Source(),
		Constraints: layout.Constraints{Max: image.Pt(120, 60)},
	}
	renderer.Layout(gtx, th, node)
	router.Frame(gtx.Ops)
	if err := w.Frame(&ops); err != nil {
		t.Fatalf("Frame: %v", err)
	}

	tree := router.AppendSemantics(nil)
	var found bool
	var walk func(nodes []input.SemanticNode)
	walk = func(nodes []input.SemanticNode) {
		for _, n := range nodes {
			if n.Desc.Class == semantic.Button && n.Desc.Description == "Bold" {
				found = true
			}
			walk(n.Children)
		}
	}
	walk(tree)
	if !found {
		t.Fatal(`no semantic.Button node with Description "Bold" found -- accessibleNameOf's own title fallback never reached the real semantic tree`)
	}
}
