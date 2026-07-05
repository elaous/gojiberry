// Package goxgio is a third gox.Node renderer, alongside gox.Render
// (server-side HTML, see gox/render.go) and domrt.Mount/Update
// (client-side DOM patching, see ../../../domrt/domrt.go): it walks the
// exact same renderer-agnostic tree onto a real Gio window
// (gioui.org), so a native desktop/mobile target is a genuinely new
// consumer of gox.Node rather than a rewrite of it.
//
// Two things distinguish this walk from the other two renderers, both
// forced by Gio being an immediate-mode toolkit rather than a retained
// DOM:
//
//   - No differ. domrt keeps the previous tree around to compute a
//     minimal patch; Gio repaints everything every frame anyway, so
//     Renderer.Layout is a plain recursive walk, structurally closer to
//     gox.Render than to domrt's patch algorithm.
//   - Interactive-widget identity. A Gio widget.Clickable must persist
//     across frames (Gio reads its accumulated click state off the
//     struct itself), but the Node tree is rebuilt from scratch on
//     every render call. Renderer solves this with a
//     map[string]*widget.Clickable keyed by identity: Node.Key if set
//     (the same field domrt already uses for keyed-list
//     reconciliation), else the element's "id" attribute -- id is the
//     more common case for a single static control like a lone button,
//     since .gox's tag syntax has no inline way to chain .WithKey onto
//     an element the way hand-written Go can. A button with neither
//     Key nor id shares Gio's zero-value "" identity with every other
//     such button and will misattribute clicks between them; callers
//     targeting Gio should give every interactive element one or the
//     other.
//
// Only a minimal tag set is understood -- div, span, p, button, and
// text/fragment nodes -- since CSS classes and inline styles (which is
// how every other tag's look is customized) have no Gio equivalent.
// div reads one layout hint, the data-axis attribute ("horizontal" or
// the default "vertical"), since Gio's layout.Flex needs an axis and
// there's no CSS to carry it on a native target; any other attribute is
// ignored. Growing beyond this set (Textarea, Modal, ...) means adding
// to typeset below, the same way domrt or gox.Render would grow to
// support a new HTML element.
package goxgio

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/elaous/gojiberry/gox"
)

// Renderer walks gox.Node trees onto a Gio layout.Context. It is not
// safe for concurrent use, matching domrt.Root and gox.Render's own
// single-goroutine assumption.
type Renderer struct {
	clickables map[string]*widget.Clickable
}

// New constructs a Renderer with no widget state; the first Layout
// call populates it as interactive elements are encountered.
func New() *Renderer {
	return &Renderer{clickables: make(map[string]*widget.Clickable)}
}

// Layout walks n and lays it out via th, returning its dimensions. Call
// this once per Gio frame with a freshly rendered n (the same
// "render(state) -> gox.Node" shape every other gox consumer uses), not
// with a diffed/patched tree -- there's nothing to patch here.
func (r *Renderer) Layout(gtx layout.Context, th *material.Theme, n gox.Node) layout.Dimensions {
	switch n.Kind {
	case gox.KindText:
		return material.Label(th, th.TextSize, n.Text).Layout(gtx)
	case gox.KindFragment:
		return r.layoutChildren(gtx, th, n.Children, layout.Vertical)
	case gox.KindRaw:
		// No DOM to inject raw HTML into on a native target; render
		// nothing rather than the markup as literal text.
		return layout.Dimensions{}
	}
	switch n.Tag {
	case "div":
		return r.layoutChildren(gtx, th, n.Children, axisOf(n))
	case "span", "p":
		return material.Label(th, th.TextSize, textOf(n)).Layout(gtx)
	case "button":
		return r.layoutButton(gtx, th, n)
	default:
		// Unrecognized tag: still lay out children so an unsupported
		// wrapper doesn't silently drop its content.
		return r.layoutChildren(gtx, th, n.Children, layout.Vertical)
	}
}

// idOf returns n's "id" attribute, the widget-identity fallback used
// when Node.Key is empty (see Renderer's doc comment).
func idOf(n gox.Node) string {
	for _, a := range n.Attrs {
		if a.Name == "id" {
			return a.Value
		}
	}
	return ""
}

func axisOf(n gox.Node) layout.Axis {
	for _, a := range n.Attrs {
		if a.Name == "data-axis" && a.Value == "horizontal" {
			return layout.Horizontal
		}
	}
	return layout.Vertical
}

// textOf concatenates n's direct KindText children, since a gox.El
// call like <span>{strconv.Itoa(props.Count)}</span> produces a single
// text child, not a Text field on the span itself.
func textOf(n gox.Node) string {
	var b strings.Builder
	for _, c := range n.Children {
		if c.Kind == gox.KindText {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

func (r *Renderer) layoutChildren(gtx layout.Context, th *material.Theme, children []gox.Node, axis layout.Axis) layout.Dimensions {
	flex := layout.Flex{Axis: axis}
	flexChildren := make([]layout.FlexChild, 0, len(children))
	for _, c := range children {
		c := c
		flexChildren = append(flexChildren, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return r.Layout(gtx, th, c)
		}))
	}
	return flex.Layout(gtx, flexChildren...)
}

// clickHandler returns n's onClick handler (the Attr named "click" with
// a non-nil Handler, exactly as gox.On("click", h) and domrt both treat
// it -- see transpile.go's eventNameFor and domrt.go's setListener), or
// nil if n has none.
func clickHandler(n gox.Node) gox.EventHandler {
	for _, a := range n.Attrs {
		if a.Name == "click" && a.Handler != nil {
			return a.Handler
		}
	}
	return nil
}

// Click simulates a real click on the interactive element identified
// by id (Node.Key if set, else its "id" attribute -- see this type's
// doc comment) via Gio's own widget.Clickable.Click, the same
// mechanism a caller would use to fire a button's handler from a
// keyboard shortcut rather than a pointer event. It reports whether an
// element with that identity has been laid out at least once (and so
// has a Clickable to click); the click itself only takes effect the
// next time Layout walks a tree containing that identity, exactly like
// a real pointer click.
func (r *Renderer) Click(id string) bool {
	clk, ok := r.clickables[id]
	if !ok {
		return false
	}
	clk.Click()
	return true
}

func (r *Renderer) layoutButton(gtx layout.Context, th *material.Theme, n gox.Node) layout.Dimensions {
	id := n.Key
	if id == "" {
		id = idOf(n)
	}
	clk, ok := r.clickables[id]
	if !ok {
		clk = new(widget.Clickable)
		r.clickables[id] = clk
	}
	if handler := clickHandler(n); handler != nil {
		for clk.Clicked(gtx) {
			handler(gox.NewEvent("click", nil, false, nil, nil))
		}
	}
	return material.Button(th, clk, textOf(n)).Layout(gtx)
}
