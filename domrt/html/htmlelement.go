//go:build js && wasm

package html

import "syscall/js"

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

// rectFromJS converts a live DOMRect/DOMRectReadOnly js.Value into a Rect.
func rectFromJS(r js.Value) Rect {
	return Rect{
		X: r.Get("x").Float(), Y: r.Get("y").Float(),
		Width: r.Get("width").Float(), Height: r.Get("height").Float(),
		Top: r.Get("top").Float(), Right: r.Get("right").Float(),
		Bottom: r.Get("bottom").Float(), Left: r.Get("left").Float(),
	}
}

// BoundingRect returns e's size and position relative to the viewport,
// e.g. for positioning a dropdown/tooltip against it.
func (e *Element) BoundingRect() Rect {
	return rectFromJS(e.Element.Call("getBoundingClientRect"))
}

// ClientRects returns one rect per CSS box e renders as (more than one
// for an inline element that wraps across lines).
func (e *Element) ClientRects() []Rect {
	list := e.Element.Call("getClientRects")
	out := make([]Rect, list.Get("length").Int())
	for i := range out {
		out[i] = rectFromJS(list.Call("item", i))
	}
	return out
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

// ClientWidth returns e's inner width in pixels: content plus padding,
// excluding border, margin, and any scrollbar.
func (e *Element) ClientWidth() int { return e.Element.Get("clientWidth").Int() }

// ClientHeight returns e's inner height in pixels: content plus
// padding, excluding border, margin, and any scrollbar.
func (e *Element) ClientHeight() int { return e.Element.Get("clientHeight").Int() }

// ClientLeft returns the width of e's left border in pixels.
func (e *Element) ClientLeft() int { return e.Element.Get("clientLeft").Int() }

// ClientTop returns the width of e's top border in pixels.
func (e *Element) ClientTop() int { return e.Element.Get("clientTop").Int() }

// ScrollWidth returns e's full content width in pixels, including the
// part hidden by overflow (>= ClientWidth).
func (e *Element) ScrollWidth() int { return e.Element.Get("scrollWidth").Int() }

// ScrollHeight returns e's full content height in pixels, including
// the part hidden by overflow (>= ClientHeight).
func (e *Element) ScrollHeight() int { return e.Element.Get("scrollHeight").Int() }

// ScrollLeft returns how far e's content is scrolled from its left edge.
func (e *Element) ScrollLeft() int { return e.Element.Get("scrollLeft").Int() }

// SetScrollLeft scrolls e's content to x pixels from its left edge.
func (e *Element) SetScrollLeft(x int) { e.Element.Set("scrollLeft", x) }

// ScrollTop returns how far e's content is scrolled from its top edge.
func (e *Element) ScrollTop() int { return e.Element.Get("scrollTop").Int() }

// SetScrollTop scrolls e's content to y pixels from its top edge.
func (e *Element) SetScrollTop(y int) { e.Element.Set("scrollTop", y) }

// ScrollTo scrolls e's own content to an absolute position, smoothly
// if smooth is true (instantly otherwise).
func (e *Element) ScrollTo(x, y int, smooth bool) {
	behavior := "auto"
	if smooth {
		behavior = "smooth"
	}
	e.Element.Call("scrollTo", map[string]any{"left": x, "top": y, "behavior": behavior})
}

// ScrollBy scrolls e's own content by a relative offset, smoothly if
// smooth is true (instantly otherwise).
func (e *Element) ScrollBy(dx, dy int, smooth bool) {
	behavior := "auto"
	if smooth {
		behavior = "smooth"
	}
	e.Element.Call("scrollBy", map[string]any{"left": dx, "top": dy, "behavior": behavior})
}

// StyleProperty returns the value of a CSS property from e's inline
// style (not any value it picked up from a stylesheet -- use
// Window.ComputedStyle for that). See also Style, for reading/writing
// several properties at once.
func (e *Element) StyleProperty(name string) string {
	return e.Element.Get("style").Call("getPropertyValue", name).String()
}

// SetStyleProperty sets a CSS property on e's inline style.
func (e *Element) SetStyleProperty(name, value string) {
	e.Element.Get("style").Call("setProperty", name, value)
}
