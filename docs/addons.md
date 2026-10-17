# Add-ons

Some features need a runtime or a model that the program should not carry: a speech engine, a vision model, a translator. They are **add-ons**: scripts that the person who runs the page adds, and that offer themselves as properties of one object, `window.addons`.

```js
window.addons = window.addons || {};
window.addons.piper = { synthesize(model, config, text, lengthScale) { /* ... */ } };
```

The rules every add-on follows:

- **Optional.** The page asks for an add-on by name and does without it. No feature stops working because an add-on is missing; the page says what it lacks.
- **Nothing before a request.** An add-on's script is added to the page by the operator (or named in the page's own configuration file); its models and data are downloaded only when a person asks, with the size and the licence shown first.
- **On the device.** An add-on runs in the page. What it is given is not sent anywhere by the page; if an add-on itself talks to a service, it says so in its own documentation, and the page does not choose such an add-on for the person.
- **Asynchronous.** Its methods return promises (or take callbacks the page then waits for), so a slow model does not freeze the page.
- **Checked by the method.** The page calls `hasAddon(name, method)` before using one; an add-on that offers less than the page asks for counts as absent.

| Name | What it offers | Used by | State |
|---|---|---|---|
| `piper` | `synthesize(model, config, text, lengthScale)` and optionally `cancel()`: text to sound with a downloaded voice | Reader voices ([reader-voices.md](reader-voices.md)) | built |
| `whisper` | speech to text on the device | dictation (today it goes through the voice server) | idea |
| `describe` | a description drafted from what a picture shows | the picture description hint (today only shape and colours, `imagehint`) | idea |
| `ocr` | text from scanned pages and images | importing scans (today `pdftext` reads text PDFs only) | idea |
| `translate` | translation of the selection or the document | none yet | idea |
| `llm` | rewrite, summarise, explain | the AI helper hub (needs its own design first) | idea |
| `diagram` | render a flowchart or a map | the flowchart and map objects (not built) | idea |
| `spell` | dictionaries that work offline | the spelling panel (today it fetches `/dict` from the server) | idea |

"Idea" means the name and the purpose are noted here, not that anything is built: the contract of each is written when its feature is.
