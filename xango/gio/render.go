// Package gio is a third xango.Node renderer, alongside xango.Render
// (server-side HTML, see xango/render.go) and domrt.Mount/Update
// (client-side DOM patching, see ../../../domrt/domrt.go): it walks the
// exact same renderer-agnostic tree onto a real Gio window
// (gioui.org), so a native desktop/mobile target is a genuinely new
// consumer of xango.Node rather than a rewrite of it.
//
// Two things distinguish this walk from the other two renderers, both
// forced by Gio being an immediate-mode toolkit rather than a retained
// DOM:
//
//   - No differ. domrt keeps the previous tree around to compute a
//     minimal patch; Gio repaints everything every frame anyway, so
//     Renderer.Layout is a plain recursive walk, structurally closer to
//     xango.Render than to domrt's patch algorithm.
//   - Interactive-widget identity. A Gio widget.Clickable must persist
//     across frames (Gio reads its accumulated click state off the
//     struct itself), but the Node tree is rebuilt from scratch on
//     every render call. Renderer solves this with a
//     map[string]*widget.Clickable keyed by identity: Node.Key if set
//     (the same field domrt already uses for keyed-list
//     reconciliation), else the element's "id" attribute -- id is the
//     more common case for a single static control like a lone button,
//     since .xg's tag syntax has no inline way to chain .WithKey onto
//     an element the way hand-written Go can. A button with neither
//     Key nor id shares Gio's zero-value "" identity with every other
//     such button and will misattribute clicks between them; callers
//     targeting Gio should give every interactive element one or the
//     other.
//
// Only a minimal tag set is understood -- div, span, p, button, input
// (type=text and type=checkbox), textarea, select/option, and
// text/fragment nodes -- since CSS classes and inline styles (which is
// how every other tag's look is customized) have no Gio equivalent of
// their own. div reads one plain layout hint, the data-axis attribute
// ("horizontal" or the default "vertical"), since Gio's layout.Flex
// needs an axis and there's no CSS to carry it on a native target; any
// other attribute is ignored except class, which Renderer.Styles (see
// ClassStyle) can map to real Gio painting/layout, and a real click
// handler (its own Attr, or one registered via Renderer.On), which
// wraps its content in a persisted widget.Clickable the same way
// button already does -- the closest native equivalent to a real DOM
// div with its own addEventListener("click", ...) (e.g. comment.go's
// own #comment-item-<id> text div, task #139's sidebar->canvas
// click-to-jump), rather than forcing every clickable region on a
// native target to be a real <button>. A div with no click handler at
// all takes the identical zero-cost path it always did (no Clickable
// ever created for it), so this adds no overhead to the overwhelming
// majority of divs in any real tree. textarea shares
// input's own layoutInput plumbing (persisted widget.Editor, Focus/
// SetValue/change-event support) with SingleLine forced false instead
// of true -- the only real difference a plain multi-line comment body
// needs; a real HTML textarea's *rows*/*cols* sizing hint has no Gio
// equivalent and is ignored, same as every other unmapped attribute
// here. Growing beyond this tag set (Modal, ...) means adding to
// typeset below, the same way domrt or xango.Render would grow to
// support a new HTML element.
//
// Two ways to wire interactivity are supported, because real xango
// producers use both: a node's own Attr handler (xango.On("click", h),
// read by clickHandler/changeHandler -- what gio-counter-demo's own
// Counter uses), and Renderer.On(id, event, fn), an ID-based registry
// checked as a fallback when a node has no Attr handler of its own.
// The latter exists because OnDesign's real editor/ui/*.xg components
// use neither xango.On nor any prop for interactivity: main.xg's own
// wire() attaches every handler externally, by element ID
// (ed.on(id, event, fn) -> a real DOM addEventListener), the same
// delegation pattern domrt itself is built on. On lets a caller
// replicate that same wiring against a xango.Node tree it doesn't
// control the construction of, with zero changes to the component.
package gio

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/elaous/gojiberry/xango"
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

// Renderer walks xango.Node trees onto a Gio layout.Context. It is not
// safe for concurrent use, matching domrt.Root and xango.Render's own
// single-goroutine assumption.
type Renderer struct {
	clickables map[string]*widget.Clickable
	checkboxes map[string]*widget.Bool
	editors    map[string]*widget.Editor
	// selectOpen tracks whether a <select>'s own dropdown is currently
	// expanded, keyed the same identity way as clickables/checkboxes/
	// editors. Unlike those, it isn't backed by a Gio widget type at
	// all -- see layoutSelect's own doc comment for why a plain
	// map[string]bool is the right shape here.
	selectOpen map[string]bool

	// focusRequests tracks ids whose editor should receive real Gio
	// input focus the next time Layout renders them -- see Focus's own
	// doc comment for why this exists (a pending flag consumed and
	// cleared by layoutInput, the same deferred-then-consumed shape
	// selectOpen and a Clickable's own accumulated click state already
	// take).
	focusRequests map[string]bool

	// toggleRequests tracks ids whose checkbox should flip its Value
	// the next time Layout renders it -- see Toggle's own doc comment
	// for why this exists (widget.Bool, unlike widget.Clickable, has
	// no public Click-equivalent shortcut of its own).
	toggleRequests map[string]bool

	// valueRequests tracks ids whose editor should have its text
	// overwritten the next time Layout renders it -- see SetValue's own
	// doc comment for why this exists (layoutInput only ever seeds a
	// widget.Editor's text once, from the node's own "value" attribute,
	// at first creation; there is no other path for a caller to re-seed
	// it later, e.g. when a panel with a text input showing a document's
	// current state is closed and reopened after that state changed).
	valueRequests map[string]string

	// checkedRequests is valueRequests' own checkbox counterpart -- see
	// SetChecked's own doc comment. A separate map, not a shared one
	// keyed the same way valueRequests is, since a checkbox and a text
	// input can't collide on identity in practice (each tag has its own
	// widget map already) but keeping the two concerns in two maps
	// avoids ever needing to encode "this pending request is a bool, not
	// a string" in a single value's own type.
	checkedRequests map[string]bool

	// handlers holds ID-based event handlers registered via On, keyed
	// by "<id>\x00<event>" (the same flat key shape layoutSelect
	// already uses for "<select id>\x00<option value>"). See On's own
	// doc comment for why this exists alongside xango.On/Attr.Handler
	// rather than replacing it.
	handlers map[string]xango.EventHandler

	// Styles maps a class name (as it appears in a node's "class"
	// attribute, one word) to the styling to apply wherever that class
	// appears. A node with multiple space-separated classes merges
	// every matching entry in attribute order, later classes winning
	// on any field they both set -- the same "last one wins" rule CSS
	// itself uses for equal-specificity single-class selectors. Nil
	// (the default) applies no styling at all, exactly matching this
	// type's behavior before Styles existed.
	Styles map[string]ClassStyle

	// IconFont, if set, lets a text-only "span"/"p" carrying its own
	// Class translate its literal text (an icon name, e.g.
	// iconSpan's own "format_bold") into a single glyph painted with
	// Typeface instead of painting that name as literal prose -- see
	// IconFont's own doc comment for why this exists and how it
	// degrades. Nil (the default) applies no translation at all,
	// exactly matching this type's behavior before IconFont existed.
	IconFont *IconFont
}

// New constructs a Renderer with no widget state and no class styling;
// the first Layout call populates widget state as interactive elements
// are encountered. Set Styles directly after New to enable class-driven
// styling.
func New() *Renderer {
	return &Renderer{
		clickables:      make(map[string]*widget.Clickable),
		checkboxes:      make(map[string]*widget.Bool),
		editors:         make(map[string]*widget.Editor),
		selectOpen:      make(map[string]bool),
		focusRequests:   make(map[string]bool),
		toggleRequests:  make(map[string]bool),
		valueRequests:   make(map[string]string),
		checkedRequests: make(map[string]bool),
		handlers:        make(map[string]xango.EventHandler),
	}
}

// Toggle requests that the checkbox identified by id (Node.Key if set,
// else its own "id" attribute) flip its Value the next time Layout
// renders it, firing its onChange the same way a real click would --
// the programmatic-click analogue Click already gives a
// widget.Clickable, needed here because widget.Bool exposes no public
// Click-equivalent shortcut of its own (its click detection is
// internal, via an unexported widget.Clickable it doesn't expose).
// Reports whether a checkbox with that identity has been laid out at
// least once; if not, id is not remembered, matching Click's own
// "nothing registered yet" contract.
func (r *Renderer) Toggle(id string) bool {
	if _, ok := r.checkboxes[id]; !ok {
		return false
	}
	r.toggleRequests[id] = true
	return true
}

// SetValue requests that the text input identified by id (Node.Key if
// set, else its own "id" attribute) have its displayed text overwritten
// with value the next time Layout renders it -- the missing re-seed
// capability layoutInput's own doc comment describes: a widget.Editor
// is only ever seeded once, from the node's own "value" attribute, at
// first creation, since Gio's own immediate-mode model has no notion of
// "this attribute changed since last frame" the way a retained-DOM
// renderer's own diff would. A caller whose text input shows a live
// snapshot of some other state (e.g. a document's current margins,
// re-read fresh each time a panel showing them is reopened) needs an
// explicit way to push a new value in, since simply rendering a tree
// with a different "value" attribute a second time has no effect once
// the underlying widget.Editor already exists.
//
// Unlike Focus/Toggle, SetValue does not require the target to already
// have been laid out at least once: the request is queued regardless,
// and applied by layoutInput whether it is seeding a brand new editor
// or re-seeding an existing one, so a caller can call this from the
// very same handler that first makes a panel visible without hitting
// Focus's own same-frame limitation (see that method's own doc
// comment). Because of that, SetValue has no boolean "was already
// registered" return value the way Focus/Toggle do -- there is nothing
// to report that a caller couldn't already know.
func (r *Renderer) SetValue(id, value string) {
	r.valueRequests[id] = value
}

// SetChecked is SetValue's own checkbox counterpart: requests that the
// checkbox identified by id (Node.Key if set, else its own "id"
// attribute) have its Value overwritten with checked the next time
// Layout renders it -- layoutInput's checkbox branch has the identical
// seed-once-at-creation limitation SetValue's own doc comment describes
// for a text input's widget.Editor, just for widget.Bool.Value instead
// of Editor text, and Toggle (unlike this) only ever flips whatever the
// current value already is, which is no help when a caller needs to
// set a *specific* value read from elsewhere (e.g. a settings panel
// reopened after its underlying config changed). Same "works even
// before the target has ever been laid out" contract as SetValue, for
// the identical reason.
func (r *Renderer) SetChecked(id string, checked bool) {
	r.checkedRequests[id] = checked
}

// Focus requests that the text input identified by id (Node.Key if
// set, else its own "id" attribute) receive real Gio input focus the
// next time Layout renders it -- the programmatic-focus analogue of
// Click, for a caller that wants to simulate focusing a text field
// without knowing its exact on-screen position first. Gio's own focus
// mechanism (key.FocusCmd) otherwise only ever fires from a real
// pointer.Event hitting the widget's own laid-out bounds, which means
// knowing exactly where those bounds fall inside a caller's own
// layout -- trivial for a single isolated input, much less so for one
// buried inside a larger tree like a real editor/ui/*.xg panel.
// Reports whether an editor with that identity has been laid out at
// least once (and so has something to focus); if not, id is not
// remembered, matching Click's own "nothing registered yet" contract.
func (r *Renderer) Focus(id string) bool {
	if _, ok := r.editors[id]; !ok {
		return false
	}
	r.focusRequests[id] = true
	return true
}

// EditorTag returns the event.Tag identifying the input/textarea
// element with the given id -- the same *widget.Editor Layout already
// uses internally to register that element's own focus/key handling
// with Gio -- so a caller can register its own key.Filter{Focus: tag,
// ...} to observe named key.Event while that element holds focus
// (Escape to close a panel, Enter to submit, Alt+letter shortcuts,
// ...), the identical capability Focus above gives a caller over
// *sending* focus, just for *observing* keys once it's there. Reports
// whether an editor with that identity has been laid out at least
// once, the same "nothing registered yet" contract Focus/Click already
// establish -- a caller must therefore tolerate ok=false on the very
// frame the element's own container first becomes visible, trying
// again on a later frame once Layout has actually run for it once.
func (r *Renderer) EditorTag(id string) (event.Tag, bool) {
	ed, ok := r.editors[id]
	if !ok {
		return nil, false
	}
	return ed, true
}

// On registers fn to fire when event ("click" or "change") occurs on
// the element identified by id (Node.Key if set, else its own "id"
// attribute -- the same identity every other interactive tag here
// uses). Registering the same id+event again replaces the previous
// handler.
//
// This exists alongside xango.On/Attr.Handler (the per-node handler
// clickHandler/changeHandler already read, and the only mechanism
// gio-counter-demo's own Counter uses) rather than replacing it,
// because real OnDesign editor/ui/*.xg components use neither: their
// interactivity is wired entirely externally, by ID, in main.xg's own
// wire() (ed.on(id, event, fn) -> element(id).AddEventListener(...)),
// the same DOM-delegation pattern domrt itself is built on. A node's
// own Attr handler (if any) always takes precedence over a handler
// registered here, so gio-counter-demo's existing per-node wiring is
// unaffected; On is purely additive, for callers whose tree has
// nothing attached at the node itself.
func (r *Renderer) On(id, event string, fn xango.EventHandler) {
	r.handlers[id+"\x00"+event] = fn
}

// handlerFor returns the handler registered via On for id+event, or
// nil if none was.
func (r *Renderer) handlerFor(id, event string) xango.EventHandler {
	return r.handlers[id+"\x00"+event]
}

// Layout walks n and lays it out via th, returning its dimensions. Call
// this once per Gio frame with a freshly rendered n (the same
// "render(state) -> xango.Node" shape every other xango consumer uses), not
// with a diffed/patched tree -- there's nothing to patch here.
func (r *Renderer) Layout(gtx layout.Context, th *material.Theme, n xango.Node) layout.Dimensions {
	switch n.Kind {
	case xango.KindText:
		return material.Label(th, th.TextSize, n.Text).Layout(gtx)
	case xango.KindFragment:
		return r.layoutChildren(gtx, th, n.Children, layout.Vertical, layout.Start)
	case xango.KindHost:
		return layout.Dimensions{}
	case xango.KindRaw:
		// No DOM to inject raw HTML into on a native target; render
		// nothing rather than the markup as literal text.
		return layout.Dimensions{}
	}

	style := r.styleOf(n)
	var content layout.Widget
	switch n.Tag {
	case "div":
		content = func(gtx layout.Context) layout.Dimensions {
			return r.layoutDivContent(gtx, th, n, style.Alignment)
		}
	case "span", "p":
		if hasElementChild(n) {
			// A span/p wrapping real element children (e.g. toolbar.xg's
			// own "font-picker-wrap" span, a button plus a hidden div),
			// not just text -- textOf/material.Label would silently
			// discard those children entirely (textOf only concatenates
			// KindText children), so anything interactive inside them
			// would never get laid out, and so never register a
			// Clickable/etc. of its own. Falls back to the same
			// layoutChildren div itself uses, so those children render
			// (and stay interactive) same as if they were one level up
			// in a div instead.
			content = func(gtx layout.Context) layout.Dimensions {
				return r.layoutChildren(gtx, th, n.Children, axisOf(n), style.Alignment)
			}
		} else {
			text := textOf(n)
			lbl := material.Label(th, th.TextSize, text)
			if r.IconFont != nil && hasClass(n, r.IconFont.Class) {
				if glyph, ok := r.IconFont.Translate(text); ok {
					lbl = material.Label(th, th.TextSize, string(glyph))
					lbl.Font.Typeface = r.IconFont.Typeface
				}
			}
			content = lbl.Layout
		}
	case "button":
		content = func(gtx layout.Context) layout.Dimensions {
			return r.layoutButton(gtx, th, n)
		}
	case "input", "textarea":
		content = func(gtx layout.Context) layout.Dimensions {
			return r.layoutInput(gtx, th, n)
		}
	case "select":
		content = func(gtx layout.Context) layout.Dimensions {
			return r.layoutSelect(gtx, th, n)
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
func classesOf(n xango.Node) []string {
	for _, a := range n.Attrs {
		if a.Name == "class" {
			return strings.Fields(a.Value)
		}
	}
	return nil
}

// hasClass reports whether n's own "class" attribute contains class as
// one of its space-separated words.
func hasClass(n xango.Node, class string) bool {
	for _, c := range classesOf(n) {
		if c == class {
			return true
		}
	}
	return false
}

// styleOf merges every class in n's own "class" attribute against
// r.Styles, in attribute order (see Styles' own doc comment for the
// merge rule). A node with no matching class, or a nil Styles map,
// returns the zero ClassStyle -- a guaranteed no-op, see ClassStyle's
// own doc comment.
func (r *Renderer) styleOf(n xango.Node) ClassStyle {
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
func idOf(n xango.Node) string {
	for _, a := range n.Attrs {
		if a.Name == "id" {
			return a.Value
		}
	}
	return ""
}

func axisOf(n xango.Node) layout.Axis {
	for _, a := range n.Attrs {
		if a.Name == "data-axis" && a.Value == "horizontal" {
			return layout.Horizontal
		}
	}
	return layout.Vertical
}

// hasElementChild reports whether n has any direct child that isn't a
// text node -- see the "span", "p" case in Layout for why this matters:
// textOf silently drops non-text children, so a span/p that has any
// needs the layoutChildren path instead, or they'd never be laid out
// (and, if interactive, never register a Clickable/etc. of their own)
// at all.
func hasElementChild(n xango.Node) bool {
	for _, c := range n.Children {
		if c.Kind != xango.KindText {
			return true
		}
	}
	return false
}

// textOf concatenates n's direct KindText children, since a xango.El
// call like <span>{strconv.Itoa(props.Count)}</span> produces a single
// text child, not a Text field on the span itself.
func textOf(n xango.Node) string {
	var b strings.Builder
	for _, c := range n.Children {
		if c.Kind == xango.KindText {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

func (r *Renderer) layoutChildren(gtx layout.Context, th *material.Theme, children []xango.Node, axis layout.Axis, alignment layout.Alignment) layout.Dimensions {
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
// a non-nil Handler, exactly as xango.On("click", h) and domrt both treat
// it -- see transpile.go's eventNameFor and domrt.go's setListener), or
// nil if n has none.
func clickHandler(n xango.Node) xango.EventHandler {
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

// layoutDivContent lays out a div's own children (the plain
// layoutChildren path every div always took), wrapped in a persisted
// widget.Clickable -- consumed the same drain-then-paint way
// layoutButton's own Clickable already is -- when the div carries a
// real click handler, so a structural div can be a genuine click
// target too, not just button (see this file's own package doc
// comment for why: a real DOM div with its own addEventListener is a
// common shape real editor/ui/*.xg components use, e.g. comment.go's
// own #comment-item-<id> text div). A div with no click handler at all
// returns exactly what layoutChildren always did, with no Clickable
// ever created for it.
func (r *Renderer) layoutDivContent(gtx layout.Context, th *material.Theme, n xango.Node, alignment layout.Alignment) layout.Dimensions {
	children := func(gtx layout.Context) layout.Dimensions {
		return r.layoutChildren(gtx, th, n.Children, axisOf(n), alignment)
	}
	id := n.Key
	if id == "" {
		id = idOf(n)
	}
	handler := clickHandler(n)
	if handler == nil {
		handler = r.handlerFor(id, "click")
	}
	if handler == nil {
		return children(gtx)
	}
	clk, ok := r.clickables[id]
	if !ok {
		clk = new(widget.Clickable)
		r.clickables[id] = clk
	}
	for clk.Clicked(gtx) {
		handler(xango.NewEvent("click", nil, false, nil, nil))
	}
	return clk.Layout(gtx, children)
}

// buttonInset mirrors material.Button's own hardcoded ButtonStyle.Inset
// (gioui.org/widget/material's own button.go) -- layoutButton's own
// icon-content path (below) builds on material.ButtonLayoutStyle
// instead of material.Button (ButtonLayoutStyle has no padding of its
// own, just chrome+click), so it re-applies the identical padding by
// hand to keep an icon button's own visual size/spacing matching a
// plain text button's.
var buttonInset = layout.Inset{Top: 10, Bottom: 10, Left: 12, Right: 12}

// accessibleNameOf returns n's "aria-label" attribute, falling back to
// "title" -- the same accessible-name precedence order
// editor/ui/toolbar.xg's own iconBtn/iconBtnNode doc comments already
// establish for the web build ("title alone is an inconsistently-
// honored accessible-name source across screen readers"): a real
// screen reader's own accessible name for an icon-only button, whose
// real visible content carries no useful text of its own for
// textOf/material.Label to expose.
func accessibleNameOf(n xango.Node) string {
	var title string
	for _, a := range n.Attrs {
		switch a.Name {
		case "aria-label":
			return a.Value
		case "title":
			title = a.Value
		}
	}
	return title
}

// layoutButton renders n as a real material.Button -- textOf(n)'s own
// direct-text-children concatenation is enough for a plain text button
// (btn(), editor/ui/toolbar.xg), but silently drops any real element
// child the same way the "span"/"p" cases' own hasElementChild check
// already guards against elsewhere in this file: an icon button
// (iconBtn(id, iconName, title, label), toolbar.xg) is
// <button><span class="material-symbols-outlined">iconName</span>
// label</button> -- an icon span child plus (usually empty) label
// text, so textOf(n) alone silently painted nothing for the icon at
// all, and exposed no real accessible name either (an icon-only
// button's own label text is "", so material.Button's Text is "" too,
// and Gio's own semantic.LabelOp((""))  carries nothing a screen
// reader would announce). Fixed the same way this file already fixes
// the analogous span/p gap: when n has a real element child, render it
// for real via layoutChildren (so the icon actually paints) inside
// material.ButtonLayoutStyle's own chrome/click (not material.Button,
// which only ever paints its own Text field) -- plus a real
// semantic.DescriptionOp from n's own title/aria-label (accessibleNameOf),
// mirroring material.IconButtonStyle's own Description field
// (gioui.org/widget/material's own button.go), since Description, not
// Label, is the real Gio convention for a control whose accessible name
// comes from something other than its own painted text.
func (r *Renderer) layoutButton(gtx layout.Context, th *material.Theme, n xango.Node) layout.Dimensions {
	id := n.Key
	if id == "" {
		id = idOf(n)
	}
	clk, ok := r.clickables[id]
	if !ok {
		clk = new(widget.Clickable)
		r.clickables[id] = clk
	}
	handler := clickHandler(n)
	if handler == nil {
		handler = r.handlerFor(id, "click")
	}
	if handler != nil {
		for clk.Clicked(gtx) {
			handler(xango.NewEvent("click", nil, false, nil, nil))
		}
	}
	if !hasElementChild(n) {
		return material.Button(th, clk, textOf(n)).Layout(gtx)
	}
	return material.ButtonLayoutStyle{
		Background:   th.Palette.ContrastBg,
		CornerRadius: 4,
		Button:       clk,
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return buttonInset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if name := accessibleNameOf(n); name != "" {
				semantic.DescriptionOp(name).Add(gtx.Ops)
			}
			return r.layoutChildren(gtx, th, n.Children, layout.Horizontal, layout.Middle)
		})
	})
}

// inputType returns n's "type" attribute, defaulting to "text" the same
// way a real <input> with no type attribute does.
func inputType(n xango.Node) string {
	for _, a := range n.Attrs {
		if a.Name == "type" {
			return a.Value
		}
	}
	return "text"
}

// valueOf returns n's "value" attribute. Only used to seed a newly
// created widget.Editor: unlike domrt (which owns a real, persistent DOM
// node it can read back at any time), xango/gio has nothing to read a live
// value from except the widget.Editor itself, so this is a one-time
// initial value, not a live binding -- setting it on every Layout call
// would stomp whatever the user just typed.
func valueOf(n xango.Node) string {
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
func checkedOf(n xango.Node) bool {
	for _, a := range n.Attrs {
		if a.Name == "checked" {
			return true
		}
	}
	return false
}

// placeholderOf returns n's "placeholder" attribute, if any.
func placeholderOf(n xango.Node) string {
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
func changeHandler(n xango.Node) xango.EventHandler {
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
// widget.Clickable) and every other tag/type (n.Tag == "textarea", or
// n.Tag == "input" with any type other than checkbox) as a
// material.Editor (backed by a persisted widget.Editor) -- single-line
// for input, multi-line for textarea, the one real behavioral
// difference a plain multi-line comment body needs (see this file's
// own package doc comment). Both widgets'
// own Update method is called directly first, to detect a change and
// fire onChange, before the material.*Style.Layout call that paints
// it -- that call drains the same widget's Update internally too, but
// finds nothing left unconsumed by then, so it's a harmless no-op; this
// mirrors layoutButton's own Clicked-then-Layout sequencing for the same
// reason (a widget's interaction state must be consumed exactly once
// per frame to fire a handler, but the material wrapper always insists
// on consuming it itself to paint correctly).
func (r *Renderer) layoutInput(gtx layout.Context, th *material.Theme, n xango.Node) layout.Dimensions {
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
		if checked, pending := r.checkedRequests[id]; pending {
			delete(r.checkedRequests, id)
			cb.Value = checked
		}
		changed := cb.Update(gtx)
		if r.toggleRequests[id] {
			delete(r.toggleRequests, id)
			cb.Value = !cb.Value
			changed = true
		}
		if changed {
			handler := changeHandler(n)
			if handler == nil {
				handler = r.handlerFor(id, "change")
			}
			if handler != nil {
				handler(xango.NewEvent("change", cb.Value, cb.Value, nil, nil))
			}
		}
		return material.CheckBox(th, cb, "").Layout(gtx)
	}

	ed, ok := r.editors[id]
	if !ok {
		ed = &widget.Editor{SingleLine: n.Tag != "textarea"}
		ed.SetText(valueOf(n))
		r.editors[id] = ed
	}
	if v, pending := r.valueRequests[id]; pending {
		delete(r.valueRequests, id)
		ed.SetText(v)
	}
	if r.focusRequests[id] {
		delete(r.focusRequests, id)
		gtx.Execute(key.FocusCmd{Tag: ed})
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
		handler := changeHandler(n)
		if handler == nil {
			handler = r.handlerFor(id, "change")
		}
		if handler != nil {
			handler(xango.NewEvent("change", ed.Text(), false, nil, nil))
		}
	}
	return material.Editor(th, ed, placeholderOf(n)).Layout(gtx)
}

// selectedAttr reports whether n's "selected" attribute is present,
// the same boolean-attribute convention checkedOf already uses for
// "checked".
func selectedAttr(n xango.Node) bool {
	for _, a := range n.Attrs {
		if a.Name == "selected" {
			return true
		}
	}
	return false
}

// selectedOption returns the value/label of n's currently-selected
// <option> child (the one with a "selected" attribute -- matching
// OnDesign's own optSelected convention for the web renderer), or
// ok=false if none is marked selected.
func selectedOption(n xango.Node) (value, label string, ok bool) {
	for _, c := range n.Children {
		if c.Tag != "option" {
			continue
		}
		if selectedAttr(c) {
			return valueOf(c), textOf(c), true
		}
	}
	return "", "", false
}

// layoutSelect lays out a `<select id=...>{<option value=...
// [selected]>label</option>...}</select>` as a custom dropdown --
// gioui.org/widget/material has no native equivalent, so unlike every
// other tag xango/gio handles, this isn't a thin wrapper around an
// existing material widget: a closed select is a material.Button
// showing the selected option's own label; an open one is that same
// button (now showing a "close" affordance) stacked above one more
// button per <option>, clicking a row both fires onChange with that
// option's value and closes the dropdown again.
//
// Two things persist across frames here, both keyed by identity
// (Node.Key if set, else the "id" attribute, matching every other
// interactive tag xango/gio handles): whether the dropdown is currently
// open (r.selectOpen -- purely xango/gio's own transient UI state, not
// caller-controlled, since a real <select> doesn't expose "is the
// dropdown open" as part of its own value either, so a plain
// map[string]bool is the right shape, not a Gio widget type), and each
// row's own click detection, which reuses r.clickables (the same map
// layoutButton already uses) keyed by "<select id>\x00<option value>"
// so a row's identity survives across frames the same way a button's
// does, without adding a second per-option map.
//
// Which option is *selected* is deliberately NOT persisted here at
// all: like a real controlled <select>, that's read fresh from the
// tree's own "selected" attribute (selectedOption, above) on whichever
// <option> child has one. The caller is expected to re-render with a
// different option marked selected once its own onChange handler
// updates whatever state the selection represents -- the same
// controlled-component contract domrt already holds every caller to,
// not a new one xango/gio invents.
func (r *Renderer) layoutSelect(gtx layout.Context, th *material.Theme, n xango.Node) layout.Dimensions {
	id := n.Key
	if id == "" {
		id = idOf(n)
	}

	trigger, ok := r.clickables[id]
	if !ok {
		trigger = new(widget.Clickable)
		r.clickables[id] = trigger
	}
	for trigger.Clicked(gtx) {
		r.selectOpen[id] = !r.selectOpen[id]
	}

	_, label, hasSelection := selectedOption(n)
	if !hasSelection {
		label = "(select)"
	}

	if !r.selectOpen[id] {
		return material.Button(th, trigger, label+" ▾").Layout(gtx)
	}

	handler := changeHandler(n)
	if handler == nil {
		handler = r.handlerFor(id, "change")
	}
	rows := make([]layout.FlexChild, 0, len(n.Children)+1)
	rows = append(rows, layout.Rigid(material.Button(th, trigger, label+" ▴").Layout))
	for _, c := range n.Children {
		if c.Tag != "option" {
			continue
		}
		value := valueOf(c)
		optLabel := textOf(c)
		rowID := id + "\x00" + value
		clk, ok := r.clickables[rowID]
		if !ok {
			clk = new(widget.Clickable)
			r.clickables[rowID] = clk
		}
		for clk.Clicked(gtx) {
			r.selectOpen[id] = false
			if handler != nil {
				handler(xango.NewEvent("change", value, false, nil, nil))
			}
		}
		rows = append(rows, layout.Rigid(material.Button(th, clk, optLabel).Layout))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}
