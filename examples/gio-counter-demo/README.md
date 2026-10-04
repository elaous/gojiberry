# gio-counter-demo

The same stateful counter `examples/wasm-counter` mounts into a real
browser DOM via `domrt`, mounted instead into a real native window via
[Gio](https://gioui.org) (`gioui.org`) through `xangogio`
(`../../xangogio`) -- proving `xango.Node` is renderer-agnostic in the
sense that actually matters: a completely different target platform (a
desktop/mobile GPU window, not a browser) is a new consumer of the
same tree, not a rewrite of it.

It's a separate Go module (like `examples/canvas-app` and
`examples/collab-editor-app`), `replace`-ing `github.com/elaous/gojiberry`
with the local checkout (so it can depend on `gioui.org`, and the cgo/
EGL/Vulkan/X11/Wayland bindings that pulls in, without adding any of
that to the root module) and `github.com/elaous/gojiberry/xangogio` with
the sibling `../../xangogio` checkout (the actual renderer -- see that
module's own README for how it works and what it's verified against;
this demo doesn't define it locally).

## How it works

- `shared/counter.xg` is `Counter`, unchanged in spirit from
  `wasm-counter`'s: it only calls `xango.El`/`xango.Text`/`xango.On`, with no
  `js`/`wasm` or Gio import at all. `data-axis="horizontal"` on the
  outer `div` is the one layout hint `xangogio` understands (there's no
  CSS to carry a flex direction on a native target); `domrt` and
  `xango.Render` both ignore it as an inert attribute.
- `main.go` is the real, interactive entry point: a standard
  `app.Window` event loop that calls `xangogio.Renderer.Layout` on every
  `FrameEvent`. Run it on a machine with a display:

  ```
  go run .
  ```

## Packaging for a real target

`make build-web`/`build-linux`/`build-windows`/`build-macos`/
`build-android`/`build-ios` package this demo via `gogio`
(`gioui.org/cmd/gogio`, a `go.mod` tool dependency) for a real target
platform -- `build-desktop`/`build-mobile`/`build-all` group them.
Linux is the one target that doesn't need `gogio` at all (no `-target
linux` -- a Gio program is already a plain, unpackaged Go binary
there), so `build-linux` is just `go build`. Verified by hand in this
sandbox: `build-web`, `build-linux`, and `build-windows` all succeed
with no extra SDK install. **Not verified here**, and not expected to
work without the real platform tooling: `build-android` (needs
`$ANDROID_HOME`, the real Android SDK/NDK) and `build-macos`/
`build-ios` (both need a real macOS host with Xcode -- the same gap
already tracked in OnDesign's own mobile-backend ROADMAP entry,
deferred to a real Mac). See the `Makefile`'s own doc comment for the
exact error each one fails with here. The per-architecture build
matrix (arm64 vs amd64 vs...) is left at `gogio`'s own defaults for
now, tracked as separate follow-up work.

## Verified

`gio_test.go`'s `TestCounterRendersAndClicksThroughRealGio` drives the
exact same `Counter`/`xangogio.Renderer` pair `main.go`'s `run` mounts
into a real desktop window, but through Gio's own headless GPU backend
(`gioui.org/gpu/headless`) instead of `app.Window`, so it runs without
a display server or compositor:

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

`xangogio.Renderer`'s own tag-by-tag coverage (`input` type=checkbox/
type=text, `select`/`option`, `Renderer.Styles`) is verified in that
module's own `render_test.go` instead, alongside `xangogio/cssgen`'s own
tests -- see `../../xangogio/README.md`'s own "Verified" section. This
demo's own test is specifically the end-to-end integration proof: a
real component, a real window, a real click firing a real handler,
through the actual `main.go` entry point's own renderer setup.

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
