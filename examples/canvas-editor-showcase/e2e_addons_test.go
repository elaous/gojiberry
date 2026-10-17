//go:build !(js && wasm)

package main

import (
	"slices"
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

// With the ocr add-on, a picture offers "Read the text in the picture": the add-on is given PNG bytes, the text is shown
// to be checked, and goes into the document, after the picture, only when the author says so.
func TestReadTheTextInAPictureWithTheOcrAddon(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Eval(`window.addons = window.addons || {}; window.addons.ocr = { recognize(o) {
		window.__ocr = {mime: o.mime, magic: Array.from(o.image.slice(0, 4)).join(','), lang: o.language};
		return Promise.resolve({text: 'Invoice 42, due in May'}); } }`, nil)
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Click(`button[data-tab="images"]`)
	d.WaitSelector("#img-insert")
	d.ChooseFile(func() { d.Click("#img-insert") }, "scan.png", pngBytes(t))
	d.WaitSelector("#img-ocr-0")
	d.Click("#img-ocr-0")
	d.WaitSelector("#img-ocr-text-0")
	d.OK(d.Value("#img-ocr-text-0") == "Invoice 42, due in May", "the text is shown: "+d.Value("#img-ocr-text-0"))
	d.OK(d.Str(`window.__ocr.magic`) == "137,80,78,71" && d.Str(`window.__ocr.lang`) == "en", "the add-on was given PNG bytes")
	d.OK(!strings.Contains(d.Text("#a11y-text"), "Invoice 42"), "nothing is in the document yet")
	d.Fill("#img-ocr-text-0", "Invoice 42, due in June")
	d.Click("#img-ocr-insert-0")
	d.WaitFor("the corrected text is in the document", `document.getElementById('a11y-text').textContent.includes('Invoice 42, due in June')`)
	d.NoErrors()
}

// With the translate add-on (and no translation server named in the address), the Language tab translates the
// selection on the device: the add-on gets the plain language tag and the text, and the translation replaces the
// selection only when asked.
func TestTranslateTheSelectionWithTheTranslateAddon(t *testing.T) {
	d := e2etest.NewSite(t).OpenURL(t, "/index.html?lang=en")
	d.WaitSelector("canvas")
	d.Eval(`window.addons = window.addons || {}; window.addons.translate = { translate(o) {
		window.__tl = {text: o.text, to: o.to, from: o.from};
		return Promise.resolve({text: '[' + o.to + '] ' + o.text, detected: 'en'}); } }`, nil)
	d.ToggleMode()
	d.Click(`button[data-tab="language"]`)
	d.WaitSelector(".gj-lang-target")
	d.CanvasDblClick(240, 164)
	d.Click(`button[data-tab="stats"]`)
	d.Click(`button[data-tab="language"]`)
	d.Select(".gj-lang-target", "FR")
	d.Click(".gj-lang-translate")
	d.WaitSelector(".gj-lang-translation")
	translated := d.Text(".gj-lang-translation")
	d.OK(strings.Contains(translated, "[fr] "), "the add-on translated it: "+translated)
	d.OK(d.Str(`window.__tl.to`) == "fr" && d.Str(`window.__tl.text`) != "", "with the plain language tag and the selected text")
	before := d.State().Str("text")
	d.Click(".gj-lang-use")
	d.Wait("s.text !== " + e2etest.J(before))
	d.OK(strings.Contains(d.State().Str("text"), "[fr] "), "Replace the selection puts the translation in the text")
	d.NoErrors()
}

// With the llm add-on, the Language tab checks grammar on the device (the model is asked for issues and its answer is
// checked against the text), and "Writing help" asks for the selection to be rewritten: the answer is shown, marked as
// machine-made, and goes into the document only when the author presses a button.
func TestLanguageTabWithTheLlmAddon(t *testing.T) {
	d := e2etest.NewSite(t).OpenURL(t, "/index.html?lang=en")
	d.WaitSelector("canvas")
	d.Eval(`window.__asks = []; window.addons = window.addons || {}; window.addons.llm = { generate(o) {
		window.__asks.push({format: o.format, prompt: o.prompt});
		if (o.format === 'json') {
			const text = o.prompt.split('Text:\n').pop();
			const i = text.indexOf('canvas');
			return Promise.resolve({text: JSON.stringify([{offset: i, length: 6, message: 'plural', suggestion: 'canvases'}])});
		}
		return Promise.resolve({text: 'Here is the result:\n"A short version."'});
	} }`, nil)
	d.ToggleMode()
	d.Click(`button[data-tab="language"]`)
	d.WaitSelector(".gj-lang-check")

	// grammar through the add-on
	d.Click(".gj-lang-check")
	d.WaitFor("an issue", "document.querySelector('.gj-lang-issue')")
	d.OK(d.State().Int("language.issues") == 1, "an issue is found by the model on the device")
	d.OK(strings.Contains(d.Text(".gj-lang-apply"), "canvases"), "with the fix on offer: "+d.Text(".gj-lang-apply"))

	// writing help on a selection
	d.OK(d.Disabled("#help-shorter"), "asking waits for a selection")
	d.CanvasDblClick(240, 164)
	d.Click(`button[data-tab="stats"]`)
	d.Click(`button[data-tab="language"]`)
	d.Click("#help-shorter")
	d.WaitSelector("#help-result")
	d.OK(d.Value("#help-result") == "A short version.", "the answer is tidied: "+d.Value("#help-result"))
	d.OK(d.Bool(`window.__asks[window.__asks.length - 1].prompt.includes('shorter')`), "and the model was asked for a shorter version")
	before := d.State().Str("text")
	d.OK(d.State().Str("text") == before && !strings.Contains(before, "A short version."), "nothing is in the document yet")
	d.Click("#help-replace")
	d.Wait("s.text !== " + e2etest.J(before))
	d.OK(strings.Contains(d.State().Str("text"), "A short version."), "Replace puts the answer in place of the selection")
	d.NoErrors()
}

// With the spell add-on, the dictionaries it keeps are offered in the Spelling tab next to the built-in one; choosing one
// reads the files the add-on hands over, and the words that are not in it are flagged.
func TestSpellingWithTheSpellAddonsDictionary(t *testing.T) {
	d := e2etest.NewSite(t).OpenURL(t, "/index.html?lang=en")
	d.WaitSelector("canvas")
	d.Eval(`(() => {
		const enc = s => new TextEncoder().encode(s);
		window.addons = window.addons || {};
		window.addons.spell = {
			models() { return Promise.resolve([{id: 'tiny', label: 'Tiny test language', lang: 'xx', size: 100, installed: true}, {id: 'big', label: 'Not installed', installed: false}]); },
			load(id) { window.__loaded = id; return Promise.resolve({aff: enc('SET UTF-8\n'), dic: enc('2\nhaus\nfenster\n')}); },
		};
	})()`, nil)
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Type(" haus fenster xyzzq ")
	d.Click(`button[data-tab="spell"]`)
	d.Click(".gj-spell-enable")
	d.WaitSelector(".gj-spell-dict")
	d.WaitFor("the add-on's dictionary is offered", `Array.from(document.querySelectorAll('.gj-spell-dict option')).some(o => o.value === 'addon:tiny')`)
	d.OK(!d.Bool(`Array.from(document.querySelectorAll('.gj-spell-dict option')).some(o => o.value === 'addon:big')`), "a dictionary that is not installed is not offered")
	d.Select(".gj-spell-dict", "addon:tiny")
	d.WaitFor("the dictionary is in use", `window.__loaded === 'tiny' && !document.querySelector('.gj-spell-dictnote')`)
	words := d.Texts(".gj-spell-word")
	d.OK(slices.Contains(words, "xyzzq") && !slices.Contains(words, "haus") && !slices.Contains(words, "fenster"), "words in the add-on's dictionary pass, the nonsense is flagged: "+strings.Join(words, " "))
	d.NoErrors()
}
