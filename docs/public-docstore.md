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

1. **Expiry in the store.** `docstore` gets `ExpiresAt` per document and a sweeper;
   reads of an expired document answer 404 even before the sweeper runs. `Server.WithTTL`
   sets the lifetime and what counts as a renewal (a `PUT`, not a `GET`).
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

## 5. Open questions

1. Is the lifetime fixed at 24 hours or adjustable per deployment? (Configurable, 24 h
   in the public offer.)
2. Should the session's single document survive a browser restart? (Yes, via the stored
   token, until it expires.)
3. How much abuse tooling is needed on day one, and who answers reports?
4. Which jurisdiction's terms apply, and who reviews them?
