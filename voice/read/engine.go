package read

import "context"

// Engine says sentences aloud.
type Engine interface {
	// ID names the engine ("device", "piper") in settings.
	ID() string
	// Label is how a person is told about it.
	Label() string
	Available() bool
	Voices() []Voice
	// Speak starts saying the utterance and returns at once.
	Speak(u Utterance, h Handler) Speech
}

// Utterance is one piece to say.
type Utterance struct {
	Text  string
	Lang  string
	Voice string
	Rate  float64
}

// Handler is what an engine reports.
type Handler struct {
	OnStart func(kind string)
	OnWord  func(index, length int)
	// OnEnd is called when the utterance has been said.
	OnEnd   func()
	OnError func(err error)
}

// What speaks, as OnStart tells it.
const (
	KindDevice     = "device"
	KindNetwork    = "network"
	KindDownloaded = "downloaded" // a voice that was downloaded and runs in the page
)

// Speech is an utterance being said.
type Speech interface {
	// Cancel stops it; no handler is called after.
	Cancel()
	// Pause and Resume hold and continue it.
	Pause()
	Resume()
}

type Downloader interface {
	Catalogue(lang string) []Installable
	LoadCatalogue(ctx context.Context, done func(error))
	// Installed says the voice is on the device.
	Installed(id string) bool
	// Install fetches the voice and keeps it.
	Install(ctx context.Context, id string, progress func(done, total int64), done func(error))
	// Remove deletes an installed voice.
	Remove(id string) error
}

// Installable is a voice that can be downloaded.
type Installable struct {
	Voice
	// Size is what has to be downloaded, in bytes.
	Size                  int64
	Quality               string
	Licence               string
	Terms, Credit, Source string
	// Installed says it is on the device already.
	Installed bool
}
