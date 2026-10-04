# Scan of opendocs: what to bring over, what to give back

This showcase (`examples/canvas-editor-showcase`) is the base for opendocs V1.
This page is a first pass over [elaous/opendocs](https://github.com/elaous/opendocs)
(read from its file names, `docs/ROADMAP.md`, `docs/architecture/` and the doc
comments of a few packages; the code behind most items was **not** read line
by line, so treat the effort notes as estimates).

## The main finding: two editor engines

opendocs does **not** use gojiberry's `canvas` package. Its editor is its own:
`editor/model` (30k lines: a gap-buffer `Document`, `BlockMeta` per line,
its own layout, columns and frames, tables, undo, sync) drawn on a canvas by
`editor/*.go` (wasm-only). It imports from gojiberry only `xango`, `domrt`,
`xangogio`, `components`, `crdt`, `buffer`, `fontserver` and `syncserver`.

So "port to opendocs V1" is really a choice, and it should be made before
copying features across:

| | Move opendocs onto gojiberry's `canvas` | Keep opendocs' engine, adopt gojiberry's services |
|---|---|---|
| What it means | `editor/model` + rendering are replaced by `canvas.TextFlow`/`PageSet`/elements | gojiberry's collab, saver, guard, share and stats are plugged into `model.Document` |
| Gains | one engine to maintain; xango element tree; per-user undo and the CRDT bridge already work | no rewrite; opendocs' columns/frames/tables stay |
| Costs | opendocs' multi-column frames, table merge/split/styling, forms, index marks, hyphenation, a11y surface would have to be rebuilt in `canvas` | the collab bridge is written against `canvas.TextFlow` (atoms/marks), so it would need a second bridge for `model.Document` |

The rest of this page lists the pieces that are worth having either way.

## Pieces that move cleanly (little coupling to either engine)

| opendocs | What it is | Why it moves easily | Where it fits here |
|---|---|---|---|
| `editor/export` (md, txt, html, rtf, epub, pdf, docx, brf) | `Exporter.Export(Snapshot) []byte` | The package deliberately knows nothing about the document; `Snapshot` is the seam. Needs one adapter, `canvas.TextFlow` items -> `export.Snapshot` | `transfer` has only Markdown and plain text today; this adds six formats |
| `editor/docximport`, `editor/pdftext` | Word and PDF import | Standalone | `transfer` importers (needs the reverse adapter: result -> `canvas.Item`s) |
| `editor/braille`, `translate`, `voice`, `featureserver`, `grammarllm`, `telemetry` | Small service clients | Standalone Go | gojiberry already has `brailleserver`, `translateserver`, `voiceserver`: reconcile the two sets, don't keep both |
| `model/spell.go`, `hunspell.go`, `langdetect.go`, `hyphenation*.go`, `dict/` | Spell check, language detection, hyphenation patterns (pure logic, no build tag) | Live in `package model` with the document, but the files are self-contained | A `canvas` feature (like `feature_text.go`): squiggles, suggestions in the context menu |
| `model/i18n.go`, `editor/i18n.go`, `langpack.go` | UI strings and language packs | Independent of the editor | `components` currently has no i18n |
| `storage` + `docmanager` (list, duplicate, tags, trash, versions) | Document lifecycle | gojiberry has `docstore` (adapters, ACL) and `saver`; opendocs adds versions, trash, tags, quota | Extend `saver`/`docstore` rather than add a second store |
| `model/signing.go`, `identity.go`, `acl.go`, `authgate.go` | Sign-in, signed documents, ACL | gojiberry has `auth`, `docstore/acl.go`, `guard` | Compare and merge; signing is new |
| `model/templates.go`, `snippets.go`, `mention.go`, `tag.go` | Templates, snippets, @mentions, tags | Mostly data plus a panel | New canvas features |
| `plugin.go`, `pluginapi.go`, `examples/plugins` | Sandboxed scripting | Depends on the editor API surface | Only after the engine choice above |
| `editor/featureconfig.go`, `docs/features.example.toml` | Build-time switches to compile subsystems out | Build tags | Useful for a V1 with optional braille, voice, hyphenation |
| `electron/`, `deploy/`, `Dockerfile`, `.github` CI, `scripts/` hooks | Desktop shell, deploy, CI, lint hooks | Independent | gojiberry has `.github/workflows/ci.yml`: compare before copying |

## Things gojiberry has that opendocs would gain

- `collab` (this PR series): per-user undo, roles enforced on the server, HTTP and WebSocket transports, `<collab.Collab>`, and now headers, footers and objects. opendocs uses `syncserver` with a character CRDT that, per its own roadmap, loses per-character formatting on remote edits and re-sends every line's order on each push.
- `guard`, token and origin checks in front of any hub.
- `canvas/xango`: the whole editor as one component tree (the showcase's `app.xg`).
- `saver`, `transfer`, `fonts` (lazy font downloads), and this PR's `canvas.CountText`/`TextFlow.Stats` and `share` (links to selected text).

## What opendocs has in the editor that the showcase does not

By roadmap entry: multi-column text and linked frames, background images,
footnote and index marks, table cell merge/split and per-cell styles, form
fields, a table of contents object, bullet-list symbols and nesting options,
find and replace, a special-characters picker, version history UI, margins
view, dictation and voice-over, braille export and an accessible text
surface, RTL and non-Latin script work (task #134), an Electron shell.

## Status

Steps 2, 3 and 4 of the order below are done (export/import, spell/hyphenation/language detection, versions/trash/tags in `saver`), as ports written for gojiberry (an adapter from `canvas` items to `export.Snapshot`, an own DOCX writer and reader). Step 6 is partly done (per-block columns, table merge and styles). The engine question is still open; [opendocs-port-plan.md](opendocs-port-plan.md) says what to port next.

## Suggested order

1. Decide the engine question above (it changes everything below it).
2. `export` + `docximport` behind `transfer` through an adapter: the biggest user-visible gain for the least coupling, and it works with either engine.
3. Spell check, hyphenation and language detection as a `canvas` feature.
4. Reconcile `docstore`/`saver` with opendocs' versions, trash and tags.
5. Reconcile the braille, translate and voice services (one set).
6. Rebuild the missing layout features (columns, frames, table merge) in `canvas` if the first option is chosen.

## Open questions for the owner

- Is V1 meant to replace `editor/model` with `canvas`, or to wrap it? (The first looks like the intent of "ported to opendocs as V1", but it is the larger bet.)
- Licensing: opendocs has `COMMERCIAL-LICENSE.md`; moving code into gojiberry (or back) needs the licences to agree.
- The Hunspell dictionaries in `dict/` carry their own licences (see `dict/README.md`); they should not be copied without checking.
