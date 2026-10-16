# Console mode

A command line for the work that is clumsy with a mouse. The engine is the `console` package
(it drives a `saver.Saver`, so it works on any store the saver does); `cmd/docsh` is the tool.

```
docsh -store files:./docs ls -l 'Report *'
docsh -store sqlite:docs.db -n mv 'Report *' Archive/        # dry run: says what it would do
docsh -store http://localhost:8090 -token T                  # an interactive session
docsh -store files:./docs -f script.txt                      # a script, one command per line
```

A store is `memory`, `files:DIR`, `sqlite:FILE` or the URL of a docstore server (with `-token`).
`-n` is a dry run, `-y` answers every question yes. A script (`-f`) **stops at the first command
that fails**, prints the line, and docsh exits 1; an interactive session prints the error and
carries on with the next line. In a script a question that is not answered yes (no `-y`) is
such a failure too: `purge` and `rebuild` return an error ("not confirmed") instead of being
left alone, so the script stops and docsh exits 1. `unpack` prints the pack's warnings and
whether it is signed and the signature checks out, and fails (exit 1 in a script) if any
document failed; `import` of a file it cannot read as a document fails in a script (an
interactive session prints "skipped" and carries on).

## Commands

| command | does |
|---|---|
| `ls [-l] [pattern...]`, `cd path`, `pwd` | look around; `..`, `.` and `/` work |
| `mkdir path...`, `rmdir path...` | make a folder; remove an empty one |
| `mv source... dest[/]` | move into a folder (a destination ending in `/`, or any folder it names, `..`, `.` and `/` included), or rename one thing (`.` and `..` are not names) |
| `cp source dest[/]` | copy a document, into a folder if `dest` ends in `/` |
| `tag [+tag\|-tag]... pattern...` | add and remove tags |
| `rm pattern...` | to the trash (documents only: a folder that matches is skipped with a note) |
| `restore pattern...`, `trash` | out of the trash; list it |
| `purge pattern...` | delete from the trash for good (asks) |
| `import [-d dir] file...` | bring in files: `.docx`, `.pdf`, `.md`, `.txt`, `.open`, JSON... |
| `export [-f format] [-o dir] [--force] pattern...` | write documents as files in a format (`docx`, `pdf`, `markdown`, `open`, ...); two documents with the same title get a short ID suffix; an existing file is replaced only with `--force` |
| `pack [--force] out.opendoc [folder]`, `unpack file.opendoc [dir]` | one [`.opendoc` pack](opendoc-format.md) of a folder or of everything; bring one in |
| `check`, `repair`, `rebuild`, `usage` | verify every document, compare catalog and files, rebuild the catalog (asks), how full the store is |

A **pattern** is a name with `*` (any run of characters) and `?` (any one character), in any
case, optionally under a folder path (`Work/Reports/Q*`). Only `*` and `?` are special: `[`, `]`
and `\` in a pattern match themselves. Words with spaces are quoted (`'Report *'`) or the space
is escaped (`Report\ *`); a backslash escapes only a space, a quote or another backslash and
stays as typed before anything else (`C:\docs\a.md`). Patterns that overlap name a document
once (`purge a 'a*'` counts it once). A pattern that matches nothing is an error, not a silent
no-op. An exact name of a document and a folder
that are called the same matches both.

## In the editor

The showcase has a **Console** tab (File menu, "Console…") that runs these commands on the documents the Save tab
shows: `console.Session` is its state and `components.ConsolePanel` its panel, like `migrate.Session` and `components.Migrate`.
It keeps a log (a live region, so a screen reader announces each answer), a line to type on (Enter runs it, the Up and
Down keys walk the commands already run, `clear` empties the log), and the folder you `cd` into as the prompt. Commands
run one at a time in the background; `purge` and `rebuild` ask in the panel with Yes and No buttons instead of `[y/N]`.
A page has no disk, so files go through `console.PageFiles`: "Add files to import…" fills an inbox that `import` and
`unpack` read by name (patterns work: `import *.md`), and what `export` and `pack` make is downloaded (a second file of
the same name needs `--force`, as on a disk). The document list refreshes after each command. The editor's file formats
that need the file worker (Word, PDF, OpenDocument) are used through it, as in the Files tab. The same `Shell` runs
under `docsh`; `Shell.Files` and `Shell.Confirm` are the two hooks a page needs. The
[terminal demo](../examples/terminal-demo/README.md) reuses the grammar.

## Safe by default

1. **Dry run.** With `-n` every command that would change something prints `would ...` and
   changes nothing; `check` and `repair` and `rebuild` too.
2. **Questions.** `purge` (which lists up to ten names) and `rebuild` ask `[y/N]` and do
   nothing on anything but `y` (or `-y`). In a script the next line is a command, never an
   answer: without `-y` the question is answered no, the output says so, and the command fails. `rm` is not destructive: the trash keeps what it takes.
3. **Nothing partial by surprise.** A command stops at the first error and says which step
   failed; what ran before it is printed. A script stops there too (see above). `pack` and
   `export` write through a temporary file, so a failure leaves no partial output, and they do
   not replace a file without `--force`. A dry run checks what is cheap to check (a folder
   that is not empty, a missing parent folder, a file that exists) and fails the same way.
4. `usage` warns when the store is over 80% full and says to `pack` first.

## What is left

Nothing planned. There is no `find` by content, no regular expressions in patterns,
and `mv` of many things to many names is not a command (rename one at a time, or script it).
