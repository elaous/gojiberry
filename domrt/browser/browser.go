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
	"github.com/elaous/gojiberry/domrt/html"
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

// Fetch wraps window.fetch(url) with a plain GET, blocking until the
// response headers arrive (not the body -- call Text or JSON to read
// it). For anything else (POST/PUT with a body, custom headers), use
// FetchOpts.
func Fetch(url string) (*Response, error) {
	return doFetch(js.Global().Call("fetch", url))
}

// FetchOptions configures a FetchOpts call. Method defaults to "GET"
// if empty. Body, if non-empty, is sent as the request body. BodyValue
// takes precedence over Body when set -- pass a FormData.Value here
// for a multipart upload (including files); the browser sets
// Content-Type with the right boundary automatically, so don't set it
// yourself in Headers.
type FetchOptions struct {
	Method    string
	Body      string
	BodyValue js.Value
	Headers   map[string]string
}

// FetchOpts wraps window.fetch(url, init) for requests Fetch's plain
// GET can't express: POST/PUT/PATCH/DELETE with a body, or custom
// headers. A common use is PUT-ing a JSON body:
//
//	resp, err := browser.FetchOpts(url, browser.FetchOptions{
//		Method:  "PUT",
//		Body:    string(jsonBytes),
//		Headers: map[string]string{"Content-Type": "application/json"},
//	})
func FetchOpts(url string, opts FetchOptions) (*Response, error) {
	init := map[string]any{}
	if opts.Method != "" {
		init["method"] = opts.Method
	}
	if !opts.BodyValue.IsUndefined() {
		init["body"] = opts.BodyValue
	} else if opts.Body != "" {
		init["body"] = opts.Body
	}
	if len(opts.Headers) > 0 {
		headers := map[string]any{}
		for k, v := range opts.Headers {
			headers[k] = v
		}
		init["headers"] = headers
	}
	return doFetch(js.Global().Call("fetch", url, js.ValueOf(init)))
}

func doFetch(promise js.Value) (*Response, error) {
	v, err := domrt.Await(promise)
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

// Bytes reads the full response body as raw bytes -- the binary
// counterpart to Text/JSON, for a body that isn't valid UTF-8 text
// (a gzip-compressed file, an image, ...): decoding it as text first,
// the way Text() does, would corrupt it (invalid byte sequences get
// replaced with U+FFFD on the JS side before Go ever sees them). Same
// arrayBuffer()-then-Uint8Array-then-CopyBytesToGo shape
// File.ReadAsArrayBuffer (fileapi.go) already uses for the same
// binary-safety reason.
func (r *Response) Bytes() ([]byte, error) {
	v, err := domrt.Await(r.body.Call("arrayBuffer"))
	if err != nil {
		return nil, err
	}
	arr := js.Global().Get("Uint8Array").New(v)
	out := make([]byte, arr.Length())
	js.CopyBytesToGo(out, arr)
	return out, nil
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

// Keys returns every key currently in this storage area, in
// storage-defined order -- for a caller that needs to enumerate a
// whole storage area rather than look up one key at a time (e.g. a
// storage adapter listing every document under its own key prefix).
func (s storageArea) Keys() []string {
	area := js.Global().Get(s.name)
	n := area.Get("length").Int()
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = area.Call("key", i).String()
	}
	return keys
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

// Document returns the global document object.
func Document() *html.Document {
	return html.GetDocument()
}

// Window returns the global window object.
func Window() *html.Window {
	return html.GetWindow()
}
