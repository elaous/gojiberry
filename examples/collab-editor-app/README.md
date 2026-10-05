# collab-editor-app

A live, multi-user document editor demonstrating `components.Collab`
paired with real real-time sync: two or more browser tabs open on the
same URL genuinely see each other's edits, over a real WebSocket, with
each tab's permissions (edit/view/delete/share/comment) determined by
its own role.

It's a separate Go module (like `examples/canvas-app`), `replace`-ing
`github.com/elaous/gojiberry` with the local checkout, so it depends on
[`nhooyr.io/websocket`](https://nhooyr.io/websocket) without pulling
that into the root module.

## How it works

- `cmd/server` is a plain `net/http` server: it serves `static/` (the
  page, `main.wasm`, `wasm_exec.js`, the components CSS the client
  needs) and handles `/ws` — a small in-memory hub that relays every
  edit to every *other* connected client and remembers the document's
  current content so a client joining later starts in sync instead of
  from a blank page.
- `client/main.xg` is the WASM half: it opens `/ws` via
  `domrt/browser.Connect`, reads its role from the page's own URL
  (`?role=owner`/`editor`/`viewer`), and renders the shared document
  inside `components.Collab`. Typing sends the whole current text over
  the socket; an incoming message replaces the local content and
  re-renders. Collab only gates the surrounding affordances
  (Delete/Share only render for `CollabOwner`, both need `CanEdit` for
  the document itself to accept input) — the client makes a
  non-editor's `Textarea` genuinely `Disabled`, since Collab's own
  read-only styling is cosmetic and can't reach into caller-supplied
  `Content` to disable it (see `Collab`'s doc comment).
- The sync strategy is deliberately the simplest one that's still
  genuinely real-time and correct for a single shared document: every
  edit is the document's entire current text, not a diff or an
  operational transform. Last edit wins if two people type at the
  same instant — a real product would want OT/CRDT merging, well
  beyond this demo's scope.

## Running it

```
cd ../.. && go run ./cmd/xgc examples/collab-editor-app   # from the repo root, transpiles client/main.xg
cd examples/collab-editor-app
GOOS=js GOARCH=wasm go build -o static/main.wasm ./client/
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" static/
cp ../../components/base.css ../../components/button.css ../../components/textarea.css ../../components/collab.css static/
go run ./cmd/server
```

Then open `http://localhost:8096/?role=owner` in one tab and
`http://localhost:8096/?role=editor` (or `?role=viewer`) in another —
edits in one appear in the other live, and each tab's Delete/Share
controls (or lack of them) reflect its own role.

Verified against two (and three) simultaneous real headless-Chromium
browser contexts: role-based UI gating (owner sees Delete/Share,
viewer doesn't, viewer's textarea is genuinely `disabled` not just
visually muted), a live edit from one tab appearing in another purely
from the WebSocket broadcast, a late-joining third client receiving
the document's current state immediately instead of a blank page, an
edit from that third (editor) client propagating to both others, and
Delete clearing the document live everywhere.


## What the roles are (and are not)

`?role=viewer` is read from the page's own URL and only changes what the client draws:
a viewer's textarea is disabled and the Delete/Share buttons are gone. **The server does
not enforce it**: it relays whatever any connected socket sends, so anyone who can reach
the server can edit. That is what a demo with no sign-in can honestly do; real role
enforcement needs the signed-in identity checked on the server (see the `collab/hub`
package, whose access lists are checked there).

The server listens on `127.0.0.1:8096` (set `ADDR` to change it) and only accepts
WebSocket connections from pages it served itself (set `ALLOWED_ORIGINS` to allow
others), so another website open in your browser cannot reach it through localhost.
