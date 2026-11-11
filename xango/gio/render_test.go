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

	got := styled.At(3, 3)
	r, g, b, a := got.RGBA()
	if uint8(r>>8) != want.R || uint8(g>>8) != want.G || uint8(b>>8) != want.B || uint8(a>>8) != want.A {
		t.Fatalf("pixel at (3,3) = %v, want background color %v", got, want)
	}
}

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
		Ops:         &ops,
		Source:      router.Source(),
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

	gtx.Reset()
	afterDims := renderer.Layout(gtx, th, node())
	if afterDims != closedDims {
		t.Fatalf("dimensions after selecting an option = %+v, want %+v (dropdown should have closed again)", afterDims, closedDims)
	}
}

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
