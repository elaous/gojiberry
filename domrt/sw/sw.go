//go:build js && wasm

// Package sw lets a Go/WASM binary run as a browser service worker: it
// runs in the ServiceWorkerGlobalScope (no DOM, event-driven), reachable
// via js.Global() the same way a page's `self` is. wasm_exec.js only
// needs globalThis/crypto/performance/TextEncoder/TextDecoder, all of
// which the service worker scope provides, so the same Go WASM runtime
// that runs a page also runs a service worker -- it just needs a JS
// bootstrap file that does `importScripts('wasm_exec.js')` instead of
// a <script> tag, since a classic service worker has no DOM to attach one.
package sw

import (
	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
)

// Event wraps a raw JS event object common to every service worker
// lifecycle/fetch event.
type Event struct{ raw js.Value }

// Raw returns the underlying JS event, for anything this package
// doesn't wrap.
func (e Event) Raw() js.Value { return e.raw }

// ExtendableEvent is install/activate: the browser waits for
// WaitUntil's work to finish before completing the lifecycle
// transition (e.g. finishing pre-caching before the worker installs).
type ExtendableEvent struct{ Event }

// WaitUntil extends the event's lifetime until work returns, via the
// event's real waitUntil(promise) -- work runs on its own goroutine so
// this call doesn't block the JS event loop.
func (e ExtendableEvent) WaitUntil(work func() error) {
	e.raw.Call("waitUntil", domrt.ToPromise(func() (js.Value, error) {
		return js.Null(), work()
	}))
}

// FetchEvent is dispatched for every request this worker's scope
// controls.
type FetchEvent struct{ Event }

// Request returns the raw JS Request object, suitable for passing
// straight to CachesMatch or fetch().
func (e FetchEvent) Request() js.Value { return e.raw.Get("request") }

// RespondWith supplies the response for this request via the event's
// real respondWith(promise) -- work runs on its own goroutine so this
// call doesn't block the JS event loop.
func (e FetchEvent) RespondWith(work func() (js.Value, error)) {
	e.raw.Call("respondWith", domrt.ToPromise(work))
}

func addSelfListener(name string, fn func(js.Value)) {
	// Never released: these listeners must live for the entire service
	// worker lifetime, which for a classic worker script is the process.
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		var v js.Value
		if len(args) > 0 {
			v = args[0]
		}
		fn(v)
		return nil
	})
	js.Global().Call("addEventListener", name, cb)
}

// OnInstall registers the worker's install handler.
func OnInstall(fn func(ExtendableEvent)) {
	addSelfListener("install", func(v js.Value) { fn(ExtendableEvent{Event{v}}) })
}

// OnActivate registers the worker's activate handler.
func OnActivate(fn func(ExtendableEvent)) {
	addSelfListener("activate", func(v js.Value) { fn(ExtendableEvent{Event{v}}) })
}

// OnFetch registers the worker's fetch handler.
func OnFetch(fn func(FetchEvent)) {
	addSelfListener("fetch", func(v js.Value) { fn(FetchEvent{Event{v}}) })
}

// CachesOpen wraps caches.open(name).
func CachesOpen(name string) (js.Value, error) {
	return domrt.Await(js.Global().Get("caches").Call("open", name))
}

// CacheAddAll wraps cache.addAll(urls).
func CacheAddAll(cache js.Value, urls []string) error {
	arr := js.Global().Get("Array").New(len(urls))
	for i, u := range urls {
		arr.SetIndex(i, u)
	}
	_, err := domrt.Await(cache.Call("addAll", arr))
	return err
}

// CachesMatch wraps caches.match(request), returning ok=false on a
// cache miss (not treated as an error).
func CachesMatch(request js.Value) (js.Value, bool) {
	v, err := domrt.Await(js.Global().Get("caches").Call("match", request))
	if err != nil || v.IsUndefined() || v.IsNull() {
		return js.Value{}, false
	}
	return v, true
}

// ClaimClients wraps self.clients.claim(), which lets an activating
// worker start controlling already-open pages immediately instead of
// only pages opened after this activation.
func ClaimClients() error {
	_, err := domrt.Await(js.Global().Get("clients").Call("claim"))
	return err
}
