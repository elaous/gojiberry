//go:build js && wasm

package browser

import (
	"syscall/js"

	"github.com/elaous/gojiberry/domrt/html"
)

// Worker wraps a browser dedicated Worker: a separate Go/WASM binary
// running on a real background thread, isolated from the DOM and this
// program's memory, reachable only through PostMessage/OnMessage (see
// domrt/worker for the worker-side half). Unlike a service worker
// (domrt/sw), a dedicated Worker needs no separate registration step --
// NewWorker starts it directly.
type Worker struct {
	v         js.Value
	onMessage js.Func
	onError   js.Func
}

// NewWorker starts a new Worker running the script at url -- typically
// a small JS bootstrap that does importScripts('wasm_exec.js') and
// instantiates a separate .wasm binary built from a domrt/worker
// program (see the wasm-worker-demo example).
func NewWorker(url string) *Worker {
	return &Worker{v: js.Global().Get("Worker").New(url)}
}

// PostMessage sends data to the worker (structured-clone, not shared
// memory -- the same constraint domrt/worker.PostMessage has sending
// the other way).
func (w *Worker) PostMessage(data any) {
	w.v.Call("postMessage", data)
}

// OnMessage registers the handler called for each message the worker
// posts back. Calling it again replaces the previous handler.
func (w *Worker) OnMessage(fn func(data js.Value)) {
	w.replace(&w.onMessage, "onmessage", func(args []js.Value) {
		fn(args[0].Get("data"))
	})
}

// OnError registers the handler called when the worker throws an
// uncaught exception.
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
