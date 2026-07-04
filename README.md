# gojiberry / gox

A JSX-for-Go: write `<div>...</div>` tags directly in Go source as an
expression, anywhere a Go expression is legal, compiling down to plain
Go that renders identically on the server (HTML strings) and in the
browser (real DOM, via WASM), with no separate template language.

```go
func Greeting(props GreetingProps) gox.Node {
	if props.Name == "" {
		return <p class="greeting">Hello, stranger!</p>
	}
	return <p class="greeting">Hello, {props.Name}!</p>
}
```

## Why tags can just be Go expressions

Go's generics use `Foo[T]`, not `Foo<T>`, so unlike TSX there's no
ambiguity between a tag and a generic instantiation to resolve. The
only remaining question is distinguishing a tag-open from the
less-than operator (`a < b`), which `.gox` resolves the same way JSX
parsers do: `<` only means "less than" immediately after something
that completes a value (an identifier, a literal, or a closing
`)`/`]`/`}`). Everywhere else — after `return`, `(`, `,`, `=`, `{`, or
at the start of a statement — a `<` can only legally begin a new
expression, so that's exactly where a tag is allowed to start.

`{expr}` holes, in both attribute values and child position, are
always a single Go expression, exactly like JSX. There's no separate
`if`/`for` template syntax: loops and conditionals are ordinary Go,
usually via an inline closure —

```go
<ul>{func() []gox.Node {
	var items []gox.Node
	for _, s := range list {
		items = append(items, <li>{s}</li>)
	}
	return items
}()}</ul>
```

— which is itself just a Go expression, so tags inside its body are
expanded by the same recursive scan, with no extra grammar required.

## Packages

- **`gox`** — the runtime. `Node` is a plain, renderer-agnostic tree
  (element / text / fragment / raw) that both the server and the
  client render from, so a component behaves identically either way.
  `gox.Render`/`gox.RenderString` render to HTML with automatic
  escaping (`gox.Raw` is the explicit escape hatch). `gox.Event.Value`
  is `any`, read back type-safely via `gox.EventValue[T]`.
- **`internal/transpile`** — the `.gox` → `.go` source-to-source
  transpiler described above, plus the stable resource-import loaders
  (JSON/JSONC/JSONL) and the `@title`/`@stylesheets`/`@wasm`
  page-metadata annotations, both described below.
- **`internal/transpile/experimental`** — the resource-import loaders
  that shell out to a mature external tool this project doesn't
  reimplement (HCL/proto/capnp/sqlc), wired in only via
  `transpile.Options.ExperimentalLoaders` — see Resource imports below.
- **`cmd/goxc`** — the CLI: `go run ./cmd/goxc <dir>` transpiles every
  `*.gox` under `<dir>` into a gofmt-clean `*.gox.go` next to it (the
  generated file is committed, like `templ`'s `*_templ.go` convention,
  so `go build`/`go test` work without running goxc first). Wired up
  as `go generate` via the repo-root `generate.go`. `-experimental`
  (or `GOX_EXPERIMENTAL=1`) enables the experimental resource imports
  below; a directory literally named `experimental` is otherwise
  skipped entirely.
- **`domrt`** — the client runtime (`GOOS=js GOARCH=wasm` only).
  `domrt.Mount` creates real DOM nodes for a `gox.Node` tree;
  `Root.Update` diffs the next tree against what's live and patches
  only what changed — text content in place, attributes added/removed,
  event listeners registered at most once per (element, event) with
  the Go closure swapped on later renders, and children reconciled by
  key when present — preserving DOM node identity (focus, scroll
  position, input state) wherever the shape didn't change.
- **`domrt/browser`** (`GOOS=js GOARCH=wasm`) — `browser.Global(name)`
  exposes any global browser API by name (e.g. `Global("WebAssembly")`)
  for one-offs; typed wrappers cover the APIs most apps need: `Fetch`/
  `Response.Text`/`Response.JSON`, `LocalStorage`/`SessionStorage`, and
  `Connect` (WebSocket).
- **`domrt/sw`** (`GOOS=js GOARCH=wasm`) — lets a Go/WASM binary run as
  a browser service worker: `OnInstall`/`OnActivate`/`OnFetch` register
  the lifecycle/fetch events, `ExtendableEvent.WaitUntil` and
  `FetchEvent.RespondWith` bridge to `event.waitUntil`/`respondWith`,
  and `CachesOpen`/`CacheAddAll`/`CachesMatch`/`ClaimClients` wrap the
  Cache Storage API. `domrt.Await`/`domrt.ToPromise` (in the `domrt`
  package itself) bridge JS Promises in both directions and are shared
  by `browser` and `sw` rather than duplicated.

## Components

A component is a plain Go function taking a typed props struct:

```go
type ButtonProps struct {
	Label   string
	OnClick gox.EventHandler
}

func Button(props ButtonProps) gox.Node {
	return <button onClick={props.OnClick}>{props.Label}</button>
}
```

`<Button label="Save" onClick={handleSave}/>` (uppercase tag name)
compiles to `Button(ButtonProps{Label: "Save", OnClick: handleSave})` —
attribute names map to struct fields by capitalizing the first letter.
If the tag has children, they're collected into a `Children
[]gox.Node` field. Lowercase tag names (`<div>`, `<span>`, custom
elements like `<my-widget>`) are real HTML elements and compile to
`gox.El(...)` instead.

### The `<html>` entrypoint

`gox.Document{Title, Head, Body}.Render()` builds the
`<html><head>...</head><body>...</body></html>` wrapper almost every
top-level page needs, so pages don't each hand-roll it:

```go
func Page(props PageProps) gox.Node {
	return gox.Document{
		Title: "My App",
		Head:  []gox.Node{gox.Style(someCSSContent)},
		Body:  <h1>Hello, {props.Name}!</h1>,
	}.Render()
}
```

`Head` is just `[]gox.Node` — append whatever `<meta>`/`<link>`/
`<script>` nodes a page needs. `gox.Style(css string) gox.Node` wraps
a `<style>` tag around raw CSS content, the same way `gox.Raw` does for
any other trusted HTML. `Body` takes one node, so use `<>...</>`
(`gox.Frag`) to group several top-level children there.

For the common case — a static title and one or more stylesheets, both
known at transpile time — `@title`/`@stylesheets` doc-comment
annotations do this for you, so `Page` only needs to return its actual
content:

```go
// @title My App
// @stylesheets App.css
func Page(props PageProps) gox.Node {
	return <h1>Hello, {props.Name}!</h1>
}
```

goxc reads these directly above a `func ... gox.Node` declaration,
reads each `@stylesheets` file the same way a resource import would,
and rewrites every one of that function's own top-level `return`
statements into the `gox.Document{...}.Render()` form above — so
conditional early returns (`if props.Name == "" { return <p>...</p> }`)
all get wrapped too, while a `return` inside a nested closure (e.g. the
immediately-invoked `func() []gox.Node {...}()` idiom used for
rendering a list) is left alone, since that's a different function's
return value, not the page's. Omitting `@title` falls back to a
package-level `DefaultTitle` identifier, which you declare once
yourself (`gox` does not declare it for you — same "trust the Go
compiler" stance as the JSON typed-import annotation: an undeclared
`DefaultTitle` just fails with an ordinary undefined-identifier error).
Use `gox.Document` directly instead when a page's title or stylesheets
genuinely need to vary at runtime, not just per document/request
content within an otherwise-static page.

A third annotation, `@wasm`, covers the other boilerplate every
WASM-consuming SSR page in this repo otherwise hand-wrote — the
`wasm_exec.js` loader plus a `WebAssembly.instantiateStreaming`
bootstrap script:

```go
// @wasm /static/main.wasm
func CanvasPage(props CanvasPageProps) gox.Node {
	return <textarea id="canvas">{props.Content}</textarea>
}
```

Unlike `@stylesheets`, `@wasm`'s value is a URL, not a file path — the
compiled `.wasm` binary doesn't exist yet when its SSR page is
transpiled (it's built separately, from a different package, usually
afterward), so there's nothing to read at transpile time, only a
location to generate `<script>` tags for. `wasm_exec.js`'s URL is
derived from the `.wasm` URL's own directory (`/static/main.wasm` →
`/static/wasm_exec.js`). Both `<script>` tags are appended *after* the
function's own returned content, inside `<body>`, not into `Head` —
scripts that call `document.getElementById` need the elements they
target to already exist.

These are deliberately a third comment shape, not a reuse of either
existing convention in this codebase: a tool directive (`//go:generate`,
`//encore:api`) triggers an external command or build-time codegen,
and `//language=SQL` (see `editors/vscode-gox`) is a pure editor hint
with zero effect on the compiled program. `@title`/`@stylesheets`/
`@wasm` are neither — they're informational metadata that a real tool
(goxc) turns into code — so they get the `@key value` shape instead,
mirroring swaggo/swag's `// @Summary`/`// @Router` handler annotations
(real, widely-used Go prior art for exactly this kind of per-function
informational comment).

## Resource imports

An import spec whose path ends in a recognized non-Go extension is a
resource import, not a Go package import — the file is loaded (and,
where needed, compiled) at transpile time instead of at runtime. Paths
resolve relative to the `.gox` file's own directory, the same
convention `go:embed` uses — not fetched from a URL or module proxy.

Resource imports split into two tiers: **stable** (on by default) and
**experimental** (opt-in via `goxc -experimental` or
`GOX_EXPERIMENTAL=1`, or `transpile.Options{Experimental: true}`).
Experimental means shelling out to a mature external tool this project
doesn't try to reimplement — gox still *recognizes* these extensions
without opting in (so it can give an actionable "enable experimental
mode" error instead of a confusing "not a valid Go import path" one),
but none of that code runs, and a directory literally named
`experimental` is skipped entirely by an ordinary `goxc`/`go generate`
walk so a checked-in experimental example can't break the default
build.

### Stable

```go
import (
	config "config.json"          // -> config (decoded any) + configBytes ([]byte)
	cfg.(Config) "config.json"    // -> cfg (as Config, which you declare) + configBytes
)
```

- **`.json`** binds the alias to the decoded value (as `any`) and
  `<alias>Bytes` to the raw content. With an `alias.(TypeName)
  "file.json"` annotation — reusing Go's type assertion syntax for a
  new meaning that's never valid in a real import spec — gox instead
  decodes directly into `TypeName` and binds `alias` as that type.
  **`TypeName` must already be declared by you** (in the same package
  or an import); gox does not generate it or infer its shape from the
  file's data — it's exactly `json.Unmarshal(configBytes, &v)` with
  the boilerplate written for you, not schema inference, so the only
  failure mode is the ordinary one of your type not matching your
  data, identical to writing the unmarshal call yourself. An earlier
  version of this tried to infer and generate the struct from sample
  data instead; that traded a small amount of boilerplate for a real
  footgun (a field that's a whole number in your sample but a fraction
  in production data would infer `int` and then fail to unmarshal) and
  was dropped in favor of this.
- **`.jsonc`** is `.json` with `//` and `/* */` comments allowed
  (stripped by a small string-literal-aware preprocessor before
  parsing — not a full JSONC library), including the same annotation.
- **`.jsonl`** is one JSON value per line; `<alias>` binds to a slice
  instead of a single value — `[]TypeName` with an annotation, `[]any`
  without one.

CSS used to have its own `.css`/`.scss` resource-import loaders here.
They're gone in favor of the `@stylesheets` page-metadata annotation
(see "The `<html>` entrypoint" above), which reads a page's
stylesheet(s) the same way but ties them to the page component they
style instead of a standalone import line.

### Experimental

```go
import (
	schema "schema.hcl"    // -> a generated package: schema.Users{}, schema.Books{}
	books  "books.proto"   // -> a generated package via protoc/protoc-gen-go
	queries "queries.sql"  // -> a generated package via sqlc
)
```

- **`.hcl`** is gox's schema DSL (`table "x" { column "y" { type = ...
  } }`), parsed with `hashicorp/hcl/v2`. Generates a real Go
  subpackage at `internal/goxgen/hcl/<alias>/` with model structs
  (`models.go`) and an initial `CREATE TABLE` migration
  (`migrations/0001_init.sql`), and rewrites the import to point at
  it — so `schema.Users{}` works like any normal imported type, which
  splicing a value into the importing file (as JSON does) couldn't
  give, since Go has no runtime notion of a type namespace. Scoped to
  snapshot generation: each run regenerates the models/migration from
  the current schema; there's no diffing against a previous version
  for incremental migrations.
- **`.proto`** shells out to the real `protoc`/`protoc-gen-go`
  toolchain (gox doesn't reimplement a protobuf compiler), using
  protoc-gen-go's `-M` file-mapping flag to place output at
  `internal/goxgen/proto/<alias>/` under gox's control rather than
  requiring the `.proto` to declare its own `go_package`. Also runs
  `protoc-gen-go-grpc` if it's on `PATH`. Requires `protoc` +
  `protoc-gen-go` on `PATH`.
- **`.capnp`** shells out to `capnp`/`capnpc-go`. Unlike protoc-gen-go,
  capnpc-go has no external flag for output location — it takes both
  the package and Go import path solely from a
  `$Go.import("...")`/`$Go.package("...")` annotation the schema
  itself must declare (the real go-capnp convention), and writes
  output next to the source file; gox parses that annotation back out
  to know what to import. Requires `capnp` + `capnpc-go` on `PATH`.
- **`.sql`** shells out to the real `sqlc` toolchain (gox doesn't
  reimplement sqlc's query analysis/codegen). sqlc's own config
  (`sqlc.yaml`/`sqlc.json`, found by walking up from the `.sql` file)
  already fully owns pairing queries with a schema and choosing output
  location — gox doesn't generate or validate that config, only
  locate it, run `sqlc generate -f <config>`, and derive the Go import
  path from its `gen.go.package`/`out` fields. Requires `sqlc` on
  `PATH` and a real sqlc project config; gox does not verify the
  config actually lists this specific `.sql` file among its query
  sources (that would mean reimplementing sqlc's own config
  resolution), only that a config exists somewhere above it.

The experimental loaders live in `internal/transpile/experimental`, a
separate package from `internal/transpile` — wired in only via
`transpile.Options.ExperimentalLoaders`, so unless a caller explicitly
imports and supplies that package, its code is never reached, not just
flag-gated within the same files. `internal/transpile/experimental`'s
own package doc has more on the design.

Neither `protoc` nor `capnp` nor `sqlc` are installed in the sandbox
this was built in. The sqlc loader's "no config found" error path is
verified, and its actual invocation and import-path derivation are
verified too, against a stubbed `sqlc` executable standing in for the
real tool (gox's own discovery/invocation/derivation logic is what's
under test, not sqlc's codegen correctness). `.proto`/`.capnp` are
verified only against their "tool not found" error path. JSON (both
paths), `@title`/`@stylesheets`/`@wasm`, and HCL are exercised fully,
including `mustCompileAndRun`-style tests that actually `go run` the
generated code.

## Examples

- `examples/ssr-demo` — components with props, a conditional branch,
  and a rendered list, run server-side:

  ```
  go generate ./...
  go run ./examples/ssr-demo
  go test ./examples/ssr-demo/...
  ```

- `examples/wasm-counter` — a stateful counter compiled to WASM and
  mounted client-side, proving domrt's diff/patch against a real
  browser:

  ```
  go generate ./...
  GOOS=js GOARCH=wasm go build -o examples/wasm-counter/main.wasm ./examples/wasm-counter/
  cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/wasm-counter/
  cd examples/wasm-counter && python3 -m http.server 8080
  # open http://localhost:8080/
  ```

- `examples/wasm-browser-api-demo` — `domrt/browser`'s `Fetch` and
  `LocalStorage` wrappers, reading a local `config.json` and round
  -tripping a stored value:

  ```
  go generate ./...
  GOOS=js GOARCH=wasm go build -o examples/wasm-browser-api-demo/main.wasm ./examples/wasm-browser-api-demo/
  cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/wasm-browser-api-demo/
  cd examples/wasm-browser-api-demo && python3 -m http.server 8080
  # open http://localhost:8080/
  ```

- `examples/wasm-sw-demo` — a cache-first service worker compiled from
  `.gox`, proving a Go/WASM binary can run as a real browser service
  worker (not just page-context script):

  ```
  go generate ./...
  GOOS=js GOARCH=wasm go build -o examples/wasm-sw-demo/sw.wasm ./examples/wasm-sw-demo/
  cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/wasm-sw-demo/
  cd examples/wasm-sw-demo && python3 -m http.server 8080
  # open http://localhost:8080/ -- devtools > Application > Service Workers
  # shows it installed, activated, and claimed the page.
  ```

- `examples/resource-imports-demo` — a JSON config decoded straight
  into a hand-declared `SiteConfig` (`config.(SiteConfig)
  "config.json"`, bound directly from the `.gox` import block) and a
  page component whose `@title`/`@stylesheets` doc-comment annotations
  wrap it in `gox.Document{}.Render()` automatically:

  ```
  go generate ./...
  go run ./examples/resource-imports-demo
  go test ./examples/resource-imports-demo/...
  ```

- `examples/http-server-demo` — SSR the way it's actually meant to be
  used: served over real HTTP (`gox.Render` takes any `io.Writer`, and
  `http.ResponseWriter` satisfies that directly — no gox changes
  needed). Verified against a real `httptest.Server` TCP listener, and
  by curling a locally-run instance:

  ```
  go generate ./...
  go test ./examples/http-server-demo/...
  go run ./examples/http-server-demo &
  curl 'http://localhost:8089/?name=World'
  ```

- `examples/canvas-app` — the big one: a bare-minimum Google-Docs-style
  typing canvas tying together gox SSR pages (`@stylesheets` for the
  page's CSS, `@wasm` for the autosave client's bootstrap `<script>`
  tags — neither page hand-writes either), a WASM autosave client
  (`domrt.Mount`/`Update` plus `domrt/browser.FetchOpts` for the
  debounced `PUT`), real Postgres
  via `database/sql` (Docker Compose for local dev, schema managed with
  [Atlas](https://atlasgo.io)'s versioned migrations), and
  Encore-annotated handlers for the same business logic. `make run`
  chains the whole thing together. It's a separate Go module
  (`examples/canvas-app/go.mod`, `replace`-ing this repo) so it depends
  on gojiberry the way an actual consumer would. See
  `examples/canvas-app/README.md` for what's
  genuinely verified here (the gox pages, the WASM client against a
  real headless browser, the store against a real Postgres) versus
  what's best-effort and unverified (the `//encore:api` handlers'
  actual behavior under `encore run`, since this sandbox's network
  policy blocked the Encore CLI installer).

## Editor support

`editors/vscode-gox` is a minimal VS Code extension: `.gox` file
association plus syntax highlighting (real Go tokenization with
`<Tag>`/`{expr}` highlighting layered on top via a TextMate injection
grammar — see that directory's README for why a naive combined pattern
list doesn't work here). No completions/hover/diagnostics yet; see
Known limitations.

It also includes a second, general-purpose injection — not gox
-specific, works in any `.go` file — for `//language=`-annotated
embedded-language raw strings, e.g. `` `//language=SQL\nSELECT...` ``
gets SQL highlighting inside the string. This is JetBrains' own
Language Injection convention from GoLand/IntelliJ, reused as-is
rather than inventing a new spelling (`@` isn't a Go token outside a
string or comment, so there's no Go precedent for an `@`-prefixed
marker to begin with). Same idea as `es6-string-html`/
`vscode-styled-components` for JS template literals. See that
directory's README for the one real constraint (the marker must share
a line with the opening backtick).

## Known limitations (v1)

- Generated `.go` files don't yet carry `//line` directives, so a
  panic inside transpiled code reports a line number in the generated
  file, not the original `.gox` source.
- No LSP support yet (completion, hover, go-to-definition,
  diagnostics) — that needs an LSP proxy translating `.gox` positions
  to `gopls`'s view of the generated file, which in turn needs the
  `//line` directives above. Syntax highlighting alone is covered by
  `editors/vscode-gox`.
- Children/attribute list diffing in `domrt` is a straightforward
  keyed-or-positional reconciliation, not a minimal-move (LIS-based)
  algorithm — correct, but not necessarily the fewest possible DOM
  moves for a heavily-reordered keyed list.
