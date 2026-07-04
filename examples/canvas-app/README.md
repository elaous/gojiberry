# gox-canvas

A bare-minimum, Google-Docs-style typing canvas: a list of documents,
each one a big textarea that autosaves as you type. It's a proof of
concept tying together everything else in this repo, not a product --
no auth, no conflict resolution, no rich text.

It's a separate Go module (`go.mod` here `replace`s
`github.com/elaous/gojiberry` with `../..`) so it consumes gojiberry
the way an actual downstream project would, rather than living inside
the main module.

## What's here

- **`canvas/store.go`** -- plain `database/sql` business logic (create
  /get/save/list a document). No framework dependency. Queries are
  annotated with the gox VS Code extension's `//language=SQL` marker
  for editor syntax highlighting; `sqlQuery` strips that marker before
  the string reaches the driver (Postgres comments are `--`, not `//`,
  so leaving it in would be a syntax error, not a no-op).
- **`canvas/schema.pg.hcl`** -- the desired-state schema (the `documents`
  table) that [Atlas](https://atlasgo.io) diffs against to generate
  migrations.
- **`canvas/migrations/`** -- versioned migrations, managed by Atlas
  (`canvas/atlas.hcl`) in the `golang-migrate` directory format
  (`<number>_<description>.up.sql` + `.down.sql`, tracked by an
  `atlas.sum` checksum file) rather than Atlas's own default format,
  since `encore.dev/storage/sqldb.DatabaseConfig.Migrations` documents
  that exact naming convention as what it expects. The one migration
  here (`1_create_documents.up.sql`) predates Atlas; `atlas migrate
  hash` retrofitted the checksum file over it, and `atlas migrate diff`
  confirms it's byte-for-byte in sync with `schema.pg.hcl` (see "What's
  actually verified" below). Going forward, edit `schema.pg.hcl` and run
  `make db-diff name=...` to generate the next migration instead of
  hand-writing SQL.
- **`docker-compose.yml`** -- a Postgres 16 container for local dev
  (mapped to host port 5433, not 5432, so it doesn't collide with a
  natively-installed Postgres some sandboxes use instead -- see that
  file's comment). It starts empty; Atlas owns applying migrations to
  it (`make db-migrate`), so its own revision-tracking table
  (`atlas_schema_revisions`) stays authoritative instead of racing a
  container auto-init script.
- **`Makefile`** -- wraps the whole workflow: `make run` chains
  `generate` (goxc) → `wasm` (build + stage the WASM client) → `db-up`
  (docker compose, waits for the healthcheck) → `db-migrate` (Atlas)
  → the dev server itself. `make watch` runs `cmd/devwatch` instead of
  the plain dev server, for live reload while editing (see below).
  `make test`, `make db-status`, `make db-diff name=...`, and `make
  clean` cover the rest.
- **`canvas/pages.gox`** -- `HomePage` (list + "new document" form) and
  `CanvasPage` (the textarea) as gox SSR components. Both are annotated
  with `// @stylesheets static/style.css` (gojiberry's
  `internal/transpile/page_metadata.go`), which reads
  `canvas/static/style.css` and wraps each function's return in
  `gox.Document{Title, Head, Body}.Render()` (`gox/document.go`)
  automatically -- neither page hand-rolls
  `<html><head>...</head><body>...</body></html>` or its own CSS
  binding. Both fall back to a shared `DefaultTitle` package var since
  neither needs a title that varies per document. `CanvasPage` is also
  annotated `// @wasm /static/main.wasm`, which appends the
  `wasm_exec.js` loader and the `WebAssembly.instantiateStreaming`
  bootstrap script after its own content automatically -- it doesn't
  write either `<script>` tag itself.
- **`canvas/api.go`** -- the same `Store` wired into real
  `//encore:api`-annotated handlers (`encore.dev/storage/sqldb`), for
  what a production deployment of this would look like.
- **`cmd/devserver`** -- a plain `net/http.ServeMux` (Go 1.22+
  method+path-param patterns) exposing the identical routes, for a
  version of this app that's actually runnable and tested without the
  Encore CLI.
- **`cmd/wasmclient`** -- the browser-side autosave client: debounces
  `textarea#canvas` input into `PUT /api/documents/{id}` calls via
  `domrt/browser.FetchOpts`, and drives a `#status` indicator through
  `domrt.Mount`/`Update` (so the diff/patch runtime is exercised here
  too, not just fetch).
- **`cmd/devwatch`** -- `make watch`'s file watcher: on any source
  change (excluding build output it writes itself, to avoid a feedback
  loop) it runs `make generate` + `make wasm`, builds `cmd/devserver`
  to a temp binary, and swaps it in for the running one via `SIGTERM`
  + restart. `cmd/devserver`'s own `DEV=1` mode (which `cmd/devwatch`
  sets) injects a small script into every page polling
  `GET /__dev/version`; once a fresh process (a new value) answers,
  the open tab reloads on its own. Not real hot module replacement --
  a compiled `.wasm` binary is one opaque blob, not swappable modules
  -- just a fast watch/rebuild/restart/reload loop, which is enough
  here since the app is server-source-of-truth (a full reload always
  reconstructs the right state from Postgres).

Both entry points (`api.go`'s Encore handlers and `cmd/devserver`) call
the same `Store` methods and render the same `pages.gox` components --
the routing layer is the only thing that differs.

## What's actually verified, and how

Run from this directory (`examples/canvas-app`), with Docker and
[Atlas](https://atlasgo.io/docs#installation) installed:

```
make run
```

That's `generate` (goxc) + `wasm` (build the WASM client) + `db-up`
(Postgres via Docker Compose) + `db-migrate` (Atlas applies
`canvas/migrations/` to it) + the dev server itself, in order. `make
test` runs the same way against the same database. Use `make watch`
instead of `make run` while actively editing: it rebuilds and restarts
the dev server on every save and reloads the open tab automatically
(see `cmd/devwatch` above). If Docker isn't
available, the equivalent manual path against a natively-installed
Postgres still works (this is what the original version of this
example used, before Docker access was confirmed in the sandbox this
was built in):

```
go generate ./...
GOOS=js GOARCH=wasm go build -o static/main.wasm ./cmd/wasmclient/
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" static/

createuser canvas --pwprompt
createdb -O canvas canvas
(cd canvas && atlas migrate apply --env local --url "postgres://canvas:canvas@127.0.0.1:5432/canvas?sslmode=disable")

DATABASE_URL="postgres://canvas:canvas@127.0.0.1:5432/canvas?sslmode=disable" go run ./cmd/devserver
```

- **`go build ./...`** -- the whole module, including `canvas/api.go`,
  compiles against the real, published `encore.dev` module (confirmed
  via `go doc encore.dev/storage/sqldb` against the actual API, not
  guessed).
- **`GOOS=js GOARCH=wasm go build ./cmd/wasmclient`** -- the WASM
  autosave client compiles for real.
- **`go test ./cmd/devserver/...`** -- `TestDevserverEndToEnd` runs the
  full create -> view -> save -> reload -> list flow through
  `httptest.NewServer` against a real, locally running Postgres (no
  mocks, no in-memory fake).
- **`docker compose up -d --wait`** genuinely pulls and starts a
  Postgres 16 container and blocks until its healthcheck passes (not
  just "the YAML parses").
- **Atlas** genuinely installed (`curl -fsSL
  https://release.ariga.io/atlas/atlas-linux-amd64-latest`) and
  exercised for real, not just config written and assumed correct:
  `atlas migrate hash` retrofitted `atlas.sum` over the pre-existing
  migration, `atlas migrate diff` confirmed it's in sync with
  `schema.pg.hcl` (no phantom drift), `atlas migrate apply` against a
  freshly emptied container actually created the `documents` table and
  its own `atlas_schema_revisions` tracking table, `atlas migrate
  status` reported it up to date, and a scratch copy with a column
  added to `schema.pg.hcl` produced a real, correct
  `ALTER TABLE ... ADD COLUMN` up/down migration pair via `atlas
  migrate diff` -- the generate-a-migration loop this whole setup
  exists for.
- **A real headless Chromium** (Playwright) drove the actual UI against
  a running `devserver`: clicked "New document", typed into the
  textarea, watched the status indicator go through
  `editing...`/`saving...`/`saved`, reloaded the page, and confirmed
  the typed content survived -- i.e. the compiled WASM binary really
  ran in a browser, really called `fetch` with a `PUT`, and the
  content really round-tripped through Postgres. The home page listing
  was also confirmed to pick up the new preview text.
- **`make watch`** (`cmd/devwatch`), end to end, with a real headless
  Chromium tab already open: editing `canvas/pages.gox` was picked up
  by the file watcher, triggered exactly one rebuild-and-restart (no
  feedback loop from the watcher seeing its own build output), and the
  open tab reloaded on its own once the new process was listening --
  no manual refresh, confirmed twice in a row (edit, wait, assert new
  content; revert, wait, assert original content again).

## What's *not* verified

The Encore CLI installer (`curl -fsSL https://encore.dev/install.sh`)
was blocked by this environment's network egress policy (403), so
`canvas/api.go`'s `//encore:api`-annotated handlers have never actually
been run under `encore run`. They compile against the real library and
follow its documented conventions, but whether `sqldb.NewDatabase`
provisions correctly, whether the `//encore:api` comments route as
described, and whether a `raw` endpoint's `*http.Request` populates
`PathValue` the way `cmd/devserver`'s stdlib mux does, is unconfirmed.
`cmd/devserver` is the genuinely-tested version of this app.
