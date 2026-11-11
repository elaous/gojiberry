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

type Node struct {
	Kind     Kind
	Tag      string // element tag name, e.g. "div"; empty for Text/Fragment
	Key      string // optional reconciliation key, used by domrt's differ
	Attrs    []Attr
	Children []Node
	Text     string // content, when Kind == KindText
	Props    any    // typed props, when Kind == KindHost
}

type Attr struct {
	Name    string
	Value   string       // plain string-valued attribute
	Handler EventHandler // set instead of Value for event bindings (onclick, oninput, ...)
}

type EventHandler func(Event)

// Event is a platform-neutral view of a DOM event.
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
	// no wrap: at the edge focus stays where it is
	FocusUp
	FocusDown
)

func (e Event) MoveFocus(root, items string, step FocusStep) bool {
	if e.moveFocus == nil {
		return false
	}
	return e.moveFocus(root, items, step)
}

func (e Event) WithFocus(f func(root, items string, step FocusStep) bool) Event {
	e.moveFocus = f
	return e
}

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

func NewEvent(typ string, value any, checked bool, preventDefault, stopProp func()) Event {
	return Event{Type: typ, Value: value, Checked: checked, preventDefault: preventDefault, stopProp: stopProp}
}

// El constructs an element node.
func El(tag string, attrs []Attr, children ...Node) Node {
	return Node{Kind: KindElement, Tag: tag, Attrs: attrs, Children: children}
}

// Text constructs a text node.
func Text(s string) Node {
	return Node{Kind: KindText, Text: s}
}

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

func (n Node) WithKey(key string) Node {
	n.Key = key
	return n
}

func Host(tag string, props any, children ...Node) Node {
	return Node{Kind: KindHost, Tag: tag, Props: props, Children: children}
}

type KeyData struct {
	Key, Code              string
	Ctrl, Shift, Alt, Meta bool
}
