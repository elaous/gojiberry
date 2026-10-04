# Plan: porting opendocs into gojiberry (as opendocs V1)

opendocs is old and "didn't quite work"; V1 replaces it and is part of the
showcase (`examples/canvas-editor-showcase`). Work happens here, not in the
opendocs repo. Rule: **port the clean pieces first, as components** (a Go
package or `components` element with its own tests), and rewrite the rest
against `canvas`. Never copy code that is tied to opendocs' own engine
(`editor/model`); rewrite it on `canvas.TextFlow`.

Decision recorded: V1 sits on gojiberry's `canvas` (the first column of
[opendocs-scan.md](opendocs-scan.md)). opendocs' collab stays out; gojiberry's
`collab` replaces `syncserver` use.

## Already ported (this series)

| opendocs | Now in gojiberry |
|---|---|
| `editor/export` (md, txt, html, rtf, epub, pdf, docx, brf) | `export/` through the `Snapshot` seam; DOCX writer is new |
| `docximport`, `pdftext` | `docximport/`, `pdftext/` with input limits; `transfer` importers |
| spell, hunspell, langdetect, hyphenation, dict | `spell/`, `hyphen/`, `lang/` (embedded SCOWL en_US) |
| `translate`, `grammarllm` | `translate/`, `grammar/` clients (same Ollama request shape) |
| find/replace, special chars, TOC, footnotes, links, comments | `canvas` features and `components` |
| versions, trash, tags, duplicate | `saver` lifecycle |
| i18n + a11y surface | `i18n/`, `components/a11y.go` |
| table merge/styles, per-block columns | `canvas` tables, `ColumnConfig` |

## Next, cleanest first

Each row: what to port, how, test that comes with it. Sizes as in
[known-limits-plan.md](known-limits-plan.md).

| # | opendocs | Port as | Size | Notes |
|---|---|---|---|---|
| 1 | `docmanager` UI (breadcrumb, list, confirm, collaborators panel, history UI) | `components.DocManager`, `components.People`, on `saver` + `docstore/acl` | M | Data side exists; this is the UI. The People panel is the only real gap in sharing. |
| 2 | `identity.go`, `authgate.go`, login page | `components.Login` on `auth`/`guard` | S | Compare with `auth` first; keep one token format. |
| 3 | `templates.go`, `snippets.go`, `tag.go`, `mention.go` | `canvas` feature + panel; tags already in `docfile.Meta` | M | Templates as `docfile.Document` values; snippets as inserted `Item`s; @mentions as a mark. |
| 4 | `margins.go` | `components.MarginsEditor` over page props | S | |
| 5 | `featureconfig.go`, `docs/features.example.toml` | build tags per subsystem (braille, voice, hyphenation, spell) | S | Needed so V1 can ship without optional services. |
| 6 | `signing.go` | `docstore` signed-document wrapper (sign on save, verify on load) | M | New to gojiberry. Check licence/crypto choices before porting. |
| 7 | `voice`, `dictation.go`, `voiceover.go` | `components.Dictation` on `voiceserver` | M | Reconcile `voice` with `voiceserver`; keep one. |
| 8 | `indexmarks.go`, `toc.go` (index half) | `canvas` index marks + an Index object | M | TOC exists. |
| 9 | `formfields.go`, `formula.go` | elements + table formulas | L | Formulas need a small evaluator; do after tables are e2e-tested. |
| 10 | Frames (linked text frames) | `canvas` linked-frame element | L | Largest layout gap; design before porting. |
| 11 | Images: shape wrap, rotate, metadata, paste/drop | `canvas` image element + clipboard handler | M | |
| 12 | `plugin.go`, `pluginapi.go` | Worker-sandboxed scripting over a small `canvas` command API | L | Only once the command surface is stable. |
| 13 | `telemetry`, `debuglog` | opt-in logger fed from `#state` | S | |
| 14 | `electron/`, `Dockerfile`, `deploy/`, `.github` hooks | compare with `.github/workflows/ci.yml`; copy only what is missing | S | Licence check on `COMMERCIAL-LICENSE.md` first. |
| 15 | cdp e2e kit (`cdptestkit_test.go`, `*_cdp_test.go`) | `internal/e2etest` chromedp driver | M + L | See known-limits plan, section C. |

## Rewrite or drop

- `editor/model` (gap buffer, BlockMeta, own layout, undo, sync): rewrite on
  `canvas`; do not port.
- `syncserver` CRDT use and `syncwire.go`: drop (gojiberry `collab`).
- `legacy/`, `mobile` notes, `integration/`: read, port only what a test needs.
- `dict/` dictionaries: licence per file (`dict/README.md`); download at
  runtime, never vendor.

## Per-port checklist

1. Read the opendocs package and its tests; list its public behaviour.
2. Write the gojiberry package/component with the behaviour, not the code.
3. Port its tests as unit tests (and its cdp test once the chromedp kit lands).
4. Add the showcase tab/menu entry, i18n ids, and an a11y label.
5. `go test ./...`, lint, wasm vet; e2e for anything user-visible.
6. Mark the row done here and in `opendocs-missing-features.md`.

## Open questions for the owner

- Licensing of anything copied from opendocs (commercial licence file).
- Whether V1 keeps the Electron shell.
- Signed documents: is signing required for V1?
