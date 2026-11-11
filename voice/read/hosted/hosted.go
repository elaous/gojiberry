package hosted

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"strings"

	"github.com/elaous/gojiberry/voice/read"
	"github.com/elaous/gojiberry/voice/read/piper"
)

// Client talks to the server.
type Client interface {
	Voices(ctx context.Context, done func([]byte, error))
	Speech(ctx context.Context, voice, text string, rate float64, done func([]byte, error)) (cancel func())
}

// Engine is the hosted adapter, read.Engine.
type Engine struct {
	Client Client
	Output piper.Output

	voices []Entry
	loaded bool
}

// Entry is a voice of the server.
type Entry struct {
	ID, Name, Lang, Licence string
}

// New is an engine with its ports.
func New(c Client, out piper.Output) *Engine { return &Engine{Client: c, Output: out} }

var _ read.Engine = (*Engine)(nil)

// Errors the engine reports.
var (
	ErrNoServer = errors.New("hosted: no voice server is set")
	ErrBadSound = errors.New("hosted: the server sent sound that cannot be played")
)

// ID implements read.Engine.
func (e *Engine) ID() string { return "hosted" }

// Label implements read.Engine.
func (e *Engine) Label() string { return "Voices on a server" }

func (e *Engine) Available() bool { return e.Client != nil && e.Output != nil }

func (e *Engine) Load(ctx context.Context, done func(error)) {
	if e.Client == nil {
		done(ErrNoServer)
		return
	}
	e.Client.Voices(ctx, func(data []byte, err error) {
		if err == nil {
			var list []Entry
			list, err = parseVoices(data)
			if err == nil {
				e.voices, e.loaded = list, true
			}
		}
		done(err)
	})
}

// Loaded says the list is in.
func (e *Engine) Loaded() bool { return e.loaded }

// Voices implements read.Engine.
func (e *Engine) Voices() []read.Voice {
	out := make([]read.Voice, 0, len(e.voices))
	for _, v := range e.voices {
		out = append(out, read.Voice{ID: v.ID, Name: v.Name, Lang: v.Lang, Local: false})
	}
	return out
}

func (e *Engine) Licence(id string) string {
	for _, v := range e.voices {
		if v.ID == id {
			return v.Licence
		}
	}
	return ""
}

// Speak implements read.Engine.
func (e *Engine) Speak(u read.Utterance, h read.Handler) read.Speech {
	s := &speech{}
	fail := func(err error) {
		if !s.cancelled && h.OnError != nil {
			h.OnError(err)
		}
	}
	if !e.Available() {
		fail(ErrNoServer)
		return s
	}
	voice := u.Voice
	if voice == "" {
		voice = e.pick(u.Lang)
	}
	ctx, cancelCtx := context.WithCancel(context.Background())
	cancel := e.Client.Speech(ctx, voice, u.Text, u.Rate, func(wav []byte, err error) {
		if s.cancelled {
			return
		}
		var a piper.Audio
		if err == nil {
			a, err = DecodeWAV(wav)
		}
		if err != nil {
			fail(err)
			return
		}
		s.playback = piper.Say(e.Output, a, u.Text, read.KindNetwork, s.guard(h))
	})
	s.stop = func() {
		cancelCtx()
		if cancel != nil {
			cancel()
		}
	}
	return s
}

func (e *Engine) pick(lang string) string {
	lang = strings.ReplaceAll(lang, "_", "-")
	base := strings.ToLower(strings.SplitN(lang, "-", 2)[0])
	best := ""
	for _, v := range e.voices {
		if strings.EqualFold(v.Lang, lang) {
			return v.ID
		}
		if best == "" && (base == "" || strings.EqualFold(strings.SplitN(v.Lang, "-", 2)[0], base)) {
			best = v.ID
		}
	}
	return best
}

type speech struct {
	cancelled bool
	stop      func()
	playback  piper.Playback
}

func (s *speech) Cancel() {
	s.cancelled = true
	if s.stop != nil {
		s.stop()
	}
	if s.playback != nil {
		s.playback.Stop()
	}
}
func (s *speech) Pause() {
	if s.playback != nil {
		s.playback.Pause()
	}
}
func (s *speech) Resume() {
	if s.playback != nil {
		s.playback.Resume()
	}
}

func (s *speech) guard(h read.Handler) read.Handler {
	return read.Handler{
		OnStart: func(k string) {
			if !s.cancelled && h.OnStart != nil {
				h.OnStart(k)
			}
		},
		OnWord: func(i, n int) {
			if !s.cancelled && h.OnWord != nil {
				h.OnWord(i, n)
			}
		},
		OnEnd: func() {
			if !s.cancelled && h.OnEnd != nil {
				h.OnEnd()
			}
		},
		OnError: h.OnError,
	}
}

const MaxSamples = 48000 * 600

func DecodeWAV(b []byte) (piper.Audio, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return piper.Audio{}, ErrBadSound
	}
	var format, channels, bits int
	var rate int
	var data []byte
	for p := 12; p+8 <= len(b); {
		id, size := string(b[p:p+4]), int(binary.LittleEndian.Uint32(b[p+4:p+8]))
		body := p + 8
		end := body + size
		if size < 0 || end > len(b) {
			end = len(b)
		}
		switch id {
		case "fmt ":
			if end-body < 16 {
				return piper.Audio{}, ErrBadSound
			}
			format = int(binary.LittleEndian.Uint16(b[body:]))
			channels = int(binary.LittleEndian.Uint16(b[body+2:]))
			rate = int(binary.LittleEndian.Uint32(b[body+4:]))
			bits = int(binary.LittleEndian.Uint16(b[body+14:]))
		case "data":
			data = b[body:end]
		}
		p = end + end%2
		if data != nil {
			break
		}
	}
	bytesPer := bits / 8
	if data == nil || rate <= 0 || rate > 192000 || channels < 1 || channels > 2 || (format != 1 || bits != 16) && (format != 3 || bits != 32) {
		return piper.Audio{}, ErrBadSound
	}
	frames := len(data) / (bytesPer * channels)
	if frames == 0 || frames > MaxSamples {
		return piper.Audio{}, ErrBadSound
	}
	out := make([]float32, frames)
	for i := range out {
		var sum float32
		for c := 0; c < channels; c++ {
			at := (i*channels + c) * bytesPer
			if bits == 16 {
				sum += float32(int16(binary.LittleEndian.Uint16(data[at:]))) / 32768
			} else {
				sum += math.Float32frombits(binary.LittleEndian.Uint32(data[at:]))
			}
		}
		out[i] = sum / float32(channels)
	}
	return piper.Audio{Samples: out, SampleRate: rate}, nil
}
