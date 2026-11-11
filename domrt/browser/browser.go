//go:build js && wasm

package browser

import (
	"encoding/json"
	"fmt"
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/domrt/html"
)

// Global returns the named global browser API object.
func Global(name string) js.Value {
	return js.Global().Get(name)
}

// Response is the result of Fetch.
type Response struct {
	Status int
	OK     bool
	body   js.Value

	done     func()
	timedOut *bool
	timeout  time.Duration
	url      string
}

// Close releases what a bounded fetch holds.
func (r *Response) Close() {
	if r.done != nil {
		r.done()
		r.done = nil
	}
}

func (r *Response) bodyErr(err error) error {
	if err != nil && r.timedOut != nil && *r.timedOut {
		return fmt.Errorf("fetch %s: timed out after %s", r.url, r.timeout)
	}
	return err
}

// Fetch wraps window.fetch with a plain GET.
func Fetch(url string) (*Response, error) {
	return doFetch(url, map[string]any{}, 0)
}

// FetchOptions configures FetchOpts.
type FetchOptions struct {
	Method    string
	Body      string
	BodyValue js.Value
	Headers   map[string]string
	Timeout   time.Duration
}

// FetchOpts wraps window.fetch with options.
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
	return doFetch(url, init, opts.Timeout)
}

// FetchTimeout is Fetch with a bounded wait.
func FetchTimeout(url string, timeout time.Duration) (*Response, error) {
	return doFetch(url, map[string]any{}, timeout)
}

func doFetch(url string, init map[string]any, timeout time.Duration) (*Response, error) {
	if timeout <= 0 {
		return awaitFetch(js.Global().Call("fetch", url, js.ValueOf(init)))
	}

	controller := js.Global().Get("AbortController").New()
	init["signal"] = controller.Get("signal")

	timedOut := new(bool)
	onTimeout := js.FuncOf(func(this js.Value, args []js.Value) any {
		*timedOut = true
		controller.Call("abort")
		return nil
	})
	timerID := js.Global().Call("setTimeout", onTimeout, timeout.Milliseconds())
	release := func() {
		js.Global().Call("clearTimeout", timerID)
		onTimeout.Release()
	}

	resp, err := awaitFetch(js.Global().Call("fetch", url, js.ValueOf(init)))
	if err != nil {
		release()
		if *timedOut {
			return nil, fmt.Errorf("fetch %s: timed out after %s", url, timeout)
		}
		return nil, err
	}
	// keep the timer: a stalled body must not hang readers
	resp.done, resp.timedOut, resp.timeout, resp.url = release, timedOut, timeout, url
	return resp, nil
}

func awaitFetch(promise js.Value) (*Response, error) {
	v, err := domrt.Await(promise)
	if err != nil {
		return nil, err
	}
	return &Response{Status: v.Get("status").Int(), OK: v.Get("ok").Bool(), body: v}, nil
}

// Text reads the full response body as a string.
func (r *Response) Text() (string, error) {
	defer r.Close()
	v, err := domrt.Await(r.body.Call("text"))
	if err != nil {
		return "", r.bodyErr(err)
	}
	return v.String(), nil
}

func (r *Response) JSON(v any) error {
	text, err := r.Text()
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(text), v)
}

// Bytes reads the full response body as raw bytes.
func (r *Response) Bytes() ([]byte, error) {
	defer r.Close()
	v, err := domrt.Await(r.body.Call("arrayBuffer"))
	if err != nil {
		return nil, r.bodyErr(err)
	}
	arr := js.Global().Get("Uint8Array").New(v)
	out := make([]byte, arr.Length())
	js.CopyBytesToGo(out, arr)
	return out, nil
}

type storageArea struct{ name string }

// LocalStorage wraps window.localStorage.
var LocalStorage = storageArea{"localStorage"}

// SessionStorage wraps window.sessionStorage.
var SessionStorage = storageArea{"sessionStorage"}

func (s storageArea) Get(key string) (value string, ok bool) {
	defer func() {
		if recover() != nil { // storage blocked: nothing is stored
			value, ok = "", false
		}
	}()
	v := js.Global().Get(s.name).Call("getItem", key)
	if v.IsNull() {
		return "", false
	}
	return v.String(), true
}

func (s storageArea) Set(key, value string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("browser: %s.setItem: %v", s.name, r)
		}
	}()
	js.Global().Get(s.name).Call("setItem", key, value)
	return nil
}

// Remove deletes key.
func (s storageArea) Remove(key string) {
	js.Global().Get(s.name).Call("removeItem", key)
}

// Clear removes every key in this storage area.
func (s storageArea) Clear() {
	js.Global().Get(s.name).Call("clear")
}

// Keys returns every key currently in this storage area.
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

// OnMessage registers the handler for incoming messages.
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

// Close closes the connection and releases its handlers.
func (s *Socket) Close() {
	s.v.Call("close")
	// close() is asynchronous: remove handlers before releasing them
	for _, ev := range []string{"onmessage", "onclose", "onerror"} {
		s.v.Set(ev, js.Null())
	}
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
