//go:build js && wasm

package worker

import "syscall/js"

// PostMessage sends data back to the creator of this worker.
func PostMessage(data any) {
	js.Global().Call("postMessage", data)
}

// OnMessage registers the handler for each incoming message.
func OnMessage(fn func(data js.Value)) {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		var v js.Value
		if len(args) > 0 {
			v = args[0].Get("data")
		}
		fn(v)
		return nil
	})
	js.Global().Call("addEventListener", "message", cb)
}

// Close terminates this worker from inside.
func Close() {
	js.Global().Call("close")
}
