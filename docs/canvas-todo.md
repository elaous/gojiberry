# Canvas features: status and to do

What is built, in one place, and what is not. "(non canvas)" means a DOM
component (like `components.FontSelector`), not something drawn on the
canvas. The gox integration is described in [gox-canvas.md](gox-canvas.md).

## Built

**Text and characters** -- the `TextFlow` is editable (typing, selection,
clipboard, Markdown tables and images in the content, **undo/redo**
(Ctrl/Cmd+Z, Ctrl/Cmd+Shift+Z or Ctrl+Y; typing and deleting runs are one step));
formats: bold, italic, underline, strike, super/subscript, color,
highlight, font family/size/scale, **case** (UPPER, lower, Title, small
caps; Ctrl/Cmd+Alt+U/L/T/M), **links** (`Format.Link`: link style, a
URL tip on hover, click or Ctrl+click to open, Ctrl/Cmd+K to edit),
format painter, Markdown in and out (`MarkdownFromItems`,
`MarkdownClipboard`).

**Paragraphs** (`ParaFormat`, `Paragraphs` feature) -- alignment, left and
right indent, first-line indent, space before and after, line spacing,
bullets, numbered lists (1. 1) (1) a. i. I. A., restart at N, nesting),
check boxes you can tick, block quotes, drop caps; Enter continues a list
and ends it on an empty item, Tab nests; Markdown lists and quotes become
paragraph formats.

**Styles** (`Styles` feature, `TextFlow.Styles`) -- named character and
paragraph styles (Normal, Title, Subtitle, Heading 1-3, Quote, Code,
Emphasis, Strong, and your own: `define-paragraph`/`define-character` from
the selection); a style expands to a format at layout time, so editing it
restyles every use; Ctrl/Cmd+Alt+0-3, Ctrl/Cmd+\\ clears formatting;
`components.StylePicker`.

**Typing aids** -- auto-closing pairs (`TextFlow.AutoClose`, at every cursor: wrap a
selection, step over a closer) and a word-completion popup (`Complete`,
`Completer`, `Vocabulary`; Up/Down, Tab/Enter, Escape), as in `FloatingText`.

**Scrolling** -- a `ScrollView` keeps the caret in view while you type, move or paste (`RevealCaret`).

**Editing features** (registered with `Stage.Features`) -- multi-cursor,
move/duplicate/delete line, Markdown copy and paste, page/column breaks and
horizontal rules, paragraphs, text case, links, page furniture.

**Pages** -- headers, footers, page numbers, **watermarks** (text or
picture), **background pictures** (cover/contain/stretch, opacity) that
inherit page to page; **"none" from one page, from this page on, or from
all pages** (menus on the header/footer badge and the page-number menu,
and commands); **two-page and book spreads**; opacity for images and
floating text (the Fade button).

**Non-canvas** -- `components.Menu`/`MenuBar` (lightweight, CSS-only
submenus), `CommandMenu` (a menu from the registered commands),
`SymbolPicker`, `LinkPreview`, `KeyBindings` (a rebinding editor over
`Stage.Rebind`), `DocumentView` (the window around the canvas),
`FontSelector`, `StylePicker`.

**Roles and comments** -- `canvas.Role` (owner, editor, viewer; the zero value is
owner): a viewer's components are held in view mode (`Stage.SetRole`, the gox
`Canvas` `role` prop), only owners and editors comment. `docstore.ACL.RoleOf`
gives the role name. **Comments** are notes on text: `Format.Comment` anchors a
`canvas.CommentSet` entry (author, Markdown body, `@username` and e-mail
mentions, replies, resolved) so it follows the text through edits; hovering the
text shows a tip with the author and plain summary; Ctrl/Cmd+Alt+N adds one
(Ctrl/Cmd+Alt+Shift+N edits, `comment.resolve`/`comment.delete`); `docfile`
saves them; `components.CommentList`/`CommentCard`/`MarkdownText` show them
(Markdown with highlighted mentions).

**Saving** ([saving.md](saving.md)) -- `docfile` (JSON, protobuf text and
binary), `saver` with Postgres, SQLite, S3, local, memory, HTTP and browser
targets, `components.Saver`; `transfer` importers/exporters (the seam, with
Markdown and text) and `components.Transfer`; **lazy fonts**: a font picked
in the selector is downloaded into the store the first time the text uses it
(`fonts.Library`, `Stage.OnFontUse`).

**gox** -- `gox.Host`, the `canvas/gox` renderer and its element registry
(`Canvas`, `TextFlow` with the `Pages` template, `Header`, `Footer`,
`PageNumber`, `Watermark`, `Background`, `Image`, `FloatingText`, `Table`,
`ScrollView`); CSS imports (`import _ "<module path>/x.css"`).

`examples/canvas-editor-showcase` puts all of it in one window.

## To do

Asked for, not started:

- **Index** (table of contents and an alphabetical index with entries marked in
  the text), **footnotes**, **outline** (the headings as a navigable tree),
  **bookmarks** (named positions and links to them).
- **Map object** (OpenStreetMap tiles) and **flowcharts** as canvas objects.
- **Multi-user editing**: the `crdt` and `syncserver` packages exist; the
  `TextFlow` is not wired to them yet. Roles are the permission half.
- Comments: a viewer cannot comment (comments need edit mode); an author list for
  `@username` completion; notifications for mentions.

- **Formula** editor and formula text element.
- `TextFlow` multi-cursor keeps one `Cursor.After` per cursor; typing at a
  break offset goes in front of the break unless the caret is "after".
- Markdown: links round-trip, but tables and images are not written back;
  case and small caps have no Markdown form.
- Importers/exporters beyond Markdown and text (Word, PDF, HTML); saving pages, floating objects and images.
- Table cell render prop for the gox `Table`, `PageSet` as its own gox
  element, `CanvasView` (a DOM component that mounts a `canvas/gox` tree).
- Spread view: images can run across the gutter by anchor, but there is
  no gutter shadow or page-turn UI.
