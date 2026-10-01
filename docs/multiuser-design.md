# Multi-user editing: design

Status: **phase 1 (`crdt` marks) is done**; phases 2-4 are not started. Roles
(`canvas.Role`) and comments, which this design builds on, exist.

## What exists

- **`crdt`**: a block document (`Block{ID, Kind, Content string, Order}`), one
  register per block, merged either last-writer-wins or, per block, with a
  per-character RGA (`Text`). Ops are small deltas; `Snapshot` onboards a new
  replica. **Characters carry no formatting**, and `Text` finds a character by
  scanning (O(n) per operation).
- **`syncserver`**: a relay that is itself a replica. `POST /ops`, `GET /ops`
  (a full snapshot), `POST/GET /presence` (replica, name, colour, block, column;
  10s TTL). No authentication and no roles. Clients poll.
- **`examples/collab-editor-app`**: WebSocket relay of the whole text; last edit
  wins. Shows `components.Collab` roles.
- **`TextFlow`**: a stream of items (`Chars` with a `Format`, paragraph formats,
  column configs, breaks, inline objects), plus side sets (comments, footnotes,
  styles). Every edit goes through `replaceRange`/`Replace`, then `changed()`.
- **Roles**: `canvas.Role` (viewer = view mode). `docstore.ACL.RoleOf` gives a
  user's role. Note a mismatch: `components.CapabilitiesForRole` lets a
  *viewer comment*; `canvas.Role.CanComment` does not.

## Model: a character sequence and a list of marks

Two structures, both CRDTs, walked together to read the document:

1. **Text**: one RGA sequence for the whole document, the way the stream
   already works. Every rune is a character with an id, `\n` ends a paragraph,
   and each non-text item (inline object, break, column config) is a special
   character (`U+FFFC` for an object, carrying its `docfile` form as a payload).
2. **Marks**: a separate list of formatting spans, in the style of Peritext.
   A mark is `{id, key, value, start, end, ts}`: `key` is a `Format` field
   (bold, colour, link, `Comment`, `Bookmark`, `Index`, ...), and `start` and
   `end` are **anchors**: a character id plus a side (before or after it).
   Anchoring to ids, not offsets, is what keeps a span right while other people
   insert and delete around it. An empty value removes the key.

Reading it is a merge of the two lists: walk the characters once, keep the
marks that cover the current character, and for each key the covering mark
with the latest timestamp wins. That is O(n + m log m) for n characters and m
marks, and can be cached per run of characters and updated incrementally.

| TextFlow | in the CRDT |
|---|---|
| rune | a character in `Text` |
| `Format` fields | marks (one per key, over the formatted range) |
| `ParaFormat` | a few attributes on the paragraph's `\n` character (one register per key; there are few paragraphs, so a mark list is not needed) |
| inline object, `Break`, `ColumnConfig` | a special character with a payload |
| comments, footnote text, named styles, title | separate LWW blocks (`comment:<id>`, `reply:<comment>:<id>`, `note:<id>`, `style:<name>`, `meta`); replies are their own blocks so two concurrent replies both survive |

### Why not one attribute set per character

It is the simplest thing to make correct, but costly:

| | per-character attributes | text + marks |
|---|---|---|
| bold 10,000 characters | 10,000 ops (or one run-length op that cannot name ids it has not seen) | **1 op** |
| memory and snapshot | n x (keys) registers, even for plain text | proportional to the number of *formatting changes* |
| text typed inside a bold range by someone else, concurrently | not bold (it had no attribute) | bold, if it lands between the anchors (what people expect) |
| reading | direct | one sweep (cheap, cacheable) |

Marks need more care (anchor sides, which end a mark grows at, tombstoned
anchors), and the rules are the ones Peritext publishes: a bold, link or
comment mark does not grow at its start; bold and colour grow at their end;
links and comments do not. The bridge decides what a *typed* character gets:
it compares the format the flow wants with what the marks would give and adds
a mark only when they differ, so ordinary typing adds no marks at all.

## The bridge (`collab` package)

Between the CRDT and a `TextFlow`:

- **Local edits**: on `OnChange`, diff the previous synced model against the
  flow's items. A text difference becomes character ops; a format difference
  becomes mark ops, one per changed run (not per character). One place covers typing, paste, formatting, paragraph commands, line
  operations and multi-cursor, because they all end in `Replace`.
- **Remote ops**: apply to the model, rebuild items (runs of equal resolved
  marks become `Chars`; `U+FFFC` becomes an `Inline`; newline attributes become
  `ParaFormat`), and set them in the flow. Each cursor is kept as a character
  id plus side and re-resolved afterwards, so a remote insert before the caret
  moves it, as it should.
- **Side data**: `CommentSet`, `FootnoteSet` and styles observe changes and
  write their blocks; remote blocks update the sets.

## Presence

Extend `syncserver.Presence` from block/column to `anchor`/`caret` (character
ids, so it survives edits) plus role. The flow draws other people's carets with
a name flag and a tinted selection (`TextFlow.RemoteCursors`), and a DOM
`components.Presence` lists them (reusing `Avatar`).

## Transport and server

- Keep the root module dependency-free: **server-sent events plus POST**
  (`GET /events` streams ops and presence, `POST /ops` sends), both stdlib and
  both workable from WASM `fetch`. WebSocket stays an option for the example
  module that already has it.
- The server is a replica per document, persists snapshots through `docstore`
  (so Postgres, SQLite and S3 work as everywhere else), and replays them on
  start. Polling `GET /ops` stays as the offline-first fallback; the outbox
  can be kept in `docstore.BrowserAdapter` across reloads.

## Roles, enforced on the server

The client already holds viewers in view mode, but the **server is the
authority**: a bearer token (the existing `auth` package) names the user,
`docstore.ACL.RoleOf` gives the role, and the relay checks each op.

| op | owner | editor | viewer |
|---|---|---|---|
| text, attributes, side data, styles | yes | yes | no |
| `comment:`/`reply:` blocks | yes | yes | **decision needed** |
| ACL changes | yes | no | no |

Rejected ops are dropped with an error the client shows ("you can only view
this document"), and the client rolls back its local copy by re-syncing.

## Undo

`History` is snapshot-based, which would undo other people's edits. With the
bridge, record each local change as its inverse **op set** and undo only those
(per-user undo). Until then, remote changes clear the undo stack while
connected.

## Plan

1. **`crdt`: marks. Done.** A `Mark` op and list with id anchors, resolution by
   sweep, snapshot; paragraph and object attributes on characters; an
   id-to-index map so edits are not O(n); convergence fuzz tests (random edits
   and formatting, shuffled and duplicated delivery).
2. **`collab` bridge.** Items to model and back (round-trip tests against
   `docfile`), diff to ops, remote apply with cursor mapping, side data. A test
   that connects two flows through a lossy, reordering fake network.
3. **Server and transport.** SSE + POST, persistence, role checks,
   presence. Tests with real `httptest` servers and two clients.
4. **UI.** Remote carets and selections, `components.Presence`, a comment-only
   mode for viewers (if wanted), per-user undo, a reconnect indicator; a
   showcase mode (`?doc=...&role=...`) and an e2e with two browser pages.

Known limits to accept: marks and tombstones both grow until a causal-stability garbage collection is added; RGA can interleave two people's simultaneous runs of
typing at the same spot (converges, not always as intended);  tables,
images and floating objects are replaced whole (last writer wins) rather than
merged inside; page furniture (headers, footers, watermarks, margins) is LWW
blocks.

## Decisions needed

1. **Can viewers comment?** The component library says yes; `canvas.Role` says
   no. If yes, add a comment-only mode (selection works in view mode already)
   and let the server accept comment blocks from viewers.
2. **Transport**: SSE + POST (no new dependency, recommended) or WebSocket.
3. **Undo while connected**: per-user undo (right, more work) or clear on
   remote change (simple, a visible limit).

## Phase 1 as built

In `crdt`: `Mark` and `Anchor` (a gap named by a character id and a side; the
zero id is the text's start or end), `Doc.Format(block, from, to, key, value,
growEnd)` (one op for any range), `Doc.Runs(block)` and `Text.Runs()` (the
sweep), `CharMark` ops in snapshots. Under MergeLWW there is no formatting.

Two fixes the design needed along the way:

- **Delivery order.** An insert whose origin had not arrived used to be
  appended at the end, so replicas receiving ops out of causal order could
  diverge. Such inserts are now held until their origin arrives.
- **Loading cost.** Finding a character was a linear scan, so loading a
  snapshot was O(n^2). A position cache, extended lazily, makes it linear.

Measured here: loading and reading a 100,000-character formatted snapshot takes
about 80 ms; one keystroke in the *middle* of a 10,000-character text costs about
0.2 ms and of a 100,000-character text about 4 ms (the cache is rebuilt behind
the insertion). Typing at the end costs next to nothing. A balanced tree would
make the middle case logarithmic; it is not needed at document sizes up to tens
of thousands of characters.

Tests: unit tests for runs, grow-at-end rules, concurrent insert inside a
range, deleted anchors, marks arriving before their characters, idempotence and
snapshots; and a randomised test (150 trials, three replicas, random edits and
formatting delivered late, out of order and twice) asserting the same text and
formatting everywhere, including on a replica onboarded from a snapshot.
