package gio

import (
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget/material"
	"github.com/elaous/gojiberry/xango"
	"image"
	"testing"
)

func TestOnFiresForANodeWithNoAttrHandler(t *testing.T) {
	w, err := headless.NewWindow(60, 30)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	node := xango.El("button", []xango.Attr{xango.A("id", "close")}, xango.Text("×"))

	var fired bool
	renderer.On("close", "click", func(xango.Event) { fired = true })

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

func TestOnLosesToANodesOwnAttrHandler(t *testing.T) {
	w, err := headless.NewWindow(60, 30)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	var attrFired, registeredFired bool
	node := xango.El("button", []xango.Attr{
		xango.A("id", "dec"),
		xango.On("click", func(xango.Event) { attrFired = true }),
	}, xango.Text("-"))
	renderer.On("dec", "click", func(xango.Event) { registeredFired = true })

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

func TestDivWithClickHandlerFiresThroughRealClick(t *testing.T) {
	w, err := headless.NewWindow(200, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	node := xango.El("div", []xango.Attr{xango.A("id", "comment-text-c1")},
		xango.Text("hi "),
		xango.El("span", []xango.Attr{xango.A("class", "comment-mention")}, xango.Text("@sam")),
	)

	var fired bool
	renderer.On("comment-text-c1", "click", func(xango.Event) { fired = true })

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

func TestDivWithNoClickHandlerStaysPlain(t *testing.T) {
	w, err := headless.NewWindow(200, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	node := xango.El("div", []xango.Attr{xango.A("id", "plain")}, xango.Text("just text"))

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

func TestSetValueSeedsBeforeFirstLayout(t *testing.T) {
	w, err := headless.NewWindow(200, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	node := xango.El("input", []xango.Attr{
		xango.A("type", "text"),
		xango.A("id", "margin-top"),
		xango.A("value", "1.0"),
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

func TestSetValueReseedsAnExistingEditor(t *testing.T) {
	w, err := headless.NewWindow(200, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	node := xango.El("input", []xango.Attr{
		xango.A("type", "text"),
		xango.A("id", "margin-top"),
		xango.A("value", "1.0"),
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

	ed.SetText("something the user typed")
	frame()
	if got, want := ed.Text(), "something the user typed"; got != want {
		t.Fatalf("editor text after a frame with no new SetValue = %q, want %q (the earlier request should not still be pending)", got, want)
	}
}

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
	node := xango.El("input", []xango.Attr{
		xango.A("type", "checkbox"),
		xango.A("id", "agree"),
		xango.On("change", func(e xango.Event) {
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

func TestSetCheckedSeedsBeforeFirstLayoutAndDoesNotFireOnChange(t *testing.T) {
	w, err := headless.NewWindow(200, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	var fired bool
	node := xango.El("input", []xango.Attr{
		xango.A("type", "checkbox"),
		xango.A("id", "dark-theme"),
		xango.On("change", func(xango.Event) { fired = true }),
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

func TestSetCheckedReseedsAnExistingCheckbox(t *testing.T) {
	w, err := headless.NewWindow(200, 60)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	renderer := New()
	node := xango.El("input", []xango.Attr{
		xango.A("type", "checkbox"),
		xango.A("id", "dark-theme"),
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
