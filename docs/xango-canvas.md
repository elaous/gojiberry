# Using xango with the canvas components

*Status: parts 1 and 3 are built (generic tags and render props; the
`canvas/xango` renderer with `Canvas`, `TextFlow`, `Header`, `Footer` and
`PageNumber`, in `canvas/canvas/xango`, demo in `examples/canvas-xango-demo`).
The rest is still a proposal; remaining work is in [canvas-todo.md](canvas-todo.md).*

## 0. What works today

```xango
import canvas "github.com/elaous/gojiberry/canvas/canvas/xango"

<canvas.Canvas width={900} height={560} mode={state.Mode}>
	<canvas.TextFlow source={state.Source} pageCount={3}
		margins={[]float64{60, 50, 60, 50}}
		pages={func(page xango.Node, n int) xango.Node {
			return <>
				<canvas.Header text="Report" />
				{page}
				<canvas.Footer><canvas.PageNumber format="Page {n}" /></canvas.Footer>
			</>
		}} />
</canvas.Canvas>
```

`root := canvas/xango.NewRoot(stage); root.Render(tree)` builds the canvas
components on the first call and updates them in place on later ones.
Differences from the first sketch: props are lower-camel (`pageCount`),
`margins` is a `[]float64` in CSS order, and `pages` is called once per
sheet with the sheet slot and its index. A sheet whose header, footer or
number equals the previous sheet's inherits it (so the "this page and
following" semantics of editing on the canvas stay the same); one that
leaves an element out where the previous had it removes it, and `none`
says so explicitly. Props are applied only when they change, so a header
edited by double-click on the canvas survives a re-render that passes the
same props.


The `canvas` package is imperative Go: you build components (`TextFlow`,
`ScrollView`, `PageSet`, ...), add them to a `Stage`, and they change
themselves as events arrive. xango is declarative: a component is a function
that returns a `xango.Node` tree, and a renderer turns the tree into something
(HTML, DOM patches, a Gio window). This note says how the two can meet
without giving up what already works.

## 1. What already works: function props and generics

A component's props are an ordinary Go struct, so a prop can be a function,
and a function written in `.xg` can return markup:

```xango
<MyApp items={items} item={func(it Item) xango.Node {
	return <li class="row">{it.Name}</li>
}} />
```

This compiled before (the transpiler hands `{...}` to Go untouched, rewriting
the tags inside it), and now has tests (`internal/transpile`).

What was missing is a way to name the item type of a *generic* component, so
`items` and the function agree on `Item`. `.xg` now accepts type arguments
on a component tag, written as in Go:

```xango
<components.List[Person]
	items={people}
	key={func(p Person) string { return p.Name }}
	item={func(p Person) xango.Node {
		return <span><strong>{p.Name}</strong>, {p.Role}</span>
	}}
/>
```

which becomes `components.List[Person](components.ListProps[Person]{...})`.
`components.List` is the first generic component (`examples/generic-list-demo`
renders one). Render props like this are how a component hands *you* the
job of drawing one item, one cell, one page header.

## 2. The same idea inside canvas components

Most canvas components already take functions where it makes sense
(`OnChange`, `Completer`, `Images`, `TransformFunc`). Two more would let a
xango function describe canvas content:

- **Cells:** `TableView.Cell func(row, col int) xango.Node` -- a cell that is
  itself a tree of canvas elements (a text run with a format, an image), the
  way a render prop gives a DOM table rich cells.
- **Headers and footers:** `PageSet.Header func(page int) xango.Node`, so a
  header can be computed from the page number and the section instead of a
  fixed string.

These need part 3, because a function that returns a `xango.Node` needs a
renderer that understands canvas nodes.

## 3. A fourth renderer: `canvas/xango`

`xango.Node` is already renderer-agnostic -- `xango.Render` makes HTML,
`domrt` patches the DOM, `xangogio` draws with Gio. A `canvas/xango` renderer
would turn a tree of *canvas* elements into a live `Stage`:

```xango
func Editor(doc Doc) xango.Node {
	return <canvas.Stage width={920} height={640} mode={doc.Mode}>
		<canvas.ScrollView x={10} y={10} width={900} height={620} lockH>
			<canvas.PageSet pages={doc.Pages} onChange={doc.SetPages} />
			<canvas.TextFlow source={doc.Markdown} transform={canvas.Markdown{}}
				pages={doc.Pages} />
			<canvas.Image src={doc.Logo} anchor={canvas.Anchor{Page: 0, X: 20, Y: 20}} wrap />
			<canvas.FloatingText text={doc.Note} anchor={doc.NoteAnchor}
				onChange={doc.SetNote} />
		</canvas.ScrollView>
	</canvas.Stage>
}
```

Nothing in the transpiler has to change: `<canvas.TextFlow ... />` is already
`canvas.TextFlow(canvas.TextFlowProps{...})`. What is new is what that call
returns.

### The one change in `xango`

Today a `xango.Node` carries `Tag` and string-valued `Attrs`. A canvas element
needs typed props (slices, pointers, `Anchor`, functions). The smallest
addition is a host node:

```go
// KindHost is a node for a renderer-specific component: Tag names it,
// Props is its typed props value, Children are its child nodes.
xango.Host(tag string, props any, children ...xg.Node) xango.Node
```

and each canvas element gets a constructor with the same shape the
`components` already use (`TextFlow(props TextFlowProps) xango.Node`), which
returns `xango.Host("canvas.TextFlow", props, children...)`. DOM and HTML
renderers would ignore or reject host nodes; `canvas/xango` keeps a registry
from tag to a small adapter:

```go
type Adapter interface {
	New(props any) canvas.Drawer          // build the component
	Update(c canvas.Drawer, props any)    // apply new props to it
}
```

### Reconciling without losing interaction state

The hard part is not drawing, it is *identity*. A canvas component holds
state the tree knows nothing about: the caret, the selection, an in-progress
drag, an open menu. If every render built fresh components, a keystroke
would reset all of it. `canvas/xango` should follow `domrt` and `xangogio`:

- **Match by position and `Key`**, reusing the component when the tag
  matches, so `Update` runs on the existing object.
- **Props describe the document, not the interaction.** `text`, `source`,
  `pages`, `anchor`, column widths are props; caret, selection, drag, and
  popups are not, and `Update` leaves them alone.
- **Don't clobber the user mid-edit.** `Update` applies a prop only if it
  differs from the value the component last reported through its callback
  (`onChange`), so the echo of your own edit coming back down as a prop is a
  no-op rather than a reset.
- **Controlled by default, like the DOM components:** edits go out through
  `onChange`, the app updates its state, and the next render passes it back.

### Layout

The canvas has no CSS. `ScrollView`, `PageSet` and `TextFlow` already lay
their own content out, and everything else takes an absolute position or a
page `Anchor`, so a `Stack`/`Row`-style container is optional; add one only
when a real screen needs it.

## 4. Mixing DOM and canvas

A page will usually want both: DOM controls (`FontSelector`, `StyleEditor`,
a key bindings editor, a menu bar) around a canvas document. With `canvas/xango`
the canvas is just another element in the DOM tree:

```xango
<div class="editor">
	<components.StyleEditor format={doc.Format} onChange={doc.SetFormat} .../>
	<CanvasView width={920} height={640}>{ Editor(doc) }</CanvasView>
</div>
```

`CanvasView` renders a `<canvas>` (plus the hidden input the canvas needs for
the keyboard) and mounts its child tree with `canvas/xango`. Both trees re-render
from the same state, so choosing a font in the DOM selector re-renders the
canvas, and typing on the canvas updates the DOM controls.

## 5. Suggested order

1. **Done:** generic component tags and render props (parts 1), with tests
   and a demo.
2. Add `xango.Host` and the canvas elements' constructors and `...Props`
   structs (mechanical: each already has most of its settings as exported
   fields).
3. `canvas/xango.Mount(canvas, hiddenInput, node)` with the adapter registry and
   the identity rules above; start with `Stage`, `ScrollView`, `PageSet`,
   `TextFlow`.
4. `CanvasView` for DOM pages; port `examples/canvas-document-demo` to a
   `.xg` tree as the acceptance test (its e2e test already pins the
   behaviour).
5. Render-prop members (`TableView.Cell`, `PageSet.Header`).

## Open questions

- **Text children.** Should `<canvas.TextFlow>text</canvas.TextFlow>` take its
  text from children (like HTML) or only from `source`? Children read well
  for short text; `source` is better for Markdown.
- **Fine-grained updates.** Canvas redraws are cheap compared with DOM
  patches, so `Update` can simply set fields and ask for a redraw. If a
  component ever needs a real diff (very large tables), `Update` is the place
  for it.
- **Server rendering.** A canvas has no server-side HTML. If a page must
  render without wasm, `CanvasView` could emit the text content as a visually
  hidden fallback (useful for accessibility anyway: a canvas is opaque to
  screen readers).
