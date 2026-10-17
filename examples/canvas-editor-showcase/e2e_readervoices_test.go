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
	window.addons = { piper: { synthesize(model, config, text, lengthScale) {
		window.__synth.push({model: new TextDecoder().decode(model), text, lengthScale});
		return Promise.resolve({samples: new Float32Array(2205), sampleRate: 22050});
	}}};
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

// hostedStub stands in for a voice server named in reader-voices.json (https://tts.test: /voices and /speak, answering
// with a tenth of a second of silence as a WAV) and for Web Audio.
const hostedStub = `(() => {
	const wav = () => {
		const n = 800, b = new DataView(new ArrayBuffer(44 + n * 2));
		const s = (o, t) => { for (let i = 0; i < t.length; i++) b.setUint8(o + i, t.charCodeAt(i)); };
		s(0, 'RIFF'); b.setUint32(4, 36 + n * 2, true); s(8, 'WAVEfmt '); b.setUint32(16, 16, true); b.setUint16(20, 1, true); b.setUint16(22, 1, true);
		b.setUint32(24, 8000, true); b.setUint32(28, 16000, true); b.setUint16(32, 2, true); b.setUint16(34, 16, true); s(36, 'data'); b.setUint32(40, n * 2, true);
		return new Uint8Array(b.buffer);
	};
	const realFetch = window.fetch.bind(window);
	window.__asked = []; window.__posts = [];
	window.fetch = (u, o) => {
		u = String(u);
		if (u === 'reader-voices.json') return Promise.resolve(new Response('{"server":"https://tts.test"}'));
		if (u === 'https://tts.test/voices') { window.__asked.push(u); return Promise.resolve(new Response('[{"id":"v1","name":"Ada","lang":"en-GB","licence":"https://tts.test/terms"}]')); }
		if (u === 'https://tts.test/speak') { window.__posts.push(JSON.parse(o.body)); return Promise.resolve(new Response(wav())); }
		return realFetch(u, o);
	};
	window.__srcs = [];
	window.AudioContext = function () {
		this.destination = {};
		Object.defineProperty(this, 'currentTime', {get() { return 0; }});
		this.resume = () => Promise.resolve(); this.suspend = () => Promise.resolve();
		this.createBuffer = () => ({copyToChannel() {}});
		this.createBufferSource = () => { const s = {connect() {}, start() {}, stop() {}, onended: null}; window.__srcs.push(s); return s; };
	};
})()`

// Voices on a server: nothing is asked of the server before the person chooses them, they are used only when voices
// that use the internet are allowed, and the text goes to the server while it reads (the bar says so).
func TestReadWithVoicesOnAServer(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).OpenURL(t, "/index.html?lang=en")
	d.WaitSelector("canvas")
	d.Eval(speechStub, nil)
	d.Eval(hostedStub, nil)
	d.Click(`button[data-tab="voices"]`)
	d.WaitSelector("#voices-hosted-note")
	d.OK(d.Int(`window.__asked.length`) == 0 && d.Int(`window.__posts.length`) == 0, "the server is not asked before the person chooses it")
	d.Click(`button.gj-menubar-button:has-text("Read aloud")`)
	d.Click(`.gj-menubar-panel li.gj-menu-item:has-text("Allow voices that use the internet")`)
	d.WaitFor("allowed", `JSON.parse(localStorage.getItem('showcase:a11y')).voices === 'net'`)
	d.Click(`#voices-panel button:has-text("Voices on a server")`)
	d.WaitFor("the list of voices is asked for", `window.__asked.length === 1`)
	d.Click(`button.gj-menubar-button:has-text("Read aloud")`)
	d.Click(`.gj-menubar-panel li.gj-menu-item:has-text("Read the document aloud")`)
	d.WaitFor("the server is asked to say the first sentence", `window.__posts.length === 1`)
	d.OK(d.Str(`window.__posts[0].text`) != "" && d.Str(`window.__posts[0].voice`) == "v1", "with the text and the voice for the language: "+d.Str(`JSON.stringify(window.__posts[0])`))
	d.WaitFor("sound is played", `window.__srcs.length === 1`)
	d.WaitFor("the bar says the text goes to the internet", `document.getElementById('read-voice') && document.getElementById('read-voice').textContent === 'Voice that uses the internet'`)
	d.Eval(`window.__srcs[0].onended()`, nil)
	d.WaitFor("the next sentence", `window.__posts.length === 2`)
	d.Click(`#read-bar button:has-text("Stop reading")`)
	d.WaitFor("the bar goes", `!document.getElementById('read-bar')`)
	d.NoErrors()
}
