//go:build js && wasm

// Package browser exposes browser/JS APIs to Go/WASM code: a generic
// Global() escape hatch for any named global object, plus typed,
// idiomatic-Go wrappers for the APIs almost every app needs (fetch,
// localStorage/sessionStorage, WebSocket) so application code doesn't
// have to hand-roll syscall/js calls for them.
package browser

import (
	"encoding/json"
	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
)

// Global returns the named global browser API object (e.g.
// Global("WebAssembly"), Global("Notification"), Global("crypto")) for
// APIs this package doesn't have a typed wrapper for yet.
func Global(name string) js.Value {
	return js.Global().Get(name)
}

// Response is the result of Fetch.
type Response struct {
	Status int
	OK     bool
	body   js.Value
}

// Fetch wraps window.fetch(url), blocking until the response headers
// arrive (not the body -- call Text or JSON to read it).
func Fetch(url string) (*Response, error) {
	v, err := domrt.Await(js.Global().Call("fetch", url))
	if err != nil {
		return nil, err
	}
	return &Response{Status: v.Get("status").Int(), OK: v.Get("ok").Bool(), body: v}, nil
}

// Text reads the full response body as a string.
func (r *Response) Text() (string, error) {
	v, err := domrt.Await(r.body.Call("text"))
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

// JSON reads the full response body and decodes it as JSON into v.
func (r *Response) JSON(v any) error {
	text, err := r.Text()
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(text), v)
}

// storageArea wraps window.localStorage / window.sessionStorage.
type storageArea struct{ name string }

// LocalStorage wraps window.localStorage.
var LocalStorage = storageArea{"localStorage"}

// SessionStorage wraps window.sessionStorage.
var SessionStorage = storageArea{"sessionStorage"}

// Get returns the stored value for key and whether it was present.
func (s storageArea) Get(key string) (string, bool) {
	v := js.Global().Get(s.name).Call("getItem", key)
	if v.IsNull() {
		return "", false
	}
	return v.String(), true
}

// Set stores value under key.
func (s storageArea) Set(key, value string) {
	js.Global().Get(s.name).Call("setItem", key, value)
}

// Remove deletes key.
func (s storageArea) Remove(key string) {
	js.Global().Get(s.name).Call("removeItem", key)
}

// Clear removes every key in this storage area.
func (s storageArea) Clear() {
	js.Global().Get(s.name).Call("clear")
}

// Socket wraps a browser WebSocket.
type Socket struct {
	v         js.Value
	onMessage js.Func
	onClose   js.Func
	onError   js.Func
}

// Connect opens a new WebSocket to url.
func Connect(url string) *Socket {
	return &Socket{v: js.Global().Get("WebSocket").New(url)}
}

// Send writes a text frame.
func (s *Socket) Send(data string) {
	s.v.Call("send", data)
}

// OnMessage registers the handler called for each incoming text frame.
// Calling it again replaces the previous handler.
func (s *Socket) OnMessage(fn func(data string)) {
	s.replace(&s.onMessage, "onmessage", func(args []js.Value) {
		fn(args[0].Get("data").String())
	})
}

// OnClose registers the handler called when the connection closes.
func (s *Socket) OnClose(fn func()) {
	s.replace(&s.onClose, "onclose", func(args []js.Value) { fn() })
}

// OnError registers the handler called on a socket error.
func (s *Socket) OnError(fn func()) {
	s.replace(&s.onError, "onerror", func(args []js.Value) { fn() })
}

func (s *Socket) replace(slot *js.Func, prop string, fn func(args []js.Value)) {
	if !slot.IsUndefined() {
		slot.Release()
	}
	*slot = js.FuncOf(func(this js.Value, args []js.Value) any {
		fn(args)
		return nil
	})
	s.v.Set(prop, *slot)
}

// Close closes the connection and releases its registered callbacks.
func (s *Socket) Close() {
	s.v.Call("close")
	for _, f := range []*js.Func{&s.onMessage, &s.onClose, &s.onError} {
		if !f.IsUndefined() {
			f.Release()
		}
	}
}
