// Package piper is the adapter that lets a downloaded Piper voice read a document aloud (voice/read.Engine). A Piper
// voice is a neural network (an .onnx file of 20 to 120 MB and a small .json beside it) that turns text into sound;
// here it runs in the page, on the device, so reading never leaves it. The voice is fetched once, from a catalogue,
// and kept in the browser (Store); nothing is fetched before the person asks for a voice to be installed.
//
// What this package does not contain is the network itself: running the model needs an ONNX runtime and a
// phonemizer, which are scripts and binaries of their own. They come in through the Runtime port, which the page
// fills from a script the operator supplies. The same goes for the pieces of the browser: Fetcher (the network),
// Store (where voices are kept), Output (the speakers). Each is a small interface, so the engine is the same in
// the page and in a test with fakes.
package piper

import (
	"context"
	"errors"
	"time"
)

// Fetcher gets the bytes at a URL.
type Fetcher interface {
	// Fetch calls progress (when not nil) with the bytes received so far and the total (0 when not known), then
	// done with the bytes or an error. Cancelling ctx stops it. Fetch returns at once; done is called later on the
	// page's thread.
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

// Runtime turns text into sound with a voice's model. It is the one part that needs an ONNX runtime and a
// phonemizer; the page supplies it (see docs/reader-voices.md for what the script has to offer).
type Runtime interface {
	// Available says there is a runtime in this page.
	Available() bool
	// Synthesize says text with the model and its configuration (the voice's .onnx.json). rate is the speed (1
	// normal). done is called with the sound or an error. The returned function cancels the work.
	Synthesize(model, config []byte, text string, rate float64, done func(Audio, error)) (cancel func())
}

// Output plays sound.
type Output interface {
	// Play plays the audio; onTime (when not nil) is called as it goes with how far it has got, onEnd when it has
	// played to the end. The returned Playback controls it.
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
	// ErrNoRuntime: the page has no Piper runtime, so nothing can be synthesized.
	ErrNoRuntime = errors.New("piper: this page has no Piper runtime")
	// ErrNotInstalled: the voice to speak with has not been downloaded.
	ErrNotInstalled = errors.New("piper: that voice is not installed")
	// ErrUnknownVoice: the catalogue has no such voice.
	ErrUnknownVoice = errors.New("piper: the catalogue has no such voice")
	// ErrBadDownload: what was downloaded is not the file the catalogue describes (the wrong size or checksum).
	ErrBadDownload = errors.New("piper: the downloaded file is not the one the catalogue describes")
)
