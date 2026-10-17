//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"io"

	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/voice/read/hosted"
)

// The page's side of the hosted adapter (voice/read/hosted): the voices of a voice server the operator named as
// "server" in reader-voices.json. Nothing is asked of the server until a person chooses this reader, and the text
// goes to it only while it reads; the Reader menu says so, and it is offered only when voices that use the internet
// are allowed.

var hostedUI struct {
	engine  *hosted.Engine
	started bool
	loading bool
	msg     string
}

// hostedAvailable says the operator named a voice server and the person allowed voices that use the internet.
func hostedAvailable() bool { return piperUI.cfg.Server != "" && networkVoices() }

// ensureHosted creates the engine and fetches the server's list of voices (once).
func ensureHosted(renderUI func()) {
	if hostedUI.started || piperUI.cfg.Server == "" {
		return
	}
	hostedUI.started, hostedUI.loading = true, true
	hostedUI.engine = hosted.New(serverClient{base: piperUI.cfg.Server}, &webAudio{})
	hostedUI.engine.Load(context.Background(), func(err error) {
		hostedUI.loading = false
		if err != nil {
			hostedUI.msg = err.Error()
			hostedUI.started = false // asked again next time
		}
		renderUI()
	})
}

// serverClient is hosted.Client over fetch(): no cookies, no referrer.
type serverClient struct{ base string }

func (c serverClient) fetch(ctx context.Context, method, path string, body any, done func([]byte, error)) {
	go func() {
		ctrl := js.Global().Get("AbortController").New()
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			select {
			case <-ctx.Done():
				ctrl.Call("abort")
			case <-stop:
			}
		}()
		opts := map[string]any{"method": method, "signal": ctrl.Get("signal"), "credentials": "omit", "referrerPolicy": "no-referrer"}
		if body != nil {
			b, _ := json.Marshal(body)
			opts["body"] = string(b)
			opts["headers"] = map[string]any{"Content-Type": "application/json"}
		}
		resp, err := domrt.Await(js.Global().Call("fetch", c.base+path, opts))
		if err != nil {
			done(nil, err)
			return
		}
		if !resp.Get("ok").Bool() {
			done(nil, io.ErrUnexpectedEOF)
			return
		}
		buf, err := domrt.Await(resp.Call("arrayBuffer"))
		if err != nil {
			done(nil, err)
			return
		}
		done(bytesOf(js.Global().Get("Uint8Array").New(buf)), nil)
	}()
}

func (c serverClient) Voices(ctx context.Context, done func([]byte, error)) {
	c.fetch(ctx, "GET", "/voices", nil, done)
}

func (c serverClient) Speech(ctx context.Context, voice, text string, rate float64, done func([]byte, error)) func() {
	ctx, cancel := context.WithCancel(ctx)
	c.fetch(ctx, "POST", "/speak", map[string]any{"text": text, "voice": voice, "rate": rate}, done)
	return cancel
}
