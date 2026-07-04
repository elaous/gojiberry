//go:build js && wasm

package dom

import (
	"syscall/js"
)

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

func (w *Window) Location() js.Value {
	return w.Window.Get("location")
}

func (w *Window) SetLocation(url string) {
	w.Window.Set("location", url)
}

func (w *Window) Alert(message string) {
	w.Window.Call("alert", message)
}

func (w *Window) Confirm(message string) bool {
	return w.Window.Call("confirm", message).Bool()
}

func (w *Window) Prompt(message, defaultValue string) string {
	return w.Window.Call("prompt", message, defaultValue).String()
}

func (w *Window) Open(url, name, features string) js.Value {
	return w.Window.Call("open", url, name, features)
}

func (w *Window) Close() {
	w.Window.Call("close")
}

func (w *Window) ClearTimeout(id int) {
	w.Window.Call("clearTimeout", id)
}

func (w *Window) ClearInterval(id int) {
	w.Window.Call("clearInterval", id)
}

func (w *Window) ClearAnimationFrame(id int) {
	w.Window.Call("cancelAnimationFrame", id)
}

// Get returns a named property not covered by a typed getter above.
func (w *Window) Get(name string) js.Value {
	return w.Window.Get(name)
}

// Call invokes a method not covered by a typed wrapper above.
func (w *Window) Call(method string, args ...any) js.Value {
	return w.Window.Call(method, args...)
}

func GetWindow() *Window {
	return &Window{Window: js.Global().Get("window")}
}
