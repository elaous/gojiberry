//go:build js && wasm

package dom

import (
	"syscall/js"
)

type Window struct {
	Window js.Value
}

func (w *Window) SetTimeout(fn func(), delay int) {
	w.Window.Call("setTimeout", js.FuncOf(func(this js.Value, args []js.Value) any {
		fn()
		return nil
	}), delay)
}

func (w *Window) RequestAnimationFrame(fn func()) {
	w.Window.Call("requestAnimationFrame", js.FuncOf(func(this js.Value, args []js.Value) any {
		fn()
		return nil
	}))
}

func (w *Window) AddEventListener(event string, fn func()) {
	w.Window.Call("addEventListener", event, js.FuncOf(func(this js.Value, args []js.Value) any {
		fn()
		return nil
	}))
}

func (w *Window) RemoveEventListener(event string, fn func()) {
	w.Window.Call("removeEventListener", event, js.FuncOf(func(this js.Value, args []js.Value) any {
		fn()
		return nil
	}))
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

func GetWindow() *Window {
	return &Window{Window: js.Global().Get("window")}
}
