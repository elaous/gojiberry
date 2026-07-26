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
// Only a minimal tag set is understood -- div, span, p, button, input
// (type=text and type=checkbox), and text/fragment nodes -- since CSS
// classes and inline styles (which is how every other tag's look is
// customized) have no Gio equivalent of their own. div reads one plain
// layout hint, the data-axis attribute ("horizontal" or the default
// "vertical"), since Gio's layout.Flex needs an axis and there's no CSS
// to carry it on a native target; any other attribute is ignored except
// class, which Renderer.Styles (see ClassStyle) can map to real Gio
// painting/layout. Growing beyond this tag set (Textarea, Modal, select
// -- Gio's widget/material has no native dropdown, so that one needs
// real custom-widget work, not just another case here) means adding to
// typeset below, the same way domrt or gox.Render would grow to support
// a new HTML element.
package goxgio

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/elaous/gojiberry/gox"
)

// ClassStyle is one class's worth of Gio-mappable styling: the narrow
// subset of CSS that has a real Gio equivalent -- padding, a background
// fill, a border, a corner radius, and cross-axis flex alignment. Most
// of CSS (the cascade, selectors beyond a flat class name, box-shadow,
// margin collapsing, ...) has no such equivalent at all, so this is
// deliberately not a general CSS-in-Go model: it's exactly the shape a
// build-time generator (or a small, hand-authored table, for a bounded
// class vocabulary like OnDesign's own real classes) can produce from
// real `.class { ... }` CSS rules -- see gojiberry's own mobile-backend
// ROADMAP entry for the fuller design, and cssgen for the generator
// itself. The zero value (all-zero Padding*, transparent Background/
// BorderColor, zero BorderWidth/CornerRadius, Alignment ==
// layout.Start) paints and lays out exactly as an unstyled node already
// did before this type existed, so an empty or missing Renderer.Styles
// entry is a no-op, not an error.
type ClassStyle struct {
	// PaddingY/PaddingX are top+bottom / left+right padding -- CSS's
	// own most common shorthand shape (`padding: Y X`, or `padding: N`
	// for PaddingY == PaddingX == N), not full independent 4-side
	// padding: real CSS asymmetric (top != bottom, or left != right)
	// padding is rare enough in practice that cssgen flags it as an
	// approximation rather than this type growing two more fields for
	// it.
	PaddingY, PaddingX unit.Dp
	Background         color.NRGBA
	BorderColor        color.NRGBA
	BorderWidth        unit.Dp
	CornerRadius       unit.Dp
	// Alignment is the cross-axis alignment for a div's own
	// layout.Flex -- ignored for every other tag, which has no flex
	// children of its own to align.
	Alignment layout.Alignment
}

// Renderer walks gox.Node trees onto a Gio layout.Context. It is not
// safe for concurrent use, matching domrt.Root and gox.Render's own
// single-goroutine assumption.
type Renderer struct {
	clickables map[string]*widget.Clickable
	checkboxes map[string]*widget.Bool
	editors    map[string]*widget.Editor

	// Styles maps a class name (as it appears in a node's "class"
	// attribute, one word) to the styling to apply wherever that class
	// appears. A node with multiple space-separated classes merges
	// every matching entry in attribute order, later classes winning
	// on any field they both set -- the same "last one wins" rule CSS
	// itself uses for equal-specificity single-class selectors. Nil
	// (the default) applies no styling at all, exactly matching this
	// type's behavior before Styles existed.
	Styles map[string]ClassStyle
}

// New constructs a Renderer with no widget state and no class styling;
// the first Layout call populates widget state as interactive elements
// are encountered. Set Styles directly after New to enable class-driven
// styling.
func New() *Renderer {
	return &Renderer{
		clickables: make(map[string]*widget.Clickable),
		checkboxes: make(map[string]*widget.Bool),
		editors:    make(map[string]*widget.Editor),
	}
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
		return r.layoutChildren(gtx, th, n.Children, layout.Vertical, layout.Start)
	case gox.KindRaw:
		// No DOM to inject raw HTML into on a native target; render
		// nothing rather than the markup as literal text.
		return layout.Dimensions{}
	}

	style := r.styleOf(n)
	var content layout.Widget
	switch n.Tag {
	case "div":
		content = func(gtx layout.Context) layout.Dimensions {
			return r.layoutChildren(gtx, th, n.Children, axisOf(n), style.Alignment)
		}
	case "span", "p":
		content = material.Label(th, th.TextSize, textOf(n)).Layout
	case "button":
		content = func(gtx layout.Context) layout.Dimensions {
			return r.layoutButton(gtx, th, n)
		}
	case "input":
		content = func(gtx layout.Context) layout.Dimensions {
			return r.layoutInput(gtx, th, n)
		}
	default:
		// Unrecognized tag: still lay out children so an unsupported
		// wrapper doesn't silently drop its content.
		content = func(gtx layout.Context) layout.Dimensions {
			return r.layoutChildren(gtx, th, n.Children, layout.Vertical, layout.Start)
		}
	}
	return r.styled(gtx, style, content)
}

// classesOf splits n's "class" attribute on whitespace, the same
// multi-class convention a real HTML class attribute uses.
func classesOf(n gox.Node) []string {
	for _, a := range n.Attrs {
		if a.Name == "class" {
			return strings.Fields(a.Value)
		}
	}
	return nil
}

// styleOf merges every class in n's own "class" attribute against
// r.Styles, in attribute order (see Styles' own doc comment for the
// merge rule). A node with no matching class, or a nil Styles map,
// returns the zero ClassStyle -- a guaranteed no-op, see ClassStyle's
// own doc comment.
func (r *Renderer) styleOf(n gox.Node) ClassStyle {
	var merged ClassStyle
	for _, class := range classesOf(n) {
		s, ok := r.Styles[class]
		if !ok {
			continue
		}
		if s.PaddingY != 0 {
			merged.PaddingY = s.PaddingY
		}
		if s.PaddingX != 0 {
			merged.PaddingX = s.PaddingX
		}
		if s.Background != (color.NRGBA{}) {
			merged.Background = s.Background
		}
		if s.BorderColor != (color.NRGBA{}) {
			merged.BorderColor = s.BorderColor
		}
		if s.BorderWidth != 0 {
			merged.BorderWidth = s.BorderWidth
		}
		if s.CornerRadius != 0 {
			merged.CornerRadius = s.CornerRadius
		}
		if s.Alignment != layout.Start {
			merged.Alignment = s.Alignment
		}
	}
	return merged
}

// styled wraps content with style's padding and, if set, a painted
// background/border -- the box-model piece CSS gives every element for
// free and Gio doesn't. content is recorded first (via op.Record) so
// its real laid-out size is known before the background/border is
// painted underneath it, then replayed on top -- the same "measure,
// then paint behind" ordering a CSS background/border already gets for
// free from being drawn before an element's own content in normal flow.
func (r *Renderer) styled(gtx layout.Context, style ClassStyle, content layout.Widget) layout.Dimensions {
	if style == (ClassStyle{}) {
		return content(gtx)
	}
	macro := op.Record(gtx.Ops)
	inset := layout.Inset{Top: style.PaddingY, Bottom: style.PaddingY, Left: style.PaddingX, Right: style.PaddingX}
	dims := inset.Layout(gtx, content)
	call := macro.Stop()

	if style.Background != (color.NRGBA{}) || style.BorderWidth > 0 {
		paintBox(gtx, dims.Size, style)
	}
	call.Add(gtx.Ops)
	return dims
}

// paintBox paints style's background fill and/or border into a size x
// size rectangle at the current origin, corner-radius-aware (a zero
// CornerRadius degenerates clip.RRect to a plain rectangle, so there's
// no separate rect-vs-rounded-rect case to handle).
func paintBox(gtx layout.Context, size image.Point, style ClassStyle) {
	rr := gtx.Dp(style.CornerRadius)
	rect := clip.RRect{
		Rect: image.Rectangle{Max: size},
		SE:   rr, SW: rr, NE: rr, NW: rr,
	}
	if style.Background != (color.NRGBA{}) {
		paint.FillShape(gtx.Ops, style.Background, rect.Op(gtx.Ops))
	}
	if style.BorderWidth > 0 {
		width := float32(gtx.Dp(style.BorderWidth))
		paint.FillShape(gtx.Ops, style.BorderColor, clip.Stroke{Path: rect.Path(gtx.Ops), Width: width}.Op())
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

func (r *Renderer) layoutChildren(gtx layout.Context, th *material.Theme, children []gox.Node, axis layout.Axis, alignment layout.Alignment) layout.Dimensions {
	flex := layout.Flex{Axis: axis, Alignment: alignment}
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

// inputType returns n's "type" attribute, defaulting to "text" the same
// way a real <input> with no type attribute does.
func inputType(n gox.Node) string {
	for _, a := range n.Attrs {
		if a.Name == "type" {
			return a.Value
		}
	}
	return "text"
}

// valueOf returns n's "value" attribute. Only used to seed a newly
// created widget.Editor: unlike domrt (which owns a real, persistent DOM
// node it can read back at any time), goxgio has nothing to read a live
// value from except the widget.Editor itself, so this is a one-time
// initial value, not a live binding -- setting it on every Layout call
// would stomp whatever the user just typed.
func valueOf(n gox.Node) string {
	for _, a := range n.Attrs {
		if a.Name == "value" {
			return a.Value
		}
	}
	return ""
}

// checkedOf reports whether n's "checked" attribute is present, matching
// the standard HTML boolean-attribute convention (presence, not value,
// means true). Same seed-only caveat as valueOf.
func checkedOf(n gox.Node) bool {
	for _, a := range n.Attrs {
		if a.Name == "checked" {
			return true
		}
	}
	return false
}

// placeholderOf returns n's "placeholder" attribute, if any.
func placeholderOf(n gox.Node) string {
	for _, a := range n.Attrs {
		if a.Name == "placeholder" {
			return a.Value
		}
	}
	return ""
}

// changeHandler returns n's onChange handler (the Attr named "change"
// with a non-nil Handler -- see eventNameFor in the transpiler), or nil
// if n has none.
func changeHandler(n gox.Node) gox.EventHandler {
	for _, a := range n.Attrs {
		if a.Name == "change" && a.Handler != nil {
			return a.Handler
		}
	}
	return nil
}

// layoutInput lays out type=checkbox as a material.CheckBox (backed by a
// persisted widget.Bool, the same persist-across-frames need and
// map[string]*T-keyed-by-identity solution layoutButton already uses for
// widget.Clickable) and every other type as a single-line
// material.Editor (backed by a persisted widget.Editor). Both widgets'
// own Update method is called directly first, to detect a change and
// fire onChange, before the material.*Style.Layout call that paints
// it -- that call drains the same widget's Update internally too, but
// finds nothing left unconsumed by then, so it's a harmless no-op; this
// mirrors layoutButton's own Clicked-then-Layout sequencing for the same
// reason (a widget's interaction state must be consumed exactly once
// per frame to fire a handler, but the material wrapper always insists
// on consuming it itself to paint correctly).
func (r *Renderer) layoutInput(gtx layout.Context, th *material.Theme, n gox.Node) layout.Dimensions {
	id := n.Key
	if id == "" {
		id = idOf(n)
	}

	if inputType(n) == "checkbox" {
		cb, ok := r.checkboxes[id]
		if !ok {
			cb = &widget.Bool{Value: checkedOf(n)}
			r.checkboxes[id] = cb
		}
		if cb.Update(gtx) {
			if handler := changeHandler(n); handler != nil {
				handler(gox.NewEvent("change", cb.Value, cb.Value, nil, nil))
			}
		}
		return material.CheckBox(th, cb, "").Layout(gtx)
	}

	ed, ok := r.editors[id]
	if !ok {
		ed = &widget.Editor{SingleLine: true}
		ed.SetText(valueOf(n))
		r.editors[id] = ed
	}
	changed := false
	for {
		ev, ok := ed.Update(gtx)
		if !ok {
			break
		}
		if _, isChange := ev.(widget.ChangeEvent); isChange {
			changed = true
		}
	}
	if changed {
		if handler := changeHandler(n); handler != nil {
			handler(gox.NewEvent("change", ed.Text(), false, nil, nil))
		}
	}
	return material.Editor(th, ed, placeholderOf(n)).Layout(gtx)
}
