//go:build js && wasm

package dom

import "syscall/js"

type Element struct {
	Element js.Value
}

func (e *Element) ParentElement() *Element {
	parent := e.Element.Get("parentElement")
	if parent.IsUndefined() || parent.IsNull() {
		return nil
	}
	return &Element{Element: parent}
}

func (e *Element) GetID() string {
	return e.Element.Get("id").String()
}

func (e *Element) SetID(id string) {
	e.Element.Set("id", id)
}

func (e *Element) GetClassName() string {
	return e.Element.Get("className").String()
}

func (e *Element) SetClassName(className string) {
	e.Element.Set("className", className)
}

func (e *Element) GetTagName() string {
	return e.Element.Get("tagName").String()
}

func (e *Element) GetTextContent() string {
	return e.Element.Get("textContent").String()
}

func (e *Element) SetTextContent(text string) {
	e.Element.Set("textContent", text)
}

func (e *Element) GetAttribute(name string) string {
	return e.Element.Call("getAttribute", name).String()
}

func (e *Element) SetAttribute(name, value string) {
	e.Element.Call("setAttribute", name, value)
}

func (e *Element) AppendChild(child *Element) {
	e.Element.Call("appendChild", child.Element)
}

func (e *Element) RemoveChild(child *Element) {
	e.Element.Call("removeChild", child.Element)
}

func (e *Element) GetInnerHTML() string {
	return e.Element.Get("innerHTML").String()
}

func (e *Element) SetInnerHTML(html string) {
	e.Element.Set("innerHTML", html)
}

func (e *Element) GetValue() string {
	return e.Element.Get("value").String()
}

func (e *Element) SetValue(value string) {
	e.Element.Set("value", value)
}

func (e *Element) AddEventListener(event string, callback js.Func) {
	e.Element.Call("addEventListener", event, callback)
}

func (e *Element) RemoveEventListener(event string, callback js.Func) {
	e.Element.Call("removeEventListener", event, callback)
}
