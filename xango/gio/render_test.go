package gio

import (
	"bytes"
	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget/material"
	"github.com/elaous/gojiberry/xango"
	"image"
	"image/color"
	"strings"
	"testing"
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
	node := xango.El("input", []xango.Attr{
		xango.A("type", "checkbox"),
		xango.A("id", "agree"),
		xango.On("change", func(e xango.Event) {
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
	node := xango.El("input", []xango.Attr{
		xango.A("type", "text"),
		xango.A("id", "name"),
		xango.On("change", func(e xango.Event) {
			fired = true
			lastValue = xango.EventValue[string](e)
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
	node := xango.El("textarea", []xango.Attr{
		xango.A("id", "comment-text"),
		xango.On("change", func(e xango.Event) {
			fired = true
			lastValue = xango.EventValue[string](e)
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
		node := xango.El(tag, []xango.Attr{
			xango.A("id", "field"),
			xango.On("change", func(e xango.Event) { last = xango.EventValue[string](e) }),
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
	node := xango.El("div", []xango.Attr{xango.A("class", "panel")},
		xango.El("span", nil, xango.Text("hi")),
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
// equivalent for, so xango/gio has to build its own dropdown rather than
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
	node := func() xango.Node {
		opt := func(value, label string) xango.Node {
			attrs := []xango.Attr{xango.A("value", value)}
			if value == selected {
				attrs = append(attrs, xango.A("selected", "selected"))
			}
			return xango.El("option", attrs, xango.Text(label))
		}
		return xango.El("select", []xango.Attr{
			xango.A("id", "color"),
			xango.On("change", func(e xango.Event) {
				fired = true
				lastValue = xango.EventValue[string](e)
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

// TestSpanWithElementChildrenLaysOutThoseChildren covers a real bug
// found while wiring OnDesign's own toolbar.xg on native: a <span>
// (or <p>) whose children include a real element, not just text --
// toolbar.xg's own "font-picker-wrap" span wraps a button plus a
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
	node := xango.El("span", []xango.Attr{xango.A("class", "wrap")},
		xango.El("button", []xango.Attr{xango.A("id", "inner")}, xango.Text("Click me")),
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
// editor/ui/toolbar.xg's own iconBtn -- a real <span> icon child plus
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

	frame := func(node xango.Node) (*image.RGBA, *Renderer) {
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

	blankBtn := xango.El("button", []xango.Attr{xango.A("id", "icon-btn")}, xango.Text(""))
	blank, _ := frame(blankBtn)

	iconSpan := xango.El("span", []xango.Attr{xango.A("class", "material-symbols-outlined")}, xango.Text("bold"))
	iconBtn := xango.El("button", []xango.Attr{xango.A("id", "icon-btn")}, iconSpan, xango.Text(""))
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
	iconSpan := xango.El("span", []xango.Attr{xango.A("class", "material-symbols-outlined")}, xango.Text("format_bold"))
	node := xango.El("button", []xango.Attr{
		xango.A("id", "bold-btn"),
		xango.A("title", "Bold"),
	}, iconSpan, xango.Text(""))

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
