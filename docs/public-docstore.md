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
   - The deadline is a side record `expiry:<id>` written through the same adapter, so nothing
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
   - Left in this item: edits through the collaboration hub (`collab:` writes) do not yet renew
     the document, so the hub must call the wrapper's `Save` of the document (or the policy must
     learn the pairing) before collaborators' edits count; two server processes on one store can
     race at the instant of expiry; backups of the store are outside this (see 2.2).
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
   the hub renews.
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
5. **Shared infrastructure for replicas**: `SessionStore`, `Limiter`, `Quota` have memory defaults
   only. Behind several replicas, plug in shared ones (the extension table below), or the caps
   are per replica. Two replicas also race at the instant of expiry (section 4.1).
6. The client side: the editor's session handling, the edit and view links, the countdown, "Keep
   this document", the report link on a shared view, `noindex` on the pages that serve it (the
   routes themselves send `X-Robots-Tag: noindex` and `Cache-Control: no-store`).
7. Reports are bounded per document (20) and rate-limited per address, but not by a global count.

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
| `POST /public/session` | a session: `{token, userId: "anon:<random>", expires, limits}`; 429 per address, 503 at the global cap |
| `POST /public/doc` | create the session's one document (409 if it has a live one; 403 if its document was taken down); returns `{id, viewId, expires, versions}` |
| `PUT /public/doc` | an edit; renews the deadline (`X-Gojiberry-Expires`); 413 over the size cap, 403 past the save cap, 429 over the rate or the byte budget; an expired document is 404, never revived |
| `GET /public/doc` | the owner's download; never renews; 404 after expiry, 410 after takedown |
| `DELETE /public/doc` | discard it; the session may then start another |
| `GET /public/view/<viewId>` | read-only; never renews; 410 once taken down |
| `POST /public/report` | `{"id": "<view id or doc id>", "reason": "..."}` -> 202; stored as `report/<id>` |
| `DELETE /admin/takedown/<id>` | operator (header `X-Gojiberry-Admin-Token`); deletes the document, its records, writes the tombstone; the view ID answers 410; idempotent; `?reason=` is kept |

Every public response carries `X-Robots-Tag: noindex, nofollow, noarchive`, `Cache-Control:
no-store`, `Referrer-Policy: no-referrer` and, with a terms URL, `Link: <url>; rel="terms"` (and
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
   (24 h) from its last write and `_SESSION_MAX_AGE` (72 h) from issue at most; reading never
   extends it. Clearing storage makes a new session, which is why the per-address limits exist.
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

### 7.3 Configuration (`GOJIBERRY_DOCSTORE_PUBLIC_*`)

| Variable | Default | |
|---|---|---|
| `GOJIBERRY_DOCSTORE_PUBLIC` | off | `true` mounts the routes (needs `_EXPIRY_TTL`) |
| `_MAX_DOC_BYTES` | 4 MiB | per document, 413 above |
| `_MAX_VERSIONS` | 2000 | saves per document (bounds how long editing can keep it alive) |
| `_IP_DAILY_BYTES` | 50 MiB | written per client address per 24 h, 429 above |
| `_SESSION_TTL`, `_SESSION_MAX_AGE` | 24h, 72h | from the last write, from issue |
| `_MAX_SESSIONS` | 10000 | live sessions, all clients, 503 above |
| `_SESSIONS_PER_MINUTE`, `_WRITES_PER_MINUTE`, `_VIEWS_PER_MINUTE`, `_REPORTS_PER_MINUTE` | 10, 30, 120, 5 | token buckets (burst = the figure): per address, per session, per address, per address |
| `_REPORT_TTL`, `_TOMBSTONE_TTL` | 720h, 720h | |
| `_TERMS_URL` | none | the `Link: rel="terms"` target (an http(s) URL) |
| `_ADMIN_TOKEN` | none | secret of the takedown route; without it the route is not mounted |
| `_TRUSTED_PROXIES` | none | CIDRs whose `X-Forwarded-For` is believed (else a caller could pick its own limiter key) |

### 7.4 Extension points

Each control is an interface with a single-process default, passed in `public.Options` (the same
pattern as `auth/passkey`, see [encryption-and-passkeys.md](encryption-and-passkeys.md) B.1):

| Interface | What it does | Default | Plug in |
|---|---|---|---|
| `SessionStore` | live sessions, the global cap, the one-document claim (`SetDoc` is a compare-and-set) | `MemorySessionStore` (10,000 live, swept at the cap) | Redis, a table |
| `Limiters{Session, Write, View, Report, Admin}` (`passkey.Limiter`) | per-group request budgets | `passkey.RateLimiter` from the Config | a shared budget, a gateway; `passkey.Unlimited{}` |
| `Quota` | bytes per address per day | `MemoryQuota` (fixed 24 h window per address) | Redis, a table |
| `Options.ClientIP` | who the client is | remote address, or `passkey.TrustedProxies(cidrs...)` | your function |
| `AdminGuard` | who may take down | `TokenAdmin` (constant-time compare; an empty token allows nobody) | operator sign-in, mTLS |
| `Blocklist` | refuse content before it is stored (422; an error fails closed, 503) | `NoBlocklist`; `HashBlocklist` (SHA-256) | a hash-matching or scanning service |
| `Challenger` | captcha or proof of work before a session and a create (403) | `NoChallenge` | a verifier (interface only, none ships) |
| `Options.Store` | documents and records | must implement `docstore.Expirer` | any adapter in `docstore.ExpiringAdapter` |

Over a limit the routes answer 429 with `Retry-After`.

