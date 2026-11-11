//go:build js && wasm

package main

import (
	"context"
	"errors"
	"math"

	"syscall/js"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/speechcoach"
	"github.com/elaous/gojiberry/xango"
)

type localWhisper struct{ model string }

var _ speechcoach.Transcriber = localWhisper{}

func whisperAvailable() bool { return hasAddon("whisper", "transcribe") }

// Transcribe implements speechcoach.Transcriber.
func (w localWhisper) Transcribe(ctx context.Context, pcm []int16, prompt, lang string) (string, error) {
	if !whisperAvailable() {
		return "", errors.New("the page has no whisper add-on")
	}
	f32 := js.Global().Get("Float32Array").New(len(pcm))
	raw := make([]byte, len(pcm)*4)
	for i, s := range pcm {
		putFloat32(raw[i*4:], float32(s)/32768)
	}
	js.CopyBytesToJS(js.Global().Get("Uint8Array").New(f32.Get("buffer")), raw)
	opts := map[string]any{"samples": f32, "sampleRate": speechcoach.SampleRate, "language": lang, "prompt": prompt, "model": w.model}
	type result struct {
		v   js.Value
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := domrt.Await(addon("whisper").Call("transcribe", opts))
		done <- result{v, err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-done:
		if r.err != nil {
			return "", r.err
		}
		t := r.v.Get("text")
		if t.Type() != js.TypeString {
			return "", errors.New("the whisper add-on gave no text")
		}
		return t.String(), nil
	}
}

func putFloat32(b []byte, f float32) {
	u := math.Float32bits(f)
	b[0], b[1], b[2], b[3] = byte(u), byte(u>>8), byte(u>>16), byte(u>>24)
}

func (s *speechUI) localChoice() xango.Node {
	if !whisperAvailable() {
		return xango.Frag()
	}
	return xango.El("div", []xango.Attr{xango.A("id", "speech-local")}, components.Checkbox(components.CheckboxProps{
		Label:   T("speech.useLocal", "Dictate on this device (the whisper add-on) instead of sending the recording to the address above"),
		Checked: s.local, OnChange: func(on bool) { s.local = on; s.render() }}))
}
