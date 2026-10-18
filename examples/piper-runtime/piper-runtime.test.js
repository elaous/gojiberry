// node --test examples/piper-runtime   (or: make piper-runtime-test)
// The first group needs nothing. The second runs the real models and compares with reference values; it is skipped
// unless PIPER_RUNTIME_MODELS names a folder with openphonemizer-en_us-fp16.onnx, openphonemizer-en_us.json,
// en_US-lessac-medium.onnx and its .json, and onnxruntime-web is installed (npm i onnxruntime-web).
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createPiperRuntime, OpenPhonemizer, spellNumbers, rpFromUs, phonemeIds, expandAcronym } = require('./piper-runtime.js');

test('numbers become words', () => {
  assert.equal(spellNumbers('12').trim(), 'twelve');
  assert.equal(spellNumbers('1,250 cases').replace(/\s+/g, ' ').trim(), 'one thousand two hundred fifty cases');
  assert.equal(spellNumbers('3.5').replace(/\s+/g, ' ').trim(), 'three point five');
  assert.equal(spellNumbers('2000000').replace(/\s+/g, ' ').trim(), 'two million');
  assert.equal(spellNumbers('1234567890123456').replace(/\s+/g, ' ').trim().split(' ').length, 16);
});

test('US phonemes become British ones for voices trained on espeak "en"', () => {
  assert.equal(rpFromUs('fˈɑːks'), 'fˈɒks');
  assert.equal(rpFromUs('hˈɛloʊ'), 'hˈɛləʊ');
  assert.equal(rpFromUs('kˈɑːɹ'), 'kˈɑː'); // the r goes where British English drops it
  assert.equal(rpFromUs('vˈɛɹi'), 'vˈɛɹi'); // and stays before a vowel
});

test('an initialism is spelled letter by letter', () => {
  assert.equal(expandAcronym('DIY'), 'D-I-Y');
  assert.equal(expandAcronym('Hello'), 'Hello');
});

test('phoneme ids are BOS, PAD, each phoneme and a PAD, EOS; unknown phonemes are left out', () => {
  const config = { phoneme_id_map: { _: [0], '^': [1], $: [2], a: [5], b: [6, 7] } };
  assert.deepEqual(phonemeIds(['a', 'z', 'b'], config), [1, 0, 5, 0, 6, 7, 0, 2]);
  assert.deepEqual(phonemeIds(['x'], { phoneme_id_map: config.phoneme_id_map, phoneme_map: { x: ['a'] } }), [1, 0, 5, 0, 2]);
});

test('the phonemizer looks words up first, then asks the model, and collapses its repeats', async () => {
  const data = {
    dictionary: { hello: 'həlˈoʊ', World: 'wˈɜːld' },
    text_tokens: { a: 3, b: 4, c: 5, _: 0 },
    phoneme_tokens: { 0: '_', 1: '<en_us>', 2: '<end>', 3: 'x', 4: 'y' },
    char_repeats: 2, start_index: 1, end_index: 2,
  };
  const asked = [];
  // the model answers token 3 (x) for every input frame of the first letter and 4 (y) for the rest, then the end
  const run = async (ids) => {
    asked.push(ids);
    const vocab = 5, frames = ids.length;
    const out = new Float32Array(frames * vocab);
    ids.forEach((id, f) => { out[f * vocab + (id === 3 ? 3 : id === 2 ? 2 : 4)] = 1; });
    return { data: out, frames, vocab };
  };
  const p = new OpenPhonemizer(data, run);
  assert.equal(await p.phonemize('Hello, world'), 'həlˈoʊ, wˈɜːld');
  assert.equal(asked.length, 0); // both are in the dictionary: the model was not asked
  assert.equal(await p.phonemize('a'), 'yx'); // not in it: asked (the fake answers y for the start frame, x for the letter)
  assert.deepEqual(asked[0], [1, 3, 3, 2]);
  assert.equal(await p.predict('ab'), 'yxy'); // the repeated letters give one symbol each
  assert.equal(await p.predict('!?'), '');
});

test('a voice for another language is refused, and a missing onnxruntime is said', async () => {
  const rt = createPiperRuntime({ phonemizerModel: new Uint8Array(1), phonemizerData: {} });
  await assert.rejects(rt.synthesize(new Uint8Array(1), JSON.stringify({ language: { family: 'de' } }), 'Hallo', 1), /US English only/);
  await assert.rejects(rt.synthesize(new Uint8Array(1), JSON.stringify({ language: { family: 'en' } }), 'Hello', 1), /onnxruntime-web is not loaded/);
});

const dir = process.env.PIPER_RUNTIME_MODELS;
let ort = null;
try { ort = require('onnxruntime-web'); } catch { /* not installed */ }
const real = dir && ort ? test : test.skip;

real('the real phonemizer and a real voice: phonemes as expected, and sound of the right length and level', async () => {
  const rd = (f) => fs.readFileSync(path.join(dir, f));
  const rt = createPiperRuntime({ ort, phonemizerModel: new Uint8Array(rd('openphonemizer-en_us-fp16.onnx')), phonemizerData: JSON.parse(rd('openphonemizer-en_us.json').toString('utf8')) });
  // values from a reference run of the phonemizer's own algorithm (Python, onnxruntime) on the same files
  assert.equal(await rt.phonemize('Hello world.'), 'həlˈoʊ wˈɜːld .');
  assert.equal(await rt.phonemize('Is it ready? Yes! Zyxwvut flibbertigibbet, quokka.'), 'ɪz ɪt ɹˈɛdi? jˈɛs! zˈɪksɪvʌt flˈɪbɚtˌɪdʒəbbɪt, kwˈɑːkə .');
  assert.equal(await rt.phonemize('The NASA team and the DIY crowd met at the café.'), 'ðə ˈɛn-ɐ-ˈɛs-ɐ tˈiːm ænd ðə dˈiː-ˈaɪ-wˈaɪ kɹˈaʊd mˈɛt æt ðə kˈæf .');
  const config = JSON.parse(rd('en_US-lessac-medium.onnx.json').toString('utf8'));
  config.inference.noise_scale = 0; config.inference.noise_w = 0; // no randomness: the same sound every time
  const voice = new Uint8Array(rd('en_US-lessac-medium.onnx'));
  const a = await rt.synthesize(voice, JSON.stringify(config), 'Hello world.', 1);
  assert.equal(a.sampleRate, 22050);
  assert.equal(a.samples.length, 18432); // the reference run of the voice on the same phonemes
  let peak = 0; for (const v of a.samples) peak = Math.max(peak, Math.abs(v));
  assert.ok(peak > 0.3 && peak < 1.01, 'level ' + peak);
  const slow = await rt.synthesize(voice, JSON.stringify(config), 'Hello world.', 1.5);
  assert.ok(slow.samples.length > a.samples.length * 1.3, 'slower speech is longer');
});
