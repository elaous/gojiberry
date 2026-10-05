# Roadmap

What is planned beyond the [opendocs port plan](opendocs-port-plan.md) and the
[known limits plan](known-limits-plan.md). Ordered by what is needed next.

## Next

1. **Migrate: done** (see [document-components.md](document-components.md)); left: skipping what an earlier run already brought (needs the source ID kept on the document), Google Docs comments and revision history.
2. **Button icons** from opendocs into `components` menus and toolbars.
3. **Fresh review** of the grown PR before it merges.

## Later

1. **Download submenu.** "Download as" with a submenu: Docx, the other export
   formats, and *Open*, opendocs' own `.open` file (a document with its history and
   comments, for moving between opendocs installations). `docfile.Open` exists
   (protobuf, `.open`); what is left is the submenu and checking that versions and
   comments are fields of the document message.
2. **`.opendoc` packs.** Many documents and their folders in one file: framed
   protobuf records (the `.open` message), a footer index, a SHA-256 per document and
   an optional signature; opens from the end of the file, appends in place, recovers
   from a cut file. Spec: [opendoc-format.md](opendoc-format.md). Then `docfile/pack`,
   `Saver.ImportPack` / `ExportPack`, and `cmd/opendoc` with a `.docx` converter, to
   be offered as open source.
3. **Console mode.** A command line for work that is clumsy with a mouse: bulk
   rename, move and tag by pattern, import and export of a folder, repair and
   rebuild of a catalog. The operations already exist as `saver.Saver` methods; the
   work is a command grammar (`mv 'Report *' Archive/`, `tag +draft *.docx`), a
   dry-run mode that says what would change, and the confirm step for destructive
   ones. A `cmd/` CLI comes first; the same engine can then drive a panel in the
   editor.
4. **Terminal app demo.** A text-mode demo of the same engine (a file manager and a
   plain-text editor over `saver.Session`) to show the components are not tied to
   the browser. It reuses the console grammar above.
5. **Store backends with conditional saves.** SQLite, local files, memory and the
   HTTP store now save the catalog conditionally; PostgreSQL, MongoDB and S3 (which
   has `If-Match` writes) still write unconditionally and keep the old limit that two
   processes can lose a catalog update.
6. The rest of the [known limits plan](known-limits-plan.md): i18n breadth,
   accessibility depth, right-to-left, Hunspell dictionaries on demand.
