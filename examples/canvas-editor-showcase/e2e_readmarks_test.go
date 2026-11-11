//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

const speechStub = `(() => {
	window.__utts = [];
	window.__voices = [];
	window.SpeechSynthesisUtterance = function (text) { this.text = text; };
	Object.defineProperty(window, 'speechSynthesis', { configurable: true, value: {
		speak(u) { window.__utts.push(u); },
		cancel() {}, pause() {}, resume() {},
		getVoices() { return window.__voices; } } });
})()`

func TestReadingAloudMarksTheSentenceAndTheWord(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).OpenURL(t, "/index.html")
	d.WaitSelector("canvas")
	d.Eval(speechStub, nil)
	d.Click(`button.gj-menubar-button:has-text("Read aloud")`)
	d.Click(`.gj-menubar-panel li.gj-menu-item:has-text("Read the document aloud")`)
	d.WaitFor("the first sentence", `window.__utts.length === 1`)
	d.WaitFor("the sentence is marked", `document.documentElement.getAttribute('data-gj-read-sentence') === '0-16'`)
	d.OK(!d.Bool(`document.documentElement.hasAttribute('data-gj-read-word')`), "no word until the engine says one")
	d.Eval(`window.__utts[0].onboundary({name: 'word', charIndex: 10, charLength: 6})`, nil)
	d.WaitFor("the word is marked", `document.documentElement.getAttribute('data-gj-read-word') === '10-16'`)
	d.Eval(`window.__utts[0].onboundary({name: 'sentence', charIndex: 0})`, nil)
	d.Eval(`window.__utts[0].onboundary({name: 'word', charIndex: 0})`, nil)
	d.WaitFor("the first word", `document.documentElement.getAttribute('data-gj-read-word') === '0-9'`)
	d.Eval(`window.__utts[0].onend()`, nil)
	d.WaitFor("the next sentence", `window.__utts.length === 2 && document.documentElement.getAttribute('data-gj-read-sentence') !== '0-16'`)
	d.OK(!d.Bool(`document.documentElement.hasAttribute('data-gj-read-word')`), "the word of the last sentence is gone")
	d.Click(`#read-bar button:has-text("Stop reading")`)
	d.WaitFor("the marks go", `!document.documentElement.hasAttribute('data-gj-read-sentence')`)
	d.NoErrors()
}

func TestNetworkVoicesAreChosenOnlyOnPurpose(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).OpenURL(t, "/index.html")
	d.WaitSelector("canvas")
	d.Eval(speechStub, nil)
	d.Eval(`window.__voices = [{name: 'Net', lang: 'en-US', localService: false}]`, nil)
	read := func() {
		d.Click(`button.gj-menubar-button:has-text("Read aloud")`)
		d.Click(`.gj-menubar-panel li.gj-menu-item:has-text("Read the document aloud")`)
		d.WaitSelector("#read-bar")
	}
	stop := func() {
		d.Click(`#read-bar button:has-text("Stop reading")`)
		d.WaitFor("the bar goes", `!document.getElementById('read-bar')`)
	}
	n := func() int { return d.Int(`window.__utts.length`) }

	read()
	d.WaitFor("speech", `window.__utts.length === 1`)
	d.OK(d.Bool(`window.__utts[0].voice === undefined`), "a voice that uses the internet is not chosen by default")
	d.OK(d.Text("#read-voice") == "The browser's own voice", "the bar says so: "+d.Text("#read-voice"))
	stop()

	d.Click(`button.gj-menubar-button:has-text("Read aloud")`)
	d.Click(`.gj-menubar-panel li.gj-menu-item:has-text("Allow voices that use the internet")`)
	d.WaitFor("the choice is kept", `JSON.parse(localStorage.getItem('showcase:a11y')).voices === 'net'`)
	before := n()
	read()
	d.WaitFor("speech", `window.__utts.length === `+e2etest.J(before+1))
	d.OK(d.Bool(`window.__utts[window.__utts.length - 1].voice && window.__utts[window.__utts.length - 1].voice.name === 'Net'`), "now the network voice is used")
	d.OK(d.Text("#read-voice") == "Voice that uses the internet", "the bar says so: "+d.Text("#read-voice"))
	stop()

	d.Eval(`window.__voices = [{name: 'Net', lang: 'en-US', localService: false}, {name: 'Local', lang: 'en-GB', localService: true}]`, nil)
	before = n()
	read()
	d.WaitFor("speech", `window.__utts.length === `+e2etest.J(before+1))
	d.OK(d.Bool(`window.__utts[window.__utts.length - 1].voice.name === 'Local'`), "a voice on the device wins even when the network is allowed")
	d.OK(d.Text("#read-voice") == "Voice on this device", "the bar says so: "+d.Text("#read-voice"))
	d.NoErrors()
}

func TestTheReaderVoiceIsChosenByAccentAndGender(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).OpenURL(t, "/index.html")
	d.WaitSelector("canvas")
	d.Eval(speechStub, nil)
	d.Eval(`window.__voices = [{name: 'Samantha', lang: 'en-US', localService: true}, {name: 'Daniel', lang: 'en-GB', localService: true}, {name: 'Anna', lang: 'de-DE', localService: true}]`, nil)
	open := func() {
		d.Click(`button.gj-menubar-button:has-text("Read aloud")`)
		d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has(> .gj-menu-label:text-is("Reader voice"))`)
	}
	open()
	d.OK(d.Has(`.gj-menubar-panel .gj-menu-label:text-is("Samantha (American, female)")`), "an American woman's voice is offered")
	d.OK(d.Has(`.gj-menubar-panel .gj-menu-label:text-is("Daniel (British, male)")`), "a British man's voice is offered")
	d.OK(!d.Has(`.gj-menubar-panel .gj-menu-label:has-text("Anna")`), "a voice of another language is not")
	d.Click(`.gj-menubar-panel .gj-menu-label:text-is("Daniel (British, male)")`)
	d.WaitFor("the choice is kept", `JSON.parse(localStorage.getItem('showcase:a11y')).voice === 'Daniel'`)
	d.Click(`button.gj-menubar-button:has-text("Read aloud")`)
	d.Click(`.gj-menubar-panel li.gj-menu-item:has-text("Read the document aloud")`)
	d.WaitFor("speech", `window.__utts.length === 1`)
	d.OK(d.Bool(`window.__utts[0].voice && window.__utts[0].voice.name === 'Daniel'`), "the chosen voice speaks")
	d.NoErrors()
}
