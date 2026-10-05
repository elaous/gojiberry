# Roadmap

What is planned beyond the [opendocs port plan](opendocs-port-plan.md) and the
[known limits plan](known-limits-plan.md). Ordered by what is needed next.

## Next

1. **Migrate** (Google Docs, Microsoft Word): a `migrate` package with a `Source`
   interface (list, fetch), bulk `.docx` import into the current folder, a Google
   Drive client (export as DOCX) and a Microsoft Graph client, tested against fake
   servers. Signing in is the host's job.
2. **Button icons** from opendocs into `components` menus and toolbars.
3. **Fresh review** of the grown PR before it merges.

## Later

1. **Download submenu.** "Download as" with a submenu: Docx, the other export
   formats, and *Open*, opendocs' own `.open` file (a document with its history and
   comments, for moving between opendocs installations). Needs the `.open`
   container written down first (what it holds, how it is versioned).
2. **Console mode.** A command line for work that is clumsy with a mouse: bulk
   rename, move and tag by pattern, import and export of a folder, repair and
   rebuild of a catalog. The operations already exist as `saver.Saver` methods; the
   work is a command grammar (`mv 'Report *' Archive/`, `tag +draft *.docx`), a
   dry-run mode that says what would change, and the confirm step for destructive
   ones. A `cmd/` CLI comes first; the same engine can then drive a panel in the
   editor.
3. **Terminal app demo.** A text-mode demo of the same engine (a file manager and a
   plain-text editor over `saver.Session`) to show the components are not tied to
   the browser. It reuses the console grammar above.
4. **Store backends with conditional saves.** SQLite, local files, memory and the
   HTTP store now save the catalog conditionally; PostgreSQL, MongoDB and S3 (which
   has `If-Match` writes) still write unconditionally and keep the old limit that two
   processes can lose a catalog update.
5. The rest of the [known limits plan](known-limits-plan.md): i18n breadth,
   accessibility depth, right-to-left, Hunspell dictionaries on demand.
