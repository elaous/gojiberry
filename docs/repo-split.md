# What could live in its own repository

A suggestion, not a plan that has started. Today everything is one Go module, `github.com/elaous/gojiberry`
(about 190 000 lines outside tests and examples), and `opendocs` (the text editor app) pulls it in with a `replace`
directive to `../gojiberry`. **Status:** the tier 1 xango row (`xango` + `domrt`, `cmd/xgc`, the VS Code extension) is done: it
lives in [github.com/elaous/xango](https://github.com/elaous/xango) and gojiberry depends on it at a tagged version. The rest of this page is the original
proposal. The names the owner gave the pieces: **Xangô** is the syntax and the engine, **Gojiberry**
was meant to be the components, **opendocs** is the editor app. This page checks that against what the code actually
depends on, and names other pieces that stand well on their own.

The import graph below was read with `go list` on 2026-10-16 (internal imports only). A package is a good candidate when
it imports nothing from the rest of the repository, or only things that would go with it.

## How to read the options

- **Own module, same repository** (a `go.mod` in the folder, `go.work` for development): the cheap first step. It
  forces the dependency edges to be honest and costs no new repository. Every row below can start here.
- **Own repository**: worth it when the piece has users who do not want the rest, a release rhythm of its own, and a
  README that can stand alone.
- **Stay**: the piece only makes sense with the rest.

**Update (2026-10-18):** the root now has `LICENSE` (AGPL-3.0), `NOTICE`, `COMMERCIAL.md` and `CONTRIBUTING.md` ([licence.md](licence.md)); a split copies them into each new repository, or gives a library its own licence. Before that, the repository had **no `LICENSE` file at its root** (opendocs has one, and a `COMMERCIAL-LICENSE.md`
that [opendocs-scan.md](opendocs-scan.md) already flags). A split needs one decision on licences first, because every new
repository needs its own and the Hunspell dictionaries, the Google Fonts files and the SCOWL data carry theirs.

## Tier 1: no dependency on the rest, split first

| Candidate | What it is | Imports from the repo | Why it stands alone | Notes |
| --- | --- | --- | --- | --- |
| **xango** (`xango`, `cmd/xgc`, `editors/vscode-xango`, `devreload`, `devwatch`, `wasmserve`) | The `.xg` syntax, the transpiler, the server and browser render path, the dev tools | none | This is the product the README opens with. It has no idea a document editor exists. | Needs `domrt` with it (next row). Keep the name; the logo and the "Xangô was called Gox" note come along. |
| **domrt** (`domrt`, `browser`, `html`, `svg`, `sw`, `worker`) | DOM, service worker and worker bindings for wasm | none | Usable by anything that renders in a browser from Go, with or without the syntax. | Could be one repository with xango (`xango` + `domrt`), or two. One is simpler: they release together today. |
| **observe** | Metrics, spans, PostHog and OTLP sinks, an allowlist model | none | A privacy-first observability core for any Go server, no PII by construction. | The closed allowlists in it name this project's metrics; they would move out as a list the host supplies. |
| **crdt** | Operation-based text CRDT | none | Small, no dependencies, useful alone. | Has to stay compatible with `collab` (below). |
| **spell, hyphen, lang, grammar, complete** | Hunspell loader, Liang hyphenation, language detection, an Ollama-compatible grammar client, completion lists | none (`canvas` imports `complete` only) | A "language tools" library: nothing here knows about a canvas. The Hunspell loader (cross products, compounds, `NEEDAFFIX`, `FORBIDDENWORD`) is rare in Go. | Natural home of the **Language Hub** that opendocs' roadmap sketches as "a separate open-source repo": `spell/dictserver` is already the thin proxy over upstream dictionaries it asks for. |
| **speechcoach, speechmodel, voice** | Atypical-speech coaching, federated averaging and a digest-pinned model loader, a speech server | none (`speechmodel` uses `speechcoach`) | Research-shaped and consent-heavy; better with its own README, its own issue tracker and its own ethics note ([euphonia-port.md](euphonia-port.md)). | Nothing here depends on the editor. |
| **i18n, guard, mail, audit, buffer, tokens, user** | Message packs and plurals, an HTTP guard, mail, an audit schema, small utilities | none | Small; give them a home only if something splits that needs them (`docstore` imports `guard`, `audit`, `user`). | Better as part of a "server kit" (below) than as seven repositories. |

## Tier 2: stand alone once one edge is cut

| Candidate | Imports | The edge to cut | Result |
| --- | --- | --- | --- |
| **Server kit: `docstore`, `docstore/encrypt`, `docstore/public`, `auth`, `auth/passkey`, `telemetry`, `audit`, `guard`, `user`** | `docfile` (encrypt and public), the rest internal to the group | `docstore/encrypt` and `docstore/public` read `docfile` (the public offer's documents-only check, the pack format in `encrypt`). Put `docfile` in the same repository, or make those two checks take a function. | A storage, sharing and sign-in layer for any app that keeps documents: expiring public store, age/SSH/passkey encryption, key directory with witnesses. By lines (28 000 in `docstore`, 4 000 in `auth`) it is the largest self-contained thing here, and the one with the most security review behind it ([audit.md](audit.md)). Its own repository would also let the outside audit ([status.md](status.md) item 89) point at one small target. |
| **Document model and formats: `docfile`, `docfile/pack`, `export`, `docximport`, `odtimport`, `htmlimport`, `transfer`, `pdftext`, `pdfua`, `a11ycheck`, `docdiff`** | `canvas` (all of them: the model mirrors canvas types) | `docfile` mirrors the canvas `Format`, `ParaFormat` and items. Splitting it means `canvas` imports `docfile`, not the reverse (the model becomes the base), or both go in one repository. | The "convert any office document to and from one model, with a fidelity report" library. DOCX, ODT, RTF, EPUB, HTML, PDF/UA and the `.opendoc` pack in one place. Of everything here, this is the part with the clearest users who do not want an editor. Do it together with `canvas` (next row) or not at all. |
| **Canvas: `canvas`, `canvas/xango`, `fonts`, `fontserver`** | `complete`; `fonts` imports `fontserver` | none that matter | The text-on-canvas engine: layout, editing, multi-cursor, pages, tables, footnotes, bidi. It is the most original thing here after Xangô. Split with the model above as one "canvas + document model" repository, or the model as its own module inside it. |
| **Collaboration: `collab`, `collab/hub`, `collab/client`, `crdt`, `syncserver`** | `canvas`, `docfile`, `crdt` | `collab` is written against `canvas`; the hub and the wire are not. | Two modules: the wire/hub/client (no editor) and the bridge to the canvas. |

## Tier 3: keep together

| Piece | Why it stays |
| --- | --- |
| **components** (`components`, `components/*.css`, the `.xg` files) | It is the editor's own widgets (menus, file panels, share dialogs, key dialogs) over `canvas`, `docfile`, `docstore` and `xango`. A general widget set (`Menu`, `Tabs`, `Combobox`, `Dialog`, `Toolbar`, the colour and date pickers' wrappers, `ARIA` helpers) is buried in it: **that part could be a repository named for the original idea, "Gojiberry: components"**, if the editor-specific panels (`Saver`, `Migrate`, `KeyDialog`, `ConsolePanel`, `People`) are moved out to the app. This is the one place where the original intent (components) does not match the code: today `components` imports the document model and the store. It is a refactoring task, not a rename. |
| **saver, migrate, console, userconfig, docsearch** | They are the editor's application layer: they join `components`, `docfile`, `docstore` and `export`. They belong in the app (opendocs) or with `components`. |
| **examples** | The showcase (about 27 000 lines) is the app's integration test. It belongs with whatever it demonstrates; if the editor moves to opendocs, the showcase becomes opendocs' own web app. |
| **internal** | By definition. |

## How this fits opendocs

`opendocs` has its own editor model (`editor/model`: a gap buffer, its own layout and undo) and depends on gojiberry for
`xango`/`domrt`, the stores and the services. The older notes ([opendocs-scan.md](opendocs-scan.md),
[opendocs-port-plan.md](opendocs-port-plan.md)) ask the open question: replace `editor/model` with `canvas`, or wrap it.
The repository split follows from the answer:

1. If opendocs **moves onto `canvas`**, the app is thin: `canvas` + document model + `components` + the server kit.
   That favours the tier 2 split above (canvas + model as one repository, server kit as another) and leaves opendocs as
   application code only.
2. If opendocs **keeps its own model**, it shares only `xango`, `domrt`, the server kit and the language tools. Then
   `canvas` and `docfile` have no second user, and splitting them costs more than it returns; split tier 1 and the server
   kit and keep the rest.

Either way the opendocs roadmap already names things that belong in the shared repositories rather than in the app:

- **Language Hub** (a separate open-source repository in its own words): `spell`, `hyphen`, `lang`, `grammar` and
  `spell/dictserver` are that, already, in Go, with the Hunspell loader its roadmap lists as "still open" for Spanish,
  French and German (here: `spell.LoadHunspell` reads `UTF-8` and `long`/`num` flags, cross products and compounds).
- **The scripting engine**: opendocs ships a Worker-isolated plugin system with a six-method API kept by hand in both Go
  and its JavaScript shim, and its roadmap says "worth designing deliberately (a generic attribute-based or
  reflection-driven dispatch)". Here the editor already has a command registry (`canvas.Command` names run by `Stage.RunCommand`, the same list the
  menus and key bindings use). The takeaway is to expose *that registry* to a sandboxed worker, so a new command is reachable from a script
  the moment it is registered. That is a package of its own (`xango` + `domrt/worker` + a command list), not part of the
  editor.
- **Linked frames, form fields, formulas, snippets, signing**: opendocs has working versions (`frames.go`,
  `formfields.go`, `formula.go`, `model/signing.go`). They are written against its own model; they should be ported as
  behaviour (the rule in [opendocs-port-plan.md](opendocs-port-plan.md)), not copied.

## Suggested order

1. **Decide the licence** (a root `LICENSE`), and write the one-paragraph answer to "what happens to `opendocs/editor/model`".
2. **Make modules in place** for the tier 1 pieces: `xango` + `domrt`, `observe`, `crdt`, the language tools, the speech
   tools. Add a `go.work`. Fix whatever breaks (little should: they import nothing).
3. **Move `xango` + `domrt` to their own repository first (done: github.com/elaous/xango).** It has the clearest identity, the existing logo and README, and
   the smallest surface; the module path changes for every importer once, with a `replace` for the transition.
4. **Move the server kit** (`docstore`, `auth`, `telemetry` and friends). Decide the `docfile` edge first. It is the piece
   most likely to be adopted by someone who does not use the editor, and the one an outside audit can be pointed at.
5. **Separate the general components** from the editor panels inside `components` (in place, as two packages), and only
   then decide whether the general ones deserve a repository named Gojiberry.
6. **Canvas + document model** last, together, and only if opendocs moves onto `canvas`.

## What it costs

- Every move changes the import path of every importer (the showcase, opendocs, `cmd/`). A `replace` per module hides it in
  development; the real cost is one pass of `gofmt -r`-style rewriting per move and a version tag per module.
- CI is one workflow today ([ci.yml](../.github/workflows/ci.yml)): the browser tests, the API dump check, the wasm size
  budget and the generate check all assume one tree. Each new repository needs its own, and the wasm budget
  moved with the showcase to the OpenDocs repository.
- The `*.xg.go` files are committed generated code. After the split, `xgc` is a dependency of every repository that has
  `.xg` files: pin it by version, not by path.
- Cross-cutting tests (the end-to-end suite drives the showcase across `canvas`, `docstore` and `components`) stay with
  the app; the split repositories keep their own unit tests, which is what they have today.
