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
  (JSON/JSONC/JSONL/CSS/SCSS) described below.
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

## Resource imports

An import spec whose path ends in a recognized non-Go extension is a
resource import, not a Go package import — the file is loaded (and,
where needed, compiled) at transpile time instead of at runtime. Paths
resolve relative to the `.gox` file's own directory, the same
convention `go:embed` uses — not fetched from a URL or module proxy.

Resource imports split into two tiers: **stable** (on by default) and
**experimental** (opt-in via `goxc -experimental` or
`GOX_EXPERIMENTAL=1`, or `transpile.Options{Experimental: true}`).
Anything experimental either shells out to a mature external tool this
project doesn't try to reimplement, or carries real footgun risk —
gox still *recognizes* these extensions/annotations without opting in
(so it can give an actionable "enable experimental mode" error instead
of a confusing "not a valid Go import path" one), but none of that
code runs, and a directory literally named `experimental` is skipped
entirely by an ordinary `goxc`/`go generate` walk so checked-in
experimental examples can't break the default build.

### Stable

```go
import (
	config "config.json"   // -> config (decoded any) + configBytes ([]byte)
	"App.css"              // -> App_css (string), identifier derived from the filename
)
```

- **`.json`** binds the alias to the decoded value (as `any`) and
  `<alias>Bytes` to the raw content, so code needing a concrete type
  can `json.Unmarshal(configBytes, &myStruct)` itself — gox doesn't
  infer a struct shape from sample data on this path (see the
  experimental `.(TypeName)` annotation below for that).
- **`.jsonc`** is `.json` with `//` and `/* */` comments allowed
  (stripped by a small string-literal-aware preprocessor before
  parsing — not a full JSONC library).
- **`.jsonl`** is one JSON value per line; `<alias>` binds to `[]any`
  instead of a single value.
- **`.css`** binds the alias to the file's content as a plain Go
  string.
- **`.scss`** compiles first via the `sass` (dart-sass) CLI, then
  binds the same way as `.css`. Requires `sass` on `PATH`; gox doesn't
  bundle or reimplement a SCSS compiler. (Kept stable rather than
  experimental since it's a straightforward compile-and-bind step with
  no inference or generated-subpackage machinery.)

### Experimental

```go
import (
	cfg.(Config) "config.json"  // -> cfg (typed Config, inferred from the file) + a generated Config struct
	schema "schema.hcl"          // -> a generated package: schema.Users{}, schema.Books{}
	books  "books.proto"         // -> a generated package via protoc/protoc-gen-go
	queries "queries.sql"        // -> a generated package via sqlc
)
```

- **JSON's `alias.(TypeName) "file.json"` annotation** — reusing Go's
  type assertion syntax for a new meaning that's never valid in a real
  import spec — infers a Go struct shape (and any nested struct types
  it needs) from the file's own sample data, generates `TypeName`, and
  binds `alias` as that type directly instead of `any`. Works the same
  way on `.jsonc` and (binding to `[]TypeName`, inferred from the
  first line) `.jsonl`. This is sample-based inference, not a schema:
  if a field could hold a different shape of value elsewhere (this
  sample's `"count": 5` infers `int`; production data sending `5.5`
  for that field would then fail to unmarshal), hand-declare the type
  and use the stable path's `<alias>Bytes` instead. This is the
  footgun-risk half of "experimental" — the loader itself lives in the
  stable `internal/transpile` package (it's not shelling out to
  anything), only the annotation is gated.
- **`.hcl`** is gox's schema DSL (`table "x" { column "y" { type = ...
  } }`), parsed with `hashicorp/hcl/v2`. Generates a real Go
  subpackage at `internal/goxgen/hcl/<alias>/` with model structs
  (`models.go`) and an initial `CREATE TABLE` migration
  (`migrations/0001_init.sql`), and rewrites the import to point at
  it — so `schema.Users{}` works like any normal imported type, which
  splicing a value into the importing file (as JSON/CSS do) couldn't
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

The experimental loaders live in `internal/transpile/experimental`
(the JSON annotation excepted, per above), a separate package from
`internal/transpile` — wired in only via
`transpile.Options.ExperimentalLoaders`, so unless a caller explicitly
imports and supplies that package, its code is never reached, not just
flag-gated within the same files. `internal/transpile/experimental`'s
own package doc has more on the design.

Neither `protoc` nor `sass` nor `capnp` nor `sqlc` are installed in
the sandbox this was built in. `.scss`'s and the sqlc loader's
"tool not found" *and* "no config found" error paths are verified;
the sqlc loader's actual invocation and import-path derivation are
verified too, against a stubbed `sqlc` executable standing in for the
real tool (gox's own discovery/invocation/derivation logic is what's
under test, not sqlc's codegen correctness). `.proto`/`.capnp` are
verified only against their "tool not found" error path. JSON
(stable and the experimental annotation), CSS, and HCL are exercised
fully, including `mustCompileAndRun`-style tests that actually `go
run` the generated code.

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

- `examples/resource-imports-demo` — a JSON config decoded into a
  hand-declared struct and a CSS file injected into the page, both
  bound directly from the `.gox` import block (stable loaders only):

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

- `examples/experimental` — the same JSON config as
  `resource-imports-demo`, but decoded via the experimental
  `cfg.(SiteConfig) "config.json"` annotation instead of a
  hand-declared struct. A directory named `experimental` is skipped by
  an ordinary `goxc`/`go generate` walk, so it needs its own explicit
  regeneration step:

  ```
  go run ./cmd/goxc -experimental examples/experimental
  go run ./examples/experimental
  go test ./examples/experimental/...
  ```

## Editor support

`editors/vscode-gox` is a minimal VS Code extension: `.gox` file
association plus syntax highlighting (real Go tokenization with
`<Tag>`/`{expr}` highlighting layered on top via a TextMate injection
grammar — see that directory's README for why a naive combined pattern
list doesn't work here). No completions/hover/diagnostics yet; see
Known limitations.

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
