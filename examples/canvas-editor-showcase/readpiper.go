//go:build js && wasm

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"net/url"
	"strings"
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/voice/read/hub"
	"github.com/elaous/gojiberry/voice/read/piper"
)

var (
	errNoPiperRuntime    = piper.ErrNoRuntime
	errPiperNotInstalled = piper.ErrNotInstalled
)

var piperUI struct {
	engine      *piper.Engine
	cfg         piper.Config
	checked     bool // the configuration was read and the store looked at
	started     bool // ensurePiper began
	catLoading  bool
	catErr      string
	busy        string
	done, total int64
	cancel      context.CancelFunc
	msg         string
}

func ensurePiper(renderUI func()) {
	if piperUI.started {
		return
	}
	piperUI.started = true
	go func() {
		cfg := piper.DefaultConfig()
		if resp, err := domrt.Await(js.Global().Call("fetch", "reader-voices.json")); err == nil && resp.Get("ok").Bool() {
			if text, err := domrt.Await(resp.Call("text")); err == nil {
				if c, err := piper.ParseConfig([]byte(text.String())); err == nil {
					cfg = c
				} else {
					piperUI.msg = err.Error()
				}
			}
		}
		piperUI.cfg = cfg
		if cfg.Runtime != "" && !(jsRuntime{}).Available() {
			if err := loadScript(cfg.Runtime); err != nil {
				piperUI.msg = err.Error()
			}
		}
		piperUI.engine = piper.New(cfg, jsFetcher{}, cacheStore{}, jsRuntime{}, &webAudio{})
		piperUI.engine.AddVoices(hub.Voices())
		loadVoiceRepos()
		piperUI.engine.Refresh(func() {
			piperUI.checked = true
			if curPrefs.Reader == "hosted" && hostedAvailable() {
				ensureHosted(renderUI)
			}
			renderUI()
		})
	}()
}

func loadScript(src string) error {
	done := make(chan error, 1)
	ok := js.FuncOf(func(js.Value, []js.Value) any { done <- nil; return nil })
	bad := js.FuncOf(func(js.Value, []js.Value) any { done <- io.ErrUnexpectedEOF; return nil })
	defer ok.Release()
	defer bad.Release()
	el := js.Global().Get("document").Call("createElement", "script")
	el.Set("src", src)
	el.Set("onload", ok)
	el.Set("onerror", bad)
	js.Global().Get("document").Get("head").Call("appendChild", el)
	return <-done
}

type jsFetcher struct{}

func (jsFetcher) Fetch(ctx context.Context, u string, progress func(received, total int64), done func([]byte, error)) {
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
		resp, err := domrt.Await(js.Global().Call("fetch", u, map[string]any{"signal": ctrl.Get("signal"), "credentials": "omit", "referrerPolicy": "no-referrer"}))
		if err != nil {
			done(nil, err)
			return
		}
		if !resp.Get("ok").Bool() {
			done(nil, io.ErrUnexpectedEOF)
			return
		}
		var total int64
		if l := resp.Get("headers").Call("get", "content-length"); l.Type() == js.TypeString {
			if n, err := parseInt(l.String()); err == nil {
				total = n
			}
		}
		body := resp.Get("body")
		if body.IsUndefined() || body.IsNull() {
			buf, err := domrt.Await(resp.Call("arrayBuffer"))
			if err != nil {
				done(nil, err)
				return
			}
			done(bytesOf(js.Global().Get("Uint8Array").New(buf)), nil)
			return
		}
		rd := body.Call("getReader")
		var out bytes.Buffer
		for {
			chunk, err := domrt.Await(rd.Call("read"))
			if err != nil {
				done(nil, err)
				return
			}
			if chunk.Get("done").Bool() {
				break
			}
			out.Write(bytesOf(chunk.Get("value")))
			if progress != nil {
				progress(int64(out.Len()), total)
			}
		}
		done(out.Bytes(), nil)
	}()
}

func parseInt(s string) (int64, error) {
	var n int64
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return 0, io.ErrUnexpectedEOF
		}
		n = n*10 + int64(r-'0')
	}
	return n, nil
}

func bytesOf(v js.Value) []byte {
	b := make([]byte, v.Get("length").Int())
	js.CopyBytesToGo(b, v)
	return b
}

func u8(b []byte) js.Value {
	a := js.Global().Get("Uint8Array").New(len(b))
	js.CopyBytesToJS(a, b)
	return a
}

type cacheStore struct{}

const voiceCache = "gj-reader-voices"

func voiceURL(key string) string {
	return js.Global().Get("location").Get("origin").String() + "/__gj-reader-voices/" + url.PathEscape(key)
}

func openVoiceCache() (js.Value, error) {
	return domrt.Await(js.Global().Get("caches").Call("open", voiceCache))
}

func (cacheStore) Get(key string, done func([]byte, bool)) {
	go func() {
		c, err := openVoiceCache()
		if err != nil {
			done(nil, false)
			return
		}
		resp, err := domrt.Await(c.Call("match", voiceURL(key)))
		if err != nil || resp.IsUndefined() || resp.IsNull() {
			done(nil, false)
			return
		}
		buf, err := domrt.Await(resp.Call("arrayBuffer"))
		if err != nil {
			done(nil, false)
			return
		}
		done(bytesOf(js.Global().Get("Uint8Array").New(buf)), true)
	}()
}

func (cacheStore) Put(key string, data []byte, done func(error)) {
	go func() {
		c, err := openVoiceCache()
		if err != nil {
			done(err)
			return
		}
		resp := js.Global().Get("Response").New(u8(data))
		_, err = domrt.Await(c.Call("put", voiceURL(key), resp))
		done(err)
	}()
}

func (cacheStore) Delete(key string, done func(error)) {
	go func() {
		c, err := openVoiceCache()
		if err != nil {
			done(err)
			return
		}
		_, err = domrt.Await(c.Call("delete", voiceURL(key)))
		done(err)
	}()
}

func (cacheStore) Keys(done func([]string)) {
	go func() {
		c, err := openVoiceCache()
		if err != nil {
			done(nil)
			return
		}
		list, err := domrt.Await(c.Call("keys"))
		if err != nil {
			done(nil)
			return
		}
		var keys []string
		prefix := voiceURL("")
		for i := 0; i < list.Length(); i++ {
			if k, ok := strings.CutPrefix(list.Index(i).Get("url").String(), prefix); ok {
				if un, err := url.PathUnescape(k); err == nil {
					keys = append(keys, un)
				}
			}
		}
		done(keys)
	}()
}

type jsRuntime struct{}

func (jsRuntime) gj() js.Value { return addon("piper") }

func (r jsRuntime) Available() bool { return hasAddon("piper", "synthesize") }

func (r jsRuntime) Synthesize(model, config []byte, text string, rate float64, done func(piper.Audio, error)) func() {
	cancelled := false
	go func() {
		res, err := domrt.Await(r.gj().Call("synthesize", u8(model), string(config), text, 1/rate))
		if cancelled {
			return
		}
		if err != nil {
			done(piper.Audio{}, err)
			return
		}
		samples := res.Get("samples")
		raw := bytesOf(js.Global().Get("Uint8Array").New(samples.Get("buffer"), samples.Get("byteOffset"), samples.Get("byteLength")))
		out := make([]float32, len(raw)/4)
		for i := range out {
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
		}
		done(piper.Audio{Samples: out, SampleRate: res.Get("sampleRate").Int()}, nil)
	}()
	return func() {
		cancelled = true
		if c := r.gj().Get("cancel"); c.Type() == js.TypeFunction {
			c.Invoke()
		}
	}
}

type webAudio struct{ ctx js.Value }

func (w *webAudio) context() js.Value {
	if w.ctx.IsUndefined() || w.ctx.IsNull() {
		ctor := js.Global().Get("AudioContext")
		if !ctor.Truthy() {
			ctor = js.Global().Get("webkitAudioContext")
		}
		w.ctx = ctor.New()
	}
	return w.ctx
}

type playback struct {
	ctx, src js.Value
	timer    js.Value
	funcs    []js.Func
	stopped  bool
}

func (w *webAudio) Play(a piper.Audio, onTime func(time.Duration), onEnd func()) piper.Playback {
	ctx := w.context()
	ctx.Call("resume")
	raw := make([]byte, len(a.Samples)*4)
	for i, s := range a.Samples {
		binary.LittleEndian.PutUint32(raw[i*4:], math.Float32bits(s))
	}
	f32 := js.Global().Get("Float32Array").New(len(a.Samples))
	js.CopyBytesToJS(js.Global().Get("Uint8Array").New(f32.Get("buffer")), raw)
	buf := ctx.Call("createBuffer", 1, len(a.Samples), a.SampleRate)
	buf.Call("copyToChannel", f32, 0)
	src := ctx.Call("createBufferSource")
	src.Set("buffer", buf)
	src.Call("connect", ctx.Get("destination"))
	p := &playback{ctx: ctx, src: src}
	ended := js.FuncOf(func(js.Value, []js.Value) any {
		p.clear()
		if !p.stopped && onEnd != nil {
			onEnd()
		}
		return nil
	})
	p.funcs = append(p.funcs, ended)
	src.Set("onended", ended)
	started := ctx.Get("currentTime").Float()
	if onTime != nil {
		tick := js.FuncOf(func(js.Value, []js.Value) any {
			if !p.stopped {
				onTime(time.Duration((ctx.Get("currentTime").Float() - started) * float64(time.Second)))
			}
			return nil
		})
		p.funcs = append(p.funcs, tick)
		p.timer = js.Global().Call("setInterval", tick, 100)
	}
	src.Call("start")
	return p
}

func (p *playback) clear() {
	if !p.timer.IsUndefined() && !p.timer.IsNull() {
		js.Global().Call("clearInterval", p.timer)
		p.timer = js.Undefined()
	}
}

func (p *playback) Stop() {
	p.stopped = true
	p.clear()
	p.src.Set("onended", js.Null())
	func() {
		defer func() { _ = recover() }() // stop() throws when the source never started or has ended
		p.src.Call("stop")
	}()
	for _, f := range p.funcs {
		f.Release()
	}
	p.funcs = nil
}

func (p *playback) Pause()  { p.ctx.Call("suspend") }
func (p *playback) Resume() { p.ctx.Call("resume") }
