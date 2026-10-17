//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// whisperStub is a whisper add-on: one model to download and a transcribe that records what it was given.
const whisperStub = `(() => {
	let installed = false;
	window.addons = window.addons || {};
	window.addons.whisper = {
		models() { return Promise.resolve([{id: 'tiny', label: 'Tiny', lang: 'en', size: 75000000, licence: 'https://example.org/mit', installed}]); },
		install(id, onProgress) { onProgress(5, 10); onProgress(10, 10); installed = true; return Promise.resolve(); },
		remove(id) { installed = false; return Promise.resolve(); },
		transcribe(o) { window.__tr = {n: o.samples.length, rate: o.sampleRate, model: o.model, kind: o.samples.constructor.name}; return Promise.resolve({text: 'spoken on the device'}); },
	};
})()`

// The Add-ons tab lists what the page has, shows the models of an add-on with size and terms only when asked,
// downloads and removes one; dictation can then run on the device through the whisper add-on and send nothing.
func TestAddonsTabAndDictationOnTheDevice(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).OpenURL(t, "/index.html?lang=en")
	d.WaitSelector("canvas")
	d.Eval(whisperStub, nil)
	d.ToggleMode()
	d.CanvasClick(240, 164)

	d.Click(`button[data-tab="addons"]`)
	d.WaitSelector("#addons-item-whisper")
	d.OK(strings.Contains(d.Text("#addons-present-whisper"), "On this page"), "the whisper add-on is there")
	d.OK(strings.Contains(d.Text("#addons-absent-ocr"), "Not on this page"), "and the ocr one is not")
	d.OK(!d.Has("#addons-whisper-0"), "no model is listed before it is asked for")
	d.Click("#addons-models-whisper")
	d.WaitSelector("#addons-whisper-0")
	row := d.Text("#addons-whisper-0")
	d.OK(strings.Contains(row, "Tiny") && strings.Contains(row, "MB") && strings.Contains(row, "Terms"), "the model with its size and terms: "+row)
	d.Click("#addons-whisper-0-install")
	d.WaitSelector("#addons-whisper-0-remove")
	d.OK(strings.Contains(d.Text("#addons-message"), "downloaded"), "it says it is downloaded: "+d.Text("#addons-message"))

	// dictation on the device
	d.Click(`button[data-tab="speech"]`)
	d.WaitSelector("#speech-local")
	d.OK(d.Str(`String(document.getElementById('speech-dictate').disabled)`) == "true", "dictation is off without an address or the add-on")
	d.SetChecked("#speech-local input", true)
	d.WaitFor("dictation on", `!document.getElementById('speech-dictate').disabled`)
	d.Click("#speech-dictate")
	d.WaitFor("recording", `document.getElementById('speech-dictate').textContent.includes('send') || document.getElementById('speech-dictate').textContent.includes('Stop')`)
	d.Sleep(1000)
	d.Click("#speech-dictate")
	d.WaitFor("the text in the document", `JSON.parse(document.getElementById('state').textContent).text.includes('spoken on the device')`)
	d.OK(d.Str(`window.__tr.model`) == "tiny" && d.Int(`window.__tr.rate`) == 16000 && d.Int(`window.__tr.n`) > 1000 && d.Str(`window.__tr.kind`) == "Float32Array",
		"the add-on was given 16 kHz samples and the model that was downloaded")

	d.Click(`button[data-tab="addons"]`)
	d.WaitSelector("#addons-whisper-0-remove")
	d.Click("#addons-whisper-0-remove")
	d.WaitSelector("#addons-whisper-0-install")
	d.NoErrors()
}
