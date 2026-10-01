# Canvas features: to do

What is **not** built yet, in the words of the requests, grouped by area.
"(non canvas)" means a DOM component (like `components.FontSelector`), not
something drawn on the canvas. Done work is in `canvas/doc.go` and the
examples; the gox integration is in [gox-canvas.md](gox-canvas.md).

## Text formatting

- More character formats: small caps, title caps, lower case, upper case,
  and the rest of what Word and extended Markdown accept.
- **Styles** (named character and paragraph styles) and, with them, the
  style editor driving the canvas.
- Paragraph formatting:
  - line spacing
  - space before and after a paragraph
  - indent left and right
  - first-line indent (first line of a paragraph only)
  - decorated or large initials (drop caps)
- Lists:
  - bullet points
  - check boxes
  - ordered lists
  - editing the numbering character and the bullet character
- A horizontal line element (like Markdown `---`).
- **Links** (URLs): the hover text shows the target.
  - Link preview (non canvas).
- Formula editor / formula text element.
- Insert symbols (non canvas picker).

## Editing

Done, as registered features (see "Features" below): multi-cursor
(Ctrl/Cmd+click, Ctrl/Cmd+D, select all matches, cursors at line ends,
add cursor above/below), move/duplicate/delete line, copy as Markdown and
paste Markdown.

- Not yet: multi-cursor for the main `TextFlow` body (it is not editable
  yet; `FloatingText` is), auto-closing and completion at several cursors,
  undo/redo.
- Markdown copy writes headings, bold, italic, strike, underline, highlight,
  super/subscript and code; links keep their text only (a character has no
  URL yet), and tables and images are not written.

## Pages

- **Watermark**: like headers and footers it is per page, inheriting the
  previous page's or setting a new one, and it can be **none**.
- **None** for page numbers, headers and footers from the UI: remove from
  one page only, from this page forward, or from all pages. (The model
  already has "empty header/footer" and "page number off"; `goxcanvas`
  exposes `None`. What is missing is the choice in the canvas menus.)
- Background image with opacity.
- Spread view mode for image placement (two facing pages).
- Opacity for `Image` (it has `Alpha`; add the edit-mode control) and
  `FloatingText`.

## Non-canvas components

- Nice, lightweight, clean **menus and sub-menus** (DOM).
- Key bindings configuration editor (`canvas.Keymap` is the model).
- Document view like Google Docs (toolbar, ruler, side panels around the
  canvas).

## Features

Each feature is a `canvas.Feature` registered with the Stage's
`canvas.Registry` (`MultiCursor`, `LineOps`, `MarkdownClipboard` are the
defaults). The rest of this list is built the same way: a type with
`Name()` and `Install(*Registry)`, commands that work on an `EditTarget`,
default chords, and unit tests against `FloatingText`. Next candidates:
lists (bullets, check boxes, numbering), indents and paragraph spacing,
links, symbols, formula, horizontal rule, watermark.

## gox integration (see gox-canvas.md)

- Done: `gox.Host`, the `goxcanvas` renderer, `Canvas`, `TextFlow`
  (with the `Pages` render prop), `Header`, `Footer`, `PageNumber`.
- Next elements: `ScrollView`, `Image`, `FloatingText`, `TableView`
  (with a cell render prop), `PageSet` as its own element.
- `CanvasView`, a DOM component that mounts a `goxcanvas` tree.
- Fragments inside `Canvas` children.
