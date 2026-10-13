//go:build !(js && wasm)

package main

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// The Speech tab: record the phrases (with a fake microphone), keep the takes in the page, download them,
// and dictate through a recogniser the person names.
func TestSpeechTabRecordsPhrasesAndDictates(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	var heard int
	rec := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodOptions {
			return
		}
		heard++
		_, _ = w.Write([]byte(`{"text":"dictated words"}`))
	}))
	defer rec.Close()

	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode() // dictation goes in at the caret, in editing mode
	d.CanvasClick(240, 164)
	d.Click(`button[data-tab="speech"]`)
	d.WaitSelector("#speech-phrase")
	first := d.Text("#speech-phrase")
	d.OK(first != "", "a phrase is shown: "+first)
	d.OK(d.Str(`String(document.getElementById('speech-download').disabled)`) == "true", "nothing to download before a take")

	// Record a phrase for a second, then stop.
	d.Click("#speech-record")
	d.WaitFor("recording", `document.getElementById('speech-record').textContent.includes('Stop')`)
	d.Sleep(1200)
	d.Click("#speech-record")
	d.WaitFor("the take kept", `document.getElementById('speech-progress').textContent.includes('Recorded: 1')`)
	d.OK(strings.Contains(d.Text("#speech-count"), "1 recording"), "the take is counted: "+d.Text("#speech-count"))

	// The phrase stays until the person moves on; Next shows another.
	d.Click("#speech-next")
	d.WaitFor("another phrase", `document.getElementById('speech-phrase').textContent !== `+e2etest.J(first))

	// The download is a zip with the manifest and the audio.
	name, data := d.Download(func() { d.Click("#speech-download") })
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || name != "my-speech.zip" {
		t.Fatalf("download %q: %v", name, err)
	}
	var wav, manifest bool
	for _, f := range zr.File {
		wav = wav || strings.HasPrefix(f.Name, "audio/") && strings.HasSuffix(f.Name, ".wav")
		manifest = manifest || f.Name == "manifest.json"
	}
	d.OK(wav && manifest, "the zip holds the manifest and a recording")

	// Dictation sends one recording to the address typed, and puts the answer in the document.
	d.OK(d.Str(`String(document.getElementById('speech-dictate').disabled)`) == "true", "dictation is off without an address")
	d.Fill("#speech-address", rec.URL+"/transcribe")
	d.WaitFor("dictation on", `!document.getElementById('speech-dictate').disabled`)
	d.OK(heard == 0, "nothing was sent before dictating")
	d.Click("#speech-dictate")
	d.WaitFor("recording for dictation", `document.getElementById('speech-dictate').textContent.includes('send')`)
	d.Sleep(1000)
	d.Click("#speech-dictate")
	d.WaitFor("the answer in the document", `JSON.parse(document.getElementById('state').textContent).text.includes('dictated words')`)
	d.OK(heard == 1, "exactly one recording was sent")

	// Deleting needs a second press.
	d.Click("#speech-delete")
	d.OK(strings.Contains(d.Text("#speech-count"), "1 recording"), "one press deletes nothing")
	d.Click("#speech-delete")
	d.WaitFor("all deleted", `document.getElementById('speech-count').textContent.startsWith('0')`)
	for _, v := range d.Axe("#speech", axeDisabled...) {
		t.Errorf("the speech tab: %s", v)
	}
	d.NoErrors()
}
