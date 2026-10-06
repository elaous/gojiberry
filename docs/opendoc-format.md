# The `.opendoc` pack format (draft 1)

A **pack** holds many documents and their folders in one file. It is to `.open`
(one document, see `docfile.Open`) what a folder is to a file: `.open` is a
document, `.opendoc` is a pack of them.

Goals, in order: **check that every byte is there and unchanged**, **open without
reading everything** (list from the end of the file, read one document with a range
read), **append without rewriting**, **survive a crash**, and **one serialization**
(protobuf, the same as the documents).

Not goals in this version: compression, encryption, partial updates of a document
in place.

## 1. Conventions

1. All integers are little-endian. Hashes are SHA-256; frame checks are CRC-32C
   (Castagnoli).
2. Every structure is aligned to 8 bytes; padding is zero bytes and must be zero.
3. Messages are protobuf wire format. Unknown fields are skipped, so fields are only
   ever added: a removed field is `reserved` and its number is never reused. The
   format version is bumped only for a change old readers cannot skip.
4. Names, IDs and tags are UTF-8. Document and folder IDs are UUIDs, the same as in
   a store.

## 2. Layout

```
header | record* | index record | signature record? | footer
```

### Header (24 bytes + meta)

| bytes | |
|---|---|
| 8 | magic `OPENDOC\0` |
| 4 | version (1) |
| 4 | flags (0; unknown bits mean "do not read") |
| 4 | length of the meta message |
| 4 | zero |
| n | `PackMeta`, then padding to 8 |

`PackMeta { string id = 1; string name = 2; int64 created = 3 /* unix ns */;
string producer = 4; string description = 5; }`.

### Record

| bytes | |
|---|---|
| 8 | length of the data (n) |
| 1 | kind: 1 document, 2 index, 3 signature; others are skipped |
| 3 | zero |
| 4 | CRC-32C of the 12 bytes above |
| n | data |
| 4 | CRC-32C of the data |
| 0–7 | padding, so the next record starts on 8 |

A document record's data is a `.open` message (a `docfile.Document` in protobuf wire
format) and nothing else. Records are self-framing: a reader can walk the file from
the header without the index, which is how a damaged tail is recovered (§5).

### Index record (kind 2)

```
message Index {
  repeated Entry  entries = 1;
  repeated Folder folders = 2;
}
message Entry {
  string id = 1;        // document UUID
  string name = 2;
  string folder = 3;    // folder UUID, "" for the top
  repeated string tags = 4;
  int64  updated = 5;   // unix ns
  uint64 offset = 6;    // of the record (its length field), from the file start
  uint64 length = 7;    // length of the data
  bytes  sha256 = 8;    // of the data
  bool   trashed = 9;
}
message Folder { string id = 1; string name = 2; string parent = 3; }
```

Folders are virtual, as in a store: a document names its folder, a folder names its
parent. The index is what a store imports or lists; nothing is parsed from the
documents to do so.

### Signature record (kind 3, optional)

`Signature { string alg = 1 /* "ed25519" */; bytes public_key = 2; bytes sig = 3; }`

The signature is over `SHA-256("opendoc-v1" || SHA-256(header bytes) || index
sha256)`. The index holds every document's hash, so one signature covers the header,
the index and all documents. A reader reports the key; deciding whether to trust it
is the application's.

### Footer (80 bytes, last in the file)

| bytes | |
|---|---|
| 8 | magic `OPENDEND` |
| 8 | offset of the index record |
| 8 | length of the index data |
| 32 | SHA-256 of the index data |
| 8 | offset of the signature record, 0 if none |
| 8 | length of the signature data |
| 4 | CRC-32C of the 72 bytes above |
| 4 | zero |

## 3. Reading

1. **List (cold open).** Read the last 80 bytes, check the magic and CRC, read the
   index at its offset, check its SHA-256 against the footer and each frame's CRCs.
   Nothing else is read. The footer and index are untrusted until these checks pass.
2. **Open one document.** Range-read the record at `entry.offset`, check the frame
   and the data CRC, check SHA-256 equals `entry.sha256`. A mismatch is an error for
   that document; the others are still readable.
3. **Verify all.** Do 2 for every entry, check no two records overlap and that every
   record in the file is in the index (a record not in the index is reported, not an
   error: it is an append that did not finish). With a signature record, check it too.

Bounds, always: a length or offset is checked against the file size before anything
is allocated or seeked to. Caps: one record 256 MiB, 1,000,000 entries, an index
256 MiB, folders nested at most 16 deep (as in a store), names 256 bytes. An index
entry pointing outside `[end of header, index offset)` is invalid.

## 4. Writing and appending

1. **New pack.** Header, records, index, (signature), footer.
2. **Append.** Seek to the old index record's offset (the new records go over the old
   index, signature and footer), write the new records, then a new index covering all
   documents, a new signature if the pack is signed, and a new footer. Truncate if the
   file got shorter.
3. **Delete or move a document** is a new index (and no new record); the bytes of a
   deleted document stay until the pack is rewritten (`Compact`).
4. **Order of writes** is records, index, signature, footer; the footer is written
   last and synced, so a footer that checks out means everything before it was written.

## 5. Recovery

If the footer or index fails its checks, walk the records from the header: each frame
is checked by its CRCs, a document record whose data parses as a `Document` is kept,
and the walk stops at the first bad frame. The result is a pack of the documents that
are intact, with names taken from `Document` metadata (id, name, folder, tags). This
loses the index's folders only if the documents' own `folder` metadata is also gone.
A recovered pack is reported as such and must be rewritten before it is signed.

## 6. Decisions and what is left open

Decided for this draft:

1. A hash per document **and** one optional signature over the whole pack (a hash alone
   does not catch someone who rewrites a record and its hash).
2. **No compression** in version 1 (documents carry images as `data:` URLs, which
   compress poorly); a flag bit and a record kind are reserved for it.
3. The reference implementation is `docfile/pack`, depending on `docfile` and the
   standard library only, so it can be lifted out. The command line tool is
   `cmd/opendoc` in this repository for now.

Open:

1. Encryption of records (a key per pack, per-record nonces), and how it composes with
   the signature.
2. Moving large attachments out of documents into their own record kind.
3. Whether the signature should allow several signers.
4. A MIME type (`application/vnd.opendocs.pack`) and a magic entry for `file(1)`.

## 7. Plan

1. **Done:** `docfile/pack` (`Writer`, `Appender`, `Reader` over `io.ReaderAt`, `Verify`,
   `Recover` and `Repack`, `Compact`), with a committed test vector
   (`testdata/vector.opendoc`, byte for byte), a test that cuts the file at every byte and
   one that flips every bit, and a fuzz target (`go test -fuzz FuzzOpen`). It uses
   only `docfile` and the standard library.
2. `Saver.ImportPack` / `Saver.ExportPack` (folders by ID, tags and trash kept) and the
   UI: "Download as .opendoc" for a folder, "Import .opendoc", "Convert to one .opendoc" in
   Migrate. *Left.*
3. **Done:** `cmd/opendoc`: `pack` and `convert` (a file or a folder tree; `.docx`, `.pdf`,
   Markdown, text, `.open`, JSON and protobuf are read through the importers, subfolders
   become folders), `unpack`, `list`, `verify`, `sign`, `recover`, `keygen`. A README that says
   which `.docx` features are not carried over is *left*.
4. **Done:** signing with Ed25519 (a key file of 64 hex characters, `opendoc keygen`);
   appending to a signed pack needs the key. Key handling beyond a file (hardware keys,
   passkeys) is part of encryption and passkeys.
