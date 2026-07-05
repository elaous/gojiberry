# gio-counter-demo

The same stateful counter `examples/wasm-counter` mounts into a real
browser DOM via `domrt`, mounted instead into a real native window via
[Gio](https://gioui.org) (`gioui.org`) -- proving `gox.Node` is
renderer-agnostic in the sense that actually matters: a completely
different target platform (a desktop/mobile GPU window, not a browser)
is a new consumer of the same tree, not a rewrite of it.

It's a separate Go module (like `examples/canvas-app` and
`examples/collab-editor-app`), `replace`-ing `github.com/elaous/gojiberry`
with the local checkout, so it can depend on `gioui.org` (and the cgo/
EGL/Vulkan/X11/Wayland bindings that pulls in) without adding any of
that to the root module.

## How it works

- `shared/counter.gox` is `Counter`, unchanged in spirit from
  `wasm-counter`'s: it only calls `gox.El`/`gox.Text`/`gox.On`, with no
  `js`/`wasm` or Gio import at all. `data-axis="horizontal"` on the
  outer `div` is the one layout hint `goxgio` understands (there's no
  CSS to carry a flex direction on a native target); `domrt` and
  `gox.Render` both ignore it as an inert attribute.
- `goxgio/render.go` is the renderer itself: `Renderer.Layout` walks a
  `gox.Node` tree and lays it out via Gio's `layout`/`widget/material`
  packages. It understands a minimal tag set (`div` as a `layout.Flex`,
  `span`/`p` as a `material.Label`, `button` as a `material.Button`)
  since CSS classes/inline styles -- how every other tag's look would
  normally be customized -- have no Gio equivalent. Because Gio is
  immediate-mode (it repaints everything every frame; there's nothing
  to diff), `Layout` is a plain recursive walk, closer in shape to
  `gox.Render` than to `domrt`'s patch algorithm. The one real wrinkle:
  a Gio `widget.Clickable` must persist across frames (Gio reads
  accumulated click state off the struct itself), but the `Node` tree
  is rebuilt from scratch on every render call, so `Renderer` keeps a
  `map[string]*widget.Clickable` keyed by `Node.Key` if set, else the
  element's `id` attribute -- see the package doc comment for the full
  reasoning, including what happens if an interactive element has
  neither.
- `main.go` is the real, interactive entry point: a standard
  `app.Window` event loop that calls `Renderer.Layout` on every
  `FrameEvent`. Run it on a machine with a display:

  ```
  go run .
  ```

## Verified

`gio_test.go` drives the exact same `Counter`/`Renderer` pair through
Gio's own headless GPU backend (`gioui.org/gpu/headless`) instead of
`app.Window`, so it runs without a display server or compositor:

- A real (software-rasterized, where no GPU device is present) GPU
  frame is rendered and read back pixel-for-pixel via `Screenshot`.
- A real `widget.Clickable.Click()` -- the same mechanism Gio itself
  uses for a "simple programmatic click" -- drives the "+"/"-"
  buttons' `onClick` handlers, not a direct Go call bypassing the
  renderer.
- The count genuinely changes (1, then back to 0) and the *rendered
  pixels* genuinely change too, confirmed by diffing full-frame
  screenshots before and after each click (with a same-state baseline
  frame first, to rule out nondeterministic rendering).

One real, non-obvious thing this surfaced: Gio's headless backend
settles a newly-rasterized glyph (e.g. the "1" that wasn't in the text
shaper's glyph atlas before) one frame late -- the screenshot taken
immediately after the frame that processes a click still shows the old
pixels even though the counter's Go-level state already changed. The
test renders one extra settle frame after each click before comparing
screenshots; three same-state frames in a row are confirmed
pixel-identical, so this is a real, bounded one-frame latency in Gio's
own glyph-atlas upload, not test flakiness.

```
go test ./...
```

In a container without a real GPU (no `/dev/dri`), `gpu/headless` still
works via Mesa's software rasterizer -- set
`LIBGL_ALWAYS_SOFTWARE=1 EGL_PLATFORM=surfaceless` before `go test`/
`go run` if `NewWindow` fails with `eglInitialize failed`. This isn't
needed on a machine with a real GPU or display.

Interactive use through a real `app.Window` (rather than
`gpu/headless`) is unverified here since this sandbox has no display
server to open a window on; the build itself (including the real cgo
X11/Wayland/EGL/Vulkan bindings `gioui.org/app` links against) is
verified via `go build ./...`.
