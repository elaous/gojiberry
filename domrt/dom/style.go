//go:build js && wasm

package dom

import "syscall/js"

// Style wraps a CSSStyleDeclaration -- either an element's own inline
// style (Element.Style) or a read-only computed style
// (Window.ComputedStyle).
type Style struct {
	Value js.Value
}

// Get returns the value of a CSS property.
func (s Style) Get(property string) string {
	return s.Value.Call("getPropertyValue", property).String()
}

// Set sets a CSS property. Only meaningful on an inline style (see
// Element.Style) -- a computed style is read-only and silently ignores it.
func (s Style) Set(property, value string) {
	s.Value.Call("setProperty", property, value)
}

// Remove clears a CSS property from an inline style.
func (s Style) Remove(property string) {
	s.Value.Call("removeProperty", property)
}

// CSSText returns the style as a single "prop: value; prop2: value2"
// string.
func (s Style) CSSText() string {
	return s.Value.Get("cssText").String()
}

// SetCSSText replaces an inline style's entire text in one call.
func (s Style) SetCSSText(text string) {
	s.Value.Set("cssText", text)
}

// Style returns e's inline style (element.style), for reading/writing
// multiple properties at once or via CSSText -- SetStyleProperty/
// StyleProperty remain for a single property at a time.
func (e *Element) Style() Style {
	return Style{Value: e.Element.Get("style")}
}
