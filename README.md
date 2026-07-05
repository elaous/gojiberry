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
  transpiler described above.
- **`cmd/goxc`** — the CLI: `go run ./cmd/goxc <dir>` transpiles every
  `*.gox` under `<dir>` into a gofmt-clean `*_gox.go` next to it (the
  generated file is committed, like `templ`'s `*_templ.go` convention,
  so `go build`/`go test` work without running goxc first). Wired up
  as `go generate` via the repo-root `generate.go`.
- **`domrt`** — the client runtime (`GOOS=js GOARCH=wasm` only).
  `domrt.Mount` creates real DOM nodes for a `gox.Node` tree;
  `Root.Update` diffs the next tree against what's live and patches
  only what changed — text content in place, attributes added/removed,
  event listeners registered at most once per (element, event) with
  the Go closure swapped on later renders, and children reconciled by
  key when present — preserving DOM node identity (focus, scroll
  position, input state) wherever the shape didn't change.
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
  conflict-resolution logic clients must trust. Every deployment detail
  — listen address, region label — comes from `GOJIBERRY_SYNC_ADDR` /
  `GOJIBERRY_SYNC_REGION` (see `syncserver.ConfigFromEnv`), so the same
  binary (`cmd/syncserver`) can be pointed at any operator's own
  infrastructure without a rebuild.

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
  ```

## Known limitations (v1)

- Generated `.go` files don't yet carry `//line` directives, so a
  panic inside transpiled code reports a line number in the generated
  file, not the original `.gox` source.
- No editor/LSP support yet (syntax highlighting, completion) — write
  `.gox` as plain text and let `go generate` + `go build` catch
  mistakes.
- Children/attribute list diffing in `domrt` is a straightforward
  keyed-or-positional reconciliation, not a minimal-move (LIS-based)
  algorithm — correct, but not necessarily the fewest possible DOM
  moves for a heavily-reordered keyed list.
