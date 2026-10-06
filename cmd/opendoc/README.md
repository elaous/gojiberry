# opendoc

A command line tool for `.opendoc` packs: many documents and their folders in one file, each
with a SHA-256, with an index at the end so a pack opens without being read through. The format
is described in [docs/opendoc-format.md](../../docs/opendoc-format.md); the code is
[docfile/pack](../../docfile/pack) (standard library and `docfile` only, so it can be lifted out).

```
opendoc pack    [-sign key] [-name N] out.opendoc  file-or-folder...
opendoc convert [-sign key] [-name N] out.opendoc  file-or-folder...     (the same command)
opendoc unpack  pack.opendoc  dir        each document as a .open file, folders as directories
opendoc list    pack.opendoc
opendoc verify  pack.opendoc             reads every document; exits 1 if anything is wrong
opendoc sign    -key key pack.opendoc    signs, or signs again, in place
opendoc recover pack.opendoc out.opendoc what is intact in a cut or damaged pack
opendoc keygen                            prints a new Ed25519 seed (64 hex characters)
```

A key file holds the seed as 64 hex characters. `opendoc keygen > my.key` makes one; keep it
private. `verify` and `list` print the public key a signed pack carries; whether to trust that
key is for you to decide.

## What goes in

`pack` and `convert` read every file under the paths given and turn each into a document:

| file | read as |
|---|---|
| `.docx` | Word (see below) |
| `.pdf` | the text of the pages |
| `.md`, `.markdown`, `.txt` | Markdown, plain text |
| `.open`, `.pb`, `.pbtxt`, `.json` | opendocs' own formats |

A file in none of these is skipped and listed on standard error. Subfolders become folders of
the pack; a document's name is its file name without the extension.

## What a `.docx` carries over

Carried over: paragraphs with headings, quotes, code and bullet or numbered lists (with their
nesting), alignment; bold, italic, underline, strike-through, super and subscript, colour,
highlight, size and font; hyperlinks, tabs, line breaks and page breaks; tables (the text of
each cell); embedded pictures (as data URLs, with their size); footnotes.

**Not carried over:** comments, tracked changes (the text is read as it stands), headers and
footers, text boxes, shapes, charts, equations, fields such as page numbers and a table of
contents (their last text is kept as plain text), and the styles' own formatting (only their
names and their list and heading roles). Cell formatting, merged cells and column widths of
tables are not read. A `.pdf` gives its text only, with no layout, images or fonts.

Check a converted pack against the original before relying on it: `opendoc unpack` writes
each document as a `.open` file you can open in an opendocs editor.

## Damage

`verify` reports a document whose bytes do not match, records in the file that the index does
not list (an append that did not finish, or documents removed since the pack was last
rewritten) and a signature that does not check out. A pack cut short or with a broken index
does not open; `recover` walks the records from the start and writes a new pack of the
documents that are whole (their folder names are lost: those live only in the index).
