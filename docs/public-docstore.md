# A public docstore: no account, one document, 24 hours (draft 1)

A free, anonymous way to try opendocs and to share a document: open the editor, write,
share the link. No sign-up. The document lives **24 hours from its last edit** and is
then deleted. The offer is part of the public product; the terms say so plainly.

## 1. Behaviour

1. **No account.** A visitor gets an anonymous session (a random token kept in the
   browser). It is not an identity and holds no personal data.
2. **One document per session.** Opening the editor again continues that document.
   Starting another asks to download or discard the first.
3. **24-hour life, renewed by editing.** Every saved *edit* sets the expiry to 24 hours
   from then. **Viewing does not renew**, and neither does keeping the page open, so a
   link cannot be kept alive for ever by being read. After 24 hours with no edit the
   document and everything attached to it are deleted. Edits by collaborators renew it
   too.
4. **A link is always offered**, as two links: *edit* and *view only*. A link is a
   capability (an unguessable 128-bit ID), not an account. The view link cannot renew.
5. **Download is always offered**, in every format the editor has (`.docx`, `.open`,
   PDF…), with a visible countdown ("Deleted in 3 h 12 min unless edited") and a warning
   in the last hour. There is no e-mail to remind anyone, so the page is the reminder.
6. **Upgrade path.** "Keep this document": sign in or sign up and the document moves
   to a real store, keeping its link.

## 2. What to say, and what not to say

1. "We hold no user data" is not accurate while a document exists: the document is
   the visitor's data, and may contain other people's. The accurate promise, and the one
   the contract should make: **no account, no profile, no tracking; the document and its
   link are deleted 24 hours after the last edit.** Say what logs exist (below) and for
   how long.
2. **Backups and copies.** If the store is backed up, the backups outlive the 24 hours.
   Either the public store is not backed up, or backups are kept no longer than a stated
   short time and the contract says so. Prefer an in-memory or ephemeral volume store
   with no backup: an outage then loses documents, which the page already warns about
   by pushing downloads.
3. **Logs.** Keep request logs without IP addresses or with a short life (say 24 hours),
   and say which.

## 3. Abuse

An anonymous upload with a public link is what spammers and worse look for. Before
launch, not after:

1. **Only documents.** The editor's own formats, size capped (a document, its images in
   total, a few MiB), no arbitrary file hosting, no executable content, links rendered
   as links.
2. **Limits** per session, per IP range and globally; creating a document costs a
   little proof of work or a challenge when a range is busy.
3. **No indexing**: unguessable IDs, `noindex`, no listing, no search.
4. **Reporting and removal.** A report link on every shared view; a takedown path that
   deletes by ID; a documented response time. The operator is a hosting provider in the
   eyes of the law (notice and action duties differ by country); this needs a lawyer's
   read, not only this document.
5. **One per session is soft.** Clearing storage gives a new session, so the real limits
   are the IP-based ones in 2.

## 4. Implementation sketch

1. **Expiry in the store: done** (`docstore.ExpiringAdapter`, generic over every `Adapter`).
   - The lifetime is an injected `ExpiryPolicy{TTL(id, now)}` (`FixedTTL(24*time.Hour)` is the
     public offer; per-tenant or per-size policies plug in the same way) and an injectable clock
     (`Now`). `Save` and `SaveIf` renew; `Load` and `List` do not; an expired document is
     invisible at once (404, left out of listings) before `Sweep` deletes it, and a delete of one
     answers 404.
   - The deadline is a side record `expiry:<id>` (one line: the deadline and nothing else, the
     format every older build parses; the creation time for `MaxAge` is a sibling record
     `expiry-created:<id>`, see the upgrade notes in the roadmap) written through the same adapter, so nothing
     is needed from memory, local files, SQLite or the nested backends beyond the wrapper, and
     `SaveIf`, `Locker`, `PrefixLister` and the quota report pass through. Order: Save writes the
     record, then the document (a failed write puts the old record back, so failures do not
     extend a life); `Sweep` deletes the document, what belongs to it (`Related`, replaceable:
     its `acl:`, `collab:` and `keys:` records and its `versions/` snapshots) and the record
     last, so a crash leaves only an orphan record (dropped once past its deadline) and never an
     unswept document. Related records are shared by documents with the same stem (`a.docx` and
     `a.pdf`, or two saver generations `<id>~<gen>.ext`): they are deleted only when no other
     live document has that stem, and version history is matched by direct children only
     (`versions/a/` is not `versions/a/b/`). `Delete` of a live document removes the same records,
     so a new document with the same id inherits neither an access list nor history.
   - Only plain documents expire (`docstore.IsDocumentKey`, an allowlist; `FixedTTL` uses it).
     Every system keyspace is exempt: `acl:`, `collab:`, `catalog:`, `versions/`, `directory/`,
     `directory-log/` (the key directory's transparency log), `directory-enroll/` (owner enrollments), `ratelimit/` (shared rate limit budgets, `docstore.StoreLimiter`),
     `passkey/`, `public/` and `report/` (the public offer's records, tombstones and reports, section 7), `folder-secret/` (a confidential folder's real name: it lives as long as the folder), `keys:` (wrapped document keys: losing one makes an encrypted document
     unreadable), `fonts/`, `expiry:`, and any `name:...` key. A test scans the repository for
     keyspace constants, so a new one fails the build until it is listed.
   - Listings read the deadlines from an in-memory copy (invalidated by this process's writes,
     at most 5 seconds old; `DeadlineCacheTTL`), so a listing is not one read per record. Another
     process's write shows in this process's listings after at most that bound; `Load`, `Save`,
     `Delete` and `Sweep` always read the record. Deadlines are compared with each process's own
     clock: keep clocks of processes sharing a store within seconds of each other (NTP).
   - `Run(ctx, interval)` sweeps in the background. `Server.WithExpiry(e)` serves the wrapper:
     reads and writes answer `X-Gojiberry-Expires` (RFC 3339), listings carry `expires`, `GET`
     never renews. The `docstore` command turns it on with
     `GOJIBERRY_DOCSTORE_EXPIRY_TTL` (e.g. `24h`) and `GOJIBERRY_DOCSTORE_EXPIRY_SWEEP`
     (default 5 minutes, at most the TTL). The `expiry:` keyspace is reserved from `/docs`.
   - Enabling expiry on a store that already has documents: a document with no record is
     adopted by the first `Sweep`. By default it gets a full grace period (deadline now + TTL),
     so nothing is deleted for one TTL, and the number adopted is logged. Set
     `GOJIBERRY_DOCSTORE_EXPIRY_ADOPT=updated` to use `UpdatedAt + TTL` instead: documents
     older than the TTL are then deleted by that first sweep, so take a backup first.
   - **Collaboration-hub edits renew: done.** The hub keeps its state under `collab:<id>`
     (system keyspace, never expires by itself, deleted with its document by the sweeper and by
     `Delete`: `DefaultRelated`), so it never touched the document. Now `hub.Config.Renewer`
     (`docstore.Renewer`, `Renew(ctx, docID) error`) is called after an *accepted* batch of
     operations from an owner or editor, on POST or WebSocket, at most once per
     `RenewInterval` per document (default one minute; a fake clock via `Config.Now`), in the
     background so a slow store never delays an ack. Joining, watching, snapshots, cursors,
     empty or refused batches and a viewer's comments never renew. An error (document gone,
     store down) is logged and never fails the edit.
     - `ExpiringAdapter.Touch(ctx, id)` (also `Renew`) is the implementation: the deadline
       record becomes now + TTL and nothing else is written, so there is no order to get wrong
       (a failure or crash leaves the old or the new deadline, the document untouched). It is
       `ErrNotFound` for a document that does not exist, has expired (not revived, not purged;
       the sweeper does that) or is only a leftover record. `id` is the document key; if no key
       has that exact name, the live saver generation keys of that logical id
       (`<id>~<gen>.<ext>`) are renewed.
     - **One process** (`cmd/collabhub` with `GOJIBERRY_DOCSTORE_EXPIRY_TTL` set): it wraps the
       store with `docstore.WithConfiguredExpiry` (the same wrapper, sweep and adopt settings as
       the docstore server) and passes the `ExpiringAdapter` to the hub as its Renewer.
     - **Separate processes**: the docstore server serves `POST /docs/{id}/renew` (204 and
       `X-Gojiberry-Expires`; **404 with the header `X-Gojiberry-Gone: 1` and the body
       `{"error":"gone"}`** if the document does not exist or has expired; 501 if the store
       does not expire; `collab:`, `acl:`, `expiry:` and `expiry-created:` ids are 400). It needs the permission
       of a PUT and nothing weaker: owner or editor on a document with an access list, so a
       viewer, a stranger or an anonymous caller is 403/401; a document nobody has shared is
       open, as for a PUT, and with `RequireACL` it is closed (a renewal never creates the
       access list a first PUT does). The hub (`GOJIBERRY_COLLAB_RENEW_URL`, optional
       `GOJIBERRY_COLLAB_RENEW_TOKEN` sent as `X-Gojiberry-Token` for the server's guard) calls it
       through `docstore.HTTPAdapter.Renew`, forwarding the `Authorization` header of the edit
       that caused it, so the server checks that editor's write role. **Public sessions cannot
       use it**: the session token of the public offer is not an `auth.Manager` sign-in, so on a
       document with an access list it is nobody (401/403); the public documents' own keys
       (`pubdoc/`) are hidden from `/docs` (400); and with the generic API closed, which is the
       default in public mode, the route is a plain 404. Nor can the hub reach a public
       document: it refuses a document id with a slash, so no room can be named `pubdoc/<id>`.
       Tests: `TestPublicSessionTokenCannotUseTheRenewRoute`,
       `TestPublicSessionTokenHasNoRoleInTheHub`. (A hub with no sign-in is open to everyone
       on a document without an access list, as documented for the hub; that is its design,
       not the public offer's.)
     - **Only the marked 404 means "gone".** `HTTPAdapter.Renew` returns `ErrNotFound` solely
       for the 404 that carries `X-Gojiberry-Gone: 1` and `{"error":"gone"}`. Any other 404 (a
       reverse proxy during a docstore deploy, a base URL that changed, `/docs` closed in public
       mode, a docstore older than the route) and a 405 or 501 is `ErrRenewUnsupported` (also a
       `*StatusError`): a plain failure, logged once per adapter with advice and counted in the
       hub's `renewFailures`; it never closes a room. Renewals then do not happen, so such a
       deployment must be fixed (the log line says how): documents expire under active editing.
     - **Expired under an open room: done.** After one successful renewal, a "not found" that
       is **confirmed** means the document expired and was swept while a stale tab kept its room:
       the hub asks the Renewer once more after `hub.Config.GoneConfirmDelay` (default 2 s) and
       closes only on a second "gone" (a success in between keeps the room and says so in the
       log). The hub then closes that room: further operations get **410 Gone** with a plain message (a WebSocket is closed
       with code 4410 and the reason), it stops saving, deletes `collab:<id>` through the store,
       leaves the hub, and refuses the id with 410 for an hour (so a reconnecting tab does not
       start a new room that writes the content back). A 404 *before* any success (a document that
       lives only in the hub, never saved to the store) never closes anything, and is logged
       **once per room** and counted once (`hubOnlyRooms` in `/healthz`), then silence for as long
       as it is edited. The edit that discovers the expiry is still acknowledged: renewals are
       asynchronous, so a slow store never delays an ack.
     - **The tombstone clears when the document returns.** The hub's one-hour memory of a closed
       room (410) is not blind: when an *editor's* request names the id, the hub asks the Renewer
       (with that editor's credentials); a success (the document was deleted and re-created, or a
       late renewal landed) clears the tombstone and counts `revivedRooms`. A viewer's request
       cannot clear it (it may not renew) and keeps getting 410 until an editor opens the
       document; a document that is still gone stays 410.
     - **Hub-only documents are swept: done; opt-in in general, ON in public mode.** A document that exists only in the hub has
       nothing to renew and no deadline. With `GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS=collab` the
       sweeper (`ExpiringAdapter.Orphans`, `docstore.CollabOrphans(grace)`) deletes a `collab:<id>`
       entry when no live document has that id (no exact key, no live generation) and the entry's
       own `UpdatedAt` is older than TTL plus a grace period (`GOJIBERRY_DOCSTORE_EXPIRY_ORPHAN_GRACE`,
       default 1h). Every hub save bumps `UpdatedAt`, so a hub-only document that is being edited
       stays; the check is repeated under the write lock, so a save never races the delete.
       **Sweep policy in public mode.** With `GOJIBERRY_DOCSTORE_PUBLIC=true` the orphan sweep
       is **on by default** (`docstore.ConfigFromEnv`: `ExpiryOrphans` true, grace 1 h unless
       `GOJIBERRY_DOCSTORE_EXPIRY_ORPHAN_GRACE` says otherwise), because the offer promises that
       nothing outlives its deadline and the state of a document that only ever lived in the hub
       has no deadline of its own. Without public mode it stays off unless
       `GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS=collab`. `GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS=off` turns it
       off in public mode too (logged as a `WARNING`, since it breaks the promise for hub-only
       state), and a grace beside `off` is a startup error. A hub process that shares the store
       (`cmd/collabhub` reads the same variables) gets the same default when it is started with
       `GOJIBERRY_DOCSTORE_PUBLIC=true`. The orphans swept are counted in `Sweep`'s result.
     - **`MaxAge`: done, default off** (`ExpiringAdapter.MaxAge`, `GOJIBERRY_DOCSTORE_EXPIRY_MAX_AGE`).
       An absolute cap counted from creation: renewals (`Save`, `SaveIf`, `Touch`) never move the
       deadline past `created + MaxAge`, so a document edited for ever still expires. This bounds
       junk-edit keep-alive for the public offer (a 24 h life with a 7-day cap, say). The creation
       time is the sibling record `expiry-created:<id>` (written before the deadline record,
       deleted before it; a creation record without a deadline record, left by a crash, is
       dropped by the sweep, and a new document never inherits one). A `Save` of a document that has
       reached the cap starts a new one (old content, versions and access list are purged), the
       same as for an expired one; so the cap bounds one document's life, not what a client that
       re-creates it does (that is the per-session limits' job, section 3).
     - **Touch renews exactly one thing.** The exact key if a live document has it; only when
       there is none, the newest live saver generation of the logical id (by `UpdatedAt`, with
       the keys of the same generation). A stale generation left by a failed cleanup of the saver
       is therefore not kept alive by renewals meant for the current one; it expires on its own.
     - **Ids ending in `/renew` or `/acl` are reserved.** The router decodes `%2F`, so such an id
       would be taken for the renew or ACL route of another document; any request naming one
       (PUT, POST, ...) is 400 with "a document id may not end in /renew or /acl". Reserved ids
       (`collab:`, `acl:`, `expiry:`, `directory/`) are matched case-insensitively (`Collab:x`
       and `EXPIRY:x` are 400 like the lowercase forms).
     - **The hub's roles and the docstore's access rule must match.** `GOJIBERRY_COLLAB_NO_ACL_ROLE`
       owner/editor against a docstore with `GOJIBERRY_DOCSTORE_REQUIRE_ACL=true` makes every renewal
       403 and the document expire under active editing. The first 401/403 per document is logged
       as a `WARNING` that says so, and `GET /healthz` on the hub reports `renewFailures`,
       `renewDenied` and `expiredRooms` (behind the hub's guard; `Hub.Health()` in process).
     - **Renewal credentials.** The renewal carries the `Authorization` of the edit; on a long-lived
       WebSocket that is the header of the upgrade request, whose token expires, so
       `hub.Config.AuthFunc` (called per renewal with that request) lets a deployment look up the
       current token. The renew interval is clamped to TTL/4 when the hub knows the TTL (in-process;
       `GOJIBERRY_COLLAB_EXPIRY_TTL` says it for a remote docstore; `GOJIBERRY_COLLAB_RENEW_INTERVAL`
       sets the interval) and a configured interval longer than the TTL is logged as a warning.
       A panicking renewer is recovered and logged. `GOJIBERRY_COLLAB_RENEW_URL` is logged with its
       user name, password, query and fragment removed, error messages are redacted the same way,
       and `GOJIBERRY_COLLAB_RENEW_TOKEN` is never logged (tests capture the log).
     - Residual limits, stated plainly: two server processes on one store can race at the instant of
       expiry (a `Save` in the same second as the deadline may be deleted; the locks serialize one
       process only); backups of the store are outside this (see 2.2); the edit that discovers an
       expiry is acknowledged and then lost with the room; a hub-only document is not kept alive by
       renewals at all, only by never being swept while edited (the orphan policy), and without
       that policy its `collab:` state stays; a save that wins a race against the discovery is
       deleted again by it, but if edits stop before any renewal runs the resurrected state waits
       for the orphan sweep; `MaxAge` cannot stop a client from creating a new document; a room is closed only after two "gone" answers `GoneConfirmDelay` apart, so a document that is deleted and re-created inside that window keeps its room (the new document's content is not the hub's state); the hub's tombstone is in memory (a restart forgets it, and a stale tab can then open a new room), and only an editor's request clears it; after a rollback to an older build the `expiry-created:` records stay and `MaxAge` is not enforced.
2. **Anonymous sessions: done** (`docstore/public`, section 7). `POST /public/session` issues an
   unguessable bearer token (256 bits, stored only as its SHA-256) for the user `anon:<random>`
   (`auth.AnonymousUser`; `anon` is a reserved provider name): no e-mail, no profile, a bounded
   lifetime, and the server keeps `session -> document` itself (one document per session, enforced
   with an atomic claim). The token opens nothing outside `/public`: it is not an `auth.Manager`
   session, so `/docs` and its ACLs never see it.
3. **Links: done.** A document has an edit capability (the session token, carried in the edit link's
   fragment) and a separate unguessable **view ID** (128 bits, derived one way from the document ID,
   which a viewer never sees). `GET /public/view/<id>` is read-only, never renews, and reveals
   neither the token nor the owner nor the document ID. (The links are not `/d/{id}` pages yet: that
   is the editor's routing, not the store's.)
4. **Collaboration** runs through the existing hub with the same expiry; an edit through
   the hub renews (done, see 1).
5. **Encryption** is the natural fit: with the key in the link's fragment (`#key`, never
   sent to the server) the server holds only ciphertext and "we hold no user data" becomes
   literally true. That needs the encrypted-collaboration work (see
   `encryption-and-passkeys.md`, A.4), so the first version is plain documents with the
   honest wording above, and encrypted mode follows.
6. **Tests.** Time is injected (a fake clock): edit renews, view does not, expiry at
   24 h, the sweeper deletes, a download after expiry fails, limits hold.

## 5. What the abuse controls still need

Built, server side, in `docstore/public` (section 7): the size cap and per-session write rate,
per-address and global limits, the daily byte budget per address, the anonymous session token and
the `session -> document` rule, the view-only ID, the report route and delete-by-ID takedown with
a tombstone, a content `Blocklist` hook and a `Challenger` hook. Still to do before launch:

1. **The legal read of the terms** and the terms text itself (the server only sends the link,
   `GOJIBERRY_DOCSTORE_PUBLIC_TERMS_URL`, as `Link: <url>; rel="terms"`); which jurisdiction; the
   documented response time to a report.
2. **Real abuse operations**: who reads `report/` and how they are told (a notifier, a queue, a
   page), a tool to list reports and take down by ID (today: read the `report/` records and call
   `DELETE /admin/takedown/<id>`), evidence handling (a takedown deletes the content; reports keep
   no copy, so what is preserved for authorities, if anything, is a legal decision), repeat
   offenders by address (the takedown bans the session, not the address), and a hash list fed by
   takedowns.
3. **A challenge when busy**: `public.Challenger` is the hook (before a session and before a
   document are created); no captcha or proof of work ships.
4. **"Only documents"**: the content check is the `Blocklist` hook and the size cap; checking that a
   body is one of the editor's own formats (and has no executable content) is a codec question and
   is not done here.
5. **Shared infrastructure for replicas**: `SessionStore` and `Quota` have memory defaults
   only (the request limiters can live in the store, `Options.LimiterFactory`). Behind several
   replicas, plug in shared ones (the extension table below), or the caps are per replica. Two replicas also race at the instant of expiry (section 4.1).
6. The client side: the editor's session handling, the edit and view links, the countdown, "Keep
   this document", the report link on a shared view, `noindex` on the pages that serve it (the
   routes themselves send `X-Robots-Tag: noindex` and `Cache-Control: no-store`).
7. Reports are bounded per document (20), deduplicated per (document, reporter, category), rate-limited
   per address, bounded as a whole (`_MAX_REPORTS`, `_MAX_REPORT_BYTES`), with slots reserved for new
   reporters and a priority list of report *categories* (`csam`, `illegal`) that is evicted last, with its own caps (section 7.2, items 12 and 17).

The retention half is done, including the hub-only sweep and the age cap; **the orphan sweep is ON by default in public mode** (see section 7).

## 6. Open questions

1. Is the lifetime fixed at 24 hours or adjustable per deployment? (Configurable, 24 h
   in the public offer.)
2. Should the session's single document survive a browser restart? (Yes, via the stored
   token, until it expires.)
3. How much abuse tooling is needed on day one, and who answers reports?
4. Which jurisdiction's terms apply, and who reviews them?

## 7. The server side as built (`docstore/public`)

A small package of HTTP routes over an expiring store; `internal/docstorecmd` mounts it when
`GOJIBERRY_DOCSTORE_PUBLIC=true`, and with it off (the default) nothing changes. Everything below
is tested, including a whole-flow test with a fake clock (`docstore/public/public_test.go`).

### 7.1 Routes

| Route | What |
|---|---|
| `POST /public/session` | a session: `{token, userId: "anon:<random>", expires, limits}`; `expires` is the short idle deadline until the first write; 429 per address or per network (issue rate, or too many live sessions), 503 at the global cap or inside its reserve (item 6) |
| `POST /public/doc` | create the session's one document (409 if it has a live one; 403 if its document was taken down; 410 if it was taken down while being created); returns `{id, viewId, expires, versions}`; 413 over the size cap, 408 for a body that arrives too slowly, 429 over the rate or the byte budget, 503 when the global storage budget or the in-flight cap is used up (item 19) or while the counters are unknown after a failed start (item 8) |
| `PUT /public/doc` | an edit; renews the deadline (`X-Gojiberry-Expires`); same refusals as a create, plus 403 past the save cap; an expired document is 404, never revived |
| `GET /public/doc` | the owner's download; never renews; 404 after expiry, 410 after takedown |
| `DELETE /public/doc` | discard it; the session may then start another |
| `GET /public/view/<viewId>` | read-only; never renews; 410 once taken down |
| `POST /public/report` | `{"id": "<view id or doc id>", "reason": "...", "category": "csam\|illegal\|spam\|other"}` -> 202; stored as `report/<id>` unless it is a duplicate or finds no slot (the answer is 202 either way; item 17); an unknown or missing category is `other`; 503 while the reporter salt cannot be read or created (item 17) |
| `GET /public/health` | `{"ok":true}` with 200, or `{"ok":false}` with 503, and nothing else: no figures, no problem names, no sweep time (they would tell an attacker when the offer is full or its sweeper is down); point a load balancer or an outside monitor at it |
| `GET /admin/health` | the detailed report, behind the admin guard (header `X-Gojiberry-Admin-Token`): `{ok, problems[], lastSweep, storageBytes, storageMax, priorityBursts}`, 200 or 503 with the problem names (`expiry-disabled`, `no-deadline`, `sweeper-stale`, `sweep-failing`, `storage-full`); no secrets and nothing per document; point the operator's monitoring at it |
| `DELETE /admin/takedown/<id>` | operator (header `X-Gojiberry-Admin-Token`; item 18); deletes the document, its records, writes the tombstone; the view ID answers 410 and the session cannot start another document; idempotent (204); an ID no record names (expired and swept, or being created) is tombstoned all the same and answers 202; `?reason=` is kept |

Every public response carries `X-Robots-Tag: noindex, nofollow, noarchive`, `Cache-Control:
no-store`, `Referrer-Policy: no-referrer`, `Access-Control-Expose-Headers: X-Gojiberry-Expires,
Retry-After, Link` (also on the generic `/docs` routes, so a script on another origin can read the
countdown and the wait after a 429 or 503) and, with a terms URL, `Link: <url>; rel="terms"` (and
the same with `rel="terms-of-service"`, the registered name); document responses carry
`X-Gojiberry-Expires`.

### 7.2 Decisions

1. **Lifetime of the records.** The document is a plain expiring document (`pubdoc/<id>`). Its
   record (`public/doc/`) and view-ID record (`public/view/`) live in the system keyspace
   `public/`, which the expiry sweeper never touches, so `Service.Sweep` deletes them when the
   document is gone (and a view request deletes them lazily); in between they hold only IDs.
   **Reports and tombstones do not expire with the document**: a report must outlive it (the
   operator may read it later) and a tombstone must keep answering 410. Each has its own TTL,
   default 30 days (`_REPORT_TTL`, `_TOMBSTONE_TTL`), enforced by the same sweep. A tombstone only
   has to outlive the longest life of the view ID (24 h plus the sweep interval), so 30 days is
   generous; shorten it to taste.
2. **One session, one document, even after takedown.** The session keeps its document ID after the
   document expires, so a new `POST /public/doc` frees it only when the old one is gone; after a
   takedown it stays refused (403) for the life of the session. A session lasts `_SESSION_TTL`
   (24 h) from its last write and `_SESSION_MAX_AGE` (72 h) from issue at most (`_SESSION_IDLE_TTL`,
   1 h, until its first write); reading never extends it. Clearing storage makes a new session, which is why the per-address limits exist.
3. **The 24 h promise is enforced, not assumed.** `public.New` refuses a store that does not
   implement `docstore.Expirer`; `docstorecmd` refuses to start with the public offer on and
   `GOJIBERRY_DOCSTORE_EXPIRY_TTL` unset; and a create whose document got no deadline (a policy
   that skips it) is rolled back with a 500.
4. **The view ID** is `base64url(SHA-256("gojiberry/public/view/v1\0" + docID)[:16])`: unguessable
   because the document ID is random and never shown to viewers, and findable from the document
   alone, so a takedown by either ID works.
5. **A guard token on the server** (`GOJIBERRY_DOCSTORE_TOKEN`) would also apply to `/public`, where
   visitors have no token: a public deployment uses an origin guard (or a separate server) and relies
   on the admin token for the one route that needs a secret.

6. **No client can fill the session cap.** Sessions are bounded per client as well as globally:
   at most `_MAX_SESSIONS_PER_ADDRESS` (10) live sessions per client, 6 issued per hour
   (`_SESSIONS_PER_HOUR`, a token bucket), and a session that has not stored a document lives
   only `_SESSION_IDLE_TTL` (1 h) until its first write extends it to `_SESSION_TTL`. One address
   can therefore hold at most 10 of the 10,000 places, however often it asks (it was 14,400 a day
   with 24-hour sessions that were never reaped). `Service.Sweep` reaps expired sessions
   (`SessionReaper`), so they do not wait for the cap to be reached. A document-less session must
   store its document within the hour; the editor should do so as it opens (the session response's
   `expires` says when).

   Two more limits close what per-/64 caps leave open. A **second-level cap per network**,
   `_MAX_SESSIONS_PER_NET` (100): live sessions per IPv6 /48 or IPv4 /24 (`public.NetKey`), 429
   above it, so an attacker holding a /48 (65,536 /64s) holds at most 100 places, not all 10,000.
   And a **reserve** for newcomers, `_SESSION_RESERVE_PERCENT` (20, `0` for none): once the live
   sessions reach 80% of `_MAX_SESSIONS`, only a visitor whose /48 or /24 has no live session yet is
   admitted (503 otherwise), so a flood from networks that already hold sessions cannot starve a
   legitimate new visitor. Residual: the reserve does not tell a botnet from visitors. Many networks,
   each with no session yet, each take one place of the reserve; and a second visitor on a network
   that already holds a session (a shared IPv4 /24, a campus) is refused while the offer is above 80%
   full, which is what the reserve costs. Behind carrier-grade NAT raise the net cap.

   A **third level** closes the same gap one step up. An attacker holding a /32 (65,536 /48s) is
   held by no per-/48 cap, and moving between /48s it looked like a new network every time, so it
   could also spend the reserve. `_MAX_SESSIONS_PER_WIDE` (500; never below the per-net figure nor
   above `_MAX_SESSIONS`) caps the live sessions per IPv6 /32 or IPv4 /16 (`public.WideKey`), 429
   above it, and **the reserve is judged at the coarsest level**: inside it only a visitor whose
   /32 or /16 has no live session is admitted. The price is that a second visitor of an ISP that
   already holds a session is refused while the offer is above 80% full; raise the reserve down (`0`)
   or the caps for a deployment that is mostly one large provider. Residual, named plainly: a
   **botnet** with addresses in many different /32s and /16s is a crowd of newcomers; each takes one
   place of the reserve and each address is still held to its own per-address cap and issue rate,
   but nothing here tells it from visitors. A `Challenger` is the answer to that.
7. **A client is an IPv4 address or an IPv6 /64** (`public.ClientKey`), for every limiter and the
   byte quota: session issue, writes (per session and per client, so new sessions do not buy
   write budget), views, reports, admin, the daily bytes and the live-session cap. A subscriber who
   rotates through the addresses of its /64 is one client. Only the second-level session cap and the
   reserve (item 6) look at the wider network (`public.NetKey`: the /48 or /24, `public.WideKey`: the /32 or /16), and so do the in-flight shares (item 19) and the admin failure counts (item 18).
8. **A global storage budget.** `_MAX_TOTAL_BYTES` (1 GiB) bounds the bytes of live public
   documents (and the version snapshots a store keeps for them, `versions/pubdoc/`). The service
   keeps a counter that writes reserve against atomically and `Sweep` rebuilds from the store (so
   a restart, or another replica's writes, are caught within one sweep interval; the first write
   after a start rebuilds it too). A write that would pass the budget is answered 503 with
   `Retry-After` before it is stored, and a full store refuses before the body is read; edits count
   only their growth. It does not ask the backend for disk usage (`QuotaReporter`): size the
   budget to the volume.

   How the counter stays right (`guardrails.go`, `budget`). It is a ledger with one entry per
   document, plus the bytes that writes have *reserved* and not stored yet. A write reserves, stores,
   then commits (or cancels), so a reservation is never part of what a reconcile replaces: a sweep
   that lists the store between a reserve and the save cannot drop it. A reconcile opens a window,
   lists, and replaces the ledger with the listing, except that a document a write committed or
   removed while the listing ran keeps the write's word; windows may overlap, each with its own
   record, so two concurrent reconciles (a sweep and the first write) lose no write. Each completed
   reconcile is a *generation*. A discard, a takedown, a rolled-back create or an edit that finds no
   deadline subtracts exactly what the ledger holds for that document, and nothing for a document the
   current generation does not count (one that expired and was recounted away), so no document is
   subtracted twice and the total cannot go negative; an impossible negative (a double refund) is
   clamped to zero and logged. The one inexact case is on the safe side: a write that lands while a
   listing runs can be counted twice until the next reconcile, never missed. If the **first**
   reconcile fails (the store cannot be listed), writes fail **closed**: 503 with `Retry-After` for
   15 seconds (`reconcileBackoff`), then one new attempt, instead of running against a count of zero.
   Reads, views and reports are not held back by it. The first reconcile runs on a context of its own
   (`context.WithoutCancel` with a 2 minute bound), not on the request that happens to start it: a
   client that goes away mid-way (an HTTP/2 stream reset, a closed connection) must not cancel the
   store listing and put every write of everyone into the 15 second backoff. Requests that find it
   running wait for it for at most their own context or `_REQUEST_TIMEOUT`, then are answered 503.

   **Version snapshots** (`versions/pubdoc/<id>/...`, the saver's layout) are written by something
   other than this service. They are added to their document's ledger entry **when they are written**:
   a service over a `*docstore.ExpiringAdapter` sets the adapter's `OnSave` hook (so build the service
   before the adapter is in use), and a deployment that writes snapshots another way calls
   `Service.NoteVersionWrite(key, size)`. A reconcile still rebuilds the figure from a listing, and a
   snapshot written while it lists is kept (it may be counted twice until the next one). The overshoot
   bound, plainly: nothing here can *refuse* a snapshot (its writer is not asking the budget), so the
   budget can be exceeded by the snapshots written after it was reached, until they are deleted
   with their document (a discard, a takedown and the expiry sweep subtract them); what the
   write-time count buys is that the budget stops admitting new documents at once instead of after
   the next sweep (up to 5 minutes of snapshot writes, before). A deleted snapshot is subtracted by
   the next reconcile only.
9. **The generic `/docs` API is closed in public mode.** Anonymous visitors must not be able to
   read or write the store's other documents. `docstorecmd` refuses to start the public offer
   unless `/docs` is closed: by default it closes it itself (`docstore.Server.CloseDocs`: 404 for
   `/docs`, `/docs/<id>`, `/docs/<id>/acl` and `/quota`; `/health`, `/auth/*` and the public routes
   stay), it leaves it to sign-in with `GOJIBERRY_DOCSTORE_REQUIRE_ACL=true` (which counts as
   closed only when an auth manager exists), and it keeps it open only with the explicit
   `GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_OPEN_DOCS=true`, logged as a warning. A value that is not a
   boolean refuses to start. `public.RequireDocsClosed(srv, allowOpen)` is the same check for
   other programs that mount the service.
10. **The client address behind a proxy.** The default client is the connection's remote address,
    which behind a reverse proxy is the proxy: every visitor would share one set of limits (and one
    visitor could lock the rest out). With `_TRUSTED_PROXIES` empty the server logs a warning at
    start; `_TRUSTED_PROXIES=none` says the server is reached directly and silences it. With the
    admin route enabled (an admin token, or a custom `AdminGuard`) an empty value is an error:
    `public.New` returns `ErrAdminNeedsProxyConfig` and the server does not start, unless the
    operator names the proxies, says `none`, or supplies `Options.ClientIP` (item 18).
11. **Slow clients, and the order of checks.** The `http.Server` has a `ReadTimeout` (5 minutes: the
    generic API takes documents up to 64 MiB), and every public request gets its own deadlines
    through `http.NewResponseController`: `_REQUEST_TIMEOUT` (30 s) to send the request, twice that
    to take the answer; a body that is too slow is answered 408. A body of 64 KiB or more declared (or
    of unknown length) must also keep a **minimum average rate**, `_MIN_UPLOAD_RATE` (32 KiB/s) after
    `_UPLOAD_GRACE` (5 s): the reader sets the connection's read deadline to the moment the rate would
    be missed if nothing more came (grace + bytes read / rate, never past `_REQUEST_TIMEOUT`), so a
    client that sends nothing is cut off after the grace, not after 30 s, and a trickle is cut as
    soon as it falls behind (408, `Connection: close`). A write is checked in this order,
    all before its body is read: session, challenge, rate (session and address), one-document
    state (409, 403), declared size (413), counters known (503), storage budget (503), bytes in
    flight (503 in all, 429 per client or network, item 19), daily byte quota (429). The quota
    is charged the declared `Content-Length` (the document cap for a chunked body, so a chunked
    upload near the end of a day's budget may be refused though it is small) and given back
    (`QuotaRefunder`, implemented by `MemoryQuota`) for what is not stored **by the server's doing**:
    the difference once the body is read (a chunked body of a few bytes leaves the quota almost
    untouched), and all of it on a lost claim (409), a failed store or a rollback, a content check
    that is down (503). What the client earned stays charged: a body that was too slow (408), that
    was abandoned half-way, that was too large, or that was refused for its content (422). Before,
    a 408 gave the bytes back, so slow uploads cost nothing. The charge remembers the window it went
    into (`WindowQuota`, implemented by `MemoryQuota`): a refund that arrives after that window
    rolled over is not credited to the new one.
12. **Bounded cost.** A document's liveness is one deadline lookup (`Expirer.Expiry`), never a prefix
    listing; `Sweep` lists by prefix, so over a `PrefixLister` (memory, local files, SQLite, the
    nested backends' adapters that have it) it costs what the public data costs, all of it capped,
    and over a store that cannot list by prefix (every listing is then the whole store) its listing
    part runs at most every `_FULL_SWEEP_INTERVAL` (1 h; a warning at start says so), the other
    calls only reap sessions. Reports are bounded as a whole: past `_MAX_REPORTS` (5000) or
    `_MAX_REPORT_BYTES` (8 MiB) the oldest are evicted, when a report is stored and by the sweep,
    ordinary reports before priority ones (item 17).
13. **Edits are counted before they are saved, and a deadline is verified.** An edit writes the
    record with the version count one higher, then saves, and puts the old record back if the save
    fails: a crash can only leave the count too high (the safe side). After the save it checks that
    the document has a deadline; if not (a policy that stopped covering it) it removes the document,
    answers 500, logs an `ALARM` line and reports `no-deadline` on `/public/health`. At start the
    policy is probed; `expiry-disabled` is reported (and logged) if public documents would get no
    deadline, and `sweeper-stale` when `Sweep` has not completed for twice the interval of `Run`
    (5 minutes if `Run` was never started).
14. **Takedown bans the session for as long as the session lives.** `TombstoneTTL` must be at least
    `SessionMaxAge` + 24 h (a document lives 24 h after an edit and edits need a session): `New`
    and `FromEnv` refuse a smaller value. The ban is the tombstone of the view ID, which a session's
    check derives from its document ID, so it holds even when the document had expired and its
    records had been swept: a takedown of an ID that no record names writes the tombstone for both
    readings of the ID (view ID, or document ID whose view ID follows) and answers 202. A takedown
    that lands during a create is not lost: the create looks for the tombstone after storing the
    document, removes it, keeps the claim (the session stays banned) and answers 410, while a
    takedown that resolves the records afterwards deletes them under the document's lock.
15. **Hidden prefixes ignore case** (`Server.Hide("pubdoc/")` hides `PubDoc/...`), and error text is
    logged without store keys (which hold document IDs): `pubdoc/<id>`.
16. **`MemoryQuota` and its keys.** It remembers at most 100,000 client keys. At that bound it first
    forgets the windows that have ended, then the least recently used keys (not arbitrary ones: an
    active client's window is not reset by a flood of new keys), and logs the drop (at most once a
    minute, with the count). A dropped key's daily bytes start afresh, so a flood of 100,000 distinct
    clients can still reset the quietest ones: put a shared `Quota` in front for a real deployment.
17. **Report slots cannot be used up by one link holder, or by a flood.** Anyone holding a view link
    can report, and a reporter with many addresses can send many reports, so a document's 20 slots
    and the global bound are protected in layers. (a) **Dedupe:** a report is keyed by (document,
    reporter, category, reason), the reason compared trimmed, lowercase, spaces collapsed; a repeat
    counts once and is answered 202 with the first report's id. The document's record keeps a short
    reference per stored report for this. The references follow the reports: a report that is
    evicted (the global bound) or deleted (its TTL) is removed from its document's record under the
    document's lock, and the slots and the dedupe are counted from the reports that still exist (a
    reference whose report is gone is dropped when the document is near full and when a duplicate
    is matched, so a deleted report's id is never handed out and a ghost never takes a slot).
    **The reporter hash** is `HMAC-SHA-256(key, "gojiberry/public/reporter/v1" 0 salt 0 client)`
    cut to 96 bits, `client` being the `ClientKey` and never stored. The salt (`public/salt`) is in
    the store and so is in any dump of it; with it alone all four billion IPv4 addresses could be
    tried, so the **key** is a secret kept *outside* that store: `_REPORT_KEY` (16+ characters), else
    derived (HMAC under its own label) from the directory's signing seed `GOJIBERRY_DOCSTORE_DIRECTORY_KEY`,
    else made for the process with a warning (the dedupe then restarts with the process). "The report
    holds no address" is true as long as the key stays out of the store: do not keep it there. The salt is
    created with a create-if-absent write (`SaveIf` with no previous content), so replicas starting
    together converge on the first one written, and it is never made up for a process when the store
    fails: a failure is retried after a backoff (1 s doubling to 30 s) and the report is answered
    503 until a salt exists. (b) **Reserved slots:** the last 5 of a document's 20 slots
    (`_REPORT_RESERVED_PER_DOC`, `0` for none) are for reporters with no report on that document;
    and once the stored reports pass `_MAX_REPORTS` less 20% (`_REPORT_RESERVE_PERCENT`, `0` for none)
    only reporters with no stored report at all are taken. (c) **Priority:** the report API takes a
    `category` from a fixed set (`csam`, `illegal`, `spam`, `other`; anything else is `other`), and
    only a category in `_REPORT_PRIORITY_CATEGORIES` (default `csam,illegal`; `none` for no list)
    can make a report a priority one; the free-text reason never does. A priority report is exempt
    from both reserves, is never evicted before the ordinary reports (the global eviction, at the bound
    and in the sweep, takes oldest first among the ordinary ones, then among the priority ones), and
    when its document is full takes the place of the document's oldest ordinary report. Three caps
    keep that path from being flooded or used to pin a document: a reporter (the salted hash) has
    `_REPORT_PRIORITY_PER_REPORTER` (3) priority reports per 24 hours, the next ones being kept as
    ordinary reports; priority reports hold at most **half** of a document's slots (10 of 20), so the
    ordinary reports of others still find room; and when priority-category reports from one network
    (`NetKey`) name more than `_REPORT_PRIORITY_BURST` (10) different documents within an hour the
    operator is told: an `ALARM` log line (once per network per hour; it names the network prefix, the
    one place an address prefix is logged) and `priorityBursts` in `GET /admin/health` (a field, not
    a problem: `/public/health` stays 200). The per-reporter counts are rebuilt from the stored
    reports by each sweep. A dropped report is answered 202 like a kept one. Residual: an attacker
    with many addresses still collects its share of the reserved slots one address at a time, can
    name a priority category itself (3 a day per address, half a document), and can spread over networks
    to stay under the burst flag; the layers make that cost one address per report slot, not remove it.
18. **Failed admin authentication is limited per /64, per network and in all; a right token never is.**
    `adminAllowed` checks the token first (constant time, `TokenAdmin`) and only a failure draws on
    a budget: per client (`Limiters.Admin`, a /64 or IPv4 address, 20 a minute), per network
    (`Limiters.AdminNet`, a /48 or /24, 60 a minute) and in all (`Limiters.AdminGlobal`, 300 a
    minute); the three are `_ADMIN_FAILURES_PER_MINUTE`, `_ADMIN_FAILURES_PER_NET_PER_MINUTE` and
    `_ADMIN_FAILURES_GLOBAL_PER_MINUTE`. Past the first two a failure answers 429 with `Retry-After`;
    when the global budget is used up the admin route goes into a **30 second cooldown** in which every
    wrong token is answered 429 (with the time left) and nothing is counted, whatever the network.
    A request with the right token is never counted and never throttled, in the cooldown or out of
    it (before, every attempt drew on the budget, so behind a proxy that was not named wrong guesses
    locked the operator out). Ten failures within a minute are logged (`WARNING`, once a minute at
    most) and the start of a cooldown is an `ALARM`. The same guard protects `GET /admin/health`.
    The per-/64 limit alone left an attacker with a /48 (65,536 /64s) 20 guesses a minute *each*; the
    per-network and global budgets bound that to 60 and 300.

    **What the limits can and cannot do**, plainly: they slow a guesser down, bound the work it can
    ask for and make a burst visible. They cannot make an online guess impossible, because a right
    guess is answered at once however many wrong ones came before, and a distributed guesser with
    enough networks is held only by the global budget. The safety is the token's length. So the
    server **refuses to start** with an admin token shorter than 16 characters (`ErrAdminTokenTooShort`
    from `New`, and an error from `FromEnv`); `GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_SHORT_ADMIN_TOKEN=true`
    overrides it explicitly, and then every start logs a loud `WARNING`. Keep `_TRUSTED_PROXIES`
    right, since the budgets are per client address (see item 10 for the start-up refusal).
19. **Caps on request bodies in flight: in all, per client, per network.** The per-client limits do
    not bound memory when many clients upload at once. `_MAX_INFLIGHT_BYTES` (256 MiB, at least
    `_MAX_DOC_BYTES`) bounds the bytes of bodies being read at the same moment: a write holds its
    declared `Content-Length` (the document cap for a chunked body) from admission until the write
    ends, trimmed to what was actually read once the body is in, and one that would pass the cap is
    answered 503 with `Retry-After` before anything is read or charged. On its own that let a few
    addresses hold the whole cap: each write held up to 4 MiB for up to the request timeout, and a
    408 gave the quota back, so about six addresses could keep 256 MiB claimed for nothing and turn
    every write of everyone else into a 503. So each client (a /64 or an IPv4 address) may hold
    `_MAX_INFLIGHT_PER_CLIENT` (cap/8, 32 MiB) and each network (a /48 or /24)
    `_MAX_INFLIGHT_PER_NET` (cap/4, 64 MiB), never below `_MAX_DOC_BYTES`, answered 429 above them.
    Holding is also short and not free: a large body must keep the minimum rate (item 11), so a stalled
    one is cut after the 5 second grace, and a 408 or an abandoned upload stays charged to the
    client's 50 MiB a day (item 11), so one client can hold a 4 MiB claim only a dozen times a day.
    Residual, stated plainly: the global cap is still shared. Four networks at 64 MiB each, or
    eight /64s at 32 MiB, fill it while they last; what bounds them is the daily bytes (a client that
    stalls is charged the full declared size), the grace, the write rate and the number of addresses
    the attacker has: keeping 256 MiB held around the clock with 4 MiB claims of 5 seconds each, 12
    a day per client, takes on the order of 90,000 addresses. A botnet that large is not stopped,
    only made expensive.
20. **Reserved IDs, in every mode.** Document IDs that start with `public/` or `report/` (in any
    case) are reserved for this package's records and reports in *all* modes, whether or not the public
    offer is on: `docstore.Server` refuses them through `/docs`. An existing document with such an ID
    must be renamed before upgrading (docs/roadmap.md, "Notes for upgraders").

### 7.3 Configuration (`GOJIBERRY_DOCSTORE_PUBLIC_*`)

| Variable | Default | |
|---|---|---|
| `GOJIBERRY_DOCSTORE_PUBLIC` | off | `true` mounts the routes (needs `_EXPIRY_TTL`) |
| `_MAX_DOC_BYTES` | 4 MiB | per document, 413 above |
| `_MAX_TOTAL_BYTES` | 1 GiB | all live public documents (and their version snapshots), 503 above |
| `_MAX_VERSIONS` | 2000 | saves per document (bounds how long editing can keep it alive) |
| `_IP_DAILY_BYTES` | 50 MiB | written per client address per 24 h, 429 above |
| `_SESSION_TTL`, `_SESSION_MAX_AGE` | 24h, 72h | from the last write, from issue |
| `_SESSION_IDLE_TTL` | 1h | a session that has not stored a document yet (the first write extends it) |
| `_MAX_SESSIONS` | 10000 | live sessions, all clients, 503 above |
| `_MAX_SESSIONS_PER_ADDRESS` | 10 | live sessions per client (IPv4 address, IPv6 /64), 429 above |
| `_MAX_SESSIONS_PER_NET` | 100 | live sessions per network (IPv6 /48, IPv4 /24), 429 above; never below the per-address figure |
| `_MAX_SESSIONS_PER_WIDE` | 500 | live sessions per wide prefix (IPv6 /32, IPv4 /16), 429 above; never below the per-net figure nor above `_MAX_SESSIONS` |
| `_SESSION_RESERVE_PERCENT` | 20 | share of `_MAX_SESSIONS` kept for visitors whose wide prefix (/32 or /16) has no live session (`0`: none), 503 otherwise |
| `_MAX_INFLIGHT_BYTES` | 256 MiB | request bodies being read at once, all clients (at least `_MAX_DOC_BYTES`), 503 above |
| `_MAX_INFLIGHT_PER_CLIENT`, `_MAX_INFLIGHT_PER_NET` | cap/8, cap/4 | the share of it one client (/64) and one network (/48, /24) may hold, 429 above; never below `_MAX_DOC_BYTES` |
| `_MIN_UPLOAD_RATE`, `_UPLOAD_GRACE` | 32768, 5s | bytes per second a body of 64 KiB or more (or of unknown length) must average after the grace, else 408 and cut off (the daily bytes stay charged) |
| `_SESSIONS_PER_HOUR` | 6 | sessions issued per client per hour (token bucket, burst = the figure; replaces `_SESSIONS_PER_MINUTE`) |
| `_WRITES_PER_MINUTE`, `_ADDRESS_WRITES_PER_MINUTE`, `_VIEWS_PER_MINUTE`, `_REPORTS_PER_MINUTE` | 30, 120, 120, 5 | token buckets (burst = the figure): per session, per client, per client, per client |
| `_MAX_REPORTS`, `_MAX_REPORT_BYTES` | 5000, 8 MiB | stored reports as a whole; the oldest ordinary ones are evicted beyond either |
| `_REPORT_RESERVED_PER_DOC`, `_REPORT_RESERVE_PERCENT` | 5, 20 | of a document's 20 report slots, and of `_MAX_REPORTS`, the share kept for reporters with no report there (`0`: none) |
| `_REPORT_PRIORITY_CATEGORIES` | `csam,illegal` | report categories (of `csam,illegal,spam,other`) never evicted before others, exempt from the reserves; `none` for no list; the free-text reason never counts |
| `_REPORT_PRIORITY_PER_REPORTER`, `_REPORT_PRIORITY_BURST` | 3, 10 | priority reports per reporter per 24 h (the rest are ordinary); different documents with priority reports from one network within an hour before the operator is told (log, `priorityBursts`) |
| `_REPORT_KEY` | none | secret (16+ characters) keying the reporter hashes; else derived from `GOJIBERRY_DOCSTORE_DIRECTORY_KEY`, else per process with a warning. Keep it out of the store |
| `_REQUEST_TIMEOUT` | 30s | to send a request body (the answer gets twice that); 408 |
| `_FULL_SWEEP_INTERVAL` | 1h | listing sweeps over a store that cannot list by prefix |
| `_REPORT_TTL`, `_TOMBSTONE_TTL` | 720h, 720h | `_TOMBSTONE_TTL` must be at least `_SESSION_MAX_AGE` + 24h or the server does not start |
| `_TERMS_URL` | none | the `Link: rel="terms"` target (an http(s) URL) |
| `_ADMIN_TOKEN` | none | secret of the takedown route and `/admin/health`; without it they are not mounted; at least 16 characters or the server does not start |
| `_ALLOW_SHORT_ADMIN_TOKEN` | off | `true` starts the server with a shorter token (loud warning at every start) |
| `_ADMIN_FAILURES_PER_MINUTE`, `_ADMIN_FAILURES_PER_NET_PER_MINUTE`, `_ADMIN_FAILURES_GLOBAL_PER_MINUTE` | 20, 60, 300 | failed admin attempts per client, per network, in all; the global one used up means a 30 s cooldown for wrong tokens (item 18) |
| `_TRUSTED_PROXIES` | none | CIDRs whose `X-Forwarded-For` is believed (else a caller could pick its own limiter key); empty logs a warning, and with the admin route on it refuses to start; `none` says there is no proxy |
| `_ALLOW_OPEN_DOCS` | off | `true` keeps the generic `/docs` API open beside the public offer (logged as a warning); by default public mode closes it (item 9) |

### 7.4 Extension points

Each control is an interface with a single-process default, passed in `public.Options` (the same
pattern as `auth/passkey`, see [encryption-and-passkeys.md](encryption-and-passkeys.md) B.1):

| Interface | What it does | Default | Plug in |
|---|---|---|---|
| `SessionStore` | live sessions, the global, per-client, per-network and per-wide-prefix caps and the reserve for new networks (`Create` takes `SessionCaps`), the one-document claim (`SetDoc` is a compare-and-set); optional `SessionReaper` | `MemorySessionStore` (10,000 live, expired ones dropped through an expiry heap on every create and by `Sweep`) | Redis, a table |
| `Limiters{Session, Write, WriteAddr, View, Report, Admin, AdminNet, AdminGlobal}` (`passkey.Limiter`), or `Options.LimiterFactory` for all the ones left nil | per-group request budgets, keyed by `ClientKey` (or the session; `AdminNet` by `NetKey`, `AdminGlobal` by one key); the admin ones count failed authentications only | `passkey.RateLimiter` from the Config | `docstore.StoreLimiters(store)` (replicas on one conditional store share the budgets; the names are `public-session`, `public-write`, `public-writeaddr`, `public-view`, `public-report`, `public-admin`, `public-admin-net`, `public-admin-global`, records under `ratelimit/`), a shared Redis budget, a gateway; `passkey.Unlimited{}` |
| `Quota` (optional `QuotaRefunder`, `WindowQuota`) | bytes per client per day, charged before the body is read; `WindowQuota` also names the window so a late refund cannot credit the next one | `MemoryQuota` (fixed 24 h window per client, at most 100,000 clients, least recently used dropped) | Redis, a table |
| `passkey.ChallengeStore` (not a `public.Options` field; `passkey.Config.Challenges`) | passkey ceremonies of the sign-in routes on the same server | `passkey.MemoryChallenges` | `passkey.StoreChallenges` (`GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES=store`, records under `passkey/challenge/<expiry minute>/`), Redis |
| `auth.SessionStore` (not a `public.Options` field; `auth.Config.Sessions`) | the signed-in (not anonymous) sessions and OpenID Connect login states of `auth.Manager` on the same server | `auth.MemorySessionStore` | `sessionstore.Store` (`GOJIBERRY_DOCSTORE_SESSIONS=store`, records under `auth/session/` and `auth/state/`), Redis |
| `Options.ClientIP` | who the client is | remote address, or `passkey.TrustedProxies(cidrs...)` | your function |
| `AdminGuard` | who may take down | `TokenAdmin` (constant-time compare; an empty token allows nobody) | operator sign-in, mTLS |
| `Blocklist` | refuse content before it is stored (422; an error fails closed, 503) | `NoBlocklist`; `HashBlocklist` (SHA-256) | a hash-matching or scanning service |
| `Challenger` | captcha or proof of work before a session and a create (403) | `NoChallenge` | a verifier (interface only, none ships) |
| `Options.Store` | documents and records | must implement `docstore.Expirer` | any adapter in `docstore.ExpiringAdapter` |

Over a limit the routes answer 429 with `Retry-After`.

### 7.4a Which backend supports what

Several features need more than save and load. The extra operations are optional interfaces, and a feature that
needs one warns, falls back or refuses at start-up when its backend lacks it. Read from the code:

| Backend | `ListPrefix` | `LoadHead` | `SaveIf` | `DeleteIf` |
| --- | --- | --- | --- | --- |
| Memory (`docstore.MemoryAdapter`) | yes | yes | yes | yes |
| Local files (`LocalFSAdapter`, one host) | yes | yes | yes | yes |
| SQLite (`SQLiteAdapter`, one host) | yes | yes | yes | yes |
| PostgreSQL (`docstore/backends/postgres`) | yes | yes | yes | yes |
| MongoDB (`docstore/backends/mongo`) | yes | no | yes | yes |
| S3 and compatible (`docstore/backends/s3`) | yes | yes | yes | yes |
| gocloud blob (`docstore/backends/blob`) | yes | yes | no | no |
| Browser storage, browser file system, HTTP client (`BrowserAdapter`, `FileSystemAdapter`, `HTTPAdapter`) | no | no | yes | no |
| `ExpiringAdapter` | yes | yes | yes | yes |

`ExpiringAdapter` offers each operation when the adapter it wraps does; the encrypting adapter does not offer
`LoadHead` (it would hand out ciphertext).

1. `SaveIf` (a save that applies only if the stored bytes are the ones the writer read, or the document is new)
   is what the shared passkey challenge store, the shared sign-in sessions, the store-backed rate limiter,
   conditional saves over HTTP, and the race-free parts of the key directory (the key cap and the log) need.
   Without it, `GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES=store` and `GOJIBERRY_DOCSTORE_SESSIONS=store` warn and fall
   back to per-process state (`store-required` makes that fatal), and `GOJIBERRY_DOCSTORE_LIMITER=store` limits per
   replica.
2. `DeleteIf` (a delete that applies only if the stored bytes are the ones read) is optional for those features:
   single-use records fall back to a conditional "used" marker written with `SaveIf`. It is what the document
   expiry sweep uses to close its last race: the document is deleted on the content that was read, so a save by another process in the deadline instant is kept (roadmap item 10, limits register 10). The S3 backend has it through `If-Match` on `DeleteObject` (the service and MinIO honor it; a compatible service that ignores the header would delete whatever is there). The gocloud blob backend cannot: the portable API has no conditional delete.
3. `LoadHead` (the first bytes of a document) only makes `RepairAll` cheaper on large files; every other feature
   works without it.
4. **The gocloud blob backend cannot be given conditional writes in general.** The portable API can create a blob
   only if it does not exist; there is no ETag or generation match to send, and each provider has its own
   (S3 `If-Match`, Google Cloud Storage generation match, Azure ETag). A create-only `SaveIf` would break the
   contract, which also covers replacing, so the backend does not claim one. For object storage use the S3
   backend; for state shared between replicas use PostgreSQL or MongoDB. A provider-specific write hook is
   possible, but only worth building for a provider someone needs, and it must be tested against that provider.

### 7.5 What is left

State plainly, before launch:

1. The byte budget, the session and per-client counts and the report counters are per
   process (the per-address daily byte budget is the exception: `GOJIBERRY_DOCSTORE_PUBLIC_QUOTA=store` shares it
   through the store with `public.StoreQuota`, fixed UTC days, counters under `public/quota/`; a store without
   conditional writes warns and falls back to memory, `store-required` refuses to start): behind several replicas each has its own (the budget is rebuilt from the shared store
   by each one's sweep, but between sweeps the replicas do not see each other's writes). Plug in
   shared ones (`SessionStore`, `Quota`) or run one replica. The request limiters can be shared
   through the store: the docstore server passes `GOJIBERRY_DOCSTORE_LIMITER=store`'s factory
   (`docstore.StoreLimiters`) to `Options.LimiterFactory`, as it does for the key directory and the
   passkey routes, on a store with conditional writes (otherwise they stay per process). The in-flight
   cap (`_MAX_INFLIGHT_BYTES`) is per process too, which is what it protects (the process's memory).
   Passkey ceremonies (sign-in on the same server) can be shared the same way:
   `GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES=store` keeps them under `passkey/challenge/<expiry minute>/`
   (`passkey.StoreChallenges`, see [encryption-and-passkeys.md](encryption-and-passkeys.md) B.1; a store without
   conditional writes warns and falls back to memory, `store-required` refuses to start; the cap across replicas can
   be exceeded by at most the writes of one sweep interval, the per-client limiter being the real bound). The public
   offer's own anonymous sessions (`public.SessionStore`) are a different thing and stay per process. The bearer
   sessions and login states of `auth.Manager` (sign-in on the same server) can be shared the same way:
   `GOJIBERRY_DOCSTORE_SESSIONS=store` keeps them under `auth/session/` and `auth/state/`
   (`auth/sessionstore`, see [encryption-and-passkeys.md](encryption-and-passkeys.md) B.1; a store without
   conditional writes warns and falls back to memory, `store-required` refuses to start).
2. The budget counts bytes the service knows about; it does not read the backend's disk usage.
3. `Sweep` still reads every tombstone and report record each cycle (their own timestamps), which
   is bounded by the report cap and the takedown rate but is not free.
4. The server-wide `ReadTimeout` is 5 minutes (generic uploads of 64 MiB need it); only the public
   routes have the tight per-request deadline. Do not rely on the generic API for slow-loris
   protection: front it with a gateway.
5. A takedown by an ID that was never a document is silently tombstoned (202): the operator's
   typo is not an error.
6. Upgrading: `_SESSIONS_PER_MINUTE` is gone (use `_SESSIONS_PER_HOUR`); `SessionStore.Create` takes
   a `SessionCaps` (per client, per network, per wide prefix, reserve) and `Session` a `Client`, a
   `Net` and a `Wide`; a short admin token (under 16 characters) now stops the server (see item 18 for
   the override); reports take a `category` and `_REPORT_PRIORITY_REASONS` is replaced by
   `_REPORT_PRIORITY_CATEGORIES`; set `_REPORT_KEY` (or the directory key) so the reporter hashes survive
   restarts; `docstore.ExpiringAdapter` has an `OnSave` hook the service uses; `/docs`
   is closed in public mode unless required ACLs with sign-in or `_ALLOW_OPEN_DOCS=true`;
   `Server.Hide` ignores case; `/public/health` is `{"ok":...}` only and the detail moved to
   `GET /admin/health`; with an admin token the server will not start with `_TRUSTED_PROXIES` unset
   (name the proxies, or `none`); IDs starting with `public/` or `report/` are reserved in all
   modes; `Quota` implementations may add `WindowQuota`.
7. Limits that remain, stated plainly:
   - **Floods from many networks (botnets).** The per-network and per-wide-prefix caps and the
     reserve bound one /48, /24, /32 or /16, not a botnet spread over thousands of them: each new
     wide prefix is a newcomer and takes a place of the reserve, and the reserve is spent when enough
     of them arrive. The same crowd can hold the global in-flight cap (item 19) for as long as it can
     pay in addresses. The real answer is a `Challenger` (item 5.3) when the offer is busy; none ships.
   - **Shared addresses.** Visitors behind one IPv4 /24 share the per-network cap, those behind one
     /16 the per-wide cap, and above 80% full the reserve rule (item 6) judged at the /16 or /32; a
     campus, an ISP or carrier-grade NAT needs higher `_MAX_SESSIONS_PER_NET` and
     `_MAX_SESSIONS_PER_WIDE`, or a smaller reserve.
   - **Reports from many addresses.** Dedupe, reserved slots, the category enum and the priority caps
     make a report flood cost one address per slot, but an attacker with many addresses still fills
     the ordinary slots of a document and can name a priority category (3 a day per address, half of a
     document, and the burst flag only tells the operator). The reporter hash holds across restarts
     only while the report key is kept and the same (item 17), and "the report holds no address"
     holds only while that key stays out of the store.
   - **The admin token** is never held back by the limiters (that is what stops a lockout): its
     strength is its length (16 characters minimum unless overridden), because the limits cannot stop
     a distributed guesser from trying; they slow it, cap its work and make it visible (item 18).
     `GET /admin/health` is as strong as that token.
   - **Counter inexactness.** A write that lands while a sweep lists the store can be counted twice
     until the next sweep (item 8): the budget errs towards refusing, never towards admitting. Version
     snapshots are counted when written but cannot be refused, so the budget can be exceeded by the
     snapshots written after it was reached (item 8).
   - **The first reconcile** still fails closed for 15 seconds when the store really cannot be
     listed; only a departing client no longer causes that.
   - **`MemoryQuota`** forgets the least recently used client when 100,000 are tracked (item 16).
