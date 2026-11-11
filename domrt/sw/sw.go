//go:build js && wasm

package sw

import (
	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
)

// Event wraps a raw service worker event object.
type Event struct{ raw js.Value }

// Raw returns the underlying JS event.
func (e Event) Raw() js.Value { return e.raw }

// ExtendableEvent is an install or activate event.
type ExtendableEvent struct{ Event }

// WaitUntil extends the event's lifetime until work returns.
func (e ExtendableEvent) WaitUntil(work func() error) {
	e.raw.Call("waitUntil", domrt.ToPromise(func() (js.Value, error) {
		return js.Null(), work()
	}))
}

// FetchEvent is dispatched for each request in the worker's scope.
type FetchEvent struct{ Event }

// Request returns the raw JS Request object.
func (e FetchEvent) Request() js.Value { return e.raw.Get("request") }

// RespondWith supplies the response for this request.
func (e FetchEvent) RespondWith(work func() (js.Value, error)) {
	e.raw.Call("respondWith", domrt.ToPromise(work))
}

func addSelfListener(name string, fn func(js.Value)) {
	// never released: listeners live as long as the service worker
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

// OnInstall registers the install handler.
func OnInstall(fn func(ExtendableEvent)) {
	addSelfListener("install", func(v js.Value) { fn(ExtendableEvent{Event{v}}) })
}

// OnActivate registers the activate handler.
func OnActivate(fn func(ExtendableEvent)) {
	addSelfListener("activate", func(v js.Value) { fn(ExtendableEvent{Event{v}}) })
}

// OnFetch registers the fetch handler.
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

// CachesKeys wraps caches.keys: the names of every cache.
func CachesKeys() ([]string, error) {
	v, err := domrt.Await(js.Global().Get("caches").Call("keys"))
	if err != nil {
		return nil, err
	}
	out := make([]string, v.Length())
	for i := range out {
		out[i] = v.Index(i).String()
	}
	return out, nil
}

// CachesDelete wraps caches.delete(name).
func CachesDelete(name string) error {
	_, err := domrt.Await(js.Global().Get("caches").Call("delete", name))
	return err
}

// CachePut wraps cache.put(request, response).
func CachePut(cache, request, response js.Value) error {
	_, err := domrt.Await(cache.Call("put", request, response))
	return err
}

// CachesMatch wraps caches.match; ok is false on a miss.
func CachesMatch(request js.Value) (js.Value, bool) {
	v, err := domrt.Await(js.Global().Get("caches").Call("match", request))
	if err != nil || v.IsUndefined() || v.IsNull() {
		return js.Value{}, false
	}
	return v, true
}

// ClaimClients wraps self.clients.claim.
func ClaimClients() error {
	_, err := domrt.Await(js.Global().Get("clients").Call("claim"))
	return err
}
