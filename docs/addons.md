# Add-ons

Some features need a runtime or a model that the program should not carry: a speech engine, a vision model, a translator. They are **add-ons**: scripts that the person who runs the page adds, and that offer themselves as properties of one object, `window.addons`.

```js
window.addons = window.addons || {};
window.addons.piper = { synthesize(model, config, text, lengthScale) { /* ... */ } };
```

## Models

An add-on that keeps models (a speech model, a dictionary, a language pair) offers three more methods, and the Add-ons tab lists, downloads and removes them without knowing what they are:

```js
models()                     // -> Promise<[{id, label, lang?, size?, licence?, installed}]>
install(id, onProgress)      // -> Promise; onProgress(bytesDone, bytesTotal) as it goes
remove(id)                   // -> Promise
```

`id` is letters, digits and `._:-`; `size` is the download in bytes; `licence` is an https address (shown as a link) or a short name (shown as text). The page asks for `models()` only when the person presses "Show the models", and for `install` only when they press Download, with the size shown on the button.

## The rules every add-on follows

- **Optional.** The page asks for an add-on by name and does without it. No feature stops working because an add-on is missing; the page says what it lacks.
- **Nothing before a request.** An add-on's script is added to the page by the operator (or named in the page's own configuration file); its models and data are downloaded only when a person asks, with the size and the licence shown first.
- **On the device.** An add-on runs in the page. What it is given is not sent anywhere by the page; if an add-on itself talks to a service, it says so in its own documentation, and the page does not choose such an add-on for the person.
- **Asynchronous.** Its methods return promises (or take callbacks the page then waits for), so a slow model does not freeze the page.
- **Checked by the method.** The page calls `hasAddon(name, method)` before using one; an add-on that offers less than the page asks for counts as absent.

| Name | What it offers | Used by | State |
|---|---|---|---|
| `piper` | `synthesize(model, config, text, lengthScale)` and optionally `cancel()`: text to sound with a downloaded voice | Reader voices ([reader-voices.md](reader-voices.md)) | built |
| `whisper` | `transcribe({samples, sampleRate, language, prompt, model})` -> `Promise<{text}>`: `samples` is a `Float32Array`, 16 kHz mono; `model` is the model the person downloaded for the language, or "" for the add-on's own pick | dictation in the Speech tab ("Dictate on this device"); the recogniser at an address stays as the other way | built |
| `describe` | `describe({image, mime, language, model})` -> `Promise<{text}>`: `image` is a `Uint8Array` of PNG bytes (longer side at most 768 pixels), `mime` is `image/png`; the model is the one downloaded for the language | the "Draft a description" button under a picture's description, when Preferences > Editor has it on; without the add-on the draft is still made from shape and colours (`imagehint`) | built |
| `ocr` | `recognize({image, mime, language, model})` -> `Promise<{text}>`: `image` is a `Uint8Array` of PNG bytes (longer side at most 2048 pixels) | "Read the text in the picture" under each picture in the Pictures tab: the text is shown to be corrected, then put after the picture or used as its long description | built |
| `translate` | translation of the selection or the document | none yet | idea |
| `llm` | rewrite, summarise, explain | the AI helper hub (needs its own design first) | idea |
| `diagram` | render a flowchart or a map | the flowchart and map objects (not built) | idea |
| `spell` | dictionaries that work offline | the spelling panel (today it fetches `/dict` from the server) | idea |

"Idea" means the name and the purpose are noted here, not that anything is built: the contract of each is written when its feature is.
