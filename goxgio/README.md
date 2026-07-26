# goxgio

A third `gox.Node` renderer, alongside `gox.Render` (server-side HTML,
see `../gox/render.go`) and `domrt.Mount`/`Update` (client-side DOM
patching, see `../domrt/domrt.go`): it walks the exact same
renderer-agnostic tree onto a real [Gio](https://gioui.org)
(`gioui.org`) window, so a native desktop/mobile target is a genuinely
new consumer of `gox.Node` rather than a rewrite of it.

It's a separate Go module (like `../examples/gio-counter-demo`, which
depends on this one rather than defining it locally), `replace`-ing
`github.com/elaous/gojiberry` with the local checkout, so it can depend
on `gioui.org` without adding it to the root module -- see gojiberry's
own mobile-backend ROADMAP entry (task #107 in OnDesign's `docs/
ROADMAP.md`) for the fuller reasoning behind keeping every Gio-touching
piece in its own module.

## How it works

`render.go`'s `Renderer.Layout` walks a `gox.Node` tree and lays it out
via Gio's `layout`/`widget/material` packages. Two things distinguish
this walk from the other two renderers, both forced by Gio being an
immediate-mode toolkit rather than a retained DOM:

- **No differ.** `domrt` keeps the previous tree around to compute a
  minimal patch; Gio repaints everything every frame anyway, so
  `Renderer.Layout` is a plain recursive walk, structurally closer to
  `gox.Render` than to `domrt`'s patch algorithm.
- **Interactive-widget identity.** A Gio `widget.Clickable`/
  `widget.Bool`/`widget.Editor` must persist across frames (Gio reads
  accumulated interaction state off the struct itself), but the `Node`
  tree is rebuilt from scratch on every render call. `Renderer` solves
  this with a `map[string]*T` per widget kind, keyed by `Node.Key` if
  set (the same field `domrt` already uses for keyed-list
  reconciliation), else the element's `id` attribute -- see the
  package doc comment for the full reasoning, including what happens
  if an interactive element has neither.

Only a minimal tag set is understood: `div` (`layout.Flex`, reading one
layout hint, the `data-axis` attribute, since there's no CSS to carry a
flex direction on a native target), `span`/`p` (`material.Label`),
`button` (`material.Button`), `input` type=text/type=checkbox
(`material.Editor`/`material.CheckBox`), and `select`/`option`. The
last one needed real custom-widget work rather than another
tag-switch case, since `widget/material` has no native dropdown
equivalent at all: a closed select is a `material.Button` showing the
selected `<option>`'s own label; an open one is that same button
stacked above one more button per option (reusing `Renderer`'s own
`clickables` map, keyed `"<select id>\x00<option value>"`, rather than
adding a second per-option map). Which option is *selected* is read
fresh from the tree's own `selected` attribute every frame, not
persisted by `Renderer` itself -- the same controlled-component
contract `domrt` already holds every caller to.

`Renderer.Styles` (`map[string]ClassStyle`) is the narrow, opt-in slice
of CSS that has a real Gio equivalent -- padding, a background fill, a
border, a corner radius, and flex cross-axis alignment -- keyed by
class name, merged in a node's own attribute order (last one wins per
field, CSS's own equal-specificity rule) and applied to every tag. Left
nil (the default), every node paints exactly as it did before `Styles`
existed. This is deliberately just the *runtime* half: turning a table
of `ClassStyle` values into real painted output.

`cssgen` is the *generator* half: given real CSS text and a list of
class names, it parses `:root`/`body.dark` custom-property tokens and
flat `.class { ... }` rules (padding, background, border,
border-radius only -- everything else a matched rule sets is reported
as a `Warning`, not silently dropped) into a real `map[string]
ClassStyle`, verified against real rules copied verbatim from
OnDesign's own `index.html`, not synthetic CSS. See `cssgen`'s own
package doc comment for exactly what it does and doesn't handle.

## Verified

`render_test.go` drives `Renderer` through Gio's own headless GPU
backend (`gioui.org/gpu/headless`), so it runs without a display server
or compositor -- real pointer/keyboard events, real screenshots, real
pixel diffs, not a bypass reaching into `Renderer`'s own unexported
state:

- `TestCheckboxHandlesARealPointerClick` / `TestTextInputHandlesRealClickFocusAndTyping`
  cover `input` type=checkbox/type=text via real `pointer.Event`/
  `key.EditEvent` dispatch through a real `input.Router` (the same
  convention Gio's own `widget` package test suite uses).
- `TestClassStyleAppliesBackgroundAndPadding` covers `Renderer.Styles`:
  rendering the identical tree with `Styles` nil versus a class mapped
  to a background color/padding, confirming the exact requested color
  is what actually got painted, not just that some pixel changed.
- `TestSelectOpensAndFiresOnChangeOnRealClicks` covers `select`/
  `option`: `Renderer.Click` (Gio's own `widget.Clickable.Click()`)
  opens the dropdown, confirming the rendered `Dimensions` actually
  grew, then selects an option, confirming `onChange` fires with the
  right value and the dropdown collapses back to its original size.

The `Counter`/click-driven integration test (proving the *whole
pipeline* -- a real component, a real window, a real click firing a
real handler -- works end to end) lives in `../examples/
gio-counter-demo`'s own `gio_test.go` instead, since it depends on
that module's own `shared.Counter` component.

`cssgen`'s own tests (`go test ./cssgen/...`) verify the generator
against CSS copied verbatim from OnDesign's `index.html`: `:root`/
`body.dark` token resolution, `var()`/hex/`rgb()`/`rgba()` color
parsing (including real alpha), 1- and 2-value padding shorthand
exactly and 3-4 value shorthand flagged as an approximation, `border:
none`/`background: none` recognized as a real zero value rather than a
failure, and a `:hover`/`.active`/`:disabled` variant of a wanted class
correctly reported as a `Warning` and *not* silently merged into the
base style.

```
go test ./...
```

In a container without a real GPU (no `/dev/dri`), `gpu/headless` still
works via Mesa's software rasterizer -- set
`LIBGL_ALWAYS_SOFTWARE=1 EGL_PLATFORM=surfaceless` before `go test` if
`NewWindow` fails with `eglInitialize failed`. This isn't needed on a
machine with a real GPU or display.

## Consuming this from another module

Add both a `require` and a `replace` pointing at this module, the same
shape `examples/gio-counter-demo`'s own `go.mod` uses:

```
require github.com/elaous/gojiberry/goxgio v0.0.0-00010101000000-000000000000
replace github.com/elaous/gojiberry/goxgio => /path/to/gojiberry/goxgio
```

## Not yet done

`goxgio` covers every tag `ui/*.gox` structurally needs, but nothing
outside this repo actually renders OnDesign's real UI through it yet --
`examples/gio-counter-demo` is still the only consumer, and it only
renders its own `Counter` demo component. Wiring `cssgen`'s output into
a real OnDesign-targeting Gio entry point is separate, not-yet-started
work -- see OnDesign's own `docs/ROADMAP.md`, task #107, for the fuller
picture.
