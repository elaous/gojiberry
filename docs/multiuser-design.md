# Multi-user editing: design

Status: proposal. Nothing here is built yet, except roles (`canvas.Role`) and
comments, which this design builds on.

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

## Model: one character sequence with attributes

Map the `TextFlow` stream onto **one RGA sequence for the whole document**,
the way the stream already works: every rune is a character, `\n` ends a
paragraph, and each non-text item is a special character.

| TextFlow | in the CRDT |
|---|---|
| rune | a character with an id |
| `Format` fields (bold, colour, link, `Comment`, `Bookmark`, `Index`, ...) | **per-character attributes**, one last-writer-wins register per (character, key) |
| inline object (`FootnoteRef`, TOC, index, later a map) | a character `U+FFFC` with an `obj` attribute (the `docfile` form) |
| `ParaFormat` | attributes on the paragraph's terminating `\n` character |
| `Break`, `ColumnConfig` | a special character with attributes |
| comments, footnote text, named styles, title | separate LWW blocks (`comment:<id>`, `reply:<comment>:<id>`, `note:<id>`, `style:<name>`, `meta`) -- replies are their own blocks so two concurrent replies both survive |

This needs one extension to `crdt`: a `CharAttr{Target, Key, Value, TS}` op and
the matching snapshot form. Formatting a selection is one op per character,
sent run-length encoded. Typing copies the format at the caret into the new
characters' attributes (what `typeFormat` does now), so no range-expansion
rules are needed. Concurrent formatting of the same character converges by
timestamp; concurrent edits and formatting of different text all survive.

## The bridge (`collab` package)

Between the CRDT and a `TextFlow`:

- **Local edits**: on `OnChange`, diff the previous synced model against the
  flow's items (characters and their attributes) and turn the difference into
  ops. One place covers typing, paste, formatting, paragraph commands, line
  operations and multi-cursor, because they all end in `Replace`.
- **Remote ops**: apply to the model, rebuild items (runs of equal attributes
  become `Chars`; `U+FFFC` becomes an `Inline`; newline attributes become
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

1. **`crdt`: attributes.** `CharAttr` op, snapshot, an id-to-index map so
   edits are not O(n); convergence fuzz tests (random edits, shuffled and
   duplicated delivery).
2. **`collab` bridge.** Items to model and back (round-trip tests against
   `docfile`), diff to ops, remote apply with cursor mapping, side data. A test
   that connects two flows through a lossy, reordering fake network.
3. **Server and transport.** SSE + POST, persistence, role checks,
   presence. Tests with real `httptest` servers and two clients.
4. **UI.** Remote carets and selections, `components.Presence`, a comment-only
   mode for viewers (if wanted), per-user undo, a reconnect indicator; a
   showcase mode (`?doc=...&role=...`) and an e2e with two browser pages.

Known limits to accept: RGA can interleave two people's simultaneous runs of
typing at the same spot (converges, not always as intended); tombstones grow
without garbage collection until a causal-stability protocol is added; tables,
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
