package braille

import "github.com/elaous/gojiberry/export"

// BRF renders Braille Ready Format output (task #180) -- plain ASCII
// braille text, the standard, embosser/braille-display-consumable
// on-the-wire representation real transcription tools (liblouis's own
// lou_translate CLI) already emit by default. Unlike every other
// Exporter in this package, real transcription can't happen inside
// Export itself: correctly applying Grade 1/Grade 2 contraction rules
// needs liblouis, a C library with no Go binding in this stack,
// reachable only via a network round trip to a configured
// gojiberry/brailleserver deployment (editor/braille's own Fetch, one
// level up in the editor package). Braille is populated by that round
// trip before Export is ever called -- the same "carries its own
// struct fields, set before Export is called" shape this package's
// own Exporter doc comment already anticipates for a future format
// needing configuration, just populated by an async fetch instead of
// a UI dialog.
type BRF struct {
	Braille string
}

// Export returns b.Braille's own bytes verbatim and a nil error --
// snap is unused (present only to satisfy the Exporter interface):
// the real transcription work already happened by the time this is
// called.
func (b BRF) Export(snap export.Snapshot) ([]byte, error) {
	return []byte(b.Braille), nil
}
