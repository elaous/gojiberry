# The `.opendoc` pack format (draft 1)

A **pack** holds many documents and their folders in one file. It is to `.open`
(one document, see `docfile.Open`) what a folder is to a file: `.open` is a
document, `.opendoc` is a pack of them.

Goals, in order: **check that every byte is there and unchanged**, **open without
reading everything** (list from the end of the file, read one document with a range
read), **append without rewriting**, **survive a crash**, and **one serialization**
(protobuf, the same as the documents).

Not goals in this version: compression, partial updates of a document in place. Encryption
is not done by the pack, which has no keys and no cryptography; it carries **encrypted
records** (§2) whose bytes are an envelope someone else made.

## 1. Conventions

1. All integers are little-endian. Hashes are SHA-256; frame checks are CRC-32C
   (Castagnoli).
2. Every structure is aligned to 8 bytes; padding is zero bytes and must be zero.
3. Messages are protobuf wire format. Unknown fields are skipped, so fields are only
   ever added: a removed field is `reserved` and its number is never reused. The
   format version is bumped only for a change old readers cannot skip. The Go reader
   and writer use a small hand-written wire package (`docfile/internal/wire`), not a
   protobuf library; groups, field number 0 and over-long varints are errors.
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
| 4 | version: 1, or 2 for a pack that holds attachments (below) |
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
| 1 | kind: 1 document, 2 index, 3 signature, 4 attachment (version 2); others are skipped |
| 1 | flags: bit 0 = the data is an envelope (an encrypted record, below); other bits mean "do not read this record" |
| 2 | zero |
| 4 | CRC-32C of the 12 bytes above |
| n | data |
| 4 | CRC-32C of the data |
| 0–7 | padding, so the next record starts on 8 |

A document record's data is a `.open` message (a `docfile.Document` in protobuf wire
format) and nothing else, **or**, when the flags' bit 0 is set, an **encrypted record**: an
opaque envelope (for the reference implementation an age file whose plaintext is that same
`.open` message). The pack does not look inside it, so everything about the record that
is computed is over the **stored** bytes: the data CRC, the index's SHA-256 and so the
signature. A pack's integrity, signature, `Verify`, `Compact` and `Recover` therefore work
**without any key**; only reading the document needs one. Packs with no encrypted record
are byte for byte what they were (the flags byte was zero, `testdata/vector.opendoc`
still passes), the header's flags stay 0, and an older reader that meets an encrypted
record fails to parse it as a document, for that document only. Records are self-framing: a reader can walk the file from
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
  bool   encrypted = 10; // the record is an encrypted record: its flags bit 0 is set too
}
message Folder { string id = 1; string name = 2; string parent = 3; }
```

For an encrypted record the writer chooses what the index shows: the reference
implementation lists a placeholder name ("Confidential document"), no tags, and the folder,
the update time and the trash flag as they are (the real name and tags travel inside the
envelope). `encrypted` in the index and the record's flag must agree; a reader that finds
them different reports that document as damaged, and unknown flag bits do the same (the
index is covered by the footer's hash and the signature, the flag by the frame's CRC).

Folders are virtual, as in a store: a document names its folder, a folder names its
parent. The index is what a store imports or lists; nothing is parsed from the
documents to do so.

### Attachment record (kind 4, version 2 only)

Bytes a document refers to by their SHA-256, stored once: a large picture, or one used by several documents or by the versions of one. The index lists them in field 3: `Attachment { bytes sha256 = 1; uint64 offset = 2; uint64 length = 3; string mime = 4; }`, repeated, with the same rules as for documents (inside the records, aligned, no overlap, hash checked by `Verify`, covered by the index hash and so by the signature). A document in the pack names one by the image URL `opendoc-att:<64 hexadecimal characters>` (`docfile.AttachmentURL`); the importer puts the `data:` URL back. A pack is written as version 2 only when it holds one (`pack.NewWriterV2`; the saver's `PackOptions.Attachments` sets the size from which an image is kept apart, 64 KiB by default in the showcase), so a pack without attachments stays version 1 and every reader opens it. **A version 1 reader refuses a version 2 pack** (its version is not one it knows) instead of showing documents without their pictures. A version 1 pack cannot get attachments by appending (`ErrNoAttachments`); `Compact`, `Recover` and `Repack` keep them. Sealed (encrypted) documents keep their pictures inside the envelope, since the pack cannot see into it.

### Signature record (kind 3, optional)

`Signature { string alg = 1 /* "ed25519" */; bytes public_key = 2; bytes sig = 3; repeated Signature more = 4; }`

The signature is over `SHA-256("opendoc-v1" || SHA-256(header bytes) || index
sha256)`. The index holds every document's hash, so one signature covers the header,
the index and all documents. A reader reports the key; deciding whether to trust it
is the application's.

**Several signers.** Field 4 holds the signatures of further signers, each over the same digest (at most 16 in all, and a signer does not nest another). The first signer is the pack's signer as before: a reader that does not know field 4 reads it as an unknown field and sees that signature, valid as ever. `Cosign` adds a signer in place by rewriting the signature record and the footer after it (nothing else moves, so the other signatures stay valid); it refuses a pack that is unsigned, whose signatures do not all hold, or that the key signed already. Changing the pack (an append, an edit of the index) changes the digest, so the signatures of the others are dropped: the appender signs again with its own key and the others sign again with `Cosign`. A co-signature interrupted half way is repaired like an interrupted append, by `Recover`.

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
3. **Open an encrypted record.** Step 2 gives the envelope as stored. `Reader.Read` of such an
   entry returns `ErrEncrypted`; `Reader.ReadWith(id, open)` passes the envelope through a
   function the caller supplies (it decrypts and returns the `.open` message), then parses it
   and checks the document's ID as for any record.
4. **Verify all.** Do 2 for every entry, check no two records overlap and that every
   record in the file is in the index (a record not in the index is reported, not an
   error: it is an append that did not finish). With a signature record, check it too. None of
   it needs a key, encrypted records included.

Bounds, always: a length or offset is checked against the file size before anything
is allocated or seeked to. Caps: one record 256 MiB, 1,000,000 entries, an index
128 MiB (128 bytes per entry at the entry cap), one entry or folder message 32 KiB, 64
tags of at most 256 bytes per entry, folders nested at most 16 deep (as in a store),
names 256 bytes. The index is parsed as a stream, counting entries as they are read.
An index is invalid if: an entry or folder ID is not a UUID as `NewID` writes it, or is
repeated; an entry points outside `[end of header, index offset)`; two entries'
records overlap; an entry or folder names a folder the index does not list. A reader also
checks that a document's own ID is the ID the index lists it under.

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
are intact, with names taken from `Document` metadata (id, name, folder, tags). An encrypted
record has no readable ID, so it is carried through only when the index is intact (with the
index's entry, as `Recovered.Sealed`, and `Repack` writes it back as an encrypted record);
without the index it is counted in `Skipped` and in `Recovered.SealedLost`, and not
recovered. **`Repack` drops such records**: the recovered pack silently lacks those
confidential documents (`opendoc recover` prints a loud warning when `SealedLost` is not
zero). If you have a copy of the pack whose index is intact, recover from that copy first;
the damaged one is better kept as it is than replaced by the repacked output. The walk
cannot count records after the first bad frame. This
loses the index's folders only if the documents' own `folder` metadata is also gone.
A document whose ID appears in more than one record is recovered once, as the last of
them. If the index is still intact, it decides which records are in the pack (a document
removed from the index is not brought back); without an index a removed document cannot
be told from a live one and is recovered. Folders are listed from the documents' folder
IDs under made-up names (`recovered-<id>`). A recovered pack is reported as such and
must be rewritten before it is signed.

## 6. Decisions and what is left open

Decided for this draft:

1. A hash per document **and** one optional signature over the whole pack (a hash alone
   does not catch someone who rewrites a record and its hash).
2. **Encrypted records** are opaque to the pack (a flag bit and an index field): no key per
   pack, no nonces in the format, nothing for a key to sign over. The envelope brings its
   own authentication, and the pack's hashes and signature cover it as stored, so a record
   cannot be swapped or altered without `Verify` noticing, keyless. Which keys open it is the
   envelope's business (docs/encryption-and-passkeys.md).
3. **No compression** in version 1 (documents carry images as `data:` URLs, which
   compress poorly); a flag bit and a record kind are reserved for it.
4. The reference implementation is `docfile/pack`, depending on `docfile` and the
   standard library only, so it can be lifted out. The command line tool is
   `cmd/opendoc` in this repository for now.

Open:

1. ~~Hiding what the index shows about an encrypted record~~ **partly done**: `PackOptions.Blur` rounds the time of a sealed record down to the day and pads what is sealed to a size bucket (1 KiB, doubling); the folder and the trash flag stay readable, like a store's catalog. Encrypting the whole index is phase 2 ([P2-14](phase-2.md)).
2. ~~Moving large attachments out of documents into their own record kind~~ **done** as version 2 (see Attachment record).
3. ~~Whether the signature should allow several signers~~ **done**: `Signature.More` (field 4), `Writer.CloseSigned`, `Cosign`, `Reader.Signers`, `opendoc cosign`.
4. ~~A MIME type and a magic entry~~ **done**: `pack.MIME` (`application/vnd.opendocs.pack`), `docfile/pack/mime/opendoc.magic` for `file(1)` and `opendoc.xml` for shared-mime-info.

### Open points explained

A pack is one file with many documents in it: a backup, an export, something to hand to somebody. It has an *index* (a list of every document with its name, folder, size, time and trash flag) and each document is a *record*. Two things are left open.

**1. What the index shows about an encrypted record.** An encrypted document's content is unreadable without its key, but the index entry beside it is plain: someone holding the file can see "a record of 3.2 MB in the folder *Medical*, changed on Tuesday, in the trash". That is the same as a store's catalogue, and it is there on purpose: the pack can be listed, verified and recovered **without any key** (`opendoc verify` checks every hash and the signature with no key). The choices: (a) leave it, say so (what is done now); (b) round the sizes up to buckets and blur the times, which hides a little and costs a little space; (c) encrypt the index too, which hides the structure but means nothing can be listed, verified or repaired without the key, and loses the keyless check. *Recommendation:* (a) for now, and (b) as an option when a pack is exported with confidential documents; (c) only if somebody needs it, since a pack you cannot verify without a key cannot be checked by whoever stores it for you. **Decided (owner, 2026-10-18):** blur now (done: the times are rounded to the day and the sizes fall in buckets, on by default in the showcase's pack download), encrypting the index is a phase 2 option ([P2-14](phase-2.md)).

**2. Large attachments as their own record kind.** Today a picture is inside the document that uses it (a `data:` address). A document with one large picture is large, and the same picture in two documents, or in the document's history, is stored again each time; appending to a pack that holds such a document rewrites nothing, but verifying and reading it moves all that data. A record kind for attachments would store each picture once (by its hash), and the documents would refer to it. The cost: a format change. A reader that does not know the new kind skips it (the format says unknown kinds are skipped) but then cannot show the pictures, so it needs version 2 of the format and a note that version 1 readers will see documents without their large pictures. *Recommendation:* yes, as version 2, written only when a pack has attachments over a size (so small packs stay readable by every reader); not urgent. **Decided (owner, 2026-10-18) and done:** version 2, as above; a version 1 reader refuses such a pack, which is plainer than showing it without its pictures.

## 7. Plan

1. **Done:** `docfile/pack` (`Writer`, `Appender`, `Reader` over `io.ReaderAt`, `Verify`,
   `Recover` and `Repack`, `Compact`), with a committed test vector
   (`testdata/vector.opendoc`, byte for byte), a test that cuts the file at every byte and
   one that flips every bit, and a fuzz target (`go test -fuzz FuzzOpen`, seeded with a pack
   that has an encrypted record). It uses only `docfile` and the standard library.
   Encrypted records: `Writer.AddEncrypted` / `Appender.AddEncrypted`, `Entry.Encrypted`,
   `ErrEncrypted`, `Reader.ReadWith`.
2. **Done:** `Saver.ExportPack` (a folder or everything; folders by ID, tags, the trash flag,
   optionally each document's history; signed if given a key) and `Saver.ImportPack` (folders
   recreated by ID under a chosen folder; a document already there is skipped, replaced (with
   the pack's folder, tags and trash flag) or copied (the UI import always skips); a bad
   document is reported and the rest come in; the signature is reported). A confidential
   (end-to-end) document is written as an encrypted record through `PackOptions.Seal` and read
   back through `ImportOptions.Open`: the index lists the placeholder name and no tags for it;
   with no `Seal` it is left out and reported to `PackOptions.Warn` (never written in the
   clear), a failing `Seal` stops the export; with no `Open`, or a failing one, the record is
   skipped with a needs-key warning (`ImportReport.NeedsKey`, `Warnings`) and the rest come
   in; an imported one is saved confidential again, subject to `Saver.ConfidentialCheck`. The
   UI: "As .opendoc" on a folder and "Import .opendoc" in the document list
   (`Session.DownloadPack`, `Session.Pick`), and "One .opendoc pack" in Migrate's format menu
   (`migrate.PackSink`). The showcase's pack download and import use the keys of the unlocked
   store (`Session.PackKeys`); a locked store leaves confidential documents out and says how many.
3. **Done:** `cmd/opendoc`: `pack` and `convert` (a file or a folder tree; `.docx`, `.pdf`,
   Markdown, text, `.open`, JSON and protobuf are read through the importers, subfolders
   become folders), `unpack`, `list`, `verify`, `sign`, `recover`, `keygen`. Its
   [README](../cmd/opendoc/README.md) says which `.docx` features are not carried over.
4. **Done:** signing with Ed25519 (a key file of 64 hex characters, `opendoc keygen`);
   appending to a signed pack needs the key. Key handling beyond a file (hardware keys,
   passkeys) is part of encryption and passkeys.
