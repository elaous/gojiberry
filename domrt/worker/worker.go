//go:build js && wasm

// Package worker lets a Go/WASM binary run inside a dedicated Web
// Worker: it runs in the worker's own global scope, reachable via
// js.Global() the same way a page's `self`/`window` is (see domrt/sw
// for the same trick applied to service workers). wasm_exec.js only
// needs globalThis/crypto/performance/TextEncoder/TextDecoder, all of
// which a worker scope provides, so the same Go WASM runtime that runs
// a page also runs a dedicated worker -- it just needs a JS bootstrap
// that does `importScripts('wasm_exec.js')` instead of a <script> tag,
// since a worker has no DOM to attach one, then instantiates its own
// .wasm binary the normal way.
package worker

import "syscall/js"

// PostMessage sends data back to whatever created this worker (the
// main thread's browser.Worker, or another worker) -- structured-clone,
// not shared memory.
func PostMessage(data any) {
	js.Global().Call("postMessage", data)
}

// OnMessage registers the handler called for each message posted to
// this worker (e.g. via browser.Worker.PostMessage on the other end).
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

// Close terminates this worker from the inside -- the same effect as
// the main thread calling browser.Worker.Terminate, but self-initiated
// (e.g. after finishing a one-shot task).
func Close() {
	js.Global().Call("close")
}
