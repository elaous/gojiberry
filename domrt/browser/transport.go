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

type FetchTransport struct{}

// RoundTrip implements http.RoundTripper.
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
