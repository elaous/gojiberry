//go:build js && wasm

// Package dom wraps browser DOM/canvas/WebGL objects (Document,
// Element, Window, Context2D, Context3D, observers, drag-and-drop) as
// typed Go structs, each with a Get/Call escape hatch onto the
// underlying js.Value for anything not covered by a typed method.
package html

import "syscall/js"

// DomNode is implemented by Element and every element type in this
// package that embeds it (CanvasElement, AnchorElement, ButtonElement,
// SelectElement, InputElement, ...), so code that only needs the
// common DOM operations can accept any of them interchangeably.
type DomNode interface {
	JSValue() js.Value
	Get(name string) js.Value
	Call(method string, args ...any) js.Value
	GetID() string
	SetID(id string)
	GetClassName() string
	SetClassName(className string)
	GetTagName() string
	GetTextContent() string
	SetTextContent(text string)
	GetAttribute(name string) string
	SetAttribute(name, value string)
	QuerySelector(selector string) *Element
	QuerySelectorAll(selector string) []Element
	AppendChild(child DomNode)
	RemoveChild(child DomNode)
	GetInnerHTML() string
	SetInnerHTML(html string)
	AddEventListener(event string, callback js.Func)
	RemoveEventListener(event string, callback js.Func)
	Set(name string, value any)
	Click()
	Focus()
	Blur()
	Hidden() bool
	SetHidden(hidden bool)
	BoundingRect() Rect
	ScrollIntoView(smooth bool)
	StyleProperty(name string) string
	SetStyleProperty(name, value string)
	Style() Style
	ClientWidth() int
	ClientHeight() int
	ScrollLeft() int
	SetScrollLeft(x int)
	ScrollTop() int
	SetScrollTop(y int)
}

// Element wraps a DOM Element/Node. Get/Call cover anything without a
// typed method below, the same escape hatch as the rest of this package.
//
// Element is also the base every more specific element type
// (CanvasElement, AnchorElement, ButtonElement, SelectElement,
// InputElement, ...) embeds to pick up the common methods below; use
// the As* cast helpers (AsCanvas, AsAnchor, ...) to obtain one of those
// from a plain Element.
type Element struct {
	Element js.Value
}

// JSValue returns the underlying js.Value, satisfying DomNode.
func (e *Element) JSValue() js.Value { return e.Element }

// ParentElement returns e's parent element, or nil if e has none.
func (e *Element) ParentElement() *Element {
	parent := e.Element.Get("parentElement")
	if parent.IsUndefined() || parent.IsNull() {
		return nil
	}
	return &Element{Element: parent}
}

// GetID returns e's id attribute.
func (e *Element) GetID() string {
	return e.Element.Get("id").String()
}

// SetID sets e's id attribute.
func (e *Element) SetID(id string) {
	e.Element.Set("id", id)
}

// GetClassName returns e's class attribute. On an SVG element this is
// unreliable -- className there is an SVGAnimatedString object, not a
// plain string -- use ClassListContains (see svg.go) instead.
func (e *Element) GetClassName() string {
	return e.Element.Get("className").String()
}

// SetClassName sets e's class attribute. On an SVG element use
// ClassListAdd/ClassListRemove instead (see GetClassName's doc comment).
func (e *Element) SetClassName(className string) {
	e.Element.Set("className", className)
}

// GetTagName returns e's tag name (e.g. "DIV"), uppercased per the DOM spec.
func (e *Element) GetTagName() string {
	return e.Element.Get("tagName").String()
}

// GetTextContent returns the concatenated text of e and its descendants.
func (e *Element) GetTextContent() string {
	return e.Element.Get("textContent").String()
}

// SetTextContent replaces e's children with a single text node containing text.
func (e *Element) SetTextContent(text string) {
	e.Element.Set("textContent", text)
}

// GetAttribute returns the named HTML attribute's value.
func (e *Element) GetAttribute(name string) string {
	return e.Element.Call("getAttribute", name).String()
}

// SetAttribute sets the named HTML attribute's value.
func (e *Element) SetAttribute(name, value string) {
	e.Element.Call("setAttribute", name, value)
}

// QuerySelector returns the first descendant of e matching the CSS
// selector, in document order, or nil when nothing matches -- the
// same subtree-scoped semantics as the DOM's own element-level
// querySelector. Meaningful on any element that can have children;
// on a void (self-closing) element like <input> or <br> there are no
// descendants to match, so it always returns nil, exactly as in the
// browser. For a document-wide search use Document.QuerySelector.
func (e *Element) QuerySelector(selector string) *Element {
	v := e.Element.Call("querySelector", selector)
	if v.IsNull() || v.IsUndefined() {
		return nil
	}
	return &Element{Element: v}
}

// QuerySelectorAll returns every descendant of e matching the CSS
// selector, in document order -- empty when nothing matches. See
// QuerySelector for the subtree scoping.
func (e *Element) QuerySelectorAll(selector string) []Element {
	return wrapAll(e.Element.Call("querySelectorAll", selector))
}

// AppendChild appends child as e's last child.
func (e *Element) AppendChild(child DomNode) {
	e.Element.Call("appendChild", child.JSValue())
}

// RemoveChild removes child from e's children.
func (e *Element) RemoveChild(child DomNode) {
	e.Element.Call("removeChild", child.JSValue())
}

// GetInnerHTML returns e's serialized child markup.
func (e *Element) GetInnerHTML() string {
	return e.Element.Get("innerHTML").String()
}

// SetInnerHTML replaces e's children by parsing html.
func (e *Element) SetInnerHTML(html string) {
	e.Element.Set("innerHTML", html)
}

// GetValue returns a form element's current value (e.g. an <input>/<textarea>).
func (e *Element) GetValue() string {
	return e.Element.Get("value").String()
}

// SetValue sets a form element's current value.
func (e *Element) SetValue(value string) {
	e.Element.Set("value", value)
}

// AddEventListener registers callback for event on e.
func (e *Element) AddEventListener(event string, callback js.Func) {
	e.Element.Call("addEventListener", event, callback)
}

// RemoveEventListener removes a listener previously registered with the same callback value.
func (e *Element) RemoveEventListener(event string, callback js.Func) {
	e.Element.Call("removeEventListener", event, callback)
}

// Click synthesizes a click on e, the same as a user click would (and,
// for elements with default click behavior like <a>/<input>, triggers
// that behavior too).
func (e *Element) Click() { e.Element.Call("click") }

// Get returns a named property not covered by a typed getter above
// (e.g. Get("dataset"), Get("style"), Get("checked")).
func (e *Element) Get(name string) js.Value {
	return e.Element.Get(name)
}

// Call invokes a method not covered by a typed wrapper above (e.g.
// Call("closest", selector), Call("insertAdjacentHTML", pos, html)).
func (e *Element) Call(method string, args ...any) js.Value {
	return e.Element.Call(method, args...)
}

// Set assigns a named property not covered by a typed setter above
// (e.g. Set("checked", true), Set("width", 100)).
func (e *Element) Set(name string, value any) {
	e.Element.Set(name, value)
}
