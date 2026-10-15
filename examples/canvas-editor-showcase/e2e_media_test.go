//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// A video is not let in without its text; it can be put in anyway and then written up in its own editor, where a
// caption is added by its times and words, the card loses its mark, and the Check tab agrees.
func TestInsertAClipNeedsItsTextAndCaptionsCanBeEdited(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Click(`button[data-tab="images"]`)
	d.WaitSelector("#media-insert")

	// no address, then no text: refused, in words
	d.Click("#media-insert")
	d.WaitFor("asks for the address", `document.getElementById('media-message') && document.getElementById('media-message').textContent.includes('address of the clip')`)
	d.Fill("#media-url", "https://example.org/talk.mp4")
	d.Fill("#media-title", "The mayor's talk")
	d.Click("#media-insert")
	d.WaitFor("refused for lack of captions", `document.getElementById('media-message').textContent.toLowerCase().includes('caption')`)
	d.OK(!d.Has("#media-url-0"), "nothing was inserted")

	// put it in anyway: the card is marked and the panel lists what is missing
	d.Click("#media-insert-later")
	d.WaitSelector("#media-cap-0-add")
	d.OK(d.Has("#media-missing-0-0"), "the panel says what is missing")
	d.Click(`button[data-tab="check"]`)
	d.Click("#gj-check-run")
	d.WaitFor("a finding for the clip", `document.getElementById('gj-check-summary').textContent.length > 0 && !document.getElementById('gj-check-summary').textContent.includes('Nothing found')`)

	// a caption with times that run backwards is refused
	d.Click(`button[data-tab="images"]`)
	d.Fill("#media-cap-0-start", "0:08")
	d.Fill("#media-cap-0-end", "0:05")
	d.Fill("#media-cap-0-text", "Good morning.")
	d.Click("#media-cap-0-add")
	d.WaitFor("refused", `document.getElementById('media-cap-0-message').textContent.includes('must end after')`)

	// a good one is added and the track shows it
	d.Fill("#media-cap-0-start", "0:02")
	d.Fill("#media-cap-0-end", "0:08")
	d.Click("#media-cap-0-add")
	d.WaitFor("added", `document.getElementById('media-cap-0-track').value.includes('Good morning.')`)
	d.OK(strings.HasPrefix(d.Str(`document.getElementById('media-cap-0-track').value`), "WEBVTT"), "the track is WebVTT")
	d.WaitFor("nothing missing", `!document.getElementById('media-missing-0-0')`)

	// the reading copy names the clip
	d.WaitFor("the reading copy has the clip", `document.getElementById('a11y-text').textContent.includes("The mayor's talk")`)
}
