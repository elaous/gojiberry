package read

import "context"

// The port of a reader voice. Reading aloud is done by an Engine, whatever it is: the browser's own speech synthesis
// (the voices the device has), a neural voice such as Piper that was downloaded and runs in the page, or anything
// else that can say a sentence. The Player in this package walks the sentences of a document over an Engine; the
// page picks the engine and a voice. Adapters live next to what they wrap (voice/read/piper, and the page's own
// glue for the Web Speech API), so this package knows no browser.
//
// Everything is called on one thread (the page's): an adapter calls the Handler from its own events, never from a
// goroutine of its own.

// Engine says sentences aloud.
type Engine interface {
	// ID names the engine ("device", "piper") in settings.
	ID() string
	// Label is how a person is told about it.
	Label() string
	// Available says the engine can speak here at all (the browser has speech synthesis; the page has a Piper runtime).
	Available() bool
	// Voices lists the voices it can speak with right now (for a downloaded engine: those installed).
	Voices() []Voice
	// Speak starts saying the utterance and returns at once. It calls the handler as the speech goes on and, last,
	// OnEnd or OnError, exactly once, unless the returned Speech was cancelled (then neither).
	Speak(u Utterance, h Handler) Speech
}

// Utterance is one piece to say.
type Utterance struct {
	Text string
	// Lang is the BCP 47 tag of the text, "" for the page's own.
	Lang string
	// Voice is the ID of the voice to use ("" for the engine's own pick for Lang).
	Voice string
	// Rate is the speed: 1 is normal, 0.75 slower, 1.5 faster.
	Rate float64
}

// Handler is what an engine reports. Any of them may be nil.
type Handler struct {
	// OnStart is called when the sound begins, with the kind of voice that speaks (the Kind constants).
	OnStart func(kind string)
	// OnWord is called as the speech reaches a word: its rune index in the utterance's Text and, when the engine
	// knows it, its length in runes (0 when it does not). An engine that does not know where it is calls it not at all.
	OnWord func(index, length int)
	// OnEnd is called when the utterance has been said.
	OnEnd func()
	// OnError is called when it could not be said (and will not be).
	OnError func(err error)
}

// What speaks, as OnStart tells it.
const (
	KindDevice     = "device"     // a voice that runs on the device, picked from those the browser offers
	KindNetwork    = "network"    // a voice that may send the text to a speech service
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

// Downloader is what an engine offers when its voices have to be fetched before they can be used. The page lists
// Catalogue for a language with sizes and licences and asks for one to be installed; nothing is fetched until it does.
type Downloader interface {
	// Catalogue lists the voices that can be installed for a language tag ("de", "en-GB"). Empty until LoadCatalogue
	// has finished.
	Catalogue(lang string) []Installable
	// LoadCatalogue fetches the list of voices; done is called when it is in (or failed).
	LoadCatalogue(ctx context.Context, done func(error))
	// Installed says the voice is on the device.
	Installed(id string) bool
	// Install fetches the voice and keeps it. progress is called with the bytes received and the bytes expected;
	// done is called when the voice is ready (or failed).
	Install(ctx context.Context, id string, progress func(done, total int64), done func(error))
	// Remove deletes an installed voice.
	Remove(id string) error
}

// Installable is a voice that can be downloaded.
type Installable struct {
	Voice
	// Size is what has to be downloaded, in bytes.
	Size int64
	// Quality is the engine's own word for it ("low", "medium", "high").
	Quality string
	// Licence is where the terms of the voice's data are written (a link), "" when the catalogue does not say.
	Licence string
	// Installed says it is on the device already.
	Installed bool
}
