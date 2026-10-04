# Document components: plug-in pieces

Each part of a document's life is a component of its own, taking the operations it
offers as functions. Nothing in them knows about `saver`: a host plugs in a
`saver.Session`, a REST client, or a test double, and a control whose operation is
nil is not drawn.

| Component | Shows | Operations it takes |
|---|---|---|
| `DocList` | documents (name, format, size, time, tags) | `Open`, `Duplicate`, `Trash`, `Delete`, `Tag`, `Refresh`, `OnTagFilter` |
| `TagFilter` | All + one chip per tag | `OnSelect` |
| `Trash` | documents in the trash, with `Children` (usually the live list) above | `Restore`, `Delete`, optional `OnSelect` for batches |
| `Versions` | snapshots of one document | `Save`, `Open`, `Delete` |
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
