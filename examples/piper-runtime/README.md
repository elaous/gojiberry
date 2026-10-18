# A Piper runtime for the reader voices (US English, no GPL)

`piper-runtime.js` is a reference implementation of `window.addons.piper` (docs/reader-voices.md): it turns the text of a sentence into sound with a downloaded Piper voice, entirely in the page, using [onnxruntime-web](https://www.npmjs.com/package/onnxruntime-web) for both the voice and the phonemizer. The phonemizer is [`nekwo/openphonemizer-en-us-onnx`](https://huggingface.co/nekwo/openphonemizer-en-us-onnx) (BSD-3-Clause-Clear), so no espeak-ng (GPL) is involved. It is the operator's to host: this repository does not ship the models or onnxruntime.

## What it does

1. Numbers become words, punctuation and spaces are kept, and each word is looked up in the phonemizer's dictionary (275,000 words); a word that is not in it goes through the model (`openphonemizer-en_us-fp16.onnx`). Initialisms are spelled by letter.
2. The phonemes are mapped to the voice's ids (`phoneme_id_map`: BOS, PAD, each phoneme and a PAD, EOS) and the voice's ONNX model makes the sound, at the speed the page asks for.
3. A voice made for British English (its `espeak.voice` is not `en-us`) gets the phonemes mapped from US to British first. A voice for another language is refused with a message the page shows: this phonemizer is US English only.

## Install

Put these next to the page (or anywhere, then name them with the `data-` attributes in the header of the script):

| File | From |
|---|---|
| `piper-runtime.js` | this folder |
| `ort.min.js` and the `ort-wasm-*.wasm` files | `node_modules/onnxruntime-web/dist/` after `npm i onnxruntime-web` |
| `openphonemizer-en_us-fp16.onnx` (31 MB), `openphonemizer-en_us.json` (9 MB) | the model page above; compare the SHA-256 sums given in docs/reader-voices.md |

Then name the script in `reader-voices.json`: `{ "runtime": "piper-runtime.js" }`. Nothing is fetched until a person chooses the downloaded voices and a sentence is read; then the page loads the script, the script loads onnxruntime-web and the phonemizer's two files (the browser keeps them). Threads in onnxruntime-web need cross-origin isolation headers on the page; without them it runs single-threaded, which is slower but works.

## Checked

`node --test examples/piper-runtime/piper-runtime.test.js` runs the parts that need no model. With `PIPER_RUNTIME_MODELS=<folder with the four files below> NODE_PATH=<node_modules with onnxruntime-web>` it also runs the real models: `openphonemizer-en_us-fp16.onnx`, `openphonemizer-en_us.json`, `en_US-lessac-medium.onnx` and its `.json`. On 2026-10-18 that run, with the files from the voices catalogue and the model page, gave:

- the phoneme strings of seven sentences (plain words, words outside the dictionary, initialisms, hyphens, punctuation) **identical** to those of the phonemizer's own algorithm (as published in the model's runner, `speech_phonemize.py`) run in Python on onnxruntime;
- for "Hello world." with the randomness of the voice switched off, **the same sound** as onnxruntime in Python given the same phoneme ids (largest difference 1e-5 on samples between -1 and 1), and a length close to that of the voice with espeak-ng phonemes (0.84 s against 0.79 s);
- speech at 1.5 times the length when asked to be slower, as the page's speed choice does.

**What has not been checked:** that it sounds right to a listener (nobody has listened); onnxruntime-web in a browser (the test runs it in node, with the same WebAssembly build); other voices than `en_US-lessac-medium`, multi-speaker voices (the first speaker is used) and low or high quality voices; long sentences and numbers beyond the simple cases (a year is read as a number, "1999" as "one thousand nine hundred ninety nine"); the speed and memory on a phone (the voice model is 63 MB for the medium quality, and onnxruntime holds it in memory).
