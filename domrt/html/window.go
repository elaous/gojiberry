//go:build js && wasm

package html

import (
	"syscall/js"
)

// Window wraps the browser's global window object. Get/Call cover
// anything without a typed method below, the same escape hatch as the
// rest of this package.
type Window struct {
	Window js.Value
}

// SetTimeout schedules fn to run once after delay milliseconds and
// returns the id ClearTimeout needs to cancel it.
func (w *Window) SetTimeout(fn func(), delay int) int {
	id := w.Window.Call("setTimeout", js.FuncOf(func(this js.Value, args []js.Value) any {
		fn()
		return nil
	}), delay)
	return id.Int()
}

// SetInterval schedules fn to run every delay milliseconds and returns
// the id ClearInterval needs to stop it.
func (w *Window) SetInterval(fn func(), delay int) int {
	id := w.Window.Call("setInterval", js.FuncOf(func(this js.Value, args []js.Value) any {
		fn()
		return nil
	}), delay)
	return id.Int()
}

// RequestAnimationFrame schedules fn before the next repaint and
// returns the id ClearAnimationFrame needs to cancel it.
func (w *Window) RequestAnimationFrame(fn func()) int {
	id := w.Window.Call("requestAnimationFrame", js.FuncOf(func(this js.Value, args []js.Value) any {
		fn()
		return nil
	}))
	return id.Int()
}

// AddEventListener registers fn for event and returns the js.Func
// backing it. Pass that same value to RemoveEventListener to
// unregister it later -- removeEventListener only works when given
// the exact function reference addEventListener was called with, so
// wrapping fn in a fresh js.Func on removal (as an earlier version of
// this method did) can never actually remove anything.
func (w *Window) AddEventListener(event string, fn func()) js.Func {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		fn()
		return nil
	})
	w.Window.Call("addEventListener", event, cb)
	return cb
}

// RemoveEventListener unregisters the listener callback returned by
// the matching AddEventListener call.
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

// ReplaceState swaps the current history entry's URL for url, without
// a navigation/reload and without creating a new Back stop -- e.g. to
// strip a one-time token out of the address bar right after reading
// it, so a bookmark, refresh, or copy-pasted link never resurfaces it.
// Fires no popstate event, matching the browser's own replaceState.
func (w *Window) ReplaceState(url string) {
	w.Window.Get("history").Call("replaceState", js.Null(), "", url)
}

// Alert shows a native alert dialog with message, blocking until dismissed.
func (w *Window) Alert(message string) {
	w.Window.Call("alert", message)
}

// Confirm shows a native OK/Cancel dialog and reports whether OK was chosen.
func (w *Window) Confirm(message string) bool {
	return w.Window.Call("confirm", message).Bool()
}

// Prompt shows a native text-input dialog pre-filled with defaultValue,
// returning the entered text (or "" if cancelled). The browser's own
// window.prompt returns JS null on cancel, not "" -- and
// syscall/js's Value.String() renders a non-string value (including
// null) as a placeholder like "<null>" rather than panicking or
// coercing it, so calling .String() directly on the raw result would
// silently turn "cancelled" into the literal 6-character string
// "<null>" instead of "". Checked explicitly here so every caller can
// rely on this func's own doc comment ("" means cancelled) without
// each one re-deriving this.
func (w *Window) Prompt(message, defaultValue string) string {
	v := w.Window.Call("prompt", message, defaultValue)
	if v.Type() != js.TypeString {
		return ""
	}
	return v.String()
}

// Open opens url in a new browsing context (or an existing one named
// name), configured by features (a comma-separated window-features string).
func (w *Window) Open(url, name, features string) js.Value {
	return w.Window.Call("open", url, name, features)
}

// Close closes the window (only effective for windows opened via Open).
func (w *Window) Close() {
	w.Window.Call("close")
}

// ClearTimeout cancels a pending timer from SetTimeout.
func (w *Window) ClearTimeout(id int) {
	w.Window.Call("clearTimeout", id)
}

// ClearInterval cancels a repeating timer from SetInterval.
func (w *Window) ClearInterval(id int) {
	w.Window.Call("clearInterval", id)
}

// ClearAnimationFrame cancels a pending callback from RequestAnimationFrame.
func (w *Window) ClearAnimationFrame(id int) {
	w.Window.Call("cancelAnimationFrame", id)
}

// InnerWidth returns the viewport width in pixels, including any scrollbar.
func (w *Window) InnerWidth() int { return w.Window.Get("innerWidth").Int() }

// InnerHeight returns the viewport height in pixels, including any scrollbar.
func (w *Window) InnerHeight() int { return w.Window.Get("innerHeight").Int() }

// OuterWidth returns the browser window's width in pixels, chrome included.
func (w *Window) OuterWidth() int { return w.Window.Get("outerWidth").Int() }

// OuterHeight returns the browser window's height in pixels, chrome included.
func (w *Window) OuterHeight() int { return w.Window.Get("outerHeight").Int() }

// ScrollX returns how far the page is scrolled from the left, in pixels.
func (w *Window) ScrollX() float64 { return w.Window.Get("scrollX").Float() }

// ScrollY returns how far the page is scrolled from the top, in pixels.
func (w *Window) ScrollY() float64 { return w.Window.Get("scrollY").Float() }

// DevicePixelRatio returns the ratio between physical and CSS pixels
// (>1 on a HiDPI/"Retina" display).
func (w *Window) DevicePixelRatio() float64 { return w.Window.Get("devicePixelRatio").Float() }

// ScrollTo scrolls the page to an absolute position, smoothly if
// smooth is true (instantly otherwise).
func (w *Window) ScrollTo(x, y int, smooth bool) {
	behavior := "auto"
	if smooth {
		behavior = "smooth"
	}
	w.Window.Call("scrollTo", map[string]any{"left": x, "top": y, "behavior": behavior})
}

// ScrollBy scrolls the page by a relative offset, smoothly if smooth
// is true (instantly otherwise).
func (w *Window) ScrollBy(dx, dy int, smooth bool) {
	behavior := "auto"
	if smooth {
		behavior = "smooth"
	}
	w.Window.Call("scrollBy", map[string]any{"left": dx, "top": dy, "behavior": behavior})
}

// ComputedStyle returns el's fully resolved, read-only style (CSS from
// stylesheets and inheritance, not just its inline style attribute).
func (w *Window) ComputedStyle(el DomNode) Style {
	return Style{Value: w.Window.Call("getComputedStyle", el.JSValue())}
}

// Get returns a named property not covered by a typed getter above.
func (w *Window) Get(name string) js.Value {
	return w.Window.Get(name)
}

// Call invokes a method not covered by a typed wrapper above.
func (w *Window) Call(method string, args ...any) js.Value {
	return w.Window.Call(method, args...)
}

// GetWindow returns the global window object.
func GetWindow() *Window {
	return &Window{Window: js.Global().Get("window")}
}
