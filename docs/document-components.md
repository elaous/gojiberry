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
   no `Export` a document goes as saved.
3. **Folder download** is `Saver.Archive`: a ZIP with the folders drawn as
   directories (they are virtual in the store), documents as saved, the trash
   left out.

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
access lists and catalogs work together. A server that lists only what the caller may
read also makes a new catalog start with just those documents.

## Damaged files in the trash

A document in the trash whose file is missing or damaged cannot be opened or moved, and
used to stop its folder from being deleted. Deleting the folder now removes such
documents (and their versions) and the status line says which ("Old deleted (2
unreadable documents in the trash were removed: a, b)"). Only a missing or damaged file
counts; any other store error still stops the operation.

## Two processes on one store

Catalog updates are conditional (`docstore.ConditionalAdapter`): memory, SQLite (a busy
timeout and an immediate write transaction), local files (an `flock` on the directory, on
Unix) and the HTTP store (`If-Match`). `Repair` waits `RepairGrace` (2 s) and looks again
before repairing, so it does not undo a delete another process is half way through, and
a rebuild never overwrites a catalog changed since it was read.
