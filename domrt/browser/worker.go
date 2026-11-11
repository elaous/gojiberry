//go:build js && wasm

package browser

import (
	"syscall/js"

	"github.com/elaous/gojiberry/domrt/html"
)

type Worker struct {
	v         js.Value
	onMessage js.Func
	onError   js.Func
}

func NewWorker(url string) *Worker {
	return &Worker{v: js.Global().Get("Worker").New(url)}
}

func (w *Worker) PostMessage(data any) {
	w.v.Call("postMessage", data)
}

func (w *Worker) OnMessage(fn func(data js.Value)) {
	w.replace(&w.onMessage, "onmessage", func(args []js.Value) {
		fn(args[0].Get("data"))
	})
}

func (w *Worker) OnError(fn func(err *html.JSError)) {
	w.replace(&w.onError, "onerror", func(args []js.Value) {
		fn(html.NewJSErrorFromEvent(args[0]))
	})
}

func (w *Worker) replace(slot *js.Func, prop string, fn func(args []js.Value)) {
	if !slot.IsUndefined() {
		slot.Release()
	}
	*slot = js.FuncOf(func(this js.Value, args []js.Value) any {
		fn(args)
		return nil
	})
	w.v.Set(prop, *slot)
}

// Terminate immediately stops the worker and releases its registered callbacks.
func (w *Worker) Terminate() {
	w.v.Call("terminate")
	for _, ev := range []string{"onmessage", "onerror"} {
		w.v.Set(ev, js.Null())
	}
	for _, f := range []*js.Func{&w.onMessage, &w.onError} {
		if !f.IsUndefined() {
			f.Release()
		}
	}
}
