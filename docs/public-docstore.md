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
     `directory-log/` (the key directory's transparency log), `ratelimit/` (shared rate limit budgets, `docstore.StoreLimiter`),
     `passkey/`, `folder-secret/` (a confidential folder's real name: it lives as long as the folder), `keys:` (wrapped document keys: losing one makes an encrypted document
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
2. **Anonymous sessions.** A signed, opaque session token (no user record); the server
   keeps `session → document` only as long as the document lives.
3. **Links** are `/d/{id}` as now, with the capability in the ID (edit) and a derived
   second ID for view only.
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

Expiry is only the retention half. Before launch the section 3 items are all still to build:
the size caps and the "only documents" check at `PUT`, per-session, per-IP-range and global
limits (the passkey routes have a per-client limiter to reuse) with a challenge when busy,
`noindex` and no listing for anonymous sessions (the `/docs` listing must not be open to them),
the report link and a delete-by-ID takedown path, the anonymous session token and the
`session -> document` record that lives and dies with the document, the view-only derived ID,
and the legal read of the terms.

## 6. Open questions

1. Is the lifetime fixed at 24 hours or adjustable per deployment? (Configurable, 24 h
   in the public offer.)
2. Should the session's single document survive a browser restart? (Yes, via the stored
   token, until it expires.)
3. How much abuse tooling is needed on day one, and who answers reports?
4. Which jurisdiction's terms apply, and who reviews them?
