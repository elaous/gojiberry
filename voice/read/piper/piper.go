package piper

import (
	"context"
	"errors"
	"time"
)

// Fetcher gets the bytes at a URL.
type Fetcher interface {
	Fetch(ctx context.Context, url string, progress func(received, total int64), done func([]byte, error))
}

// Store keeps what was downloaded, under keys.
type Store interface {
	Get(key string, done func([]byte, bool))
	Put(key string, data []byte, done func(error))
	Delete(key string, done func(error))
	// Keys lists what is kept.
	Keys(done func([]string))
}

// Audio is sound: mono samples between -1 and 1.
type Audio struct {
	Samples    []float32
	SampleRate int
}

// Duration is how long the sound lasts.
func (a Audio) Duration() time.Duration {
	if a.SampleRate <= 0 {
		return 0
	}
	return time.Duration(float64(len(a.Samples)) / float64(a.SampleRate) * float64(time.Second))
}

// Runtime turns text into sound with a voice's model.
type Runtime interface {
	// Available says there is a runtime in this page.
	Available() bool
	Synthesize(model, config []byte, text string, rate float64, done func(Audio, error)) (cancel func())
}

// Output plays sound.
type Output interface {
	Play(a Audio, onTime func(pos time.Duration), onEnd func()) Playback
}

// Playback is sound being played.
type Playback interface {
	Stop()
	Pause()
	Resume()
}

// Errors the engine reports.
var (
	ErrNoRuntime = errors.New("piper: this page has no Piper runtime")
	// ErrNotInstalled: the voice to speak with has not been downloaded.
	ErrNotInstalled = errors.New("piper: that voice is not installed")
	// ErrUnknownVoice: the catalogue has no such voice.
	ErrUnknownVoice = errors.New("piper: the catalogue has no such voice")
	ErrBadDownload  = errors.New("piper: the downloaded file is not the one the catalogue describes")
)
