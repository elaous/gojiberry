//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

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

func TestDownloadAReaderVoiceAndReadWithIt(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).OpenURL(t, "/index.html?lang=en")
	d.WaitSelector("canvas")
	d.Eval(speechStub, nil)
	d.Eval(piperStub, nil)
	d.Click(`button[data-tab="voices"]`)
	d.WaitSelector("#vsrc-piper")
	d.Click("#vsrc-piper button")
	d.WaitSelector("#voices-load")
	d.OK(d.Int(`window.__asked.filter(u => u === 'https://voices.test/voices.json').length`) == 0, "the catalogue is not asked for before the person says so")
	d.Click("#voices-load")
	d.WaitSelector("#voices-item-0")
	text := d.Text("#voices-item-0")
	d.OK(strings.Contains(text, "en-US") && strings.Contains(text, "medium") && strings.Contains(text, "MB"), "the voice is listed with its size: "+text)
	d.OK(strings.HasSuffix(d.Str(`document.getElementById('voices-licence-0').href`), "/MODEL_CARD"), "and the page of its terms")
	d.OK(d.Bool(`document.getElementById('voices-download-0').disabled`), "a voice of a catalogue whose terms the project has not read waits for the person to say they read them")
	d.Click("#voices-read-0 input")
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

func TestVoiceSourcesHubRepositoryAndOwnFiles(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).OpenURL(t, "/index.html?lang=en")
	d.WaitSelector("canvas")
	d.Eval(speechStub, nil)
	d.Eval(piperStub, nil)
	d.Eval(`(() => {
		const repoFiles = {
			'https://repo.test/pack/voices.json': JSON.stringify({"fr_FR-demo-medium": {key: "fr_FR-demo-medium", name: "demo", language: {code: "fr_FR"}, quality: "medium", num_speakers: 1,
				files: {"fr/demo/medium/fr_FR-demo-medium.onnx": {size_bytes: 5}, "fr/demo/medium/fr_FR-demo-medium.onnx.json": {size_bytes: 2}, "fr/demo/medium/MODEL_CARD": {size_bytes: 9}}}}),
			'https://repo.test/pack/fr/demo/medium/fr_FR-demo-medium.onnx': 'MODEL',
			'https://repo.test/pack/fr/demo/medium/fr_FR-demo-medium.onnx.json': '{}',
		};
		const prev = window.fetch;
		window.fetch = (u, o) => {
			u = String(u);
			if (u in repoFiles) { window.__asked.push(u); return Promise.resolve(new Response(repoFiles[u], {headers: {'content-length': String(repoFiles[u].length)}})); }
			if (u.startsWith('https://huggingface.co/rhasspy/piper-voices/')) { window.__asked.push(u); return Promise.resolve(new Response('not the real file', {headers: {'content-length': '17'}})); }
			return prev(u, o);
		};
	})()`, nil)
	d.Click(`button[data-tab="voices"]`)

	d.WaitSelector("#voices-item-3")
	hub := d.Text("#voices-list")
	d.OK(strings.Contains(hub, "CC0") && strings.Contains(hub, "Public domain") && strings.Contains(hub, "CC BY-SA 4.0"), "the hub shows the terms of its voices: "+hub)
	d.OK(strings.Contains(hub, "en-US") && strings.Contains(hub, "en-GB"), "American and British voices: "+hub)
	d.OK(d.Int(`window.__asked.filter(u => u !== 'reader-voices.json').length`) == 0, "nothing is asked of any server before the person says so")
	d.OK(!d.Bool(`document.getElementById('voices-download-0').disabled`), "a hub voice can be downloaded without a further confirmation")
	d.Click("#voices-download-0")
	d.WaitFor("a file that does not match what the hub pinned is refused", `(document.getElementById('voices-message')||{textContent: ''}).textContent.includes('could not be downloaded')`)
	d.OK(!d.Has("#voices-has-0"), "and nothing is kept")

	d.Click("#vsrc-add button")
	d.Fill("#vsrc-add-name", "Demo pack")
	d.Fill("#vsrc-add-address", "http://repo.test/pack/voices.json")
	d.Click("#vsrc-add-go")
	d.WaitFor("a plain http address is refused", `(document.getElementById('voices-message')||{textContent: ''}).textContent.includes('not the address of a catalogue')`)
	d.Fill("#vsrc-add-address", "https://repo.test/pack/voices.json")
	d.Click("#vsrc-add-go")
	d.WaitSelector("#vsrc-repo-load")
	d.OK(d.Int(`window.__asked.filter(u => u.startsWith('https://repo.test/')).length`) == 0, "the repository is not asked before the person says so")
	d.Click("#vsrc-repo-load")
	d.WaitSelector("#voices-item-0")
	d.OK(strings.HasPrefix(d.Str(`document.getElementById('voices-licence-0').href`), "https://repo.test/pack/") && strings.HasSuffix(d.Str(`document.getElementById('voices-licence-0').href`), "/MODEL_CARD"), "with its own model card")
	d.OK(d.Bool(`document.getElementById('voices-download-0').disabled`), "its voice waits for the person to say they read the terms")
	d.Click("#voices-read-0 input")
	d.Click("#voices-download-0")
	d.WaitSelector("#voices-has-0")
	d.OK(d.Int(`window.__asked.filter(u => u === 'https://repo.test/pack/fr/demo/medium/fr_FR-demo-medium.onnx').length`) == 1, "the files came from the repository's own address")
	d.Eval(`location.reload()`, nil)

	d.WaitSelector("canvas")
	d.Eval(speechStub, nil)
	d.Eval(piperStub, nil)
	d.Click(`button[data-tab="voices"]`)
	d.WaitSelector("#vsrc-repo-0")
	d.Click("#vsrc-files button")
	d.WaitSelector("#vsrc-files-model")
	d.ChooseFile(func() { d.Click("#vsrc-files-model") }, "mine.onnx", []byte("MODEL"))
	d.ChooseFile(func() { d.Click("#vsrc-files-config") }, "mine.onnx.json", []byte(`{"audio":{"sample_rate":22050},"language":{"code":"en_GB"},"phoneme_id_map":{"a":[1]}}`))
	d.WaitFor("both are chosen", `document.getElementById('vsrc-files-model-name').textContent === 'mine.onnx' && document.getElementById('vsrc-files-config-name').textContent === 'mine.onnx.json'`)
	d.OK(d.Bool(`document.getElementById('vsrc-files-go').disabled`), "keeping waits for the person's word that the voice is theirs to use")
	d.Click("#vsrc-files-confirm input")
	d.Click("#vsrc-files-go")
	d.WaitFor("it is kept", `(document.getElementById('voices-message')||{textContent: ''}).textContent.includes('en_GB-mine-own is kept')`)
	d.Click("#vsrc-device button")
	d.WaitSelector("#vsrc-device-about")
	d.NoErrors()
}
