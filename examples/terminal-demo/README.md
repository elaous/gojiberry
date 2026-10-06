# terminal-demo

The document components are not tied to the browser. This example drives the same
`saver.Session` that stands behind `components.DocList`, `Saver` and `Capacity`, and draws what
it holds as text: the folder path, the folders and documents with their tags, sizes and times,
how full the store is, and the status line. The file work uses the [console](../../docs/console.md)
grammar; a small line editor writes the text of a document.

```
go run ./examples/terminal-demo                 # in memory
go run ./examples/terminal-demo -store files:./docs
```

```
== /Lists/ ==
 * Shopping list                 61 bytes  2026-10-06 14:02  [home]
-- 61 bytes of 1 MiB (1,048,576 bytes) used (0%)
-- Shopping list saved
>
```

| command | does |
|---|---|
| `new NAME` | a new document, in the editor |
| `open NAME` | open a document of this folder |
| `edit` | back to the editor |
| `save [NAME]` | save (or save under a name) |
| `ls`, `cd`, `mkdir`, `mv`, `cp`, `tag`, `rm`, `restore`, `purge`, `pack`, `unpack`, `check`, `usage`, `help` | the console's commands; the list follows `cd` |
| `quit` | leave (`quit!` throws away unsaved text) |

In the editor a bare line is appended, and: `:a TEXT`, `:i N TEXT` (insert before line N),
`:r N TEXT`, `:d N`, `:w` (save), `:q` (back; refused while there are unsaved changes), `:q!`
(back, discarding them).

It reads lines and writes screens, so it needs no raw terminal mode and no dependencies, and
it is tested by scripting a session (`main_test.go`).
