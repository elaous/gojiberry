// Package gox is the runtime for gojiberry components: a renderer-agnostic
// Node tree that can be walked to produce HTML (server-side) or to drive
// real DOM nodes (client-side, see domrt). Generated code from .gox files
// targets this package's constructors.
package gox

// Kind discriminates the variants of Node.
type Kind int

const (
	KindElement Kind = iota
	KindText
	KindFragment
	KindRaw
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
type Event struct {
	Type           string
	Value          string // convenience accessor for input/change events' target.value
	Checked        bool   // convenience accessor for checkbox target.checked
	preventDefault func()
	stopProp       func()
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
func NewEvent(typ, value string, checked bool, preventDefault, stopProp func()) Event {
	return Event{Type: typ, Value: value, Checked: checked, preventDefault: preventDefault, stopProp: stopProp}
}

// El constructs an element node. attrs may be nil. Generated code from
// .gox files targets this for every lowercase (HTML) tag.
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
// keyed lists: gox.El("li", nil, gox.Text(item)).WithKey(item.ID).
func (n Node) WithKey(key string) Node {
	n.Key = key
	return n
}
