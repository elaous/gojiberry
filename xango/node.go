// Package xango is the runtime for gojiberry components: a renderer-agnostic
// Node tree that can be walked to produce HTML (server-side) or to drive
// real DOM nodes (client-side, see domrt). Generated code from .xg files
// targets this package's constructors.
package xango

// Kind discriminates the variants of Node.
type Kind int

const (
	KindElement Kind = iota
	KindText
	KindFragment
	KindRaw
	// KindHost is a node for a renderer-specific component (see Host).
	KindHost
)

// Node is a single node in a UI tree: an element, a text run, or a
// fragment (a childless-tag grouping with no wrapper element).
type Node struct {
	Kind     Kind
	Tag      string // element tag name, e.g. "div"; empty for Text/Fragment
	Key      string // optional reconciliation key, used by domrt's differ
	Attrs    []Attr
	Children []Node
	Text     string // content, when Kind == KindText
	Props    any    // typed props, when Kind == KindHost
}

// Attr is a single attribute or event binding on an element.
type Attr struct {
	Name    string
	Value   string       // plain string-valued attribute
	Handler EventHandler // set instead of Value for event bindings (onclick, oninput, ...)
}

// EventHandler is a callback bound to a DOM event via an Attr built with On.
// It is inert during server-side rendering; domrt wires it to real DOM
// events on the client.
type EventHandler func(Event)

// Event is a platform-neutral view of a DOM event. domrt populates it from
// a real browser event; this package has no browser/WASM dependency.
//
// Value carries whatever data the event has attached (a string for
// input/change events, a float64 for a range slider, a custom type for
// an application-defined event), erased to any so a single, non-generic
// Event can be stored in a plain xango.Attr and mixed into a homogeneous
// []xango.Attr. Use EventValue[T] for a type-safe read.
type Event struct {
	Type           string
	Value          any
	Checked        bool // convenience accessor for checkbox target.checked
	preventDefault func()
	stopProp       func()
	moveFocus      func(root, items string, step FocusStep) bool
}

// FocusStep says where MoveFocus goes among the candidates.
type FocusStep int

const (
	FocusNext FocusStep = iota
	FocusPrev
	FocusFirst
	FocusLast
)

// MoveFocus moves the keyboard focus within a widget while the event is being handled: root is
// a CSS selector for the widget, found from the event's target outward (the target itself
// counts); items selects the candidates inside it (written for the widget, so ":scope > li" is
// the widget's own children, and ":scope" alone is the widget element itself); step picks one relative to the element that has the focus, wrapping
// around, or the first or last. Candidates that are disabled, aria-disabled or not shown are
// skipped. It reports whether something was focused; with no browser behind the event (a test,
// the server) it does nothing and reports false.
func (e Event) MoveFocus(root, items string, step FocusStep) bool {
	if e.moveFocus == nil {
		return false
	}
	return e.moveFocus(root, items, step)
}

// WithFocus returns e with the function MoveFocus calls; used by domrt (and tests).
func (e Event) WithFocus(f func(root, items string, step FocusStep) bool) Event {
	e.moveFocus = f
	return e
}

// EventValue reads e.Value as T, returning the zero value of T if the
// event didn't carry a T (e.g. a click event has no string payload).
func EventValue[T any](e Event) T {
	v, _ := e.Value.(T)
	return v
}

// PreventDefault calls the underlying event's preventDefault, if any.
func (e Event) PreventDefault() {
	if e.preventDefault != nil {
		e.preventDefault()
	}
}

// StopPropagation calls the underlying event's stopPropagation, if any.
func (e Event) StopPropagation() {
	if e.stopProp != nil {
		e.stopProp()
	}
}

// NewEvent constructs an Event; used by domrt (and tests) to populate the
// callback hooks that PreventDefault/StopPropagation invoke.
func NewEvent(typ string, value any, checked bool, preventDefault, stopProp func()) Event {
	return Event{Type: typ, Value: value, Checked: checked, preventDefault: preventDefault, stopProp: stopProp}
}

// El constructs an element node. attrs may be nil. Generated code from
// .xg files targets this for every lowercase (HTML) tag.
func El(tag string, attrs []Attr, children ...Node) Node {
	return Node{Kind: KindElement, Tag: tag, Attrs: attrs, Children: children}
}

// Text constructs a text node. Content is HTML-escaped at render time.
func Text(s string) Node {
	return Node{Kind: KindText, Text: s}
}

// Frag groups children with no wrapper element, e.g. for a component that
// returns multiple siblings.
func Frag(children ...Node) Node {
	return Node{Kind: KindFragment, Children: children}
}

// A constructs a plain string-valued attribute.
func A(name, value string) Attr {
	return Attr{Name: name, Value: value}
}

// On constructs an event-binding attribute, e.g. On("click", handler).
func On(name string, handler EventHandler) Attr {
	return Attr{Name: name, Handler: handler}
}

// Key returns a copy of n with its reconciliation key set, for use in
// keyed lists: xango.El("li", nil, xango.Text(item)).WithKey(item.ID).
func (n Node) WithKey(key string) Node {
	n.Key = key
	return n
}

// Host constructs a node for a renderer-specific component: tag names it
// (for example "canvas.TextFlow"), props is its typed props value (any
// Go type -- slices, structs, functions), and children are its child
// nodes. Only a renderer that knows the tag can draw it (canvas/xango draws
// the canvas components); the HTML renderer reports an error, and domrt
// and xango/gio draw nothing.
func Host(tag string, props any, children ...Node) Node {
	return Node{Kind: KindHost, Tag: tag, Props: props, Children: children}
}

// KeyData is the Event.Value of a keyboard event (keydown, keyup,
// keypress): which key, and which modifiers were held. Key is the DOM
// KeyboardEvent.key ("a", "Enter", "ArrowUp"), Code the physical key
// ("KeyA", "Digit8").
type KeyData struct {
	Key, Code              string
	Ctrl, Shift, Alt, Meta bool
}
