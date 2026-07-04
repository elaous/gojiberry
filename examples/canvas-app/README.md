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
- **`canvas/migrations/1_create_documents.up.sql`** -- the one table.
- **`canvas/pages.gox`** -- `HomePage` (list + "new document" form) and
  `CanvasPage` (the textarea) as gox SSR components. Both are annotated
  with `// @stylesheets style.css` (gojiberry's
  `internal/transpile/page_metadata.go`), which reads `canvas/style.css`
  and wraps each function's return in
  `gox.Document{Title, Head, Body}.Render()` (`gox/document.go`)
  automatically -- neither page hand-rolls
  `<html><head>...</head><body>...</body></html>` or its own CSS
  binding. Both fall back to a shared `DefaultTitle` package var since
  neither needs a title that varies per document.
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

Both entry points (`api.go`'s Encore handlers and `cmd/devserver`) call
the same `Store` methods and render the same `pages.gox` components --
the routing layer is the only thing that differs.

## What's actually verified, and how

Run from this directory (`examples/canvas-app`):

```
go generate ./...
GOOS=js GOARCH=wasm go build -o static/main.wasm ./cmd/wasmclient/
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" static/

# a real Postgres, migrated:
createuser canvas --pwprompt   # or: docker run -e POSTGRES_PASSWORD=... postgres
createdb -O canvas canvas
psql "postgres://canvas:canvas@127.0.0.1:5432/canvas" -f canvas/migrations/1_create_documents.up.sql

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
- **A real headless Chromium** (Playwright) drove the actual UI against
  a running `devserver`: clicked "New document", typed into the
  textarea, watched the status indicator go through
  `editing...`/`saving...`/`saved`, reloaded the page, and confirmed
  the typed content survived -- i.e. the compiled WASM binary really
  ran in a browser, really called `fetch` with a `PUT`, and the
  content really round-tripped through Postgres. The home page listing
  was also confirmed to pick up the new preview text.

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
