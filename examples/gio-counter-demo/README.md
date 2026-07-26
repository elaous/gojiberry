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
  `span`/`p` as a `material.Label`, `button` as a `material.Button`,
  `input` type=text/type=checkbox as a `material.Editor`/
  `material.CheckBox`) since CSS classes/inline styles -- how every
  other tag's look would normally be customized -- have no Gio
  equivalent. Because Gio is immediate-mode (it repaints everything
  every frame; there's nothing to diff), `Layout` is a plain recursive
  walk, closer in shape to `gox.Render` than to `domrt`'s patch
  algorithm. The one real wrinkle: a Gio `widget.Clickable`/
  `widget.Bool`/`widget.Editor` must persist across frames (Gio reads
  accumulated interaction state off the struct itself), but the `Node`
  tree is rebuilt from scratch on every render call, so `Renderer` keeps
  a `map[string]*T` per widget kind, keyed by `Node.Key` if set, else the
  element's `id` attribute -- see the package doc comment for the full
  reasoning, including what happens if an interactive element has
  neither. `select`/`option` (a native dropdown) is a known gap:
  `widget/material` has no equivalent widget, so supporting it means
  building a real custom widget, not just another tag-switch case.
  `Renderer.Styles` (`map[string]ClassStyle`) is the narrow, opt-in
  slice of CSS that does have a real Gio equivalent -- padding, a
  background fill, a border, a corner radius, and flex cross-axis
  alignment -- keyed by class name, merged in a node's own attribute
  order (last one wins per field, CSS's own equal-specificity rule) and
  applied to every tag. Left nil (the default), every node paints
  exactly as it did before `Styles` existed. This is deliberately just
  the *runtime* half: turning a table of `ClassStyle` values into real
  painted output. Building that table *from* real CSS (parsing
  `.class { ... }` rules, resolving `var(--x)` custom properties) is
  separate, not-yet-built work -- see gojiberry's own mobile-backend
  ROADMAP entry for the fuller two-part design and why a general CSS
  parser isn't needed for it. `goxgio/cssgen` is that generator: given
  real CSS text and a list of class names, it parses `:root`/
  `body.dark` custom-property tokens and flat `.class { ... }` rules
  (padding, background, border, border-radius only -- everything else
  a matched rule sets is reported as a `Warning`, not silently
  dropped) into a real `map[string]ClassStyle`, verified against real
  rules copied verbatim from OnDesign's own `index.html`, not
  synthetic CSS.
- `main.go` is the real, interactive entry point: a standard
  `app.Window` event loop that calls `Renderer.Layout` on every
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

`TestCheckboxHandlesARealPointerClick` and
`TestTextInputHandlesRealClickFocusAndTyping` cover the `input`
support the same way: `widget.Bool` (unlike `widget.Clickable`) has no
exported `Click()` shortcut, so the checkbox test dispatches a real
`pointer.Event` press/release pair through a real `input.Router` at the
exact center of the `Dimensions` `Layout` itself returned -- the same
convention Gio's own `widget` package tests use for `Bool`. The text
input test drives a real pointer click first (which focuses the
`widget.Editor` through its own click handling, exactly as a real user
clicking into a field would) and then a real `key.EditEvent`, the same
event Gio's own `widget` package tests use to simulate typed input --
never `Editor.SetText` directly, which would skip both the click-to-
focus path and the change-detection `onChange` fires through.

`TestClassStyleAppliesBackgroundAndPadding` covers `Renderer.Styles`
the same way: rendering the identical tree with `Styles` nil versus a
class mapped to a background color/padding, confirming not just that
some pixel changed but that the exact requested color is what actually
got painted at a sample point inside the div's own padding.

`goxgio/cssgen`'s own tests (`go test ./goxgio/cssgen/...`) verify the
generator against CSS copied verbatim from OnDesign's `index.html`:
`:root`/`body.dark` token resolution, `var()`/hex/`rgb()`/`rgba()`
color parsing (including real alpha), 1- and 2-value padding shorthand
exactly and 3-4 value shorthand flagged as an approximation, `border:
none`/`background: none` recognized as a real zero value rather than a
failure, and -- the one real bug this test suite caught while being
written -- a `:hover`/`.active`/`:disabled` variant of a wanted class
correctly reported as a `Warning` and *not* silently merged into the
base style (the first version matched only an exact class name, so
`.find-toggle:disabled` was silently ignored rather than
warned-about; fixed by extracting the selector's own leading class
name before the wanted-class lookup, not after).

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
