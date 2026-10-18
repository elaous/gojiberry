/*
 * A reference Piper runtime for the reader voices (window.addons.piper), for US English voices, with no espeak-ng and
 * no GPL code: onnxruntime-web runs both the voice (Piper) and the phonemizer (openphonemizer, BSD-3-Clause-Clear,
 * https://huggingface.co/nekwo/openphonemizer-en-us-onnx). See README.md in this folder and docs/reader-voices.md.
 *
 * The page asks for one thing:   synthesize(model, config, text, lengthScale) -> {samples: Float32Array, sampleRate}
 *   model        Uint8Array of the voice's .onnx
 *   config       the text of its .onnx.json
 *   lengthScale  1 / speed (1 normal, 1.33 slower, 0.67 faster)
 *
 * Use in a page: name this script as "runtime" in reader-voices.json (the page loads it when a person chooses the downloaded
 * voices), or add it by hand:
 *
 *   <script src="piper-runtime.js"
 *           data-ort="ort.min.js"
 *           data-phonemizer-model="openphonemizer-en_us-fp16.onnx"
 *           data-phonemizer-data="openphonemizer-en_us.json"></script>
 *
 * The three data- attributes default to files of those names next to this script. Nothing but this script is loaded with
 * the page: onnxruntime-web (ort.min.js, with its .wasm files beside it) and the phonemizer's two files are fetched when
 * the first sentence is read. If the page has loaded `ort` already it is used. From code (and tests):
 * createPiperRuntime({ort, phonemizerModel, phonemizerData, fetch}) where the two are an address, bytes or parsed JSON.
 */
(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root && typeof document !== 'undefined') {
    const s = document.currentScript;
    const here = s && s.src ? s.src : '';
    const rel = (name) => (here ? new URL(name, here).href : name);
    root.addons = root.addons || {};
    root.addons.piper = api.createPiperRuntime({
      ortScript: (s && s.dataset.ort) || rel('ort.min.js'),
      phonemizerModel: (s && s.dataset.phonemizerModel) || rel('openphonemizer-en_us-fp16.onnx'),
      phonemizerData: (s && s.dataset.phonemizerData) || rel('openphonemizer-en_us.json'),
    });
  }
})(typeof window !== 'undefined' ? window : undefined, function () {
  'use strict';

  const PUNCTUATION = '().,:?!/–';
  const SPLIT = new RegExp('([' + PUNCTUATION.replace(/[\\\]^-]/g, '\\$&') + ' ])');
  const KEPT = new Set((PUNCTUATION + '- ').split(''));

  // The letters of an initialism are said by their names (a capital standing alone; "A" is left to the article).
  const LETTER_NAMES = Object.fromEntries('BCDEFGHIJKLMNOPQRSTUVWXYZ'.split('').map((c, i) => [c, (
    'bˈiː sˈiː dˈiː ˈiː ˈɛf dʒˈiː ˈeɪtʃ ˈaɪ dʒˈeɪ kˈeɪ ˈɛl ˈɛm ˈɛn ˈoʊ pˈiː kjˈuː ˈɑːɹ ˈɛs tˈiː jˈuː vˈiː ' +
    'dˈʌbəljuː ˈɛks wˈaɪ zˈiː').split(' ')[i]]));

  // ---- numbers as words (the model reads letters only) ----
  const ONES = ['zero', 'one', 'two', 'three', 'four', 'five', 'six', 'seven', 'eight', 'nine', 'ten', 'eleven', 'twelve',
    'thirteen', 'fourteen', 'fifteen', 'sixteen', 'seventeen', 'eighteen', 'nineteen'];
  const TENS = ['', '', 'twenty', 'thirty', 'forty', 'fifty', 'sixty', 'seventy', 'eighty', 'ninety'];
  const SCALES = [[1e9, 'billion'], [1e6, 'million'], [1e3, 'thousand']];

  function spellInteger(n) {
    if (n < 20) return ONES[n];
    if (n < 100) return TENS[Math.floor(n / 10)] + (n % 10 ? ' ' + ONES[n % 10] : '');
    if (n < 1000) return ONES[Math.floor(n / 100)] + ' hundred' + (n % 100 ? ' ' + spellInteger(n % 100) : '');
    for (const [size, name] of SCALES) {
      if (n >= size) return spellInteger(Math.floor(n / size)) + ' ' + name + (n % size ? ' ' + spellInteger(n % size) : '');
    }
    return String(n);
  }

  /** Numbers in the text as words: "1,250" and "12" and "3.5" ("three point five"). Too large a number is spelled digit by digit. */
  function spellNumbers(text) {
    return text.replace(/\d[\d,]*(?:\.\d+)?/g, (m) => {
      const [whole, frac] = m.replace(/,/g, '').split('.');
      const digits = (s) => s.split('').map((d) => ONES[+d]).join(' ');
      const w = whole.length > 12 ? digits(whole) : spellInteger(Number(whole));
      return ' ' + w + (frac ? ' point ' + digits(frac) : '') + ' ';
    });
  }

  // ---- US -> British espeak phonemes, for voices trained on espeak's "en" (not "en-us") ----
  const US_VOWEL = 'aeiouæɑɒɔəɛɜɪʊʌ';
  const NOT_BEFORE_VOWEL = '(?![ˈˌ]?[' + US_VOWEL + '])';
  const DARK = ''; // holds the "dark" vowel past the ɑː rule
  const RP_FROM_US = [
    ['ɑːɹ' + NOT_BEFORE_VOWEL, DARK], ['[ɔo]ːɹ' + NOT_BEFORE_VOWEL, 'ɔː'], ['ɜːɹ' + NOT_BEFORE_VOWEL, 'ɜː'],
    ['ɪɹ' + NOT_BEFORE_VOWEL, 'ɪə'], ['ɛɹ' + NOT_BEFORE_VOWEL, 'eə'], ['ʊɹ' + NOT_BEFORE_VOWEL, 'ʊə'],
    ['ɑː', 'ɒ'], ['oʊ', 'əʊ'], ['ɝ', 'ɜː'], ['ɚ', 'ə'], ['ɾ', 't'], ['ᵻ', 'ɪ'], [DARK, 'ɑː'],
    ['(?<=[ːəɪʊaeɛæʌɒ])ɹ' + NOT_BEFORE_VOWEL, ''],
  ].map(([us, rp]) => [new RegExp(us, 'gu'), rp]);

  /** espeak "en-us" phonemes as espeak "en" would write them. Lossy: "father" comes out with the vowel of "lot". */
  function rpFromUs(ipa) {
    for (const [us, rp] of RP_FROM_US) ipa = ipa.replace(us, rp);
    return ipa;
  }

  function expandAcronym(word) {
    return word.split('-').map((sub) => {
      const chars = [...sub];
      let out = '';
      for (let i = 0; i < chars.length; i++) {
        out += chars[i];
        const b = chars[i + 1];
        if (b !== undefined && b !== b.toLowerCase() && b === b.toUpperCase()) out += '-';
      }
      return out;
    }).join('-');
  }

  const isWordChar = (c) => /[\p{L}\p{N}]/u.test(c);
  const argmax = (a, from, to) => { let best = from; for (let i = from + 1; i < to; i++) if (a[i] > a[best]) best = i; return best - from; };
  const title = (w) => (w ? w[0].toUpperCase() + w.slice(1).toLowerCase() : w);

  /**
   * The phonemizer: text -> espeak-style US English phonemes. `data` is openphonemizer-en_us.json (parsed), `run` takes an
   * array of token ids and gives the model's logits as {data: Float32Array, frames, vocab}.
   */
  class OpenPhonemizer {
    constructor(data, run) {
      this.dictionary = data.dictionary;
      this.textTokens = data.text_tokens;
      this.phonemes = data.phoneme_tokens;
      this.repeats = data.char_repeats | 0;
      this.start = data.start_index | 0;
      this.end = data.end_index | 0;
      this.special = new Set([this.phonemes[0], this.phonemes[this.start], this.phonemes[this.end]]);
      this.run = run;
    }

    async phonemize(text) {
      text = spellNumbers(text).replace(/ \./g, '.').replace(/\./g, ' .');
      const cleaned = [...text].filter((c) => isWordChar(c) || KEPT.has(c)).join('');
      let out = '';
      for (const piece of cleaned.split(SPLIT)) if (piece) out += await this.word(piece);
      return out;
    }

    entry(word) {
      if (KEPT.has(word)) return word;
      if (LETTER_NAMES[word]) return LETTER_NAMES[word];
      for (const form of [word, word.toLowerCase(), title(word)]) {
        if (Object.prototype.hasOwnProperty.call(this.dictionary, form)) return this.dictionary[form];
      }
      return null;
    }

    async word(word) {
      const found = this.entry(word);
      if (found !== null) return found;
      const parts = expandAcronym(word).split(/(-)/);
      if (parts.length <= 1) return this.predict(word);
      let out = '';
      for (const part of parts) if (part) out += (this.entry(part) ?? await this.predict(part));
      return out;
    }

    async predict(word) {
      const ids = [];
      for (const c of word.toLowerCase()) {
        const id = this.textTokens[c];
        if (id === undefined) continue;
        for (let i = 0; i < this.repeats; i++) ids.push(id);
      }
      if (!ids.length) return '';
      const { data, frames, vocab } = await this.run([this.start, ...ids, this.end]);
      let out = '';
      let previous = null;
      for (let f = 0; f < frames; f++) {
        const token = argmax(data, f * vocab, (f + 1) * vocab);
        if (token === 0) continue;
        if (token !== previous) {
          if (token === this.end) break;
          const symbol = this.phonemes[token];
          if (!this.special.has(symbol)) out += symbol;
        }
        previous = token;
      }
      return out;
    }
  }

  /** The ids of Piper's input: BOS, PAD, each phoneme followed by PAD, EOS. Phonemes the voice does not know are left out. */
  function phonemeIds(phonemes, config) {
    const map = config.phoneme_id_map;
    const remap = config.phoneme_map || {};
    const ids = [...map['^'], ...map['_']];
    for (const p of phonemes) {
      for (const q of remap[p] || [p]) {
        if (!map[q]) continue;
        ids.push(...map[q], ...map['_']);
      }
    }
    ids.push(...map['$']);
    return ids;
  }

  /** Creates the runtime. `ort` is onnxruntime-web (or onnx runtime for node); the phonemizer files are fetched on first use. */
  function createPiperRuntime(opts) {
    const fetchFn = opts.fetch || (typeof fetch === 'function' ? fetch.bind(globalThis) : null);
    let phonemizer = null;
    let voice = { key: '', session: null };
    let generation = 0;

    let loadingOrt = null;
    const ortLib = () => {
      const o = opts.ort || (typeof globalThis !== 'undefined' ? globalThis.ort : null);
      if (!o) throw new Error('onnxruntime-web is not loaded');
      return o;
    };
    // onnxruntime-web is loaded on the first sentence, from the address named (a script element; the page's own if it has it)
    const loadOrt = () => {
      if ((opts.ort || (typeof globalThis !== 'undefined' && globalThis.ort)) || !opts.ortScript || typeof document === 'undefined') return Promise.resolve();
      loadingOrt = loadingOrt || new Promise((resolve, reject) => {
        const el = document.createElement('script');
        el.src = opts.ortScript;
        el.onload = resolve;
        el.onerror = () => { loadingOrt = null; reject(new Error('could not load onnxruntime-web from ' + opts.ortScript)); };
        document.head.appendChild(el);
      });
      return loadingOrt;
    };
    const bytesOf = async (source) => {
      if (source instanceof Uint8Array) return source;
      if (source instanceof ArrayBuffer) return new Uint8Array(source);
      const r = await fetchFn(source);
      if (!r.ok) throw new Error('could not fetch ' + source + ' (' + r.status + ')');
      return new Uint8Array(await r.arrayBuffer());
    };
    const jsonOf = async (source) => {
      if (typeof source === 'object' && source !== null && !(source instanceof Uint8Array) && !(source instanceof ArrayBuffer)) return source;
      return JSON.parse(new TextDecoder().decode(await bytesOf(source)));
    };

    async function getPhonemizer() {
      if (phonemizer) return phonemizer;
      phonemizer = (async () => {
        await loadOrt();
        const ort = ortLib();
        const [model, data] = await Promise.all([bytesOf(opts.phonemizerModel), jsonOf(opts.phonemizerData)]);
        const session = await ort.InferenceSession.create(model, { executionProviders: ['wasm'] });
        const run = async (ids) => {
          const input = new ort.Tensor('int64', BigInt64Array.from(ids, BigInt), [1, ids.length]);
          const out = await session.run({ text: input });
          const logits = out[session.outputNames[0]];
          return { data: logits.data, frames: logits.dims[1], vocab: logits.dims[2] };
        };
        return new OpenPhonemizer(data, run);
      })();
      phonemizer.catch(() => { phonemizer = null; }); // asked again next time
      return phonemizer;
    }

    // a voice's session is kept while the same voice reads (the page hands the bytes over again for each sentence)
    const fingerprint = (model) => {
      let h = 2166136261;
      const step = Math.max(1, Math.floor(model.length / 4096));
      for (let i = 0; i < model.length; i += step) h = Math.imul(h ^ model[i], 16777619) >>> 0;
      return model.length + ':' + h;
    };
    async function sessionFor(model) {
      const key = fingerprint(model);
      if (voice.key !== key) {
        voice = { key, session: ortLib().InferenceSession.create(model, { executionProviders: ['wasm'] }) };
        voice.session.catch(() => { voice = { key: '', session: null }; });
      }
      return voice.session;
    }

    async function synthesize(model, configText, text, lengthScale) {
      const mine = ++generation;
      const cancelled = () => mine !== generation;
      const config = typeof configText === 'string' ? JSON.parse(configText) : configText;
      const family = (config.language && config.language.family) || String(config.espeak && config.espeak.voice || '').split('-')[0];
      if (family && family !== 'en') {
        throw new Error('this runtime phonemizes US English only; the voice is for "' + family + '"');
      }
      await loadOrt();
      const ort = ortLib();
      const ph = await getPhonemizer();
      let phonemes = await ph.phonemize(text);
      const espeakVoice = (config.espeak && config.espeak.voice) || 'en-us';
      if (espeakVoice !== 'en-us') phonemes = rpFromUs(phonemes);
      if (cancelled()) throw new Error('cancelled');
      const ids = phonemeIds([...phonemes], config);
      const session = await sessionFor(model);
      if (cancelled()) throw new Error('cancelled');
      const inf = config.inference || {};
      const feeds = {
        input: new ort.Tensor('int64', BigInt64Array.from(ids, BigInt), [1, ids.length]),
        input_lengths: new ort.Tensor('int64', BigInt64Array.from([BigInt(ids.length)]), [1]),
        scales: new ort.Tensor('float32', Float32Array.from([
          inf.noise_scale ?? 0.667, (inf.length_scale ?? 1) * (lengthScale || 1), inf.noise_w ?? 0.8]), [3]),
      };
      if ((config.num_speakers || 1) > 1) feeds.sid = new ort.Tensor('int64', BigInt64Array.from([0n]), [1]);
      const out = await session.run(feeds);
      if (cancelled()) throw new Error('cancelled');
      return { samples: Float32Array.from(out[session.outputNames[0]].data), sampleRate: (config.audio && config.audio.sample_rate) || 22050 };
    }

    return { synthesize, cancel() { generation++; }, phonemize: async (t) => (await getPhonemizer()).phonemize(t) };
  }

  return { createPiperRuntime, OpenPhonemizer, spellNumbers, rpFromUs, phonemeIds, expandAcronym };
});
