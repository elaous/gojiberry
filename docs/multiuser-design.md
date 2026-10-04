# Multi-user editing: design

Status: **phases 1 (`crdt` marks), 2 (the `collab` bridge) 3 (server and transport) and 4 (UI) are done**. Roles
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
| comments and replies (new records, comment marks) | yes | yes | yes |
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
2. **`collab` bridge. Done.** Items to model and back (round-trip tests against
   `docfile`), diff to ops, remote apply with cursor mapping, side data. A test
   that connects two flows through a lossy, reordering fake network.
3. **Server and transport. Done.** SSE + POST, persistence, role checks,
   presence. Tests with real `httptest` servers and two clients.
4. **UI.** Remote carets and selections, `components.Presence`, a comment-only
   mode for viewers (if wanted), per-user undo, a reconnect indicator; a
   showcase mode (`?doc=...&role=...`) and an e2e with two browser pages.

Known limits to accept: marks and tombstones both grow until a causal-stability garbage collection is added; RGA can interleave two people's simultaneous runs of
typing at the same spot (converges, not always as intended);  tables,
images and floating objects are replaced whole (last writer wins) rather than
merged inside; page furniture (headers, footers, watermarks, margins) is LWW
blocks.

## Decisions

1. **Viewers can comment** (as `components.Collab` already says). The server
   accepts a viewer's comment marks and new comment and reply records; the
   canvas's `Role.CanComment` still treats viewers as read-only until the
   comment-only mode of phase 4.
2. **Transport**: server-sent events plus POST.
3. **Undo while connected**: per-user undo, in phase 4; until then remote
   edits clear the undo stack.

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

## Phase 2 as built

`collab.New(replica, flow)` starts a shared document from a flow's content;
`collab.Join(replica, flow)` waits for one (the flow's own content is replaced by
the first operations, and nothing is shared before then). Then:

- `Drain()` returns the operations to send; `Apply(ops...)` takes the ones that
  arrive; `Snapshot()` onboards a newcomer; `Sync()` shares side data changed
  without a text edit (commands that change a comment or note already do via
  `TextFlow.Touch`); `Err()` reports flow content that cannot be shared.
- **Atoms.** The flow's items are flattened to atoms: a character of text with
  its format as string attributes, or a non-text item (paragraph format, break,
  column config, inline object) as a reserved character with its `docfile` form
  in a `ctl` attribute. Rules and tables now have a `docfile` form too.
- **Local edits** go through `OnChange`: the text is diffed (common ends
  trimmed, the middle matched by longest common subsequence, one replacement if
  it is huge) into `Doc.Splice` calls, then the attribute differences become
  `Doc.Format` marks, one per changed run.
- **Remote edits** rebuild the flow's items from the replica
  (`TextFlow.SetRemoteContent`, which is not an edit, keeps the caret in the
  text and clears undo). Every cursor, selection end and extra cursor is kept as
  the id of the character before it and moves with it.
- **Side data** (comments, replies, footnotes, styles) are `lww:side` blocks,
  merged whole-value (`crdt.LWWKind`): two simultaneous replies both survive.
- New in `crdt`: `Doc.Splice`, `Doc.VisibleIDs`, `Doc.PositionAfter`, and
  LWW-kind blocks inside a character-merged document.

Tests: typing, deleting, formatting, paragraph formats, breaks, columns, rules,
footnotes, comments with replies and styles all reach the other flow; cursors
move with their text; unshareable content is refused with a clear error; and a
randomised test (hundreds of trials) edits two flows at random -- typing,
backspace, bold, colour, links, paragraph alignment, newlines -- delivers
operations late, out of order and twice, and asserts both flows end identical
and that each replica always matches its own flow.

Limits: images and other inline objects with no `docfile` form are refused
(`ErrUnsupported`) rather than silently dropped; the whole document is re-read
on each local change (O(n), fine for tens of thousands of characters); undo is
cleared by remote edits until per-user undo (phase 4).

## Phase 3 as built

Three small packages and a command:

- **`collab/wire`**: the types both ends share (`Role`, `Presence`), so a WASM
  client does not import the server's dependencies.
- **`collab/hub`**: the server. `hub.New(Config{Store, Roles, User})`, `Handler()`.
  Each document is a `crdt` replica kept in memory, loaded from and saved to a
  `docstore.Adapter` under `collab:<id>` (saves are debounced; `Flush` and
  `Close` save now). Routes: `GET /docs/{id}/events` (server-sent events:
  `hello` with the role, `snapshot`, then `ops` and `presence` from others,
  with keep-alive pings), `POST /docs/{id}/ops`, `POST /docs/{id}/presence`,
  `GET /docs/{id}/snapshot`, `GET /docs/{id}/role`. A subscriber that cannot keep
  up is dropped and resyncs when it reconnects.
  - **Roles are enforced here.** Owners and editors may change anything. Anyone
    else who can read may comment: a `comment` mark on the text (and nothing else
    about the text: no inserts, deletes, other marks, or changes to the block's
    kind, order or deleted flag) and *new* `comment:`/`reply:` records, not
    changes or deletions of existing ones. A batch with any refused op is refused
    whole with a 403 and a message.
  - `hub.ACLRoles(store, who)` takes roles from docstore's access lists (owner,
    editors, viewers; no list means open, as in docstore); `hub.AuthUser(manager)`
    names the caller from a bearer token.
- **`collab/client`**: `client.Dial(Options)` returns a `Conn`. Events arrive on a
  channel in order and without loss (an unbounded queue). It reconnects with
  backoff, and every reconnection starts with a fresh snapshot. `Send` keeps ops
  until the server accepts them, so work done offline is delivered later (the
  crdt makes a resend harmless). A refusal comes back as a `Rejected` event.
  It needs only `net/http`, so it works from WASM.
- **`collab.Session`**: `collab.Connect(replica, flow, options)` ties a `Bridge`
  to a `Conn`. Local edits are sent as they happen (`Bridge.Outgoing`); call
  `Poll()` from the goroutine that owns the flow to apply what has arrived (it
  returns whether to redraw). If the server has no content yet and you may edit,
  the flow's content becomes the document. `PublishCursor()` shares your cursor
  (as character ids) and `PeerCursors()` places everyone else's in your text.
- **`cmd/collabhub`**: the server as a program, backed by the same docstore
  settings (`GOJIBERRY_DOCSTORE_BACKEND`, sign-in) and `GOJIBERRY_COLLAB_ADDR`
  (default `:8081`), with a Dockerfile.

Tests: the role table (every kind of op for every role, including a comment mark
smuggling a block delete), ACL roles, every endpoint and its errors, the event
stream, dropping a slow subscriber, persistence and reload, and -- over real HTTP
with two or three sessions -- shared typing and formatting, concurrent edits,
presence following the text, a viewer's edit refused while its comment gets
through, a connection cut mid-session (both sides converge, nothing left
waiting), and a refused connection reported.

Not yet: nothing outstanding that is known.

## Phase 4 as built

- `TextFlow.Subscribe` lets several observers hear about changes (the gox
  element replaces `OnChange` on every render, so the bridge no longer uses it);
  `Session.RefreshPeers` puts other people's cursors in `Flow.RemoteCursors`,
  drawn as a tinted selection, a caret and a name flag (`canvas/flowpeers.go`).
- Per-user undo (`collab/undo.go`): each local change records its inverse by
  character id (inserted ids, deleted runs with their predecessor, old format
  values); `TextFlow.UndoHook` routes Ctrl+Z / Ctrl+Shift+Z to it, so one
  person's undo never touches another's text.
- Viewers may comment: `Role.CanComment` is true for viewers and a
  comment-only target (`canvas/commenttarget.go`) passes through nothing but
  the comment mark; the hub applies the same rule to `comment:`/`reply:` blocks.
- `components.Presence`: avatars, connection status with the number of
  changes waiting to be sent, and a "View only" badge.
- The showcase joins a document with `?doc=&hub=&token=&name=&color=`;
  `cmd/collabhub` serves it. `e2e/live.js` runs three browsers (owner, editor,
  viewer): shared typing, presence, per-user undo/redo, a viewer's comment.
- `domrt` no longer re-inserts nodes that are already in place when patching
  children; doing so took the focus away from the hidden text input on every
  re-render of a live document.
- In view mode a drag does not select text (a double click does); the
  comment-only target reads that selection.

## Access and hardening since

- Who may call the hub is a `guard.Guard` (`Config.Guard`; `GOJIBERRY_COLLAB_ALLOW_ORIGINS` / `GOJIBERRY_COLLAB_TOKEN` for `cmd/collabhub`): allowed origins, a token, both, or your own check. See [server-access.md](server-access.md).
- With a `User` function, a replica id is tied to the signed-in user who first used it in a document; another user sending ops, presence or opening a stream under it gets 403. Anonymous callers cannot be bound.
- A viewer's operations are bounded: counters (op, mark and character ids) may not run far past the document's clock, marks may not clear comments, records are size-limited.
- Shutdown: `Hub.Stop` refuses writes and ends streams, then the server drains, then `Hub.Close` saves.
- Two clients that open an empty document together and start from the same content write it as the replica `seed`, so their operations are identical and merge into one copy (different starting content from the two is still both kept).
- When a client reconnects to a hub that answers with less than it holds (a restart that lost recent saves), it sends its whole document again; operations are idempotent. Viewers cannot, and their comment marks are not resent.
- Several comments can sit on the same text (`Format.Comment` holds their IDs, comma-separated); comment and note IDs carry the replica's prefix so concurrent comments never collide; a commenter's records must name them as author when the hub has a `User` function.
- A session repairs itself when the server refuses something it sent (`Session.Repair`, called by `Poll` unless `NoRepair`): the replica is dropped, a new one joins from the server's copy, and the flow shows what the server has; the refused edits are lost.
- Inline images are shared by URL (see `docfile.ImageItem`); an image that cannot be saved (no URL) is refused by the bridge. Embed images as `data:` URLs only when small: they travel inside the document's operations.
- `hub.ACLRolesWith(store, who, noACL)` chooses what everyone may do on a document that has no ACL (`GOJIBERRY_COLLAB_NO_ACL_ROLE=owner|editor|viewer|none` for `cmd/collabhub`); `none` makes only shared documents open.
- Presence is throttled at both ends: a client sends at most one cursor update per `client.Options.PresenceInterval` (default 100ms) and the hub passes on at most one per replica per `hub.Config.PresenceInterval` (default 100ms, `GOJIBERRY_COLLAB_PRESENCE_INTERVAL` for `cmd/collabhub`; negative turns it off). Updates inside the interval are merged and the latest always goes out when it ends, so a cursor dragged across the text costs a few messages a second and the final position is never lost. Newcomers are told each cursor's latest position at once.

## Transports and the `Collab` element

- A session talks to the hub through a `collab.Transport` (`collab/link`: `Dial(client.Options) Conn`, where a `Conn` delivers events, sends ops and presence and reports what is still waiting). `collab.HTTPEndpoints()` is the default (SSE `GET /docs/{id}/events` plus `POST /ops` and `/presence`); `ws.WebSockets()` (`collab/ws`, own import so the root module stays dependency-free) uses one socket at `GET /docs/{id}/ws` carrying JSON `wire.Frame`s (`hello`, `snapshot`, `ops`, `presence`, `ack`, `reject`). Both go through the same hub checks (guard, roles, replica binding, throttling, authorization, comment authors), and the same session scenarios run over each.
- A browser WebSocket cannot set headers, so under `js` the sign-in token goes in the query as `access_token` (the hub treats it as `Authorization: Bearer`) and a guard token as `guard_token` (`guard.Options.TokenQuery`). Native clients send headers.
- `collab/gox.Collab` wraps canvas elements and makes the text flow inside shared: `Transport`, `Client` (hub URL, document, headers), `Name`, `Color`, `OnState` (status, role, peers, pending, repairs, error). The server's role is applied to the stage. `Flow` chooses what is shared: only `textflow` is implemented; `header`, `footer`, `inserts` and `*` report `ErrFlowNotSupported` until their bridges exist. Content props only seed the document, and a deterministic seed replica means two tabs starting from the same content merge into one copy. The showcase opens as a shared document with `?doc=…&transport=http|ws`.
