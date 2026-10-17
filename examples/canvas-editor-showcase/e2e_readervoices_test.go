//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// piperStub stands in for the network, the Piper runtime and Web Audio: a catalogue with one English voice, a model of
// five bytes, a runtime that "synthesizes" a tenth of a second of silence, and an audio context whose sources end when
// the test says.
const piperStub = `(() => {
	const files = {
		'reader-voices.json': '{"catalogue":"https://voices.test/voices.json","base":"https://voices.test/"}',
		'https://voices.test/voices.json': JSON.stringify({"en_US-test-medium": {key: "en_US-test-medium", name: "test", language: {code: "en_US"}, quality: "medium", num_speakers: 1,
			files: {"en/en_US/test/medium/en_US-test-medium.onnx": {size_bytes: 5}, "en/en_US/test/medium/en_US-test-medium.onnx.json": {size_bytes: 2}, "en/en_US/test/medium/MODEL_CARD": {size_bytes: 9}}}}),
		'https://voices.test/en/en_US/test/medium/en_US-test-medium.onnx': 'MODEL',
		'https://voices.test/en/en_US/test/medium/en_US-test-medium.onnx.json': '{}',
	};
	const realFetch = window.fetch.bind(window);
	window.__asked = [];
	window.fetch = (u, o) => {
		u = String(u);
		if (u in files) { window.__asked.push(u); return Promise.resolve(new Response(files[u], {headers: {'content-length': String(files[u].length)}})); }
		return realFetch(u, o);
	};
	window.__synth = [];
	window.gjPiper = { synthesize(model, config, text, lengthScale) {
		window.__synth.push({model: new TextDecoder().decode(model), text, lengthScale});
		return Promise.resolve({samples: new Float32Array(2205), sampleRate: 22050});
	}};
	window.__srcs = [];
	let now = 0;
	window.AudioContext = function () {
		this.destination = {};
		Object.defineProperty(this, 'currentTime', {get() { return now; }});
		this.resume = () => Promise.resolve(); this.suspend = () => Promise.resolve();
		this.createBuffer = () => ({copyToChannel() {}});
		this.createBufferSource = () => { const s = {connect() {}, start() {}, stop() {}, onended: null}; window.__srcs.push(s); return s; };
	};
})()`

// Downloaded voices: the catalogue is asked for only when the person says so, a voice is downloaded with its size and
// licence shown, kept, used to read the document (the bar says a downloaded voice speaks) and removed.
func TestDownloadAReaderVoiceAndReadWithIt(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).OpenURL(t, "/index.html?lang=en")
	d.WaitSelector("canvas")
	d.Eval(speechStub, nil)
	d.Eval(piperStub, nil)
	d.Click(`button[data-tab="voices"]`)
	d.WaitSelector("#voices-load")
	d.OK(d.Int(`window.__asked.filter(u => u === 'https://voices.test/voices.json').length`) == 0, "the catalogue is not asked for before the person says so")
	d.Click("#voices-load")
	d.WaitSelector("#voices-item-0")
	text := d.Text("#voices-item-0")
	d.OK(strings.Contains(text, "en-US") && strings.Contains(text, "medium") && strings.Contains(text, "MB"), "the voice is listed with its size: "+text)
	d.OK(strings.HasSuffix(d.Str(`document.getElementById('voices-licence-0').href`), "/MODEL_CARD"), "and the page of its terms")
	d.Click("#voices-download-0")
	d.WaitSelector("#voices-has-0")
	d.WaitSelector("#voices-have-0")
	d.OK(strings.Contains(d.Text("#voices-have"), "Test"), "it is among the voices on the device: "+d.Text("#voices-have"))

	d.Click(`#voices-panel button:has-text("Downloaded voices (Piper)")`)
	d.Click(`button.gj-menubar-button:has-text("Read aloud")`)
	d.Click(`.gj-menubar-panel li.gj-menu-item:has-text("Read the document aloud")`)
	d.WaitFor("the runtime was asked to say the first sentence", `window.__synth.length === 1`)
	d.OK(d.Str(`window.__synth[0].model`) == "MODEL", "with the voice's model")
	d.WaitFor("sound is played", `window.__srcs.length === 1`)
	d.WaitFor("the bar says which voice", `document.getElementById('read-voice') && document.getElementById('read-voice').textContent.includes('Downloaded voice')`)
	d.Eval(`window.__srcs[0].onended()`, nil)
	d.WaitFor("the next sentence", `window.__synth.length === 2`)
	d.Click(`#read-bar button:has-text("Stop reading")`)
	d.WaitFor("the bar goes", `!document.getElementById('read-bar')`)

	d.Click(`button[data-tab="voices"]`)
	d.WaitSelector("#voices-remove-0")
	d.Click("#voices-remove-0")
	d.WaitSelector("#voices-none")
	d.NoErrors()
}
