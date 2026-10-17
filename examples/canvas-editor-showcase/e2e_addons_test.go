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

// With the describe add-on, the draft description comes from what the picture shows: the add-on is given the picture as
// PNG bytes, the draft says it came from the add-on and can be wrong, and nothing goes into the description until the
// author uses it.
func TestDraftDescriptionFromTheDescribeAddon(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify({hint: 'on'}))`, nil)
	d.Reload()
	d.WaitSelector("canvas")
	d.Eval(`window.addons = window.addons || {}; window.addons.describe = { describe(o) {
		window.__desc = {mime: o.mime, magic: Array.from(o.image.slice(0, 4)).join(','), lang: o.language, kind: o.image.constructor.name};
		return Promise.resolve({text: 'A bar chart with two blue bars.'}); } }`, nil)
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Click(`button[data-tab="images"]`)
	d.WaitSelector("#img-insert")
	d.ChooseFile(func() { d.Click("#img-insert") }, "bar.png", pngBytes(t))
	d.WaitSelector("#img-hint-0")
	d.Click("#img-hint-0")
	d.WaitSelector("#img-draft-0")
	draft := d.Text("#img-draft-0")
	d.OK(strings.Contains(draft, "A bar chart with two blue bars.") && strings.Contains(draft, "describe add-on") && strings.Contains(draft, "can be wrong"), "the draft says where it is from: "+draft)
	d.OK(d.Str(`window.__desc.magic`) == "137,80,78,71" && d.Str(`window.__desc.mime`) == "image/png" && d.Str(`window.__desc.kind`) == "Uint8Array" && d.Str(`window.__desc.lang`) == "en",
		"the add-on was given PNG bytes and the language")
	d.OK(d.Str(`document.getElementById('img-alt-0').value`) == "", "nothing is in the description yet")
	d.Click("#img-usedraft-0")
	d.WaitFor("the description has the draft", `document.getElementById('img-alt-0').value === 'A bar chart with two blue bars.'`)
	d.NoErrors()
}
