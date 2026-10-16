# Canvas features: status and to do

What is built, in one place, and what is not. "(non canvas)" means a DOM
component (like `components.FontSelector`), not something drawn on the
canvas. The xango integration is described in [xango-canvas.md](xango-canvas.md).

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

**Outline and bookmarks** -- `TextFlow.Outline()` lists the headings (Heading 1-3 and
Title styles, or Markdown-style big bold text); `components.Outline` shows
them, the current one marked, and a click goes there (`GotoOffset`, with the
`ScrollView` bringing it to the top). **Bookmarks** name a stretch of text
(`Format.Bookmark`, so they follow edits): Ctrl/Cmd+Alt+B adds one,
Ctrl/Cmd+Alt+G goes to one, `bookmark.remove` removes it, and a link to
`#name` jumps to it; `components.BookmarkList` lists them.

**Footnotes, table of contents, index** -- Ctrl/Cmd+Alt+F adds a footnote at the
caret: a mark in the text (an inline object, numbered in text order) and the
note's text drawn under a short rule in a band kept at the bottom of every
page (`FootnoteBand`, 64px by default, only while there are notes; a note too
long for the band is cut with an ellipsis). `toc.insert` puts a table of
contents (the outline with page numbers) in the text; Ctrl/Cmd+Alt+X marks
the selection as an index entry (`Format.Index`) and `index.insert` puts the
alphabetical index (terms, with their pages) in; both blocks keep themselves
current, with page numbers settling one redraw after a change. `docfile` saves
all of it; `components.FootnoteList` lists the notes.

**Roles and comments** -- `canvas.Role` (owner, editor, viewer; the zero value is
owner): a viewer's components are held in view mode (`Stage.SetRole`, the xango
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

**xango** -- `xango.Host`, the `canvas/xango` renderer and its element registry
(`Canvas`, `TextFlow` with the `Pages` template, `Header`, `Footer`,
`PageNumber`, `Watermark`, `Background`, `Image`, `FloatingText`, `Table`,
`ScrollView`); CSS imports (`import _ "<module path>/x.css"`).

`examples/canvas-editor-showcase` puts all of it in one window.

## Done since this list was written

- **Typewriter scrolling**: `ScrollView.Typewriter` and `TypewriterAt` keep the line being typed at one height of the view (the middle by default) and move
  the page instead; typing, keys and commands only, never a click. A preference in the showcase (Preferences > Editor), off by default.

- **Table styles**: named looks for a whole table (`TableSchemes`, `Table.ApplyScheme`, `Table.SchemeOf`) from the table's handle menu, applied as cell
  formatting so they are saved and exported with the table.

- **Table cell render prop and `CanvasView`**: `TableView.RenderCell` / xango `Table` `RenderCell` draw the content of a cell (a bar, an icon);
  `canvas/xango.View` (`NewView`, `Render`, `Find`) puts a canvas with a canvas/xango tree inside an ordinary page, with a visually hidden text
  copy beside it for screen readers and for a page read without the canvas (`examples/canvas-view-demo`).

- **Footnotes**: a page gives its notes the room they need (up to 40 % of the page, `TextFlow.FootnoteBand` to change it) and the text
  above gives way, so a mark and the start of its note stay on one page; a note that does not fit goes on at the foot of the next page;
  nothing is cut with an ellipsis. A note is at most `MaxFootnoteRunes` (1000) characters; the dialog it is typed in
  (`components.NoteEditor`, `Stage.EditNote`) shows a read-only counter only while the note is being typed. Pressing a note's text on the
  page edits it (`TextFlow.OnFootnote`). A click on a row of the index goes to the term's first mark, as one on the table of contents does.
- **Columns**: `Reset column widths` (Edit > Columns) gives the columns of a region equal widths again after a grip was dragged.
- **Outline**: sections fold (`components.Outline` `Collapsed`/`OnToggle`) and move (`TextFlow.SectionRange`, `TextFlow.MoveSection`; move
  buttons and drag and drop in the panel, `components.OutlineUp`/`OutlineDown` for the keyboard path); bookmarks are marked on the
  canvas while editing (`BookmarkMarkColor`, never exported).

- **Multi-cursor at a break**: two cursors at one offset of a page or column break keep their own side of it (typing
  goes in front of the break at one and behind it at the other).
- **Markdown write-back**: tables (pipe tables), rules and pictures that know their address are written by
  `MarkdownFromItems` and by the Markdown export.

These were "asked for, not started" and are built. The current state of the editor is the
[roadmap](roadmap.md) and [status.md](status.md); this page lists the canvas core.

- **Multi-cursor**: select all occurrences (`Mod+Shift+L`), add a cursor above or below (`Mod+Alt+Up`, `Mod+Alt+Down`),
  cursors at line ends (`Alt+Shift+I`), next match (`Mod+D`): `canvas/feature_multicursor.go`.
- **Multi-user editing**: built, with per-user undo, remote carets, presence, comment-only viewers, roles and a hub that
  renews expiring documents ([multiuser-design.md](multiuser-design.md), roadmap).
- **Comments**: the author list for `@username` completion is `canvas.Roster` (the handle of a person, never an address
  that is not theirs to show), and mentions are replies in the comment list.
- **Formulas**: an equation item (`docfile.EquationItem`), read from Word's Office Math (`docximport`), drawn and
  exported (`docfile/equation.go`).
- **Importers and exporters** beyond Markdown and text: Word, PDF, HTML, RTF, EPUB and OpenDocument (`export`,
  `docximport`, `odtimport`, `htmlimport`, `pdftext`), with a fidelity report of what each drops; saving pages,
  floating objects and images.
- **`PageSet` as an xango element** (`TagPage` in `canvas/xango`).
- **Spread view**: images run across the gutter by anchor; `PageSet.Gutter` is the space between facing sheets; the inner edges
  of a facing pair are shaded toward the fold (`PageStyle.GutterShade`) and, with `PageSet.OnTurn` set, each outer bottom corner
  has a folded tab that asks the host to show the neighbouring pair (the showcase scrolls there).
- **Image legend** (`Image.Legend`): drawn under the picture, saved in `docfile`, written by the exports and Markdown; WebP is converted to PNG on insert.

## To do

Asked for, not started:

- **Map object** (OpenStreetMap tiles) and **flowcharts** as canvas objects.
- Comments: notifications for mentions.
- Markdown: case and small caps have no Markdown form (the format has no syntax for them; a limit of Markdown).
