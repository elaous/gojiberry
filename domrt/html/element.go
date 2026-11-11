//go:build js && wasm

package html

import "syscall/js"

// DomNode is implemented by element types.
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

// Element wraps a DOM Element or Node.
type Element struct {
	Element js.Value
}

// JSValue returns the underlying js.Value, satisfying DomNode.
func (e *Element) JSValue() js.Value { return e.Element }

// ParentElement returns e's parent element, or nil.
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

// GetClassName returns e's class attribute.
func (e *Element) GetClassName() string {
	return e.Element.Get("className").String()
}

// SetClassName sets e's class attribute.
func (e *Element) SetClassName(className string) {
	e.Element.Set("className", className)
}

// GetTagName returns e's tag name, uppercased.
func (e *Element) GetTagName() string {
	return e.Element.Get("tagName").String()
}

// GetTextContent returns the concatenated text of e and its descendants.
func (e *Element) GetTextContent() string {
	return e.Element.Get("textContent").String()
}

// SetTextContent replaces e's children with a text node.
func (e *Element) SetTextContent(text string) {
	e.Element.Set("textContent", text)
}

// GetAttribute returns an attribute's value, or "" when absent.
func (e *Element) GetAttribute(name string) string {
	v := e.Element.Call("getAttribute", name)
	if v.IsNull() || v.IsUndefined() {
		return ""
	}
	return v.String()
}

// HasAttribute reports whether the element has the named attribute.
func (e *Element) HasAttribute(name string) bool {
	return e.Element.Call("hasAttribute", name).Bool()
}

// SetAttribute sets the named HTML attribute's value.
func (e *Element) SetAttribute(name, value string) {
	e.Element.Call("setAttribute", name, value)
}

// QuerySelector returns the first descendant matching a CSS selector.
func (e *Element) QuerySelector(selector string) *Element {
	v := e.Element.Call("querySelector", selector)
	if v.IsNull() || v.IsUndefined() {
		return nil
	}
	return &Element{Element: v}
}

// QuerySelectorAll returns every descendant matching a CSS selector.
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

// RemoveEventListener removes a listener added with the same callback.
func (e *Element) RemoveEventListener(event string, callback js.Func) {
	e.Element.Call("removeEventListener", event, callback)
}

// Click synthesizes a click on e.
func (e *Element) Click() { e.Element.Call("click") }

// Get returns a named property not covered by a getter.
func (e *Element) Get(name string) js.Value {
	return e.Element.Get(name)
}

// Call invokes a method not covered by a wrapper.
func (e *Element) Call(method string, args ...any) js.Value {
	return e.Element.Call(method, args...)
}

// Set assigns a named property not covered by a setter.
func (e *Element) Set(name string, value any) {
	e.Element.Set(name, value)
}
