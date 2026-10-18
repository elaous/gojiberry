# Reader voices

Reading aloud (Read aloud menu) says the document one sentence at a time. *What* says it is an **engine** behind one port, `read.Engine` in `voice/read`:

| Engine | What it is | Where it runs | Needs |
|---|---|---|---|
| **Voices on this device** (`device`) | the browser's own speech synthesis (Web Speech API) | the browser or the operating system; some voices send the text to a speech service, and the page uses those only when the person allowed it | nothing; more voices are added in the operating system's settings |
| **Downloaded voices (Piper)** (`piper`) | a neural voice, downloaded once into the browser | in the page, on the device; the text is never sent anywhere | a Piper runtime script (below), a download of 20 to 120 MB per voice |
| **Voices on a server** (`hosted`) | the voices of a voice server the operator runs (`cmd/voiceserver`, Piper behind it) | on the server; **the text is sent to it** while it is read, so the page offers it only when the person allowed voices that use the internet, and the bar says "Voice that uses the internet" | a voice server and its address in `reader-voices.json` (below) |

The port is small: `Available`, `Voices`, `Speak(utterance, handler)` returning a `Speech` that can be cancelled, paused and resumed. The handler reports `OnStart(kind)`, `OnWord(index, length)`, `OnEnd` and `OnError`. `read.Player` walks the sentences of a document over any engine (it starts the next, marks the sentence and the word, pauses, skips and stops); the page picks the engine and the voice. An engine that has voices to fetch also offers `read.Downloader` (`Catalogue`, `LoadCatalogue`, `Install`, `Installed`, `Remove`).

## The Piper adapter

`voice/read/piper` is the adapter. It holds four small ports, which the page fills with the browser and a test fills with fakes:

- `Fetcher` (the network: `fetch()` in the page), `Store` (where voices are kept: the Cache Storage, in a cache called `gj-reader-voices`), `Output` (the speakers: Web Audio), and `Runtime` (turns text into sound with a voice's model).

**Downloading.** Nothing is fetched until the person asks. The Reader voices tab shows what a click will contact (the catalogue's host), then lists the voices for the document's language with quality, size and a link to the voice's model card (whose voice it is and under what terms its data is used). Downloading a voice fetches its `.onnx.json` and `.onnx`, checks each against the size and MD5 sum the catalogue gives, and only then keeps both. A voice can be removed again. The default catalogue is `rhasspy/piper-voices` on Hugging Face; an operator can point the page at a mirror.

**Words.** Piper does not say where it is in the text, so the word being said is marked by how far the sound has got: a word is reached when the share of the sound played equals the share of the text before it. It follows pause and resume.

## What the operator adds

Running a model needs an ONNX runtime and a phonemizer (Piper turns text into phonemes first). They are scripts and binaries of their own, so they are **not** part of this repository; the page expects one script to offer them as `window.addons.piper` (`window.addons` is the one object the page looks in for optional add-ons, so a script adds its entry and leaves the others alone):

```js
window.addons = window.addons || {};
window.addons.piper = {
  // model: Uint8Array of the voice's .onnx; config: the text of its .onnx.json; lengthScale: 1 / speed
  async synthesize(model, config, text, lengthScale) {
    return { samples: Float32Array /* mono, -1..1 */, sampleRate: 22050 };
  },
  cancel() {}, // optional: stop the work in progress
};
```

### A phonemizer that needs no espeak-ng: openphonemizer (US English)

The usual phonemizer for Piper voices is espeak-ng, which is GPL. The operator who wants a runtime without it can use [`nekwo/openphonemizer-en-us-onnx`](https://huggingface.co/nekwo/openphonemizer-en-us-onnx), a US English grapheme-to-phoneme model in ONNX (it runs on `onnxruntime-web` like the voices do). What its model card says, read on 2026-10-18 (it is the model's own description; the runtime in `examples/piper-runtime/` has been run against it):

- **What it is.** A DeepPhonemizer forward transformer converted from OpenPhonemizer; output is espeak-style US English (en-us) phonemes, as logits over phoneme tokens. A word in its dictionary is looked up directly; the model is used for words that are not in it.
- **Licence.** BSD-3-Clause-Clear for the model and the dictionary (OpenPhonemizer) and for the ONNX conversion. The card gives no full licence text: the operator should read the upstream licence before shipping it.
- **Files** (about 102 MB in all; the page loads the one it needs): `openphonemizer-en_us-fp16.onnx` (30,977,712 bytes, the default, SHA-256 `a77b17647a02cb9ac42025cfc60c8e83c70f36785183a4388f17722fb1c7dc9b`), `openphonemizer-en_us-fp32.onnx` (61,437,915 bytes, `b0557ca5639d839d77d040c2610f2b1e95f584379cc1cdfa5c4984fb3d8f1b78`) and `openphonemizer-en_us.json` (9,223,033 bytes, `36dda6260c7e560a4d846528387fd07ababcd1012a52b530063fefadd5987db0`: the tokenizers and the dictionary). Compare the sums with the ones on the model page before use.
- **What to know.** English (US) only. Voices trained on British "en" phonemes need the US phonemes mapped first (the card names a `rp_from_us` step in its runner). The card does not describe the input encoding, but its runner (in another repository) and the export script do: the text goes in as lower-case letters, each repeated `char_repeats` (3) times between a start and an end token, as an int64 tensor `text` of shape `[1, T]`, and `logits` of shape `[1, T, vocab]` come back; the reference runtime follows that.

A runtime that uses it is in `examples/piper-runtime/` (see its README for the install and for what has been checked against the real models). What this means for the page: nothing changes in the contract above. `window.addons.piper.synthesize` still receives the text and returns samples; the phonemizer is the script's own business. Nothing is fetched from that model page by the page unless the operator's script does it, and the Add-ons tab would list the model like any other (`models()` in the add-on contract, with size and terms) once the script offers it. Other languages need another phonemizer; this one does not cover them.

A script of that shape can be built on `onnxruntime-web` and a phonemizer such as espeak-ng compiled to WebAssembly. Next to the page, `reader-voices.json` (optional) says where things are; every address is https or relative to the page:

```json
{ "catalogue": "https://voices.example/piper/voices.json", "base": "https://voices.example/piper/", "runtime": "piper-runtime.js" }
```

`catalogue` is the address of `voices.json`, `base` the address the paths in it are below (the catalogue's folder when left out), `runtime` the script to load when the page has no `addons.piper` yet. Without a runtime the Reader voices tab says so and offers nothing to download.

## The server adapter

`voice/read/hosted` asks a voice server for its voices (`GET /voices`, a list of `id`, `name`, `lang` and a `licence` address) and, for each sentence, for the sound (`POST /speak` with `text`, `voice` and `rate`, answered with a WAV the adapter decodes and plays through the same output as the Piper voices, marking the words by how far the sound has got). Nothing is asked of the server until a person chooses it; the text goes to it only while it reads. The server lists the voices of the file named by `GOJIBERRY_VOICESERVER_VOICES` (`[{"id","name","lang","licence","model"}]`, the model paths stay on the server) and refuses a voice it does not list; with no file `/speak` uses the one model it has. Name it in `reader-voices.json`:

```json
{ "server": "https://voices.example" }
```

The voices, their licences and who may use them are the operator's to decide; the page shows the address of a voice's terms when the server gives one (https only).

### Running a voice server

`cmd/voiceserver` execs a Piper binary for each request; it needs nothing but that and the voices. This is the setup it was run with on 2026-10-18, on Linux with Python 3.11 (the `piper` command comes with the `piper-tts` package, which brings its own espeak-ng data):

```sh
python3 -m venv piper && ./piper/bin/pip install piper-tts
curl -LO https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/lessac/medium/en_US-lessac-medium.onnx
curl -LO https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/lessac/medium/en_US-lessac-medium.onnx.json
cat > voices.json <<'EOF'
[{"id": "en_US-lessac-medium", "name": "Lessac", "lang": "en-US",
  "licence": "https://huggingface.co/rhasspy/piper-voices/blob/main/en/en_US/lessac/medium/MODEL_CARD",
  "model": "/srv/voices/en_US-lessac-medium.onnx"}]
EOF
GOJIBERRY_VOICESERVER_ADDR=:8084 \
GOJIBERRY_VOICESERVER_PIPER_BINARY=$PWD/piper/bin/piper \
GOJIBERRY_VOICESERVER_VOICES=$PWD/voices.json \
GOJIBERRY_VOICESERVER_ALLOW_ORIGINS=https://your-page.example \
  go run ./cmd/voiceserver
```

`GET /voices` lists the voices (the `model` paths stay on the server); `POST /speak` with `{"text", "voice", "rate"}` answers a WAV; a voice that is not in the file is refused. The page sends no cookie and no token, so allow its origin (`ALLOW_ORIGINS`) rather than a token; leave the server without a guard and anyone can use it for speech. Each voice has its own licence: list only those the operator may offer, and give each its `licence` address (https only), which the page shows. Put the server behind TLS (the page refuses an address that is not https).

Run against the real Piper with that voice, `/voices` and `/speak` answered as described, `rate` 1.25 gave a shorter sound (59,436 bytes against 62,508 at normal speed), an unknown voice was refused with 400, and the page's WAV decoder (`voice/read/hosted.DecodeWAV`) read the answer (22,050 Hz, 16-bit mono). **Not run:** the page against a running server in a browser (the browser test uses a stand-in), a container image (the Dockerfile does not install Piper), several voices at once, or load.

## What has been checked

See also [addons.md](addons.md) for the registry this script is added to. The port, the player and the adapter are tested with fakes (`voice/read`, `voice/read/piper`, `voice/read/hosted`, `voice/server`), and the page's flow (list, download, read with the voice, remove) with a stand-in for the network, the runtime and Web Audio (`TestDownloadAReaderVoiceAndReadWithIt`; `TestReadWithVoicesOnAServer` for the server, with a stand-in for it). **No real voice server has been run against the page** (the operator runs one). **A real Piper runtime has not been run against it**: whether a particular `addons.piper` script and a particular voice sound right is for the operator to try. The Cache Storage keeps a voice until the browser clears site data.
