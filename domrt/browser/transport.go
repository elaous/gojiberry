//go:build js && wasm

package browser

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
)

// FetchTransport implements http.RoundTripper by translating a real
// *http.Request into a browser fetch() call and the resulting Response
// back into a real *http.Response -- task #107's own mobile-backend
// groundwork (see OnDesign's docs/ROADMAP.md, "genuine native mobile
// backend"): the point is that application code written against plain
// *http.Client/*http.Request never has to know or care whether it's
// running in a browser or as a real native binary. On native/mobile,
// http.DefaultTransport already does real TCP/TLS via the OS -- no
// wrapper needed there at all, that's the whole win. On web, wire this
// in instead:
//
//	client := &http.Client{Transport: browser.FetchTransport{}}
//	resp, err := client.Get("https://example.com")
//
// Deliberately not a replacement for Fetch/FetchOpts/FetchTimeout
// above: those already have real callers across this repo and
// OnDesign, migrating them is its own separate, mechanical task (see
// the ROADMAP entry's own "third bucket"), not attempted here. This is
// new infrastructure for code written from here on, or migrated
// deliberately -- both can coexist indefinitely, they just reach the
// same browser fetch() through two different Go-level shapes.
type FetchTransport struct{}

// RoundTrip implements http.RoundTripper. The request body (if any) is
// read fully into memory before the call -- fetch() has no streaming
// request-body support worth relying on across browsers -- and so is
// the response body, the same "read the whole thing, no true
// streaming" tradeoff Response.Bytes() above already makes for the
// same reason. Cancellation (req.Context() being done, including a
// deadline) aborts the underlying fetch via a real AbortController,
// mirroring doFetch's own timeout-driven abort above.
func (FetchTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		defer req.Body.Close()
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("reading request body: %w", err)
		}
		body = b
	}

	init := map[string]any{"method": req.Method}
	if len(req.Header) > 0 {
		// A real Headers object, built via repeated append calls, not a
		// plain map[string]any -- a plain object literal can only ever
		// hold one value per key, silently dropping a request with
		// multiple values for the same header (multiple Accept entries,
		// say); Headers.append preserves every one, matching what
		// *http.Request.Header (a map[string][]string) can actually
		// represent.
		headers := js.Global().Get("Headers").New()
		for k, vs := range req.Header {
			for _, v := range vs {
				headers.Call("append", k, v)
			}
		}
		init["headers"] = headers
	}
	if len(body) > 0 {
		arr := js.Global().Get("Uint8Array").New(len(body))
		js.CopyBytesToJS(arr, body)
		init["body"] = arr
	}

	controller := js.Global().Get("AbortController").New()
	init["signal"] = controller.Get("signal")

	ctx := req.Context()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			controller.Call("abort")
		case <-done:
		}
	}()

	resp, err := domrt.Await(js.Global().Call("fetch", req.URL.String(), js.ValueOf(init)))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("fetch %s: %w", req.URL, err)
	}

	respBody, err := domrt.Await(resp.Call("arrayBuffer"))
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	arr := js.Global().Get("Uint8Array").New(respBody)
	buf := make([]byte, arr.Length())
	js.CopyBytesToGo(buf, arr)

	header := http.Header{}
	entries := resp.Get("headers").Call("entries")
	for {
		next := entries.Call("next")
		if next.Get("done").Bool() {
			break
		}
		pair := next.Get("value")
		header.Add(pair.Index(0).String(), pair.Index(1).String())
	}

	status := resp.Get("status").Int()
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", status, resp.Get("statusText").String()),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(buf)),
		ContentLength: int64(len(buf)),
		Request:       req,
	}, nil
}
