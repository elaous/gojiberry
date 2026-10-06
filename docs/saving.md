# Saving, importing and fonts

Everything here is non-canvas: Go packages plus DOM components that sit
around the canvas editor. `examples/canvas-editor-showcase` wires all of it
(File menu, the Save and Import/Export tabs, a font that downloads lazily).

## The document model (`docfile`)

`docfile.Document` is what gets written:

| field | holds |
|---|---|
| `Title`, `Updated` | name and time (unix ns, stamped on save) |
| `Content []Item` | text runs with their `Format`, paragraph formats, column configs, breaks |
| `Styles` | the named styles (`canvas.StyleDef`) |
| `Fonts` | the fonts the document uses and where their files are |
| `Meta` | free key/value pairs |

`docfile.FromFlow(title, flow)` captures a `TextFlow`; `doc.Apply(flow)`
puts it back. The types mirror the canvas ones with explicit protobuf field
numbers (`pb:"N"` tags), so saved files survive refactors of the canvas
types; a test fails if a canvas field is added without its mirror.

Formats (`docfile.Formats()`):

- `json` (`.json`) -- indented JSON
- `proto-text` (`.pbtxt`) -- protobuf text format
- `proto` (`.pb`) -- protobuf wire format

The schema is derived from the Go types and printed by `docfile.Schema()`
as `.proto` source. There is no `protoc` step; the messages are dynamic.
`docfile.Detect(data)` guesses a format from the bytes.

Not saved yet: pages (headers, footers, watermarks, backgrounds),
floating objects, images and tables outside the text flow.

## Targets (`saver`, `docstore`)

A target is a `docstore.Adapter`. The adapters:

| target | adapter |
|---|---|
| Postgres | `postgres.Adapter` of `docstore/backends` (`{"dsn": ...}`) |
| SQLite | `docstore.SQLiteAdapter` (`{"path": "docs.db"}`), pure Go, not for WASM |
| S3 / MinIO / any bucket | `s3.Adapter`, `blob.Adapter` of `docstore/backends` |
| files | `docstore.LocalFSAdapter` |
| memory | `docstore.MemoryAdapter` |
| a docstore server | `docstore.NewHTTPAdapter(url, client)` -- how a WASM app reaches any of the above |
| this browser | `docstore.NewBrowserAdapter(prefix)` -- localStorage |

A browser cannot speak to a database, so in a WASM app Postgres, SQLite and
S3 are used through a docstore server (`cmd/docstore`, backend chosen by
`GOJIBERRY_DOCSTORE_BACKEND`) and the `HTTPAdapter`.

```go
s := &saver.Saver{Targets: []saver.Target{
    {Name: "server", Label: "Server", Adapter: docstore.NewHTTPAdapter("/api", nil)},
    {Name: "browser", Label: "This browser", Adapter: docstore.NewBrowserAdapter("")},
}}
key, err := s.Save(ctx, "server", "report", docfile.JSON, doc) // "report.json"
doc, err = s.Load(ctx, "server", key)
list, err := s.List(ctx, "server") // newest first; fonts and foreign files are left out
```

`saver.Build(ctx, name, label, docstore.Config{...})` makes a target from the
same config a server uses.

## The Saver component

`components.Saver(props)` is a controlled form: target and format menus, a name,
Save, and the list of documents saved in the target with Open and Delete.
`saver.NewSession(s, get, onOpen)` keeps its state and does the work in the
background; `session.Props()` gives the component its props, and
`session.OnChange` is where you render again.

## Import and export (`transfer`)

For other file formats. An `Importer` reads a file into a `Document`, an
`Exporter` writes one; `transfer.Default()` registers JSON, protobuf,
Markdown and plain text. Add more (Word, PDF, HTML) with
`AddImporter` / `AddExporter`. `components.Transfer` draws one button per
format; `transfer.Download` and `transfer.PickFile` (WASM) do the browser
side.

## Lazy fonts

`fonts.Library` is the document's fonts:

1. Choosing a font in the `FontSelector` only **adds** it (`lib.Add(font)`).
2. The first time text really uses it, the file is **downloaded into the
   store** and loaded into the browser. The canvas reports that moment:
   `Stage.OnFontUse` is called, at a draw, once per family/weight/style the
   content uses (named styles and paragraph styles count).
3. Later sessions load it from the store; nothing is fetched again.

```go
lib := &fonts.Library{Blobs: saver.Blobs(adapter)}   // any docstore backend
stage.OnFontUse = func(u canvas.FontUse) { lib.UseAsync(u.Family, u.Weight, u.Italic) }
lib.OnReady = func(r fonts.Ready) {
    fonts.LoadFace(r.Family, r.Variant, r.Data, func(error) { redraw() })
}
// in the selector's OnSelect:  lib.Add(font)
// saving:                      doc.Fonts = docfile.FromFonts(lib.Added())
// opening:                     for _, f := range docfile.ToFonts(doc.Fonts) { lib.Add(f) }
```

Files are kept under `fonts/<Family>/<weight>-<n|i>.<ext>`; `Saver.List`
skips that prefix, so fonts and documents can share one store. Fonts with no
URL (installed on the device) are never downloaded. A family that was not
added (a web-safe font) is ignored. Errors go to `Library.OnError`, and the
next use tries again.
