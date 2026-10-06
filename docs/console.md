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
`-n` is a dry run, `-y` answers every question yes.

## Commands

| command | does |
|---|---|
| `ls [-l] [pattern...]`, `cd path`, `pwd` | look around; `..`, `.` and `/` work |
| `mkdir path...`, `rmdir path...` | make a folder; remove an empty one |
| `mv source... dest[/]` | move into a folder (a destination ending in `/`, or an existing folder), or rename one thing |
| `cp source dest[/]` | copy a document, into a folder if `dest` ends in `/` |
| `tag [+tag\|-tag]... pattern...` | add and remove tags |
| `rm pattern...` | to the trash |
| `restore pattern...`, `trash` | out of the trash; list it |
| `purge pattern...` | delete from the trash for good (asks) |
| `import [-d dir] file...` | bring in files: `.docx`, `.pdf`, `.md`, `.txt`, `.open`, JSON... |
| `export [-f format] [-o dir] pattern...` | write documents as files in a format (`docx`, `pdf`, `markdown`, `open`, ...) |
| `pack out.opendoc [folder]`, `unpack file.opendoc [dir]` | one [`.opendoc` pack](opendoc-format.md) of a folder or of everything; bring one in |
| `check`, `repair`, `rebuild`, `usage` | verify every document, compare catalog and files, rebuild the catalog (asks), how full the store is |

A **pattern** is a name with `*` and `?`, in any case, optionally under a folder path
(`Work/Reports/Q*`). Words with spaces are quoted (`'Report *'`) or escaped. A pattern that
matches nothing is an error, not a silent no-op. An exact name of a document and a folder
that are called the same matches both.

## Safe by default

1. **Dry run.** With `-n` every command that would change something prints `would ...` and
   changes nothing; `check` and `repair` and `rebuild` too.
2. **Questions.** `purge` and `rebuild` ask `[y/N]` and do nothing on anything but `y` (or
   `-y`). `rm` is not destructive: the trash keeps what it takes.
3. **Nothing partial by surprise.** A command stops at the first error and says which step
   failed; what ran before it is printed.
4. `usage` warns when the store is over 80% full and says to `pack` first.

## What is left

The same engine can drive a panel in the editor, and the [terminal demo](roadmap.md) reuses the
grammar; neither exists yet. There is no `find` by content, no regular expressions in patterns,
and `mv` of many things to many names is not a command (rename one at a time, or script it).
