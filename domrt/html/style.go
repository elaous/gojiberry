//go:build js && wasm

package html

import "syscall/js"

// Style wraps a CSSStyleDeclaration.
type Style struct {
	Value js.Value
}

// Get returns the value of a CSS property.
func (s Style) Get(property string) string {
	return s.Value.Call("getPropertyValue", property).String()
}

// Set sets a CSS property on an inline style.
func (s Style) Set(property, value string) {
	s.Value.Call("setProperty", property, value)
}

// Remove clears a CSS property from an inline style.
func (s Style) Remove(property string) {
	s.Value.Call("removeProperty", property)
}

// CSSText returns the style as a CSS text string.
func (s Style) CSSText() string {
	return s.Value.Get("cssText").String()
}

// SetCSSText replaces an inline style's entire text in one call.
func (s Style) SetCSSText(text string) {
	s.Value.Set("cssText", text)
}

// Style returns e's inline style.
func (e *Element) Style() Style {
	return Style{Value: e.Element.Get("style")}
}
