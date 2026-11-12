# gojiberry

[![CI](https://github.com/elaous/gojiberry/actions/workflows/ci.yml/badge.svg)](https://github.com/elaous/gojiberry/actions/workflows/ci.yml)
![coverage](https://img.shields.io/badge/coverage-89.0%25-97CA00)

Go packages for building a collaborative document editor and its server: a ready-made component library written in
[Xangô](https://github.com/elaous/xango) markup, a canvas text engine, offline-first sync (`crdt`, `syncserver`), document
storage (`docstore`, `docfile`), sign-in (`auth`), and speech, translation and braille proxies.

## Licence

Free software under the **GNU Affero General Public License, version 3** ([LICENSE](LICENSE)). If you run a changed version for
other people over a network, the AGPL asks you to offer them its source. The project asks for credit ([NOTICE](NOTICE)), and
a **commercial licence** is available for use the AGPL does not fit ([COMMERCIAL.md](COMMERCIAL.md)). Contributions are
accepted on the terms in [CONTRIBUTING.md](CONTRIBUTING.md); [docs/licence.md](docs/licence.md) says what is and is not settled.


## Xangô

The `.xg` files in this repository (the components, the examples) are written in Xangô, a JSX-style renderer for Go. Xangô
has its own repository, [github.com/elaous/xango](https://github.com/elaous/xango), which holds the `xango` runtime, the
`xgc` compiler, the `domrt` browser runtime, `xango/gio`, the VS Code extension and the language documentation. It was
extracted from this repository. Where this README names `xango` or `domrt/...` packages, they are
`github.com/elaous/xango` and `github.com/elaous/xango/domrt/...`.

Gojiberry imports `github.com/elaous/xango` and pins `xgc` as a Go tool in `go.mod`: `go generate ./...` (`make generate`)
runs `go tool xgc .`. To move to another Xangô release, run
`go get github.com/elaous/xango@vX.Y.Z && go mod tidy && make generate` and commit the regenerated `.xg.go` files.

## Getting Started

Requires Go 1.26+. This module (`github.com/elaous/gojiberry`) is
private, so it isn't `go get`-able from outside this checkout the way a
public package would be — every consumer in this repo (including every
separate-module example under `examples/`) uses it via a local
`replace` directive pointing at this checkout instead. From your own
project elsewhere on disk:

```
# your-project/go.mod
require github.com/elaous/gojiberry v0.0.0
replace github.com/elaous/gojiberry => /path/to/gojiberry
```

How to write `.xg` components and render them is in the
[Xangô README](https://github.com/elaous/xango#readme).

## Packages

- **`buffer`** — `GapBuffer[T]`, the text-editing data structure
  classic Emacs (among others) uses: O(1) amortized `InsertAt`/
  `DeleteAt` at a moving edit point, paying an O(distance) memmove only
  when that point jumps somewhere new (a click, Ctrl+Home) rather than
  on every single keystroke the way a plain slice's
  `append(a[:i], append(new, a[i:]...)...)` does regardless of where
  `i` is. Generic specifically so a real document can pair each
  character with its own formatting in one buffer instead of two
  parallel slices that could drift out of sync (see
  `examples/canvas-editor-app/model.go`'s `char` type, which does
  exactly this). Verified against a plain-slice reference
  implementation through 5,000 random insert/delete/read operations
  (`TestAgainstNaiveReference`) and benchmarked against that same naive
  approach for the realistic case -- typing 1,000 characters in the
  middle of an already-100,000-character document -- at roughly 200x
  faster (`go test ./buffer/... -bench=.`).
- **`crdt`** — the offline-first sync engine: a last-writer-wins
  register per document block, keyed by ID. Each replica (a phone, a
  laptop, a sync server) mutates its own copy freely while offline via
  `Doc.Put`/`Doc.Delete`; `Doc.Drain()` returns only the Ops written
  since the last sync (the delta a metered connection actually sends),
  and `Doc.Apply(ops...)` merges received Ops in, converging to the
  same state on every replica regardless of delivery order or
  duplication. `Doc.Snapshot()` returns the full current state instead,
  for onboarding a brand-new replica with nothing to diff against yet.
- **`syncserver`** — the decoupled backend half: a thin HTTP relay
  (`POST /ops` to push a delta, `GET /ops` for a full snapshot) that is
  itself just another CRDT replica, so it never needs bespoke
  conflict-resolution logic clients must trust. Alongside the real
  document Ops, `POST /presence` lets a replica broadcast its own
  ephemeral cursor position/display identity (`Presence` — replica,
  name, color, plus a client-defined block ID/offset this package
  never interprets), and `GET /presence?replica=X` returns every
  *other* live replica's own last-broadcast one, excluding the
  caller's own; entries older than `presenceTTL` (10s, several sync
  ticks' worth of grace) are pruned lazily on read rather than by a
  background sweep. Unlike an Op, a Presence is never merged into
  `doc` or persisted — a peer's cursor a few seconds ago is noise, not
  state worth keeping. Every deployment detail
  — listen address, region label, merge strategy — comes from
  `GOJIBERRY_SYNC_ADDR` / `GOJIBERRY_SYNC_REGION` /
  `GOJIBERRY_SYNC_STRATEGY` (`"lww"`, the default, or `"char-crdt"` —
  see `syncserver.ConfigFromEnv`), so the same binary (`cmd/syncserver`)
  can be pointed at any operator's own infrastructure, running either
  merge strategy, without a rebuild. `Config.Strategy` must match
  whatever strategy every client uses: a `MergeCharCRDT` client relayed
  through a `MergeLWW` server doesn't just lose the fine-grained merge,
  it silently empties the block's `Content` outright
  (`TestMergeCharCRDTServerConvergesConcurrentEdits` is the regression
  test for exactly that). Every response also carries permissive CORS
  headers, with OPTIONS preflight requests short-circuited before
  reaching the routes at all — a client's own static assets are almost
  never served from this relay's own origin, so without this a
  browser's `fetch()` would refuse every request before `handleOps`
  ever saw it.
- **`fontserver`** — a small standalone "font CDN," the same
  environment-configured, CORS-open, single-binary shape as
  `syncserver` (`cmd/fontserver`). It serves two font sources behind
  one HTTP API: a local directory of `.ttf`/`.otf` files (`Repo.Scan`
  walks `GOJIBERRY_FONTSERVER_DIR`, reads each file's sfnt `name`
  table for its family/subfamily, and derives a CSS-style numeric
  weight and italic flag from the subfamily string, e.g. `"Bold
  Italic"` → `700, true` — `GET /local/fonts` for the resulting
  manifest, `GET /local/files/{path}` for the bytes, `POST
  /local/rescan` to pick up files added after startup without a
  restart), and a cached, proxied view of the Google Fonts Developer
  API (`GET /google/fonts`) — proxied so the API key
  (`GOJIBERRY_FONTSERVER_GOOGLE_KEY`) never reaches the browser, and
  cached (`GOJIBERRY_FONTSERVER_GOOGLE_CACHE_TTL`, default 24h) so a
  picker re-querying it doesn't hit Google on every keystroke. With no
  key configured, or on a fetch failure, `/google/fonts` still returns
  `200` with an empty family list rather than an error — the same
  graceful-degradation posture the editor's own Lora `<link>` already
  takes when a web font fails to load, so a font picker built against
  this endpoint works identically whether or not a deployment has
  opted into the online catalog. `Repo.Open` only ever serves a path
  its last `Scan` actually produced, rather than trusting a
  traversal-cleaned path alone, so a request like
  `/local/files/../../etc/passwd` is rejected outright rather than
  merely normalized. `GET /local/symbols?family={name}` (`cmap.go`)
  answers a different question than `/local/fonts`: not "what families
  exist" but "what characters does this specific one actually have a
  glyph for" -- it reads that family's own representative variant
  (`Repo.VariantPath`, the lightest non-italic-preferred one) and
  parses its `cmap` table (`sfntCmapRanges`, alongside `sfntNames`'
  `name`-table parser, both sharing `findSfntTable`'s table-directory
  walk), returning a sorted, merged list of inclusive rune ranges
  rather than a flat list of individual codepoints -- the `cmap`
  table itself is already range-encoded (format 4/12's own segments),
  so this is both the natural parse output and a compact wire format,
  even for a CJK font with tens of thousands of glyphs. Understands
  subtable formats 4 (segment mapping, BMP-only, what most Windows/
  Unicode subtables use -- including walking an *indirect* segment's
  own `glyphIdArray` to correctly split out individual "holes" rather
  than assuming a whole declared segment is populated), 12 (segmented
  coverage, full Unicode range), and 0 (a legacy 256-glyph Mac table);
  `cmapPreferredSubtable` scores every subtable by platform/encoding
  (full-Unicode Windows/Unicode subtables first, then BMP-only, then
  Symbol-encoded dingbat fonts like Wingdings/Webdings -- whose own
  codepoints already sit in the `cmap` at their real Private Use Area
  positions, no extra offsetting needed -- then the legacy Mac table
  last) and picks the best-scoring one that's *also* in a format we
  can actually decode, rather than failing outright on an
  undecodable-but-otherwise-preferred subtable. This is what powers
  OnDesign's special-character picker browsing a font's own real
  symbol repertoire (as opposed to the fixed curated table it ships
  with) -- see that repo's own README for the client-side half.
  `/local/symbols` only ever reads a file `Repo.Scan` already found on
  disk -- Google Fonts (or any other *remote* font registry) has no
  file to read locally, so symbol-browsing a family from one of those
  isn't supported yet: that would need fontserver to itself fetch and
  decompress the actual font binary (WOFF2, not the raw sfnt this
  parser reads) just to reach its `cmap`, real additional work tracked
  as known future scope rather than built here. Similarly, "Google
  Fonts" is really just the one remote web-font registry this server
  happens to speak today, not the only one that could exist --
  `fetchGoogleFontsAPI`'s own `func(apiKey string) ([]googleFont,
  error)` shape is already a plugin point in spirit; making that
  literal (a small registry interface a deployment could register
  additional remote catalogs against, the same way `docstore`'s
  `RegisterAdapter` lets a deployment add a storage backend without
  touching this package) is also future work, not done here.
- **`docfile`, `saver`, `transfer`** — saving canvas documents. `docfile.Document`
  mirrors the editor's content (text runs, paragraph formats, columns, breaks,
  named styles, fonts) and writes as JSON, protobuf text or protobuf binary
  (`docfile.Schema()` prints the `.proto`). `saver.Saver` pairs a format with
  named targets, each a `docstore.Adapter`: Postgres, **SQLite** (new,
  pure Go), S3/MinIO, local files, memory, `docstore.NewHTTPAdapter` (a
  docstore server in front of any of those, for a WASM client) or
  `docstore.NewBrowserAdapter` (localStorage). `components.Saver` is the form
  and `saver.Session` its state. `transfer.Registry` is the seam for
  importers and exporters (Markdown and plain text built in), with
  `components.Transfer` for the buttons. See [docs/saving.md](docs/saving.md).
- **`fonts.Library`** — fonts added from the `FontSelector` are downloaded into
  a store (any docstore backend) the first time the document uses them, then
  loaded from there. See [docs/saving.md](docs/saving.md#lazy-fonts).
- **`docstore`** — a storage-agnostic document persistence server. The
  backends that need a cloud or database client (Postgres, MongoDB, S3 and
  MinIO, the Go CDK blob) live in the nested module
  `docstore/backends` (`.../backends/postgres`, `mongo`, `s3`, `blob`, and
  `.../backends/all` to register them all), so the main module, and every
  browser build, link none of those SDKs; `docstore/backends/cmd/docstore`
  is the server with all of them (the Docker image builds that one), while
  `cmd/docstore` has memory, files and SQLite. The
  `Adapter` interface (`Save`/`Load`/`List`/`Delete`, keyed by document
  ID) is implemented six ways: `MemoryAdapter` (in-process, the
  zero-dependency default), `LocalFSAdapter` (one file per document,
  base64url-encoded ID as the filename), `postgres.Adapter` (one
  `documents` table, created on first connect if missing),
  `mongo.Adapter` (one `documents` collection, `_id` set directly to
  the document ID), `s3.Adapter` (one object per document, built
  against a narrow `s3API` interface so its key-prefixing and
  pagination logic has native tests against an in-memory fake with no
  live bucket required), and `blob.Adapter` (task #174's own concrete
  deliverable: one blob per document, built directly on
  [go-cloud](https://gocloud.dev)'s `*blob.Bucket` -- a *portable
  client library*, not an infra-provisioning tool, opened by URL
  scheme (`"file:///..."`, `"s3://..."`, `"gs://..."`, `"mem://"` for
  tests, whichever driver package a given binary blank-imports) rather
  than one hand-written adapter per cloud, the way `s3.Adapter` itself
  is; registered as `"blob"`, alongside `"s3"`/`"minio"`, not in place
  of them, so an existing deployment configured for either keeps
  working unchanged). Each backend is a self-contained plugin, not
  a case in some central switch: it defines its own JSON-tagged config
  type (`postgres.Config{DSN}`, `mongo.Config{URI, DB}`,
  `s3.Config{Bucket, Prefix, Endpoint, ForcePathStyle}`,
  `LocalFSConfig{Dir}`, `blob.Config{URL, Prefix}`) and registers itself
  under a name via `RegisterAdapter` (the same self-registration
  convention `database/sql.Register`/`image.RegisterFormat` use) from
  its own `init()`. `s3.Adapter` is registered under two names, `"s3"`
  and `"minio"` -- the identical adapter and config either way,
  `"minio"` only differing in which way `ForcePathStyle` defaults (on,
  since MinIO doesn't support AWS's own virtual-hosted-style addressing
  by default) so a deployer pointing at a self-hosted MinIO instance
  doesn't need to know that AWS-vs-MinIO distinction exists at all to
  configure it correctly. `Config` (the server's own config) and
  `BuildAdapter` never reference any backend-specific field at all --
  just `Backend` (the registered name) and `BackendConfig`
  (`json.RawMessage`, that backend's own config blob, opaque to
  everyone but the backend itself) -- so adding a new backend is
  purely additive: a new file registering a new name, zero changes to
  `docstore.go`, `server.go`, or any existing backend. `Server` exposes
  whichever `Adapter` `BuildAdapter` constructs over plain HTTP
  (`PUT`/`GET`/`DELETE /docs/{id}`, `GET /docs` for a metadata listing,
  `GET /health`, `GET /quota` for how much storage is left -- an
  optional `QuotaReporter` interface (`Usage(ctx) (Quota, error)`)
  an `Adapter` implements if and only if it has a real, checkable
  answer, `handleQuota` reporting `{"available":false}` rather than a
  wrong or made-up number for one that doesn't; today only
  `LocalFSAdapter` does, via the underlying disk's own free space
  (`syscall.Statfs`, Linux-only -- `diskusage_linux.go`/
  `diskusage_other.go`), with `Postgres`/`MongoDB`/`S3`/`Memory`/`Blob`
  left unimplemented since none has a generic, queryable notion of
  "quota" this package could answer for them -- go-cloud's own
  `*blob.Bucket` has no such concept either, being a portable client
  library over whichever backend it's opened against, not a
  provisioning/billing API), with the same environment-
  configured, CORS-open,
  single-binary shape as `syncserver`/`fontserver` --
  `GOJIBERRY_DOCSTORE_BACKEND` names the backend,
  `GOJIBERRY_DOCSTORE_BACKEND_CONFIG` carries its JSON config directly
  (e.g. `GOJIBERRY_DOCSTORE_BACKEND=postgres
  GOJIBERRY_DOCSTORE_BACKEND_CONFIG='{"dsn":"postgres://..."}'`), so
  `cmd/docstore` is one binary that switches its real storage
  technology without a rebuild. This is the piece a browser client talks to for any backend
  it cannot itself speak (a browser has no Postgres/MongoDB/S3 wire
  protocol, and no filesystem beyond its own sandboxed storage) — a
  client's own choice of storage adapter only ever needs to know
  whether it's talking to `LocalStorage`, `IndexedDB`, or this one HTTP
  API, never which concrete backend the server operator picked behind
  it. Postgres is tested with `sqlmock` and MongoDB's tests run against a
  live server when `TEST_MONGO_URI` is set (they skip loudly without it;
  CI has a MongoDB service container and fails on a skip, and
  `docs/testing-workflow.md` has a no-Docker recipe); every other adapter,
  and the HTTP server itself, is exercised with real, no-live-service-
  required native tests. Optionally mounts `auth`'s own `/auth/*`
  Sign-in-with-OIDC routes alongside `/docs/*` — see `auth` below;
  off by default. Also optionally enforces a per-document ACL
  (`acl.go`) once one's been set via `PUT /docs/{id}/acl` — an
  `Owner` email plus a `Grants` map (email → `editor`/`viewer`),
  stored as an ordinary document of its own under a reserved `acl:`
  key through the exact same `Adapter` every real document already
  goes through, so no backend needs its own code to support it.
  `GET /docs/{id}/acl` (Owner-only) and `PUT` (bootstraps ownership on
  first use — whatever `Owner` the request body claims is always
  overwritten with the caller's own identity, never trusted from the
  client) sit alongside the enforcement itself: every `GET`/`PUT`/
  `DELETE /docs/{id}` checks that document's own ACL, if any, against
  the caller's bearer token. A document nobody has ever shared this
  way is completely unaffected — enforcement, and `/docs/{id}/acl`
  itself, are no-ops without an `auth.Manager` configured at all,
  since without one no request can ever resolve to a real identity to
  check an ACL against in the first place.
- **`auth`** — a minimal OpenID Connect login flow ("Sign in with
  Google/Microsoft/any OIDC-compliant provider") for servers in this
  repo that need to know who a request is from. One generic
  `Manager`/`Config`/`Provider` implementation driven entirely by an
  issuer URL plus a client ID/secret — Google, Microsoft/Azure AD,
  Okta, Auth0, or any other OIDC-compliant provider all work through
  the same code, no per-provider branching. `User` carries only `ID`
  (`"{provider}:{sub}"`, so two providers' subject claims can never
  collide), `Email` (set only when the ID token says
  `email_verified: true`, or the provider sets `AssumeEmailVerified`;
  `VerifiedEmail` says so), and `Name` — no picture/locale/etc. Sessions are
  bearer tokens (`Authorization: Bearer <token>`), not cookies: this
  repo's HTTP servers already answer CORS with a wildcard
  `Access-Control-Allow-Origin`, which a credentialed cookie-based
  session can't safely ride along with, and a bearer token sidesteps
  that entirely while matching how a browser client already keeps
  everything else (documents, local config) in its own storage rather
  than a server-set cookie. `Manager.Routes()` mounts `GET
  /auth/providers` (list configured providers), `GET
  /auth/{provider}/login` (redirect to that provider's own consent
  screen, with a server-generated, single-use, 10-minute CSRF `state`
  value), `GET /auth/{provider}/callback` (exchanges the code, verifies
  the ID token via `go-oidc`, mints a 30-day-default session, and
  redirects back to the caller-supplied `ReturnTo` with the session
  token in the URL fragment — `#auth_token=...`, never a query
  parameter, so it never lands in server access logs), `GET /auth/me`
  (resolve the current bearer token to a `User`, or 401), and `POST
  /auth/logout`. `docstore` is the first consumer — set
  `GOJIBERRY_DOCSTORE_AUTH_PROVIDERS` (a JSON array of providers) and
  `GOJIBERRY_DOCSTORE_AUTH_BASE_URL` and its `/auth/*` routes mount
  automatically alongside `/docs/*`; leave `AUTH_PROVIDERS` unset (the
  default) and docstore behaves exactly as it did before this package
  existed. Identity only — resolving a bearer token to a `User` is as
  far as this package goes; document ownership/sharing/permissions on
  top of that identity is a separate layer, `docstore`'s own `acl.go`
  (OnDesign's task #140 — see that repo's `docs/CHANGELOG.md` for the
  full writeup).
- **`translate/server`** — a small translation proxy, the same
  environment-configured, CORS-open, single-binary shape as
  `syncserver`/`fontserver`/`docstore` (`cmd/translateserver`). One
  route, `POST /translate` (`{"text","targetLang","sourceLang"}` →
  `{"translatedText","detectedSourceLanguage"}`), forwards to DeepL's HTTP API
  (a small client in `translate/server/deepl.go`, no SDK) — the
  API key (`GOJIBERRY_TRANSLATESERVER_DEEPL_KEY`) lives only in this
  server-to-DeepL request, never reaching the browser, the same reason
  `fontserver` proxies the Google Fonts API instead of handing its own
  key out. With no key configured, `/translate` reports `503` rather
  than refusing to start at all — the same graceful-degradation
  posture `fontserver`'s own `handleGoogleFonts` takes with no Google
  key. The real DeepL client is reached through a narrow `translator`
  interface (`TranslateText`), so tests inject a fake instead of
  hitting the network, the same "swappable network call" shape
  `fontserver`'s own `fetchGoogleFonts` field already established.
  DeepL only for now; a second provider would slot in behind that same
  interface without touching the HTTP layer at all.
- **`voice/server`** — a small standalone speech proxy, the same
  environment-configured, CORS-open, single-binary shape as
  `syncserver`/`fontserver`/`translate/server` (`cmd/voiceserver`), but
  for a different reason than `translate/server`'s DeepL key: Piper
  (text-to-speech) and a whisper.cpp-compatible CLI (speech-to-text)
  are both genuinely local, offline tools with no API key or network
  call involved at all — this package exists purely because neither
  one speaks HTTP on its own, execing each as a subprocess per request
  instead (`POST /speak` pipes text into Piper's own stdin and reads
  the WAV it writes to a temp `--output_file`; `POST /transcribe`
  writes an uploaded WAV to a temp file and reads back whatever a
  whisper.cpp-compatible `-otxt` run writes). `Server.speak`/
  `Server.transcribe` are swappable func fields, the same shape
  `translate/server`'s own `translator` interface and `fontserver`'s
  own `fetchGoogleFonts` field already established, so tests cover the
  full HTTP surface without needing real Piper/whisper installs.
  Either `GOJIBERRY_VOICESERVER_PIPER_BINARY`/`_WHISPER_BINARY` left
  unset reports `503` on its own route rather than refusing to start
  at all, the same graceful-degradation posture every server here
  takes with something unconfigured.
- **`braille/server`** — the same environment-configured, CORS-open,
  single-binary shape as `voice/server` (`cmd/brailleserver`), and for
  the same underlying reason: [liblouis](https://liblouis.io), the
  standard open-source engine for real contracted (Grade 2) or
  uncontracted (Grade 1) braille transcription, is a C library that
  speaks no HTTP of its own, so this package execs its `lou_translate`
  CLI as a subprocess per request (`POST /translate` pipes text into
  `lou_translate`'s own stdin and reads the ASCII-braille text it
  writes to stdout — liblouis's own default output encoding, and
  already BRF's own on-the-wire representation, so no further
  re-encoding is needed downstream). `Server.translate` is a
  swappable func field, the same shape `voice/server`'s own
  `speak`/`transcribe` fields establish, so tests cover the full HTTP
  surface without needing a real liblouis install.
  `GOJIBERRY_BRAILLESERVER_LOU_TRANSLATE_BINARY` left unset reports
  `503` on `/translate` rather than refusing to start at all;
  `GOJIBERRY_BRAILLESERVER_TABLE` picks the default liblouis
  translation table (`en-us-g2.ctb`, contracted Grade 2 US English,
  out of the box), overridable per request via the JSON body's own
  `table` field.
- **`devreload`** / **`devwatch`** — the two halves of xango's dev-mode
  hot-reload loop, generalized out of `examples/canvas-app`'s own
  hand-rolled version so any xango server can opt in with a few lines
  instead of re-deriving it. `devreload` is the browser-side half:
  `Reloader.Wrap(html)` injects a small script that polls
  `Reloader.Handler()`'s endpoint and reloads the tab once the served
  version differs from the one the page was rendered with; a nil
  `*Reloader` makes both calls no-ops, so production code needs no
  separate devMode branch. `devwatch` is the process-supervision half:
  `devwatch.Run(Config{...})` watches a source tree, and on a relevant
  change (debounced, with `.xg.go`/VCS metadata always skipped via
  `DefaultIgnore`) reruns the project's own `Rebuild` and restarts a
  managed server subprocess — not real hot module replacement (a
  compiled binary is one opaque unit), just a fast watch/rebuild/
  restart/reload cycle, the same tradeoff `examples/canvas-app`'s own
  version already made. `cmd/xgdev` is a generic CLI front end for
  projects with no Go code of their own to wire `devwatch.Run` into:
  `go run github.com/elaous/gojiberry/cmd/xgdev -build "..." -- ./myserver`.
  `examples/canvas-app`'s `cmd/devwatch`/`cmd/devserver` now use both
  packages directly, verified via a real watch/rebuild/restart cycle
  against the running app (a file change triggers a real rebuild, a
  real process restart, and the served `/__dev/version` genuinely
  changes) as well as a real headless-Chromium/Postgres run.

## A ready-made component library

`components` is a small library of common UI components built on the
pattern above: `Link`, `Button`, `Chip`, `RadioGroup`, `Tabs`,
`Accordion`, `Dropdown` (single/multiple, with per-option icons and,
in multi-select, checkboxes), `DatePicker`, `ColorPicker`, `Slider`,
`Carousel`, `Wizard`, `FileUploader` (click-to-browse and
drag-and-drop, with an optional progress bar), `ContextMenu`
(typically opened from a real right-click via `domrt/html`'s
`Element.OnContextMenu`, with a full-viewport backdrop that closes it
on an outside click), `Tooltip` (pure CSS `:hover`/`:focus-within` —
no JS, nothing to keep positioned), `Loader` (an indeterminate spinner
for when there's no known completion percentage), `Progress`
(`Variant: ProgressLinear` or `ProgressCircle`, both determinate or
`Indeterminate`), `Modal` (a backdrop plus a centered content box —
the base `Dialog`, and through it `Confirm`/`Alert`, build on; use it
directly for fully custom modal content), `Dialog` (`Modal` with the
common title/body/footer-actions shape), `Confirm` and `Alert`
(`Dialog` presets for a yes/no decision and a single acknowledgment,
each rendered with `role="alertdialog"` instead of `Dialog`'s default
`role="dialog"`, per ARIA, since both demand an immediate response),
and `Toast` (a non-modal, transient notification — no backdrop, no
opinion on where it sits on screen; wrap one or more in your own
`position:fixed` container to stack them).

Also `Checkbox` and `CheckboxGroup` (RadioGroup's independent-selection
counterpart — any number, including none, may be checked), `Switch` (a
real checkbox, visually hidden but still keyboard/screen-reader
accessible, styled as a sliding track via the CSS `:checked` sibling
selector — the same "real control, custom look" idea `FileUploader`'s
hidden file input already uses), `Textarea`, `OTPInput` (N
single-character boxes for a verification code; like `FileUploader`
and `ContextMenu`, advancing focus to the next/previous box on
input/backspace needs a real DOM reference `xango.Event` doesn't carry,
so it's the caller's job via `domrt/html`, keyed off each box's
`fmt.Sprintf("%s-%d", ID, i)` id), `Breadcrumbs`, `Pagination` (with
ellipsis windowing so its width stays constant regardless of page
count), `Avatar` (an image, or a colored initials fallback when there's
no `Src`), `Search` (a basic ARIA combobox — ranking/filtering/
debouncing `Suggestions` is the caller's job, the same split
`FileUploader` uses for reading files), `ImageZoom` (click a thumbnail
for a full-viewport enlarged overlay, `Modal`-style backdrop and all),
and `Popover` (anchored, click-triggered content with the same
outside-click-to-close backdrop trick as `ContextMenu`, for when
`Tooltip`'s pure-hover/non-interactive content isn't enough).

Last, `Icon`: a small set of built-in glyphs (`IconCheck`, `IconClose`,
the four `IconChevron*`, `IconPlus`/`IconMinus`, `IconMenu`, `IconStar`)
or any caller-supplied `Glyph`, sized and colored via CSS. It's
deliberately a Unicode character in a `<span>`, not an inline `<svg>`:
see the `Progress` circle-variant note below for why an `<svg>` tree
silently fails to render once mounted client-side, a constraint this
hits directly since an icon is exactly the kind of thing you'd reach
for `<svg>` for. Reach for `domrt/svg.CreateElement` directly (paired
with one of this package's existing `Icon *xango.Node` slots on `Chip`/
`Dropdown`/`ContextMenu`) if you need a real vector icon client-side.

Three more round out the set. `Collab` wraps shared content and gates
what a collaborator can see and do with it by role — `CollabOwner`
(edit/view/delete/share/comment), `CollabEditor` (edit/view/comment),
or `CollabViewer` (view/comment only, plus a "View only" badge) —
`CapabilitiesForRole` gives each role's defaults, or pass a custom
`CollabCapabilities` to override them. It has no networking or
persistence of its own, the same pure-function-of-props shape as
everything else here; see `examples/collab-editor-app` for pairing it
with real multi-user sync over `domrt/browser.Socket`. `Floating`
formalizes the "wrap one or more Toasts in your own `position:fixed`
container" idiom Toast's own doc comment already describes, generalized
to any content pinned to a corner/edge of the viewport (or the nearest
positioned ancestor, via `Absolute`) — a floating action button, a
badge, a persistent status widget. `Box` is a full-height (or
explicitly sized) layout container split into `len(Divisions)` stacked
sub-boxes, each independently sized by percentage, a fixed unit value,
or left flexible to fill whatever height its siblings don't claim —
built on CSS Grid's `grid-template-rows`, which already handles
exactly that mix of units in one declaration.

`DataTable` rounds out data display: a sortable, optionally
selectable/searchable/paginated table. It isn't generic over your own
row type — there's no way to write a type parameter into the `.xg`
tag syntax every other component here is invoked through (`<DataTable
columns=... />`) — so, the same way `Tabs`' own `Tab.Content` or
`DropdownOption`'s own `Icon` already work, you turn each real row into
a `DataTableRow` first: a stable `ID` (selection tracking, and the
keyed `<tr>` reconciliation it gets for free) plus its own
already-rendered `Cells`. It reuses `Checkbox`'s own plain markup for
`Selectable` rather than a new selection primitive, `Search` directly
for its own `Search` field, and `Pagination` directly for its own
`Pagination` field; sorting/filtering the underlying data is the
caller's job, the same controlled-component split every other
component here takes — it only reflects `SortKey`/`SortDesc`/
`Selected`/`Search.Value` back and fires `OnSort`/`OnSelectChange`/
`OnSearchChange` when they change. `DataView[T]` is the data-*source*
half: a small generic type (no `.xg` tag needed, since nothing renders
it directly) that owns the paginated/filtered/sorted query state for a
dataset fetched from some backend and hands `DataTable` already-loaded
data as plain props, the same "component is a pure function of props,
networking is the caller's job" split `Collab` already takes. `Fetch`
is the one required field: a `func(DataViewQuery) (DataViewResult[T],
error)` adapter translating `DataView`'s own generic page/search/sort
request into whatever real pagination/filter/sort protocol your
backend actually speaks (JSON:API, OData, GraphQL, a bespoke REST
endpoint — deliberately left open rather than DataView committing to
one); `PaginationProps()`/`SearchProps()` hand back ready-to-wire
`Pagination`/`Search` configs bound to its own current state.

`Grid`, despite the name's overlap with `DataTable`, is a different
kind of thing entirely: a plain CSS Grid layout container, `Box`'s own
sibling for a two-dimensional layout instead of `Box`'s single-axis
stacked divisions. It has no notion of rows, selection, sorting, or
pagination at all — just `Children` arranged into a grid, `Width`
(`""` fills the parent, the same default `Box`'s own `Height` takes),
`Columns` (a `grid-template-columns` value; `""` defaults to a
responsive auto-fill of 200px-minimum tracks), and `Gap` (the space
between cells). Reach for `DataTable` when what you're actually
browsing is tabular data, `Grid` when the job is just arranging an
arbitrary set of already-built children into a responsive layout.

**Known gap: `DataTable`/`Grid` render every `Rows`/`Children` entry
unconditionally** — there's no windowing/virtualization of any kind, so
a dataset large enough that converting it all to `[]DataTableRow` (or
building that many already-rendered `Children`) every render becomes
the actual bottleneck has no better option today than paginating it
smaller via `DataView` (which already avoids fetching more than one
page's worth at a time, just not rendering-side windowing within a
page). A `DataSource`-style interface swapped in for `Rows` wouldn't
fix this on its own — `DataTable` isn't generic (no way to write a type
parameter into the `.xg` tag syntax it's invoked through), so such an
interface could only ever hand back `[]DataTableRow` too, no different
from passing the slice directly — the part that would actually help is
rendering only the currently-visible rows and re-querying the source as
the viewport scrolls, a real, separate feature not attempted here.

`Theme` scopes which `--gj-*` design-token palette (`components/
base.css`, which now carries both a `.gj-theme-light` and a
`.gj-theme-dark` palette alongside `:root`'s own light defaults)
applies to its own `Children` — a plain wrapping `<div>` carrying a
class (or an inline style for one-off `Custom` token overrides), since
CSS custom properties already cascade to every descendant regardless
of DOM depth. Nestable: a `Theme` inside another `Theme` re-scopes from
that point down, composing in a way a single page-wide `body.dark`
class toggle (the pattern `OnDesign`'s own `applyConfig` uses) can't.

`ContextProvider`/`ContextValue` are gojiberry's answer to the "how
would a Context API even work here" question a stateless-component
library like this one has to answer differently than React: a
`xango.Node` tree is built *eagerly*, one plain Go function call at a
time, entirely before `xango.Render`/`domrt.Mount` ever walks it, so
there's no implicit, ambient scope left for either renderer to inject a
value into by the time it sees the tree — whatever a descendant needs
has to already be resolved during that eager build. `ContextProvider`
resolves this the same way stdlib's own `context.Context` is meant to
be used: `ContextProvider(ctx, key, value, func(ctx context.Context)
xango.Node { ... })` calls your `render` closure with a *new* context
carrying `value` attached, and whatever it returns is built from that
— typically reading the value back out with `ContextValue[T](ctx,
key)` and handing it down as an ordinary prop, or nesting a further
`ContextProvider` of its own. `NewContextKey` mints an unforgeable key
(identity is the pointer, not the debug name you pass it), the same
collision-proofing convention `context.Context`'s own doc comment
recommends for a plain interface{}/any key. This is deliberately not
fully transparent the way React's Context is — an intermediate
component between the `ContextProvider` and the real consumer still
has to accept and forward `ctx` (or the value) itself to reach a
descendant several layers down — but it does mean those intermediate
layers only ever thread one generic `context.Context` parameter,
rather than growing a bespoke prop of their own for every distinct
value some deeper descendant happens to need, and it works identically
in SSR and client rendering since resolution always happens before
either renderer runs.

Last, a `.xg` syntax note: `<>...</>` has no room for an attribute at
all, so there was previously no way to give a fragment its own
reconciliation `Key` (`xango.Node.Key`, which `domrt`'s differ already
reads) without dropping out of `.xg` syntax entirely and calling
`xango.Frag(...).WithKey(id)` by hand. `<Fragment key="...">...
</Fragment>` (or `key={expr}` for a non-literal key) now covers that
case directly in `.xg` syntax — the same split JSX itself draws
between the bare `<>...</>` shorthand and the long-form `<React.
Fragment key={id}>...</React.Fragment>` for exactly the same reason.
`Fragment` is a reserved pseudo-tag name for this, not an ordinary
Component, so it's only ever useful for a fragment that's itself one
entry in a keyed list (e.g. a `DataTable` row rendered as multiple
sibling cells with no wrapper element) — most keyed lists, including
`DataTable`'s own row reconciliation above, still just call `.WithKey`
on an ordinary keyed element instead.

None of them import
`domrt` or `syscall/js` — every interactive one is a pure function of
its props, taking the current state and `On*` callbacks the same way
`examples/wasm-counter`'s `Counter` (in the Xangô repository) does, so a single component works unchanged
in an SSR-only page and in a WASM page driving it with
`domrt.Mount`/`Update`. `Progress`'s circle variant is drawn with a CSS
conic-gradient rather than `<svg>`/`<circle>`: `domrt.Mount`/`Update`
create elements with plain `createElement`, which builds the wrong
(HTML, not SVG) element for those tags, so an SVG-based ring would
silently fail to render once mounted client-side even though it looks
fine in server-rendered HTML (the browser's HTML parser switches
namespace for a parsed `<svg>` tag on its own — `domrt` doesn't):

```go
components.RadioGroup(components.RadioGroupProps{
	Name:     "plan",
	Options:  []components.RadioOption{{Value: "free", Label: "Free"}, {Value: "pro", Label: "Pro"}},
	Selected: state.Plan,
	OnChange: func(v string) { state.Plan = v; render() },
})
```

`DatePicker`/`ColorPicker`/`Slider` are thin styled wrappers around
`<input type="date"/"color"/"range">` — the browser does the actual
picker UI and locale/keyboard handling. `Dropdown` is a fully custom
widget instead, since no native `<select>` can render per-option icons;
it implements the ARIA listbox pattern's roles/attributes and its
keyboard model (Down/Up open it, arrows/Home/End move, Enter/Space
choose, Escape closes). `FileUploader` renders a `<label>` wrapping a
visually-hidden `<input type="file">`, so click-to-browse works with
zero JS (a label natively opens its associated control), but since
`xango.Event` carries no file/`DataTransfer` payload, reading the actual
selected or dropped files is done through `domrt/html` directly by the
consuming app (`InputElement.Files()`, or `Element.OnDrop` for
drag-and-drop) — see `FileUploaderProps`'s doc comment and the
`wasm-components-demo` example below. `ContextMenu` has the same
split for the same reason: `xango.Event` carries no `clientX`/`clientY`,
so the consuming app wires `domrt/html`'s `Element.OnContextMenu` on
whatever should be right-clickable and passes the position it reports
straight through as `ContextMenuProps.X`/`Y`.

CSS is split one file per component (`components/button.css`,
`components/modal.css`, ...) plus `components/base.css` for the shared
`--gj-*` design tokens and the couple of `@keyframes` more than one
component reuses (`gj-spin`, `gj-progress-indeterminate`) — pull in
`base.css` plus only the per-component files a page actually uses.
Importing the `components` package already registers all of them (see
the CSS imports section of the Xangô README), so `xango.Document` and `domrt.Mount` include them
with nothing to link or copy; to ship only some, import the files you
want yourself, e.g. `import _ "github.com/elaous/gojiberry/components/button.css"`,
or use plain `<link rel="stylesheet">` tags. A component's doc comment says which stylesheet
it needs; `Dialog` (and `Confirm`/`Alert`, built on it) also needs
`modal.css`, since they render inside `Modal`'s box.

## Examples

- `fonts` (package) — finding fonts: a `Repository` lists the families
  one source offers (`WebSafe`, `Static`, `Local` -- a directory of
  .ttf/.otf files, `Server` -- a gojiberry `fontserver` (its local
  fonts or Google Fonts catalog), `BrowserLocal` -- the fonts installed
  on the user's machine via the Local Font Access API), `Multi` merges
  them, `Cached` remembers the answer, and `Search`/`CSSFamily`/
  `FontFaceCSS` help a UI use the result. Used by the new `components`
  `FontSelector` (searchable, previews each font in itself, filters by
  source, remembers recents) and `StyleEditor` (edits a `canvas.Format`:
  font, size, weight, bold/italic/underline/strike/super/subscript,
  colors, with a live preview). Demo: `examples/wasm-style-editor-demo`.

- `canvas` (package) — components drawn on an HTML `<canvas>` rather
  than built from DOM. Every component has a view mode (read-only, the
  default) and an edit mode (`SetMode`); layout, hit-testing, editing
  state and drawing are plain Go behind a small `Painter` interface so
  they're unit-tested natively, and only the browser glue
  (`Stage.Bind`, `LoadImage`) is js/wasm. A `Stage` draws several
  components on one canvas and routes mouse/keyboard events to them.
  - `TableView` — Obsidian-style table: click-to-edit cells, Tab/Enter/
    arrow navigation, `+` bars to add a column/row, grips above columns
    and left of rows (and a 2x2 icon for the table) that select and open
    a context menu (insert, move, sort, duplicate, delete, clear).
  - `Menu` — a canvas context menu (no view/edit modes).
  - `TextFlow` — flows text into a page of a given width/height using
    font family/size/weight/style and line height, in one or more
    columns (a width per column plus a gap); in edit mode, drag the grip
    above a gap to resize the neighbouring columns. Can flow around
    `Obstacle`s.
  - `Image` — a bitmap with a crop (clip), opacity (alpha) and a
    text-wrap-around mode; in edit mode a toolbar picks Move (left/right
    drag, snapping to 5), Clip, Alpha or Wrap, and the `TextFlow` it is
    attached to re-wraps live.
  - `FloatingText` — a text box anchored at x, y with a width and height
    that text wraps around like an `Image`, but whose own text is
    editable (caret, selection, typing, per-character bold/italic/size).
  - `ScrollView` — a scroll container (no edit mode): a width x height
    viewport onto larger canvas objects, clipped like CSS `overflow`,
    scrolled by wheel or scrollbar, with the vertical and horizontal
    scroll lockable. Components inside it work unchanged.
  - `PageSet` — pages as sheets (margins, border, shadow, like Google
    Docs) with per-page headers, footers and page numbers; each page's
    header/footer/number config is "same as the previous page" or new.
  - `TextFlow` also flows continuously across a `PageSet`: its content is
    a stream of `Chars` (text + `Format`), `ColumnConfig` (column widths
    and gap, kept until the next one) and `Break` (user page or column
    break); the layout adds natural page breaks when text overflows.

  Also: `TextFlow.Transform` turns a `Source` string into the content
  stream (`canvas.Markdown` is the first: headings, bold/italic, strike,
  underline, highlight, `^super^`/`~sub~`, code, links, lists, tables,
  rules, images and `\pagebreak`/`\columnbreak`/`\columns N`
  directives); inline objects (tables, rules, inline `Image`s) sit in
  the text and are fitted to the column and page; floating `Image`s and
  `FloatingText` take a page `Anchor`; headers and footers are editable
  by double-click (per page: same as previous, or new) and page numbers
  are selectable, with a settings menu.

  Editing: `FloatingText` has color and highlight palettes, code-editor
  auto-closing of quotes/brackets, word auto-completion, and key
  bindings (`Keymap`, e.g. Ctrl+B, Ctrl+Alt+C / Ctrl+Alt+V to copy and
  paste a format). Text in a `TextFlow` can be selected (drag or double
  click) and copied; copying a format takes the minimum common
  formatting of the selection, shared across components through the
  `Stage`'s `FormatClipboard`. A `TableView` can float like an image
  (page `Anchor`, `Wrap`, drag its icon to move it).

  `.xg` components can be generic and take function props with markup:
  `<components.List[Person] items={people} item={func(p Person) xango.Node { return <b>{p.Name}</b> }} />`
  (see `examples/generic-list-demo`).

  `canvas/canvas/xango` draws an xango tree on a canvas (`<canvas.Canvas>`,
  `<canvas.TextFlow pages={func(page xango.Node, n int) xango.Node {...}}>`,
  `<canvas.Header>`, `<canvas.Footer>`, `<canvas.PageNumber>`), updating the
  live components in place on each render: see `docs/xango-canvas.md` and
  `examples/canvas-xango-demo`. What is still to do is in `docs/canvas-todo.md`.

  Editing capabilities are *features* registered with the Stage
  (`canvas.Registry`, `canvas.Feature`): multi-cursor (Ctrl/Cmd+click,
  Ctrl/Cmd+D, Ctrl/Cmd+Shift+L, Alt+Shift+I), line operations (Alt+Up/Down
  move, Alt+Shift+Up/Down copy, Ctrl/Cmd+Shift+K delete) and Markdown copy
  and paste (Ctrl/Cmd+Shift+C / V), page and column breaks (Ctrl/Cmd+Enter,
  with Shift). The `TextFlow` is editable in edit mode, and
  the [OpenDocs](https://github.com/elaous/opendocs) editor registers everything -- the xango canvas
  tree, the features, the style editor and font selector -- in one editor. Each registers commands and default key
  chords; leave one out of `Stage.Features` (or `<canvas.Canvas features=...>`)
  to switch it off, or add your own. The status of everything built and what
  is left is in `docs/canvas-todo.md`.

  Demos, each with a chromedp e2e test run by `go test` (skipped when
  no Chromium is found): `examples/canvas-table-demo`,
  `examples/canvas-flow-demo`, `examples/canvas-image-demo`,
  `examples/canvas-scroll-demo`, `examples/canvas-document-demo`,
  `examples/canvas-xango-demo` (run one with
  `make -C examples/<name> run`). The full editor, with every feature on, is
  [OpenDocs](https://github.com/elaous/opendocs).

- `examples/wasm-components-demo` — a gallery of every component in
  `components`, each wired to real local state and mounted with
  `domrt.Mount`/`Update`. Verified against a real headless Chromium:
  every one of the 37 components driven through its actual
  interactivity (clicking a tab/accordion header/carousel arrow/wizard
  step, opening the dropdown and picking a multi-select option,
  removing a chip, dragging the slider, setting the date/color inputs,
  uploading a real file read back through `FileUploader` + the File
  API, a real right-click opening `ContextMenu` at the actual click
  position then closing it via an item selection and, separately, via
  an outside click on its backdrop, hovering to reveal a `Tooltip` --
  checked via its bubble's real computed opacity, not just DOM
  presence, since it's always in the DOM -- confirming `Loader`
  and both `Progress` variants are actually animating via their
  computed `animation-name`, opening a `Modal` and closing it with a
  real click on its backdrop (not the box itself, proving a click
  inside the content can't accidentally trigger it), a `Dialog`
  closed three different ways (Cancel, Save, backdrop) each setting a
  distinct result, `Confirm`/`Alert` each checked for
  `role="alertdialog"` plus both of their outcomes, a `Toast`
  dismissed via its own close button, toggling `Checkbox`/`Switch` and
  a multi-select `CheckboxGroup`, typing into an `OTPInput` and
  confirming focus genuinely advances/retreats across its boxes via
  `document.activeElement` (not just that the value changed), a
  `Breadcrumbs` click checked to both fire its handler and, via
  `PreventDefault`, not actually navigate the page, `Pagination`
  checked for its ellipsis windowing at a large page count plus
  jumping to a specific page, an `Avatar` checked in both its image and
  initials-fallback form, typing into `Search` and picking a real
  suggestion from the resulting list, and opening/closing both
  `ImageZoom` (via its own backdrop) and `Popover` (via its own
  backdrop, with the panel's content checked for real visibility), not
  just a compile check:

  ```
  go generate ./...
  GOOS=js GOARCH=wasm go build -o examples/wasm-components-demo/main.wasm ./examples/wasm-components-demo/
  cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/wasm-components-demo/
  cp components/*.css examples/wasm-components-demo/
  cd examples/wasm-components-demo && python3 -m http.server 8080
  # open http://localhost:8080/
  ```

- `examples/canvas-app` — the big one: a bare-minimum Google-Docs-style
  typing canvas tying together xango SSR pages (a CSS import for the
  page's stylesheet, `@title`, and `@wasm` for the autosave client's bootstrap `<script>`
  tags — neither page hand-writes either), a WASM autosave client
  (`domrt.Mount`/`Update` plus `domrt/browser.FetchOpts` for the
  debounced `PUT`), real Postgres
  via `database/sql` (Docker Compose for local dev, schema managed with
  [Atlas](https://atlasgo.io)'s versioned migrations), and
  Encore-annotated handlers for the same business logic. `make run`
  chains the whole thing together; `make watch` (`cmd/devwatch`)
  rebuilds and restarts on every save and reloads the open tab
  automatically, no true WASM hot-swapping but genuinely verified end
  to end. It's a separate Go module
  (`examples/canvas-app/go.mod`, `replace`-ing this repo) so it depends
  on gojiberry the way an actual consumer would. See
  `examples/canvas-app/README.md` for what's
  genuinely verified here (the xango pages, the WASM client against a
  real headless browser, the store against a real Postgres) versus
  what's best-effort and unverified (the `//encore:api` handlers'
  actual behavior under `encore run`, since this sandbox's network
  policy blocked the Encore CLI installer).

- `examples/showcase-app` — every capability above, on one page: the
  full 37-component gallery, `domrt/browser`'s core wrappers plus DOM
  extras (fetch/localStorage/window+document dimensions/scroll/style/
  SVG/image loading/form submission/cookies/custom events/global error
  capture/geolocation), the newer platform wrappers (IndexedDB/
  MediaDevices/IntersectionObserver/ResizeObserver/drag-and-drop/
  Clipboard), Canvas2D/WebGL, typed elements + the File API,
  client-side routing, and a Web Worker round trip — each section
  wired up exactly the way its original standalone demo did (the
  sections are literally that code, consolidated), so nothing here is
  a simplified restatement. Verified against a real headless Chromium,
  section by section, the same real checks each standalone demo uses
  (a genuine `IntersectionObserver` flip from off-screen to
  intersecting once its element is actually scrolled into view in this
  longer, merged page, unlike the standalone demo where it starts
  already visible):

  ```
  go generate ./...
  GOOS=js GOARCH=wasm go build -o examples/showcase-app/main.wasm ./examples/showcase-app/
  (cd examples/showcase-app/worker && GOOS=js GOARCH=wasm go build -o ../worker.wasm .)
  cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/showcase-app/
  cp components/*.css examples/showcase-app/
  cd examples/showcase-app && python3 -m http.server 8080
  # open http://localhost:8080/
  ```

- `examples/canvas-editor-app` — a document editor whose content is
  drawn directly onto an HTML5 `<canvas>` via `domrt/html`'s
  `Context2D`, the way Google Docs' own editor renders (not
  `contenteditable`, not a `<textarea>` — see `examples/canvas-app`
  above for the latter). `model.go` holds the document as a
  `buffer.GapBuffer` of `char` (a rune paired with its own `Bold`/
  `Italic` in one buffer, not two parallel slices — see `buffer`
  above), so insert/delete/selection/format toggling stay O(1)
  amortized even in a large document instead of an O(n) slice shift on
  every keystroke; `layout.go` word-wraps it
  against the canvas's own `MeasureTextWidth` so layout always matches
  what's actually drawn, and hit-tests a click/drag position back to a
  character index. Real keyboard input (including paste and IME
  composition) comes from a visually-hidden `<input>` kept focused
  behind the canvas: character insertion rides its native "input"
  event (read the value, insert it, clear the field so the next
  keystroke starts clean), while arrows/Backspace/Delete/Enter are
  intercepted on "keydown" via two small additions this example
  motivated — `Element.OnKeyDown`/`OnKeyUp` (a typed `KeyEvent`) and
  `Element.OnMouseDown`/`OnMouseMove`/`OnMouseUp` (a typed
  `MouseEvent`) — since xango.Event carries neither key nor position
  information. A real, non-obvious gotcha surfaced building this:
  clicking a non-focusable element like a bare canvas makes the
  browser clear focus right after every mousedown handler returns,
  silently undoing a same-handler call to focus the hidden input,
  unless `MouseEvent.PreventDefault` is called first. Content
  autosaves to `localStorage` as a run-length-encoded JSON array.

  `linewise.go` adds VSCode-style commands on top of the same buffer:
  Ctrl/Cmd+Left/Right jumps (and, with Shift, selects) by word boundary;
  Alt+Up/Down moves the *logical* line under the cursor (a run
  delimited by `'\n'`, deliberately distinct from `layout.go`'s
  word-*wrapped* visual row — one logical line soft-wrapped across
  three rows is still one line to this command); Shift+Alt+Down
  duplicates it; Ctrl/Cmd+Shift+U/L upper/lowercases the current
  selection. The canvas also now scrolls: `Element.OnWheel` (another
  small addition this example motivated, alongside `domrt/html`'s new
  `Selection`/`GetSelection` wrapper) drives a `scrollY` offset that
  every draw call and hit-test subtracts/adds, clamped to the
  document's actual content height, with the viewport auto-following
  the cursor on every edit *except* a manual wheel-scroll or the caret
  blink timer -- both call `redrawPreservingScroll` instead of
  `redraw`, or the editor would snap back to the cursor on every single
  repaint and manual scrolling would be impossible.

  The document also supports full multi-cursor editing (VSCode's
  Alt+Click to add a caret, Escape to collapse back to one): `model.go`
  keeps a primary `Cursor`/`Anchor` plus `ExtraCursors []cursor`, and
  every buffer-mutating command (`insert`, `backspace`, `delete`,
  format/case toggling) fans out across all of them through
  `forEachCursorDescending`, which processes cursors from highest
  position to lowest so an edit never invalidates a not-yet-processed
  cursor, then rebases every *already*-processed (higher) cursor by
  each subsequent edit's length delta — miss that second half and
  cursors above an edit silently drift by the wrong amount whenever two
  cursors' selections differ in size (covered by
  `TestMultiCursorReplaceDifferentLengthSelections`). Line/word commands
  in `linewise.go` (move/duplicate line) aren't multi-cursor aware —
  rather than risk silently corrupting other cursors' positions, they
  collapse to just the primary first, a visible effect instead of a
  latent bug. `model_test.go` covers the cursor model natively (no
  browser needed) via a `//go:build !(js && wasm)` split: `model.go`/
  `linewise.go` carry no build tag so `go test` can exercise them
  directly, and `native_stub.go` supplies the `func main` other
  platforms need since the real one (in `main.xg`) is wasm-only.

  Verified against a real headless Chromium, via a committed test
  (`canvas_editor_test.go`, driven by chromedp) —
  not just ad hoc manual checking: typing, Backspace, Enter/line
  breaks, click-to-position, Shift+Arrow and real mouse-drag selection,
  Bold/Italic actually flipping the model's per-character format (not
  just the button registering a click), move/duplicate-line actually
  reordering the buffer's logical lines (not just the visual rows),
  word-boundary selection landing on exact character offsets,
  upper/lowercasing an actual selection, wheel-scrolling both
  auto-following the cursor while typing past the viewport and clamping
  correctly at both ends, and multi-cursor editing end to end
  (Alt+Click adding a second caret, a single keystroke inserting at
  both, Escape collapsing back to one):

  ```
  go generate ./...
  go test ./examples/canvas-editor-app/...
  ```

  Or run it directly in a browser:

  ```
  GOOS=js GOARCH=wasm go build -o examples/canvas-editor-app/main.wasm ./examples/canvas-editor-app/
  cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/canvas-editor-app/
  cd examples/canvas-editor-app && python3 -m http.server 8080
  # open http://localhost:8080/
  ```

- `examples/collab-editor-app` — a live, multi-user document editor
  demonstrating `components.Collab` paired with real real-time sync:
  two or more browser tabs open on the same URL genuinely see each
  other's edits, over a real WebSocket, with each tab's permissions
  (edit/view/delete/share/comment) determined by its own role. It's a
  separate Go module (like `examples/canvas-app`), `replace`-ing
  `github.com/elaous/gojiberry` with the local checkout so it can
  depend on [`github.com/coder/websocket`](https://github.com/coder/websocket)
  (v1.8.15, the same module the root `go.mod` and `collab/hub` use)
  without pulling that into the root module. `cmd/server` is a plain
  `net/http` server: it serves `static/` and handles `/ws` — a small
  in-memory hub that relays every edit to every *other* connected
  client and remembers the document's current content so a client
  joining later starts in sync instead of from a blank page.
  `client/main.xg` opens `/ws` via `domrt/browser.Connect`, reads its
  role from the page's own URL (`?role=owner`/`editor`/`viewer`), and
  renders the shared document inside `components.Collab`. Every edit
  sends the document's entire current text over the socket rather than
  a diff or an operational transform — last edit wins on a genuine
  simultaneous edit, a deliberate simplification well short of a real
  product's OT/CRDT merging. Collab only gates the surrounding
  affordances (Delete/Share only render for `CollabOwner`, both need
  `CanEdit` for the document itself to accept input) — the client makes
  a non-editor's `Textarea` genuinely `Disabled` itself, since Collab's
  own read-only styling is cosmetic and can't reach into
  caller-supplied `Content` to disable it (see `Collab`'s doc comment).
  Verified against two (and three) simultaneous real headless-Chromium
  browser contexts: role-based UI gating (owner sees Delete/Share,
  viewer doesn't, viewer's textarea is genuinely `disabled` not just
  visually muted), a live edit from one tab appearing in another purely
  from the WebSocket broadcast, a late-joining third client receiving
  the document's current state immediately instead of a blank page, an
  edit from that third (editor) client propagating to both others, and
  Delete clearing the document live everywhere:

  ```
  cd examples/collab-editor-app
  (cd ../.. && go tool xgc examples/collab-editor-app)
  GOOS=js GOARCH=wasm go build -o static/main.wasm ./client/
  cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" static/
  cp ../../components/base.css ../../components/button.css ../../components/textarea.css ../../components/collab.css static/
  go run ./cmd/server
  # open http://localhost:8096/?role=owner in one tab, ?role=editor or ?role=viewer in another
  ```

- `examples/block-editor` — the mobile-first, offline-first pitch end
  to end: two replicas (a phone and a desktop) edit the same
  block-based invoice fully offline, sync only their delta Ops through
  `crdt.Doc`, and converge to identical state regardless of order —
  then render it two ways from the same blocks: `Editor` (thumb-sized
  editable inputs) and `Output` (the clean final document), proving
  data capture and rendered output are decoupled components:

  ```
  go generate ./...
  go run ./examples/block-editor
  go test ./examples/block-editor/...
  ```

  Pair it with the sync relay to see the same Ops cross a real HTTP
  boundary instead of an in-process `Drain`/`Apply` call:

  ```
  GOJIBERRY_SYNC_ADDR=:8080 GOJIBERRY_SYNC_REGION=br-datacenter-1 go run ./cmd/syncserver
  # add GOJIBERRY_SYNC_STRATEGY=char-crdt too if the client uses MergeCharCRDT
  ```

- `examples/domrt-bench` — task #128's own rendering benchmarks for
  `domrt`'s `patch`/`patchChildren`: a wasm harness exposes
  `window.runBench(scenario, n)`, running n update cycles of one of
  four scenarios against a real mounted `domrt.Root` (a 100-item keyed
  `<ul>`) and timing them with Go's own `time.Now`/`time.Since` inside
  the wasm program itself, so browser-launch/subprocess overhead never
  pollutes the measurement. `attrs-only` (same keys/order, only text
  changes) and `keyed-reorder` (same keys, shuffled order every cycle)
  both stay cheap -- neither creates or destroys a single DOM node, the
  cheapest paths `patch`/`patchChildren` have. `append` (one new keyed
  child added per cycle, so the list itself grows from 100 to 600 over
  the run) and `full-replace` (a completely different 100-key set every
  cycle, so nothing ever matches) both cost several times more, and
  land close to each other in this run's own numbers -- `append`'s
  cost is `patchChildren`'s cheap per-item positional-match path
  applied to a steadily growing list (so its total cost across the run
  is quadratic in iteration count), while `full-replace` pays a more
  expensive per-item create/destroy cost against a constant, smaller
  list every time; the two land in the same rough neighborhood by
  coincidence of this benchmark's own parameters, not because the two
  code paths cost the same per item.

  ```
  cd examples/domrt-bench
  make bench   # builds the wasm harness, then runs all four benchmarks
  # or directly: go test -bench=. -benchtime=1x .
  ```

## Development

The root `Makefile` wraps the repo-wide checks used throughout this
README: `make check` regenerates every `*.xg.go` and runs
`gofmt`/`vet`/`test`/lint for both native and `GOOS=js GOARCH=wasm`
and for every nested module; `make docs`
regenerates every `docs/*.txt` API dump. Every example under
`examples/` has its own `Makefile` too (`make run`, `make test`, and
where relevant `make wasm`/`make clean`), each just running the exact
commands that example's own section above documents -- read the
`Makefile` itself if you want to see precisely what a shortcut expands
to, or run the commands by hand if you'd rather not use `make` at all.

`make lint`/`make wasm-lint` run `golangci-lint` (pinned as the tool
dependency of its own module, `tools/go.mod`, so the linter's large
dependency tree -- protobuf and prometheus among it -- stays out of the
library's module graph; the Makefile builds the binary from `tools/`
and runs it from the repository root, so nobody needs a separate global
install) the same native-then-`GOOS=js GOARCH=wasm` way
`vet`/`wasm-vet` already do, both wired into `make check`. `js`/`wasm`-only
code (`canvas` and the examples) is invisible to a native-only pass, so
`wasm-lint` covers what `lint` alone can't see; see `.golangci.yml`'s
own doc comment for why that needs a host-native `golangci-lint`
binary with `GOOS`/`GOARCH` set in its own environment rather than
`go tool` directly (which would try to cross-compile the linter itself
into wasm and then fail to execute it). `examples/` is excluded from
both -- tutorial code, not the library, with its own separate lint
backlog (mostly the same wasm-only-visibility false positives) tracked
as a follow-up rather than gating the library's own clean baseline.

`make coverage` runs the native (non-`js`/`wasm`) test suite with a
coverage profile and prints the per-function summary; `make
coverage-html` turns the same profile into `coverage.out.html`, a
browsable, per-line annotated report (both `coverage.out` and
`coverage.out.html` are gitignored -- regenerate rather than commit
them). `js`/`wasm`-only files (`canvas` and the examples) have no native
test suite to profile this way; their own coverage is whatever their
example's headless-browser verification exercises, which this doesn't
measure.

`make coverage-badge` rewrites this README's own coverage badge (top
of this file) from that same profile via
`.github/scripts/gen-coverage-badge.sh` -- a small, dependency-free
shell script that reads `go tool cover -func`'s own real "total:" line
and rewrites the badge line to a static `img.shields.io/badge/...` URL
(color thresholds matching shields.io's own convention for a
percentage badge), no API token or external account needed. This repo
is private, so a self-hosted SVG served via `raw.githubusercontent.com`
(what this used to be, and what the CI badge just above it might look
like it implies) doesn't work: that domain only renders a private
repo's file for a live, signed-in github.com session, 404ing for
anyone without one -- a stale tab, a different markdown viewer, or
just enough time for GitHub's own short-lived signing token to expire.
`img.shields.io` renders unauthenticated regardless of repo
visibility (as does the CI badge, which is a first-party `github.com`
endpoint GitHub explicitly designed to be embeddable either way), so
this only needed the *text* CI computes, not a file it serves. CI
(`ci.yml`) rewrites and commits this line on every push to `main` --
not on pull-request builds or feature-branch pushes, since only
`main`'s own README is what anyone actually sees -- so the number
shown here can't silently drift from what `make coverage` itself would
print.

`cmd/wasmdist` writes a precompressed `.gz` (and `.br`) beside each file of a
built wasm app (the [OpenDocs](https://github.com/elaous/opendocs) showcase builds with it), ready to upload to any static
host or to serve with `wasmserve`; [docs/deploy.md](docs/deploy.md) has the
recipe and the cache and compression settings (no credentials needed).

`make bench` runs every Go benchmark in the module: `buffer`'s own
`GapBuffer`-vs-naive-slice benchmarks (fast, native) and
`examples/domrt-bench`'s own patch/patchChildren benchmarks (drives a
real, wasm-built page through a real headless Chromium via chromedp,
skipping itself rather than failing if Chromium isn't set
up -- see that example's own README/Makefile). `make perf` is `bench`'s
profiling counterpart: the same benchmarks, but with a CPU and memory
profile captured per package under `perf/` (Go's own `-cpuprofile`/
`-memprofile`, which only work against a single package per `go test`
invocation, so this discovers every benchmark-bearing package first
rather than hardcoding the list) -- inspect a profile with `go tool
pprof -top perf/<name>.cpu.out`. `make perf-clean` removes `perf/`.

### Git hooks (lint before you push)

CI runs `make lint`/`make wasm-lint` on every push and PR, but catching
a lint failure locally saves the round trip. Opt in once per clone:

```
git config core.hooksPath .githooks
```

This points git at `.githooks/pre-push` (checked into this repo, since
`.git/hooks/` itself never is), which runs `make lint` before any `git
push` leaves your machine.

## Known limitations (v1)

- `examples/`' own `golangci-lint` baseline (deliberately excluded
  from `make lint`/`wasm-lint`, see "Development" above) still has a
  handful of real findings, mostly the same wasm-only-visibility
  "unused" false positive `//nolint:unused` already resolves
  elsewhere in this repo, concentrated in the older demo apps —
  tracked separately rather than fixed alongside the library's own
  clean baseline.
