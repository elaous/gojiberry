//go:build js && wasm

package dom

// Rect is an element's size and position relative to the viewport, as
// returned by Element.BoundingRect (getBoundingClientRect).
type Rect struct {
	X, Y, Width, Height, Top, Right, Bottom, Left float64
}

// Focus moves keyboard focus to e.
func (e *Element) Focus() { e.Element.Call("focus") }

// Blur removes keyboard focus from e.
func (e *Element) Blur() { e.Element.Call("blur") }

// Hidden returns whether e is hidden (the hidden attribute/property).
func (e *Element) Hidden() bool { return e.Element.Get("hidden").Bool() }

// SetHidden hides or shows e.
func (e *Element) SetHidden(hidden bool) { e.Element.Set("hidden", hidden) }

// TabIndex returns e's position in the tab order (-1 if it's
// programmatically focusable but not reachable via Tab).
func (e *Element) TabIndex() int { return e.Element.Get("tabIndex").Int() }

// SetTabIndex sets e's position in the tab order.
func (e *Element) SetTabIndex(index int) { e.Element.Set("tabIndex", index) }

// GetTitle returns e's title attribute (usually shown as a tooltip).
func (e *Element) GetTitle() string { return e.Element.Get("title").String() }

// SetTitle sets e's title attribute.
func (e *Element) SetTitle(title string) { e.Element.Set("title", title) }

// GetDir returns e's directionality ("ltr", "rtl", or "auto").
func (e *Element) GetDir() string { return e.Element.Get("dir").String() }

// SetDir sets e's directionality.
func (e *Element) SetDir(dir string) { e.Element.Set("dir", dir) }

// ContentEditable returns whether e's contents can be edited in place
// ("true", "false", "plaintext-only", or "inherit").
func (e *Element) ContentEditable() string { return e.Element.Get("contentEditable").String() }

// SetContentEditable sets whether e's contents can be edited in place.
func (e *Element) SetContentEditable(value string) { e.Element.Set("contentEditable", value) }

// OffsetWidth returns e's layout width in pixels, including borders and padding.
func (e *Element) OffsetWidth() int { return e.Element.Get("offsetWidth").Int() }

// OffsetHeight returns e's layout height in pixels, including borders and padding.
func (e *Element) OffsetHeight() int { return e.Element.Get("offsetHeight").Int() }

// OffsetLeft returns e's distance in pixels from its offsetParent's left edge.
func (e *Element) OffsetLeft() int { return e.Element.Get("offsetLeft").Int() }

// OffsetTop returns e's distance in pixels from its offsetParent's top edge.
func (e *Element) OffsetTop() int { return e.Element.Get("offsetTop").Int() }

// BoundingRect returns e's size and position relative to the viewport,
// e.g. for positioning a dropdown/tooltip against it.
func (e *Element) BoundingRect() Rect {
	r := e.Element.Call("getBoundingClientRect")
	return Rect{
		X: r.Get("x").Float(), Y: r.Get("y").Float(),
		Width: r.Get("width").Float(), Height: r.Get("height").Float(),
		Top: r.Get("top").Float(), Right: r.Get("right").Float(),
		Bottom: r.Get("bottom").Float(), Left: r.Get("left").Float(),
	}
}

// ScrollIntoView scrolls e's ancestors so it's visible, smoothly if
// smooth is true (instantly otherwise).
func (e *Element) ScrollIntoView(smooth bool) {
	behavior := "auto"
	if smooth {
		behavior = "smooth"
	}
	e.Element.Call("scrollIntoView", map[string]any{"behavior": behavior, "block": "nearest"})
}

// StyleProperty returns the value of a CSS property from e's inline
// style (not any value it picked up from a stylesheet -- use Window's
// Get/Call escape hatch for getComputedStyle if that's what's needed).
func (e *Element) StyleProperty(name string) string {
	return e.Element.Get("style").Call("getPropertyValue", name).String()
}

// SetStyleProperty sets a CSS property on e's inline style.
func (e *Element) SetStyleProperty(name, value string) {
	e.Element.Get("style").Call("setProperty", name, value)
}
