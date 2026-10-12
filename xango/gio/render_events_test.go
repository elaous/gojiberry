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

// TestOnFiresForANodeWithNoAttrHandler proves Renderer.On's ID-based
// fallback actually fires against a node carrying no Attr handler of
// its own -- the exact shape every real OnDesign editor/ui/*.xg
// component takes, since none of them use xango.On (interactivity is
// wired externally by ID instead, matching main.xg's own
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

// TestOnLosesToANodesOwnAttrHandler confirms On is purely additive: a
// node that DOES carry its own Attr handler (xango.On, the mechanism
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
