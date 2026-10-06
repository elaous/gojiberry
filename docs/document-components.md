# Document components: plug-in pieces

Each part of a document's life is a component of its own, taking the operations it
offers as functions. Nothing in them knows about `saver`: a host plugs in a
`saver.Session`, a REST client, or a test double, and a control whose operation is
nil is not drawn.

| Component | Shows | Operations it takes |
|---|---|---|
| `DocList` | documents (name, format, size, time, tags), folders and a breadcrumb | `Open`, `Duplicate`, `Trash`, `Delete`, `Tag`, `Refresh`, `OnTagFilter`; folders: `OpenFolder`, `NewFolder`, `RenameFolder`, `DeleteFolder`, `Move` |
| `TagFilter` | All + one chip per tag | `OnSelect` |
| `Trash` | documents in the trash, with `Children` (usually the live list) above | `Restore`, `Delete`, optional `OnSelect` for batches |
| `Versions` | snapshots of one document | `Save`, `Open`, `Delete` |
| `People` | who can open the document (owner, editors, viewers), as `user.Person` | `Invite`, `SetRole`, `Remove` (over a `docstore.ACL`, or any backend) |
| `Saver` | the save form, composing the three above | `OnSave`, `OnSaveAsNew`, `OnTarget`, `OnFormat`, `OnName` |

```go
// <Trash restore={restore} delete={purge}> <DocList .../> </Trash>
components.Trash(components.TrashProps{
    Docs: gone, Restore: restore, Delete: purge, Pending: pending,
    Children: []xango.Node{components.DocList(components.DocListProps{Docs: live, Trash: trash, Open: open})},
})
```

## The interface

```go
type DocsAction func(ctx context.Context, ids []string) error   // Trash, Restore, Delete: by ID, in batches
type DocAction  func(ctx context.Context, id string) error      // Open, Duplicate, a version's key
```

An action may take a while. Components run it in the background through a
`*components.Pending`, the Go side of a Promise: it knows whether an operation is
running (the buttons disable) and how the last one ended (the error is shown under
the component, `role="alert"`). One `Pending` can serve every component on a page;
its `onChange` is where the host renders again. A nil `Pending` just runs the action.

A backend can also be given as an interface. `TrashProps.Use(backend)` fills
`Restore` and `Delete` from anything with
`Restore(ctx, ids ...string) error` and `Purge(ctx, ids ...string) error`
(`saver.Session` is one).

## Documents have UUIDs

A document is known by its ID, a UUID (v4) kept in the document itself
(`Document.ID()`, in its metadata) and never changed. Its name is a label: **two
documents may have the same name** and are still two documents. Every operation
takes an ID.

`saver` stores a document under `<id><ext>` and keeps a catalog per target
(`catalog:index`: ID -> name, format, tags, trash flag), so a list is one read and
two same-named documents never share a key. The trash is a flag in the catalog: a
trashed document stays where it is (nothing is copied or overwritten, restoring is
instant, its versions stay). Versions are `versions/<id>/<time><ext>`. The documents
carry the same facts, so a lost or damaged catalog is rebuilt from them
(`Saver.Rebuild`); a document from before IDs is listed under its key.

`Session.Save` replaces the saved document being edited (it knows its ID);
`SaveAsNew` makes another with a new ID; `Detach` makes the editor's content
unsaved (call it after an import).

## Asking before deleting for good

`components.Asker` puts a yes/no question (the `Confirm` dialog) in front of
anything that cannot be undone. `DocList` (Delete, Delete folder), `Trash` (Delete
forever, Empty trash) and `People` (Remove) take its `Ask` as their `Confirm` prop;
with none they act at once. Render `asker.Dialog()` once in the page (`Saver` does
it when given `Asker`; a `saver.Session` has one). Words come from `Translate`
(`confirm.*`, `docs.confirm*`).

## Folders

Folders are first-class like documents: a UUID, a name that need not be unique, a
parent. `saver` keeps them in the catalog (each also written as
`catalog:folder/<id>`, so a rebuild finds empty ones) and a document's folder in its
metadata and the catalog. `Saver.CreateFolder/RenameFolder/MoveFolder/DeleteFolder/
Move`; a folder is deleted only when it holds no document and no folder (trashed
documents in it go back to the top level); folders nest at most
`saver.MaxFolderDepth`. Saving a document again does not move it, `Move` does; a new
document is saved in the folder being shown (`Session.OpenFolder`), and opening a
document shows its folder.

## Link and Download

1. **Link.** `DocListProps.Link` / `FolderLink` give an item's address and
   `CopyLink` puts it on the clipboard; all three are needed for the Link button.
   `Session.Link(kind, id)` is how a host answers: the showcase uses
   `share.DocPath` (`/d/{id}`, a shared document) and `share.FolderPath`
   (`/f/{id}`; a page opened there shows that folder).
2. **Download.** `Download(ctx, id, format)` and `DownloadFolder(ctx, id)`.
   With `DownloadFormats` a row gets a "Download as…" menu, without it one
   button. `Session.Deliver` hands the file to the person (`transfer.Download` in
   a browser), `Session.Export` converts (`transfer.Registry.Export` fits); with
   no `Export` a document goes as saved. A choice with a `Group` is shown under that
   heading (an `<optgroup>`); the showcase groups Documents (Word, PDF, RTF, EPUB), Web
   and text, Data, and opendocs.
   Choosing `open` first asks (`OpenDownload`, `Session.DownloadOpen`): the dialog shows the
   size of the current version and of the whole history (the nearest KiB/MiB with the exact
   bytes, `components.SizeText`), a checkbox to drop the history, and what the file will
   weigh. The `open` format is special: `Saver.ExportOpen` writes a `.open` file that carries the
   document's versions (`Document.History`, oldest first), and saving such a document
   (`Saver.Save`, so also an import) stores the versions again at the times they were saved
   and keeps the newest `MaxVersions` (the oldest versions, imported or not, go first).
   Importing into an id the store already holds replaces that document: the file it
   replaces stays as its previous copy (`prev`), and the imported revisions are added to
   the versions it has. A document in the store never holds a history inside its own file.
3. **Packs.** A folder row has "As .opendoc" (`DownloadPack`, `Saver.ExportPack`: folders by ID,
   tags, the trash flag and each document's history in one `.opendoc` file) and the list has
   "Import .opendoc" (`ImportPack`, from `Session.Pick`; `Saver.ImportPack` skips documents
   already there (the UI import always uses Skip; Replace and Copy are for callers of
   `ImportPack`, and Replace takes the pack's folder, tags and trash flag too) and reports what it skipped, what failed and whether the pack was signed).
   A confidential (end-to-end) document goes into a pack only through `PackOptions.Seal`, as an
   encrypted record (placeholder name, no tags in the index), and comes back through
   `ImportOptions.Open`; without them the session's download leaves it out and says so, and an
   import skips it with a needs-key warning.
   Migrate's format menu has "One .opendoc pack": the documents are gathered into a pack and
   downloaded, and nothing is saved in the store.
4. **Folder download** is `Saver.Archive`: a ZIP with the folders drawn as
   directories (they are virtual in the store), documents as saved, the trash
   left out. A folder ZIP does not include version history; download a document as
   `.open` for that.

## Migrate

`migrate` moves documents in from other platforms; `components.Migrate` is its
panel and `migrate.Session` its state (like `saver.Session` for the document list).

1. **Sources.** `migrate.Source` lists (`List(folder)`) and fetches (`Fetch(item)`, a
   `.docx`). `GoogleDocs` uses the Drive API (Google Docs exported as Word),
   `OneDrive` uses Microsoft Graph (the `.docx` files); both take an access token and
   an optional `BaseURL` and `Client`, and are tested against fake servers. Getting
   the token (OAuth) is the host's job; the showcase has a box for it, and
   `?driveapi=` / `?graphapi=` change the addresses.
2. **Word files on this computer** (`migrate.Files`, from `transfer.PickFiles`) go
   through the same path.
3. **Run** walks the folders chosen (at most 2000 files, 16 deep), converts each file
   with `docximport`, and saves it through a `Sink`. `SaverSink` keeps the source's
   folders (reusing a folder of the same name), tags each document with its platform
   (`google-docs`) and saves under the folder open in the Save tab. A file that fails
   is listed in the report; the rest go on. A "Convert to" menu picks the format the
   documents are stored in: `.open` by default (`docfile.Open`, opendocs' own format:
   protobuf wire format, byte for byte what `docfile.ProtoBinary` writes, so the two
   cannot be told apart by content, only by extension), or any other format. Migrating twice makes the documents
   twice (they get new IDs).

## Icons

`components.Material(name)` draws a Material Symbols icon (the ones opendocs uses, by the
same names: `format_bold`, `delete`, `link`...). `MenuItem.Icon` puts one before a menu
entry, `CommandMaterial` maps the editing commands to theirs (the Edit menu uses it),
and the document list, migrate and sidebar-tab buttons show them next to their names.
It is a `<span>` with a CSS mask made from the icon's path data, because the DOM runtime
builds elements without the SVG namespace, so an inline `<svg>` would not draw. Only the
~85 icons in `material_data.go` are in the binary (no font); they are Apache-2.0, and
`sh components/material_update.sh` regenerates the file from the
`@material-symbols/svg-400` package when more are needed.

## One Person

`user.Person` (`ID`, `Name`, `Role`; `user.Normalize` for addresses) is shared by
`docstore.ACL.People()` and `components.People`. It also has `Email`, `Username` and
`Picture` (OpenID Connect claims). **Access is decided by `ID` and the verified `Email`
only** (`Person.AccessIDs`): a username is whatever the person chose at their provider,
so it is for finding and showing people and never grants access (otherwise a username
of "ana@x.com" would inherit Ana's documents).

## Shared stores

A store several people use (an HTTP docstore with access lists) should give each person
their own catalog: `saver.Target.CatalogKey = saver.UserCatalogKey(user.ID)` (the
`auth.User.ID` of the signed-in person). The default is one catalog for the store, which
is right for a store of one person and wrong for many. Folder records live next to the
catalog. **The server enforces it:** with `RequireACL`, a signed-in person may read and
write only the keys under `docstore.UserCatalogPrefix(their ID)`, the shared
`catalog:index` is closed, and a listing shows each person only their own catalog, so
access lists and catalogs work together. **This changes a setup that ran RequireACL
with the shared `catalog:index`: its saves now get 403. Give each person their own
`Target.CatalogKey` (above).** A server that lists only what the caller may
read also makes a new catalog start with just those documents.

## Damaged files in the trash

A document in the trash whose file is missing used to stop its folder from being deleted.
Deleting the folder now removes such documents (and their versions) and the status line
says which ("Old deleted (2 documents in the trash whose files were gone were removed: a,
b)"). A file that is there but does not parse is not destroyed (a newer version of the
library may read it): that document goes to the top level of the trash, untouched. Any
other store error still stops the operation.

## Two processes on one store

Catalog updates are conditional (`docstore.ConditionalAdapter`): memory, PostgreSQL (a transaction that
reads the row `FOR UPDATE`), MongoDB (a replace filtered on the bytes read), S3 and MinIO (`If-None-Match: *`
to create and `If-Match` on the ETag read to replace; needs a store with conditional writes), SQLite (a busy
timeout and an immediate write transaction), local files (an `flock` on the directory, on
Unix) and the HTTP store (`If-Match`). `Repair` waits `RepairGrace` (2 s) and looks again
before repairing, so it does not undo a delete another process is half way through, and
a rebuild never overwrites a catalog changed since it was read. The browser's
localStorage target writes conditionally too (`SaveIf` under a Web Lock shared by every tab
of the origin, and read back to be sure), so two tabs saving at once make the second start
over on the catalog the first wrote and keep both changes. Where the browser has no Web
Locks the read-back still catches most of it. `docstore.Locker` is the optional interface
for a store that offers a lock instead (Rebuild and Repair hold it).

## Damaged files and previous copies

Every saved file is a new *generation*: its key is the document's ID, a short random
suffix and the format extension, and the catalog entry records the key, a SHA-256 checksum
and the key and checksum of the previous copy. A save writes the new file first, then
swaps the catalog entry, then deletes the copy before last, so a crash between the two
steps leaves the old document intact and the new file an orphan for `Repair`.

`Saver.Load` checks the checksum and parses the file. If the current copy is missing or
damaged it opens the previous copy instead, and writes it back as the current one. A
store error (offline, permission) is never taken for damage. If no copy is good, `Load`
returns `saver.ErrDamaged`. `Saver.Verify` does this for every document and reports which
were restored, lost or sealed (a file with no checksum yet gets one); the Documents tab
has a Check button for it. `Saver.PreviousCopies` is 0 for the default of one previous
copy, or negative for none; more than one is not stored. Local files are written with
`fsync` of the file and its directory, so a power cut does not leave a half-written file.
Copy-on-write and checksums are in the data, so they work with every store.

A save that finds the store full (`docstore.ErrStorageFull`: a browser's quota, a full
disk) first gives up the previous copy of that document, and if that is not enough writes
over the current file, as before copies existed, so a save that fits is not refused. A
rebuild keeps the checksums of the catalog it replaces: when the newest file of a document
is not the one that catalog vouched for and the earlier save is, the earlier save is the
document again (the changed file is left out and removed with the other leftovers once it
is an hour old); with no earlier save the changed file stays flagged and is found damaged
on the next read, not sealed as good; only with no readable catalog are files taken as
they are. `Verify` also looks at the previous copies and forgets
one that is missing or damaged (the next save makes a new one), and removes leftover files
(`Saver.CollectOrphans`: generation-named files nothing points to, older than an hour,
what a crashed save leaves). A rename, tag or trash rewrites the current file but keeps
the earlier save as the previous copy (a document with only one save gets that file as its
previous copy). A rename or tag in a store with no room for a second copy of the file is
refused.

`docstore.RequestPersistence` and `Persisted` ask the browser not to evict the storage.

**A store that is filling up.** From 80% of a target's `Capacity` the Storage meter shows
what to do (`components.CapacityAdvice`: download your documents, delete old versions and
the trash, or save elsewhere; a stronger text from 95%), and the status line after a save
adds one sentence. A save the store refuses says "Not saved: the storage is full" and the
same advice, so the person knows to download the document first. The percentages are of
`Target.Capacity` (the showcase's browser target is 5 MiB), or of what the adapter reports.

**Limits that remain** (what the checks do not do):

1. Only one previous copy is kept; two bad files in a row (the current and the previous) are
   a lost document, which `Verify` and `Load` report and do not hide.
2. A checksum protects what the catalog has seen. A catalog lost completely is rebuilt from
   the files as they are, so damage that still parses is trusted from then.
3. `Verify` reads every file: on a remote store that is every document over the network.
4. Leftover files are removed after an hour, so a crash that is never followed by a
   `Verify` leaves them (they cost space, not data).
5. The cross-process lock of local files is `flock` (Unix); elsewhere the conditional write
   alone protects the catalog. A browser without Web Locks relies on the read-back, which
   catches most, not all, simultaneous saves from two tabs.
6. In a store with no room for a second copy of a file, a rename, tag or trash is refused
   (a save still goes through by overwriting in place, without the safety of a copy).
7. Memory and localStorage keep nothing across their own loss (clearing site data): the
   advice above, to download, is the protection.

## Encrypted stores

`docstore/encrypt` wraps any adapter so the store holds ciphertext only: each saved file is an
age file whose data key is wrapped for each recipient (the owner's SSH key, a recovery key, whoever
the document is shared with). The saver, folders, versions, `Verify` and packs work on it
unchanged, because they see an `Adapter`; the catalog stays readable, as plaintext metadata (names, tags, folders,
sizes), which is also why a confidential item's name needs the placeholder described in
[encryption-and-passkeys.md](encryption-and-passkeys.md) (not built yet). Server-side features
(search, spelling, translation, the collaboration hub) cannot read ciphertext.

```go
pub, _ := encrypt.ParseSSHRecipient(sshPublicKeyLine)   // label, type and fingerprint to confirm
id, _ := encrypt.ParseSSHIdentity(sshPrivateKeyPEM)     // unencrypted key; open a passphrase one first
store := encrypt.New(adapter, encrypt.Static{To: []age.Recipient{pub.Key}, Keys: []age.Identity{id}})
```

`New` is strict: a stored file that is not an age file for its own id is refused (so a store writer
cannot swap in plaintext or another document's ciphertext). To migrate a store that was clear, set
`store.AllowPlaintext = true`, run `store.RekeyAll(ctx)`, and turn it off again. Rolling a file back
to an older ciphertext of the same id is not detected by this layer.

## A folder on this computer

`docstore.FileSystemAdapter` (browser builds) saves documents as files in a folder the person
chose, through the File System Access API (Chrome, Edge, Opera: `FileSystemSupported`). The page
is served from any static host over HTTPS (or localhost); no server of ours is involved.

1. `PickFolder` opens the browser's picker (from a click) and `Remember` keeps the handle in
   IndexedDB, so `RestoreFolder` finds the same folder on the next visit.
2. **Permission is the browser's.** After a restart it usually has to be asked for again, with a
   click: `Permission` says `granted`, `prompt` or `denied`, and `Request` asks. The
   `components.FolderPicker` shows "Reconnect folder" in that state, and the folder is not a
   save target until then, so nothing is written to it by surprise. If permission is withdrawn
   in the middle of a session, a save fails with a message that says to reconnect.
3. Files are named as `LocalFSAdapter` names them (the ID, base64url), so the same folder can be
   opened by the Go tools (`docsh -store files:DIR`) and by the docstore server. A file is
   written through a writable stream that replaces the old one when closed, so a write is whole
   or absent; the catalog is saved conditionally (a Web Lock plus a read-back, as the browser
   store does).
4. The browser does not say how much room the disk has, so a chosen folder has no capacity meter.
   A quota error is `docstore.ErrStorageFull`.
5. `OriginPrivate` is the same adapter on the browser's private file system: nothing to pick or
   allow, in every current browser, but the person cannot browse it, so it is storage and not
   "my folder".
6. Where the picker is missing (Firefox, Safari) the control says so and points to the browser's
   own storage and to downloads. The showcase's tests replace `showDirectoryPicker` with one that
   returns an origin-private folder, because headless Chrome has no dialog.

## What a browser build links

`docstore`'s server-side backends (the Go CDK blob adapter, S3, MongoDB and PostgreSQL, like
SQLite) are `//go:build !js`: a page has no use for a database driver or a cloud SDK, and
linking them put gRPC, the AWS SDK and the Mongo and Postgres drivers into every
browser build. The showcase's `main.wasm` went from 73.7 MB to 31.1 MB without them.
