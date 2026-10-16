# Features of the editor, with a picture of each

The showcase (`examples/canvas-editor-showcase`) is the editor with every feature switched on; this page shows each one in a picture and a few lines. The pictures are made by a test, so they can be made again when the editor changes: `make feature-shots`. How to try each feature by hand is in [feature-testing.md](feature-testing.md); what is done and what is missing is in [status.md](status.md).

## Editing on the canvas

![Editing on the canvas](features/img/editor-edit.webp)

Everything you see is drawn on a canvas by Go code, and it is a real editor: type, select, copy and paste, undo and redo (each typing run is one step), several cursors at once (Ctrl+D picks the next match, Ctrl+click adds a cursor), move or copy lines, page breaks, and Markdown pasted as formatted text. A screen reader gets the same text from a hidden copy beside the canvas.

## Format

![Format](features/img/tab-format.webp)

The first tab sets character formatting (bold, italic, underline, strike, super and subscript, colour, highlight, case, font and size) and paragraph formatting (alignment, indents, spacing, lists, check boxes, quotes). The buttons act on the selection, or on what you type next.

## Styles

![Styles](features/img/tab-styles.webp)

Named character and paragraph styles (Normal, Title, Heading 1 to 3, Quote, Code and your own) and a style editor. A style is expanded when the page is laid out, so changing it restyles every use; a theme editor with live contrast checks sets the colours of the document, and a theme can be shared as a file.

## Symbols

![Symbols](features/img/tab-symbols.webp)

A picker for special characters and symbols, searchable and operable by keyboard. A chosen symbol is typed at the caret.

## Keys

![Keys](features/img/tab-keys.webp)

Every command has a key, and every key can be changed here. The tab lists them, finds conflicts, remembers your choices between visits, and shows a cheat sheet.

## Comments

![Comments](features/img/tab-comments.webp)

Comments are notes anchored to text, with replies, resolved state and @mentions of people by handle (never an address that is not theirs to show). They follow the text when it is edited and travel with Word files.

## Review (suggestions)

![Review (suggestions)](features/img/tab-review.webp)

In suggestion mode your edits are proposals until someone accepts them: inserted text is marked, deleted text is struck through, and the list lets you accept or reject each. Two people proposing at once end up with the same document.

## Outline

![Outline](features/img/tab-outline.webp)

The headings of the document as a list: click one to go there, fold what is under a heading, and move a section (with its subsections) up or down with the buttons or by dragging. Bookmarks are listed too, and marked on the page while you edit.

## Footnotes

![Footnotes](features/img/footnotes.webp)

A footnote is typed in a small dialog with a counter of the characters left (limit 1000), and appears at the foot of the page under a short rule. The page gives its notes the room they need and the text above gives way; a note too long for the page goes on at the foot of the next one, and nothing is cut. Press a note on the page to edit it.

## Columns

![Columns](features/img/columns.webp)

Text can run in up to four columns from any paragraph on, with the space between them set (narrow, standard, wide). Drag the grip above a gap to change widths; **Reset column widths** (Edit > Columns) makes them equal again. A column break moves text to the next column.

## Table styles

![Table styles](features/img/table-styles.webp)

Tables have cell formatting (background, colour, bold, alignment, merged cells) and, from the table's handle menu, **named table styles**: plain, bold header, blue or green header with banded rows, grey banded, banded with a bold first column. A style is applied in one step as cell formatting, so it is saved with the table, written by the exports that write cell shading, and can still be changed cell by cell; the menu marks the style the table is dressed in now.

## Two-page spread

![Two-page spread](features/img/spread.webp)

The sheets can be shown two to a row as facing pages (View > Two-page spread). The inner edges are shaded toward the fold, a picture can run across the gutter, and a folded corner on each outer bottom corner turns to the next or previous pair.

## Page and print layout

![Page and print layout](features/img/tab-page.webp)

Paper size and orientation, margins, headers and footers, page numbers, a different first page, odd and even pages, a watermark and background, and a table of contents that links to the headings. The exports write the same layout.

## Pictures

![Pictures](features/img/tab-images.webp)

Insert a picture from a file; the tab asks you to describe it for people who cannot see it, or to say it is decorative, flags the ones with no description, and can draft a description on your device. Pictures can be inline or floating, wrapped, cropped and faded.

## Find and replace

![Find and replace](features/img/tab-find.webp)

Search the document with case and whole-word options, step through matches, replace one or all, and search across all your documents.

## Spelling

![Spelling](features/img/tab-spell.webp)

Misspelled words are underlined with suggestions, and your own words are kept. English is built in; other languages' Hunspell dictionaries load on demand from a dictionary server you choose.

## Language

![Language](features/img/tab-language.webp)

The document's language, the language of a selection, detection from the text, hyphenation, translation and grammar checks through servers you point it at (an Ollama-compatible server works for grammar). Right-to-left and mixed-direction text lays out correctly.

## Stats

![Stats](features/img/tab-stats.webp)

Words, characters, paragraphs, sentences, pages used and reading time, for the whole document and for the selection.

## Share

![Share](features/img/tab-share.webp)

Select text and get a link to it. The link carries the capability in its fragment, so the address you share is the permission you give.

## People

![People](features/img/tab-people.webp)

Who can see the document and as what: add or remove people, set a role (owner, editor, viewer, comment-only), set an expiry, and see who is online. With encryption on, keys are managed here too.

## Accessibility check

![Accessibility check](features/img/tab-check.webp)

Finds what a tool can: pictures with no description, empty headings, tables with no header row, paragraphs that only look like lists, low contrast. Each finding jumps to the place, many have a one-click fix, and export can be blocked while errors remain.

## Files: import and export

![Files: import and export](features/img/tab-transfer.webp)

Import Word, OpenDocument, HTML, PDF text and Markdown; export Word, OpenDocument, PDF (tagged), RTF, EPUB, HTML, Markdown and plain text. Before you export, the tab says what the chosen format leaves out of this document.

## Save

![Save](features/img/tab-save.webp)

Where the document is kept: this browser, a folder on your computer, a server, or an end-to-end encrypted store. Versions are kept and can be compared and restored; documents can be put in folders, tagged, trashed and brought back.

## Migrate

![Migrate](features/img/tab-migrate.webp)

Bring documents from other editors: Word files, a Google account (with comments and, if you ask, the revision history) and others. A second run leaves what an earlier run brought, unless you ask for it again.

## Console

![Console](features/img/tab-console.webp)

A command line over your documents (`ls`, `cat`, `mv`, `rm`, `find` by name, and more) that runs in the page, with the same rules as the editor: nothing is deleted without asking.

## Public link

![Public link](features/img/tab-public.webp)

Put a copy on a public server for 24 hours with no account. Every edit renews the 24 hours, reading does not, and after 24 idle hours it is deleted for good. There is an edit link and a read-only link, a countdown, and a way to report.

## Speech

![Speech](features/img/tab-speech.webp)

Dictate into the document through a recogniser you name, and, if you want, record a set of phrases to build a collection of your own speech. The microphone is on only while recording; recordings stay in the page unless you download them.

## Privacy

![Privacy](features/img/tab-privacy.webp)

What the page counts about its own use (nothing leaves the page unless you choose), crash codes as a separate opt-in, and what each choice does in plain words.

## Preferences

![Preferences](features/img/tab-prefs.webp)

Reduced motion, high contrast, text size, wide spacing, larger targets, distraction-free mode, language of the interface, plain-words mode, dwell click and single-switch scanning, and more, in one searchable list that can be exported as a file and follows you when you ask.

## Read aloud

![Read aloud](features/img/menu-read-aloud.webp)

Read the whole document or the selection aloud, a sentence at a time, with the sentence and the word marked as they are spoken. A bar offers previous, pause, next and stop; speed is slow, normal or fast; and the **Reader voice** menu lists the voices for the document's language with their accent and gender (American or British, female or male).

## Menus and commands

![Menus and commands](features/img/menu-columns.webp)

Every command is in a menu, has a key, and is the same command the keys and the toolbar call. Menus are operated with the arrow keys, and every entry can be reached without a mouse.
