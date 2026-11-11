//go:build js && wasm

package html

import (
	"syscall/js"
)

// Window wraps the browser's global window object.
type Window struct {
	Window js.Value
}

// package-level: GetWindow returns a fresh Window on every call
var pendingTimers = map[int]js.Func{}

// separate id space from timers: ids can be equal
var pendingFrames = map[int]js.Func{}

// SetTimeout runs fn once after delay milliseconds; returns its id.
func (w *Window) SetTimeout(fn func(), delay int) int {
	var cb js.Func
	var id int
	cb = js.FuncOf(func(this js.Value, args []js.Value) any {
		delete(pendingTimers, id)
		cb.Release()
		fn()
		return nil
	})
	id = w.Window.Call("setTimeout", cb, delay).Int()
	pendingTimers[id] = cb
	return id
}

// SetInterval runs fn every delay milliseconds; returns its id.
func (w *Window) SetInterval(fn func(), delay int) int {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		fn()
		return nil
	})
	id := w.Window.Call("setInterval", cb, delay).Int()
	pendingTimers[id] = cb
	return id
}

// RequestAnimationFrame runs fn before the next repaint; returns its id.
func (w *Window) RequestAnimationFrame(fn func()) int {
	var cb js.Func
	var id int
	cb = js.FuncOf(func(this js.Value, args []js.Value) any {
		delete(pendingFrames, id)
		cb.Release()
		fn()
		return nil
	})
	id = w.Window.Call("requestAnimationFrame", cb).Int()
	pendingFrames[id] = cb
	return id
}

// AddEventListener registers fn for an event and returns its callback.
func (w *Window) AddEventListener(event string, fn func()) js.Func {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		fn()
		return nil
	})
	w.Window.Call("addEventListener", event, cb)
	return cb
}

// RemoveEventListener unregisters a listener from AddEventListener.
func (w *Window) RemoveEventListener(event string, callback js.Func) {
	w.Window.Call("removeEventListener", event, callback)
}

// Location returns the window's Location object (href, pathname, etc.).
func (w *Window) Location() js.Value {
	return w.Window.Get("location")
}

// SetLocation navigates the window to url.
func (w *Window) SetLocation(url string) {
	w.Window.Set("location", url)
}

// ReplaceState swaps the history entry's URL without a navigation.
func (w *Window) ReplaceState(url string) {
	w.Window.Get("history").Call("replaceState", js.Null(), "", url)
}

// Alert shows a native alert dialog and waits for dismissal.
func (w *Window) Alert(message string) {
	w.Window.Call("alert", message)
}

// Confirm shows an OK/Cancel dialog and reports OK.
func (w *Window) Confirm(message string) bool {
	return w.Window.Call("confirm", message).Bool()
}

// Prompt shows a text input dialog pre-filled with defaultValue.
func (w *Window) Prompt(message, defaultValue string) string {
	v := w.Window.Call("prompt", message, defaultValue)
	if v.Type() != js.TypeString {
		return ""
	}
	return v.String()
}

// Open opens url in a new browsing context.
func (w *Window) Open(url, name, features string) js.Value {
	return w.Window.Call("open", url, name, features)
}

// Close closes a window opened by Open.
func (w *Window) Close() {
	w.Window.Call("close")
}

// ClearTimeout cancels a SetTimeout timer and releases its callback.
func (w *Window) ClearTimeout(id int) {
	w.Window.Call("clearTimeout", id)
	releasePendingTimer(id)
}

// ClearInterval cancels a SetInterval timer and releases its callback.
func (w *Window) ClearInterval(id int) {
	w.Window.Call("clearInterval", id)
	releasePendingTimer(id)
}

// ClearAnimationFrame cancels a pending animation-frame callback.
func (w *Window) ClearAnimationFrame(id int) {
	w.Window.Call("cancelAnimationFrame", id)
	if cb, ok := pendingFrames[id]; ok {
		delete(pendingFrames, id)
		cb.Release()
	}
}

// no-op if id already fired
func releasePendingTimer(id int) {
	if cb, ok := pendingTimers[id]; ok {
		delete(pendingTimers, id)
		cb.Release()
	}
}

// InnerWidth returns the viewport width in pixels, including any scrollbar.
func (w *Window) InnerWidth() int { return w.Window.Get("innerWidth").Int() }

// InnerHeight returns the viewport height in pixels, including any scrollbar.
func (w *Window) InnerHeight() int { return w.Window.Get("innerHeight").Int() }

// OuterWidth returns the browser window's width in pixels, chrome included.
func (w *Window) OuterWidth() int { return w.Window.Get("outerWidth").Int() }

// OuterHeight returns the browser window's height in pixels, chrome included.
func (w *Window) OuterHeight() int { return w.Window.Get("outerHeight").Int() }

// ScrollX returns how far the page is scrolled horizontally.
func (w *Window) ScrollX() float64 { return w.Window.Get("scrollX").Float() }

// ScrollY returns how far the page is scrolled vertically.
func (w *Window) ScrollY() float64 { return w.Window.Get("scrollY").Float() }

// DevicePixelRatio returns the ratio of physical to CSS pixels.
func (w *Window) DevicePixelRatio() float64 { return w.Window.Get("devicePixelRatio").Float() }

// ScrollTo scrolls the page to an absolute position.
func (w *Window) ScrollTo(x, y int, smooth bool) {
	behavior := "auto"
	if smooth {
		behavior = "smooth"
	}
	w.Window.Call("scrollTo", map[string]any{"left": x, "top": y, "behavior": behavior})
}

// ScrollBy scrolls the page by a relative offset.
func (w *Window) ScrollBy(dx, dy int, smooth bool) {
	behavior := "auto"
	if smooth {
		behavior = "smooth"
	}
	w.Window.Call("scrollBy", map[string]any{"left": dx, "top": dy, "behavior": behavior})
}

// ComputedStyle returns el's resolved, read-only style.
func (w *Window) ComputedStyle(el DomNode) Style {
	return Style{Value: w.Window.Call("getComputedStyle", el.JSValue())}
}

// Get returns a named window property.
func (w *Window) Get(name string) js.Value {
	return w.Window.Get(name)
}

// Call invokes a window method.
func (w *Window) Call(method string, args ...any) js.Value {
	return w.Window.Call(method, args...)
}

// GetWindow returns the global window object.
func GetWindow() *Window {
	return &Window{Window: js.Global().Get("window")}
}
