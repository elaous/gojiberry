# Roadmap

What is planned beyond the [opendocs port plan](opendocs-port-plan.md) and the
[known limits plan](known-limits-plan.md). Ordered by what is needed next.

For one page that says what is present, what is missing and where each item stands (open, done, partial,
blocked), see [status.md](status.md); this file keeps the history of each item.

## Merges since the last full test run

6 of 10 since the last full run (full run on main 8a90a42 (#355) with GOJIBERRY_REQUIRE_BROWSER=1: `make check` passed in full on the first try, and so did the coverage run; coverage score 89.7% (was 89.8%), README badge updated).
10th merge from here and the counter resets to 0 after it. PRs A, B and C are done (#99, #100, #101).

## Notes for upgraders

- **Passkey challenge store (`GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES`).** Default `memory`: nothing changes. In
  `store` mode the record names and the token format changed (`passkey/challenge/<expiry minute>/<hash>`, token
  `<random>.<expiry minute>`): a ceremony begun before the upgrade fails once (410; the person starts again, a
  ceremony lives about five minutes). Records of the old layout (`passkey/challenge/<hash>`, no minute) are no longer used: the sweep deletes them once
  they are more than 24 hours old (by the store's modification time; if a store gives none, after one read shows
  them unparseable or expired) and never counts them, so nothing needs deleting by hand (only names that are exactly
  the prefix and 64 lowercase hex characters are touched; any other name under `passkey/challenge/` is left alone).
  `store` on a store without
  conditional writes no longer stops the server: it prints a multi-line warning and keeps ceremonies in memory;
  set `store-required` to keep the old refusal. Details: [encryption-and-passkeys.md](encryption-and-passkeys.md) B.1.
- **Shared sign-in sessions (`GOJIBERRY_DOCSTORE_SESSIONS`).** New, default `memory`: nothing changes for a
  single process. IDs starting with `auth/session/` or `auth/state/` (any case) are now reserved from `/docs` in
  every mode, like `ratelimit/`: an existing document with such an ID becomes unreachable through the server, so
  rename it in the store before upgrading. With `store` the sessions and OpenID Connect login states of the session
  manager live in the store under those prefixes (a session signed in before the switch is not in the store: people
  sign in again once; switching back to `memory` likewise ends the stored sessions). `store` needs conditional
  writes: on a store without them the server prints a warning that names the fix and keeps sessions in memory;
  `store-required` refuses to start instead. Go API: `auth.Config.Sessions`, `Manager.SetSessionStore`, the
  `auth.SessionStore` interface (`MemorySessionStore` is the default) and the package `auth/sessionstore`; `auth`
  exports `LoginState` and `ErrStoreFull`. Details: [encryption-and-passkeys.md](encryption-and-passkeys.md) B.1.
  **The OpenID Connect login routes now have a request budget per client address** (20 at once, 30 a minute, the
  passkey routes' numbers; key `login|<address>`; over it `GET /auth/<provider>/login` answers `429` with
  `Retry-After` and writes nothing), in every `GOJIBERRY_DOCSTORE_SESSIONS` mode. It lives where
  `GOJIBERRY_DOCSTORE_LIMITER` says (per process, or shared through the store). **Behind a reverse proxy set
  `GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES`** (the proxy's CIDRs; it names the client for the passkey routes
  too): without it every client shares the proxy's address and one budget, and the server logs a warning at start.
  Go API: `auth.Limiter`, `Manager.SetLoginLimiter`; the default (nil) is no limit, so an embedder that does not
  call it is unchanged.
- **Reserved document IDs.** IDs starting with `directory/` are reserved for the key directory
  (like `acl:` and `collab:`): `docstore.Server` refuses to read, write, list or delete them
  through `/docs`, so an existing document with such an ID becomes unreachable through the
  server. Rename it in the store before upgrading. The same holds for IDs starting with `public/`
  or `report/` (the public offer's records and reports) in any case (`Public/x`, `REPORT/x`), **in all
  modes**, with the public offer on or off, and for a provider named
  `anon` in `GOJIBERRY_DOCSTORE_AUTH_PROVIDERS` (reserved for anonymous users, like `passkey`).
- **Public docstore hardening (flood, accounting, reports, admin).** `GET /public/health` now answers
  `{"ok":true}` (200) or `{"ok":false}` (503) only; the report with the problem names, storage figures,
  `priorityBursts` and last sweep is `GET /admin/health` (admin token). With `GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_TOKEN`
  set, the server **refuses to start** unless `GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES` names the reverse
  proxies or says `none`, **and the token is at least 16 characters** (`_ALLOW_SHORT_ADMIN_TOKEN=true`
  overrides, with a loud warning). Failed admin authentications are rate limited per client (/64), per
  network (/48, /24) and in all (a 30 s cooldown for wrong tokens when the global budget is used up); a
  correct token never is. New variables: `_MAX_SESSIONS_PER_NET` (100, per IPv6 /48 or IPv4 /24),
  `_MAX_SESSIONS_PER_WIDE` (500, per IPv6 /32 or IPv4 /16; the reserve is judged at that level),
  `_SESSION_RESERVE_PERCENT` (20), `_MAX_INFLIGHT_BYTES` (256 MiB, 503 above) with `_MAX_INFLIGHT_PER_CLIENT`
  (cap/8) and `_MAX_INFLIGHT_PER_NET` (cap/4), both 429, `_MIN_UPLOAD_RATE` (32 KiB/s) and `_UPLOAD_GRACE`
  (5s), `_REPORT_RESERVED_PER_DOC` (5), `_REPORT_RESERVE_PERCENT` (20), `_REPORT_PRIORITY_CATEGORIES`
  (`csam,illegal`), `_REPORT_PRIORITY_PER_REPORTER` (3 a day), `_REPORT_PRIORITY_BURST` (10 documents an
  hour), `_REPORT_KEY` (keys the reporter hash; else derived from `GOJIBERRY_DOCSTORE_DIRECTORY_KEY`, else per
  process) and the three `_ADMIN_FAILURES_*_PER_MINUTE`. A 408 or an abandoned upload no longer refunds the daily
  bytes. `POST /public/report` takes a `category` (`csam`, `illegal`, `spam`, `other`); only it, never the free text,
  can make a report a priority one. Go API: `SessionStore.Create` takes a `public.SessionCaps` (was the per-client
  integer) and `Session` has a `Net` and a `Wide`; a `Quota` may implement `WindowQuota`; `docstore.ExpiringAdapter` has an
  `OnSave` hook; the report record gains a keyed reporter hash, a category and a priority flag, and a store key
  `public/salt` (created with create-if-absent); reports are deduplicated per (document, reporter, category, reason).
  Details: [public-docstore.md](public-docstore.md) sections 7.2 and 7.5.
- **Expiry records and rollback.** The deadline record `expiry:<id>` is one line holding only the
  deadline (RFC 3339), the format every earlier build parses, so a rollback to an older build
  keeps working. The creation time that `MaxAge` needs lives in a new system keyspace,
  `expiry-created:<id>` (reserved from `/docs` like `expiry:`, never a document, deleted with the
  deadline record). A build that predates it ignores those records, so after a rollback they
  stay in the store (they appear in a raw listing of the backend and in an old build's `/docs`
  listing; delete `expiry-created:*` by hand if that matters) and `MaxAge` is not enforced. A
  record that no build can parse (damaged by hand) no longer stops anything: it is logged once,
  skipped by `Load`, `Save` and `Sweep`, and replaced with a full lifetime by the next sweep (or
  deleted when its document is gone). An id starting with `expiry-created:` is reserved too.
- **Renewals: only a marked 404 means "gone".** `POST /docs/{id}/renew` answers a document that
  is gone with `404`, `X-Gojiberry-Gone: 1` and `{"error":"gone"}`; `HTTPAdapter.Renew` returns
  `ErrNotFound` only for that, and `ErrRenewUnsupported` for any other 404, 405 or 501. A
  collaboration hub of this version needs a docstore of this version for rooms to be closed when
  their document expires (with an older docstore the hub just logs that renewals are unsupported,
  once, and never closes a room), and the hub closes a room only after two consecutive "gone"
  answers (`hub.Config.GoneConfirmDelay`). In public mode the orphan sweep
  (`GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS`) is on by default; `off` disables it.
- **Public mode closes `/docs`.** With `GOJIBERRY_DOCSTORE_PUBLIC=true` the generic `/docs`, `/docs/<id>/acl`
  and `/quota` routes answer 404 unless sign-in with `GOJIBERRY_DOCSTORE_REQUIRE_ACL=true` protects them or
  `GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_OPEN_DOCS=true` keeps them open. `Server.Hide` prefixes now match without
  regard to case. `GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS_PER_MINUTE` is replaced by `_SESSIONS_PER_HOUR`, and
  `GOJIBERRY_DOCSTORE_PUBLIC_TOMBSTONE_TTL` must be at least `_SESSION_MAX_AGE` + 24h. The docstore HTTP
  server now has a 5 minute `ReadTimeout`.
- **E-mail now needs an explicit `email_verified: true`.** `auth` used to treat an absent
  `email_verified` claim as verified; it now leaves `User.Email` empty unless the provider sent
  `true` (new `User.VerifiedEmail` says so). Access lists that match by e-mail stop matching users
  of a provider that omits the claim. If such a provider does verify every address, set
  `Provider.AssumeEmailVerified` (`"assumeEmailVerified": true` in
  `GOJIBERRY_DOCSTORE_AUTH_PROVIDERS`). The key directory publishes only for `VerifiedEmail` users
  and its lookup is closed to passkey sessions.

- **Key directory (this release).** New reserved ID prefixes `directory-log/` (the transparency log),
  `directory-enroll/` (owner key enrollments) and `ratelimit/` (shared rate limit budgets) join `directory/`:
  `docstore.Server` refuses them through `/docs`, so a document with such an ID becomes unreachable; rename it
  before upgrading. **Every reserved prefix is now compared without case** (`ACL:x`, `Collab:x`, `DIRECTORY/x`
  are refused like their lower-case forms; a backend that folds case would alias them). Everything new is off by
  default and nothing is required: `GOJIBERRY_DOCSTORE_LIMITER` (`memory`, the default, or `store`), `GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES` (`memory`, the default, `store` or `store-required`; `passkey/challenge/` records),
  `GOJIBERRY_DOCSTORE_DIRECTORY_KEY_PREV` (retired keys for rotation), `..._DIRECTORY_REQUIRE_POP`,
  `..._DIRECTORY_LOOKUP` (`any` or `domain`) and `..._DIRECTORY_REVOKE_WINDOW`. Records published by the
  earlier version still verify (and move to the current key when published again). The directory's limits are
  now token buckets (10 publishes and 60 lookups per user, 120 lookups per client IP and 1200 for the server,
  a minute on average and at once) instead of a fixed window. Publishing writes a log entry first, so a store
  that cannot take the extra records (a quota) fails publishes. For people embedding the library:
  `docstore.DirectoryService` gained `Challenge`, `Revoke`, `Log` and a `Lookup(ctx, email, includeRevoked)`;
  `encrypt.DirectorySigner` gained `KeyID`, `Pseudonyms` and `Verify(kid, ...)`; `passkey.Limiter`,
  `Unlimited` and `RateLimiter` are aliases of the `docstore` ones (code that used them compiles unchanged);
  the directory's rate limits are chosen with `Server.WithDirectoryLimits` and the lookup rule with
  `Server.WithDirectoryPolicy`. Nested module `docstore/backends`: the four backends implement
  `PrefixLister` (no schema change), and postgres and mongo implement the new `docstore.ConditionalDeleter`.
  Review fixes in this release: the log routes (`/directory/log`, `/log/head`, `/log/address`) need a
  **provider session** (a passkey session is refused), apply the lookup policy and have their own per-user
  budget; the log's `address` is a **keyed** hash (`AdapterDirectory.LogAddressFor`; the unkeyed
  `encrypt.LogAddress` is gone); a lookup spends the user's own budget before the shared IP and server ones
  (`GOJIBERRY_DOCSTORE_DIRECTORY_GLOBAL_BUDGET=domain` makes the server budget one per e-mail domain); a
  tombstone carries its revoke entry's number; malformed addresses (not exactly one `@`) are 400; challenge
  tokens are honoured for the current directory key only; publish and revoke are conditional writes with void
  log entries on a lost race and a 503 on a busy log; `Resign` (now run by `Startup` **before the server
  listens**) moves only records that a log entry backs and returns a `ResignReport` (`Resign` used to return a
  count), so rotate through a retired key only if it never leaked; the start-up census is logged and shown by
  `/health` (`directory.unknown_keys`). **Owner-held identity:** an address can enroll an Ed25519 owner key
  (`POST /directory/enroll`), after which every publish and revoke needs the owner's signature and lookups mark
  keys `OwnerSigned`; `docstore.DirectoryService` gained `Head`, `LogAddress`, `Enrollment`, `EnrollChallenge`,
  `Enroll`, `RequestReset` and `CancelReset` and `Revoke` takes the owner signature;
  `docstore/directoryclient` is the Go and browser client. New settings: `GOJIBERRY_DOCSTORE_REPLICAS` (default
  1: with more than one process on a store **without conditional writes** the directory refuses to start, with
  one it warns) and `..._DIRECTORY_GLOBAL_BUDGET`. `StoreLimiter` re-dates a record from a clock more than 5
  seconds ahead; `SweepRateLimits` skips what it cannot read, deletes conditionally and keeps at most 100,000
  records (`SweepRateLimitsMax`). `public.Options.LimiterFactory` lets the public offer's budgets live in the
  store (the docstore server passes `GOJIBERRY_DOCSTORE_LIMITER`'s factory). `components.RecipientRow` and
  `DirRow` gained `OwnerSigned` and `OwnerEnrolled` (new strings `recipients.ownerSigned`, `ownerSignedHelp`,
  `ownerUnsigned`, `ownerUnsignedHelp`, translated in fr, de and es).

- **Encrypted files now carry their protection level, signed (frame v3).** Inside each encrypted file
  the frame is `GJE\x03` + id + level + the seq of the signed `keys:<ID>` record + a hash of its
  recipient list + a hash of the content + an owner's Ed25519 signature over all of it; files written
  before are v1 (no level) or v2 (level, unsigned) and still read, with a warning logged once. The
  first save or re-key of a document writes v3 (`Sharing.Repair(doc)` upgrades one at once), and only
  from then does the file prove its level: a v1 file cannot detect a downgrade, so the first save
  after upgrading pins the level. age has no sender authentication, so without the signature anyone who
  knew a recipient's public key could forge a frame; with it a forged frame is `encrypt.ErrForgedFrame`.
  **Upgrade notes:** set `Adapter.Signer` (a confidential document is not saved without it,
  `ErrNoFrameSigner`) and list the signers in `StoreKeyring.OwnerSigners`; run `Sharing.Repair` per
  document to upgrade old files, after which the record says `signed` and unsigned frames in it are
  forged (`Adapter.RequireSigned` refuses them, for every document id the keyring binds). The
  re-key looks at every file again just before saying `signed` and seals again one saved unsigned
  meanwhile; what a save in the few steps left (or an older build that wrote unsigned frames) leaves
  under a `signed` record is recovered by `Sharing.Repair`/`Recover` with the explicit
  `encrypt.AcceptUnsigned()` option, which seals unsigned files again and never accepts a v3 frame
  with a bad signature. A revoked owner signer stays trusted until it is removed from every
  `OwnerSigners` list (the procedure is in encryption-and-passkeys.md); `saver.VerifyReport.Warnings`
  carries a pending check that could not read one record. Older builds cannot read v2 or v3 files.
  The checks now look at every file of a document (an unreadable one that is not provably someone
  else's is `ErrUnreadable`, not skipped), `Sharing.PendingStatus`/`saver.VerifyReport.Pending`
  surface a re-key left pending, `FileSeqFloor` is cached and `RaiseMany` batches it, and a floor
  whose storage fails reports `ErrFloorDegraded` (`encrypt.SetFloorDegradedHandler`; the showcase
  keeps an in-memory floor, which cannot degrade, so a host with a `LocalStorageSeqFloor` must set it or poll
  `StorageSeqFloor.Err()`). A client that reads or saves with a `StoreKeyring` or
  `ConfidentialKeyring` now also reads the record on `Load` (it needs `OwnerSigners`, as saving
  already did); a key-only reader (`Static`) is unchanged. New errors: `docstore.ErrDowngrade`
  (`encrypt.ErrDowngrade`) from `Load`/`Save`/re-key/`SetProtection`, `saver.VerifyReport.Downgraded`;
  the owner's way forward is `Sharing.Recover`. The owner-signed index lives at `keys:index`
  (`encrypt.IndexKey`), which a custom `Adapter.Skip` must keep readable like the other `keys:` records.

## Next

1. **Migrate: done** (see [document-components.md](document-components.md)); left: skipping what an earlier run already brought (needs the source ID kept on the document), Google Docs comments and revision history.
2. **Button icons: done** (`components.Material`, see [document-components.md](document-components.md)); left: a toolbar of icon buttons for the formatting commands.
3. **Download submenu: done** (grouped "Download as", and `.open` with the version history; see [document-components.md](document-components.md)).

## Later

1. **`.opendoc` packs: done** (`docfile/pack`, `cmd/opendoc`, `Saver.ImportPack`/`ExportPack` with injected `Seal`/`Open` for confidential documents, the document-list buttons and Migrate's "One .opendoc pack"; spec in [opendoc-format.md](opendoc-format.md)).
2. **Encryption and passkeys.** Encrypt documents to SSH keys and passkeys (envelope
   encryption, a wrapped data key per recipient, keys stay with the person) and sign in
   with a passkey (WebAuthn). Design and open decisions:
   [encryption-and-passkeys.md](encryption-and-passkeys.md). Done (libraries): encrypted
   documents with signed recipient lists, the passkey as a recipient and as a way in, and
   (step 5) encrypted records in packs, confidential catalog entries (`saver` protection
   level, placeholder name, `Reveal`, `SetProtection`, `encrypt.ConfidentialCheck`). Left:
   1. **Done in the showcase** (`examples/canvas-editor-showcase`, `encstore`; encryption stays off unless
      turned on; section 5.7 of the design): "Encrypt this store" in the Save tab (a local passkey through PRF, an
      SSH public key, a recovery key shown before it is committed; unlock state in memory only, a Lock button that
      closes the open document), lock marks and "Make (not) confidential" with a question for documents and
      folders, Reveal, sealed `.opendoc` packs (`encrypt.PackSealer` through `Session.PackKeys`), and the People
      tab's keys control. Also done there (PR #81 review): every change of the keys is a transaction with a
      journal (a failure puts everything back, a crash is finished or undone on the next load, and finishing
      needs the journal's signed link), a passkey alone needs an explicit acknowledgement, Erase the encrypted
      store for lost keys, removing a key and replacing a passkey, seals, links and an anchor in the signed
      frames that make a replaced key list detectable at unlock, passkey failures told apart, the directory
      picker and "Publish my keys" behind `dirclient` (the address is confirmed by the person), and placeholders
      drawn in the interface's language. Left: the real app (the owner signers must live out of reach of the
      store's writers: the seals cannot stop a whole-store rollback, see section 5.7; a passkey from a server
      session rather than a local one), and publishing with proof of possession (the showcase holds only the
      owners' public keys, and a passkey would need another assertion per signature).
   2. **Done:** a keyring that wraps a confidential document's files for people's keys only, and re-keys
      its older copies on the switch (`ConfidentialKeyring`, `Sharing.SetProtection`, `RekeyHook`).
   3. **Done (components and library):** the People-tab "my passkey" control with the second-key rule and
      the recovery key shown once, and the verified-e-mail mark with `encrypt.KeyDirectory` /
      `AdapterDirectory` (C.4); the server rule (the `directory/` prefix is reserved from `/docs`, records
      are signed by a configured directory key and written only by `POST /directory/publish`, which needs a
      provider session with a verified e-mail) and a demo host, `examples/people-keys-demo`; the showcase's People tab has the same key controls, with the directory picker and a publish button when a docstore server is configured (5.7). Also done:
      revoking (`DELETE /directory/keys/<fingerprint>`) and rotating the directory key (`KEY_PREV`, `Resign`),
      proof of possession and ssh-ed25519 attestation (`Held`/`Attested`, a stronger mark), a signed
      transparency log (`GET /directory/log`, keyed addresses, signed head, truncation checks), shared rate
      limits (`docstore.StoreLimiter`, `GOJIBERRY_DOCSTORE_LIMITER=store`, also for the public offer), lookup
      budgets and a lookup policy, `PrefixLister` in the postgres, mongo, s3 and blob backends, and, after
      review, atomic publish and revoke across replicas, `Resign` that does not launder forged records, and
      **owner-held identity** (an enrolled owner key that must sign every publish and revoke, the
      `OwnerSigned` mark, rotation and a 24 hour reset, `encrypt.AuditAddress`) with the Go and browser
      client `docstore/directoryclient` and its wiring in `examples/people-keys-demo` (publish, revoke,
      enroll, watch my address; e2e with the virtual authenticator). Left: wiring it in the app, and the showcase's owner-signed publish (the
      showcase has the directory picker and a plain publish, 5.7; the note in step 4b), trust on first use of the owner key and witnesses for the log (see the limits in
      step 4b).
      **Round 3 of the directory review (behavior changes and upgrade notes):** the client's
      `Key.OwnerSigned` is now true only when the row's owner signature verifies under a key the client
      pinned (`Client.Pins`) or, for its own address, one in the chain `WatchLog` audited; a first sight is
      `OwnerUnpinned`, a different key `OwnerChanged`. `WatchLog` pins the log pseudonym of the address with
      the client's own entries (`ErrAddressMismatch`), keeps recent entries pending so a later void still
      cancels them, and shows voided entries. Entry times (`At`) must not go back along the chain
      (`VerifyLog`, `AuditAddress`; a replica never writes a time before its predecessor's). A publication's
      owner signature (message version 2) covers the key's label; version 1 entries still verify. The log
      route returns only the caller's own address unless the caller owns an enrolled address or is the
      operator. An enrollment crash window (log entry written, record not) is completed from the log by the
      next owner operation or at start-up, and `OperatorResetEnrollment`
      (`POST /admin/directory/reset-enrollment/<pseudonym>`, `GOJIBERRY_DOCSTORE_DIRECTORY_ADMIN_TOKEN`)
      logs a signed `operator-reset` entry that watchers are told about. The tombstone sweep deletes only
      the record it read. `encrypt.DefaultSkip` and the reserved-id check compare **without case**: a
      document id such as `Directory/x` that an earlier version stored in the encrypted keyspace is now
      treated as reserved (rename it before upgrading). `directoryclient` refuses a non-https `BaseURL`
      except for localhost. Set `GOJIBERRY_DOCSTORE_REPLICAS` to the real number of processes: leaving it
      unset, or declaring 1 while the store shows another writer, is logged loudly. The in-process rate
      limiter keeps at most 100,000 keys in least-recently-used order: a caller flooding with more distinct
      keys than that resets other callers' buckets, so per-caller limits are a fair-use control, not a
      defence against unlimited identities (that belongs to the gateway); the whole-server directory lookup
      budget is split in 16 shards by user.
      **Round 4 of the directory review (behavior changes):** `WatchLog` with no saved position requires the
      log to start where it asked (entry 1 at the zero hash for a first run), a filtered answer for an
      address the audited state says is enrolled is `ErrFilteredEnrolled`, and `WatchState.Made` is now a list
      of `MadeEntry` (the server's number plus the operation, fingerprint and signature the client sent,
      checked against the log entry; **a stored state with the old list of bare numbers no longer decodes**:
      drop it, the next run starts from the log); `Client.Enroll/Rotate/CompleteReset` pin the key the client
      made, not the one in the answer. `Client.Pins` now holds only confirmed keys: trust on first use is kept
      in `WatchState.Seen` and marks rows `OwnerFirstSeen` (not `OwnerSigned`/`OwnerPinned`); a second owner
      key in one answer is `OwnerChanged`. Enrollment reconciliation also runs before every publish and
      revoke, finds addresses through published keys, and is repeated by `RunSweep` (and after the grace
      following a restart); `AdapterDirectory.ReconcileGrace` sets the grace. Pruning a tombstone removes the
      retired-name copies of the key with it. `Server.WithDirectoryClientIP` (wired from
      `GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES`) names the client for the per-IP lookup and failed-admin
      budgets. Documented limits: a clock-ahead replica, log reads sharing the lookup shards, and the
      per-address (not per-user) admin failure budget behind a proxy (step 4b).
   4. **Done:** hiding folder names of confidential folders (and the showcase's switch for them).
   5. **Downgrade and deleted-record detection: done** (library; C.5 and step 5.8 of
      [encryption-and-passkeys.md](encryption-and-passkeys.md)). The level and the record's seq are
      inside the encrypted frame, so a store writer who deletes or replays a `keys:<ID>` record is
      caught on `Save`, `Load` and every re-key with no client state (`ErrDowngrade`); an owner-signed
      `keys:index` lists the end-to-end documents (`Sharing.VerifyIndex`), and `SeqFloor` has a
      persistent form (`FileSeqFloor`, `LocalStorageSeqFloor`). The frame is signed by an owner signer, so
      a writer who knows only public keys cannot forge one. Left, stated plainly: a whole-store rollback
      to an older genuine consistent snapshot (files, records and index together) cannot be told from
      the truth by anything inside the store, and only a client-side floor that outlives it notices
      (a fresh client has none); files still in the unsigned v1/v2 frames are forgeable until their
      document is re-keyed (`Sharing.Repair`); wiring `VerifyIndex`, a floor and its degraded warning
      into the app's sign-in (the showcase has an in-memory floor and no sign-in).
3. **Public docstore.** An anonymous offer: no account, one document per session, a link
   and a download always, deleted 24 hours after the last edit (viewing does not renew).
   Design, wording of the promise, and the abuse controls that must exist before launch:
   [public-docstore.md](public-docstore.md). Expiry in `docstore` is done (`ExpiringAdapter`: TTL policy, renewed by writes, sweeper, `X-Gojiberry-Expires`, `GOJIBERRY_DOCSTORE_EXPIRY_TTL`/`_SWEEP`/`_ADOPT`); **server side done** (`docstore/public`, `GOJIBERRY_DOCSTORE_PUBLIC=true`: anonymous sessions, one document per session, size/version/rate/daily-byte caps, view-only IDs, reports, takedown with tombstones, `Blocklist`/`Challenger`/`AdminGuard`/`SessionStore`/`Quota` hooks; see [public-docstore.md](public-docstore.md) section 7); hardened after review (per-client session cap with a 1 h idle TTL and an hourly issue rate, limits keyed by IPv6 /64, a global storage budget and report bound, `/docs` closed in public mode, request deadlines, charge-before-body with refunds, `/public/health`, takedown that bans after the records are swept; section 7.2 items 6-15); hardened again (a per-/48, per-/24, per-/32 and per-/16 session cap with a reserve for new wide networks, an exact byte ledger that fails closed and counts version snapshots when written, a minimal public health with the detail behind the admin guard, deduplicated reports with a category enum and capped priority, failed-only admin rate limit per /64, network and in all with a 16 character minimum token, caps on bodies in flight per client, per network and in all with a minimum upload rate, a keyed reporter hash; items 6, 8, 16-20); left: the legal terms text, real abuse operations (who reads reports), a captcha or proof of work behind `Challenger`, shared stores for replicas (the counters and limits are per process, section 7.5), the editor side (links, countdown, report link, creating the document within the idle hour); collaboration-hub edits renew a document (`hub.Config.Renewer`, `ExpiringAdapter.Touch`, `POST /docs/{id}/renew`, never for public sessions), a room whose document expired is closed with 410 after a confirming check, hub-only state is swept (`GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS=collab`, on by default in public mode) and `MaxAge` caps a document's life.
4. **Console mode: done** (`console`, `cmd/docsh`, [console.md](console.md)); left: the editor panel that drives the same engine.
5. **Terminal app demo: done** (`examples/terminal-demo`: a file manager and a line editor over `saver.Session`, with the console grammar).
6. **Store backends with conditional saves: done** for PostgreSQL, MongoDB and S3/MinIO (`SaveIf` in `docstore/backends`; the PostgreSQL one is tested with sqlmock, S3 against a fake that enforces `If-Match`/`If-None-Match`, and MongoDB, which has no fake, runs when `TEST_MONGO_URI` is set, so it has not run against a live server yet).
7. The rest of the [known limits plan](known-limits-plan.md): i18n breadth,
   accessibility depth, right-to-left, Hunspell dictionaries on demand.

- **Done:** TypeScript in the browser from Go. tsgo builds to wasm and transpiles; sizes and conclusions in [typescript-in-wasm.md](typescript-in-wasm.md).

## Backlog: pluggable infrastructure for passkeys

`auth/passkey` takes each piece of shared infrastructure as an interface, so a deployment can
plug in its own once it exists, with no change to the library (see
[encryption-and-passkeys.md](encryption-and-passkeys.md), "Extension points"). What ships is the
single-process default of each. To build when a deployment needs it:

1. **A shared rate limiter: done through the store** (`docstore.StoreLimiter`, `GOJIBERRY_DOCSTORE_LIMITER=store`,
   injected as `passkey.Config.Limiter`: replicas on one conditional store share the budget). Left: Redis or a
   gateway's decision behind the same `Limiter` interface, for a deployment that has one. Default: a per-address
   token bucket in the process.
2. **A shared challenge store: done through the store** (`passkey.StoreChallenges`,
   `GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES=store`, atomic `Take` by `DeleteIf` or a conditional used marker). Left:
   Redis behind the same interface, for a deployment that has one (no client ships). The bearer sessions of
   `auth.Manager` are shared too now (`GOJIBERRY_DOCSTORE_SESSIONS=store`, backlog item 7 follow-up). Default: memory.
3. **Account stores with a conditional write.** `AdapterStore` already does compare-and-set
   (`Updater`, `Creator`) over any `docstore.ConditionalAdapter`; the S3, Mongo and Postgres
   adapters get it from #55. To check once #55 merges: run the passkey tests against each
   backend (a live Postgres and Mongo in CI), and add a store that is not a docstore (SQL
   tables) behind the same two interfaces.
4. **Per-account limits** (not only per address): a failed-sign-in budget per account, through
   the same `Limiter` with a different key.
5. **The same pattern for the rest of the encryption stack**: `Sharing` and `Adapter` take the
   store and signer as interfaces already; a shared lock (`docstore.Locker`) for stores without
   conditional writes is the open piece.

## Backlog from the limits review

The actionable items left by the [limits register](limits-register.md), in priority order. Work them
**strictly one at a time**: the next starts only when the one before is merged. Each has a one-line plan and
a way to avoid hitting the limit meanwhile.

1. **Done:** **filtered to full read in `directoryclient` `WatchLog` is now anchored to the saved head** (a read that starts at `st.Seq+1` must have `Prev == st.Hash`; otherwise the hash at `st.Seq`, read with `entryAt` when the cursor is past it, must equal `st.Hash`; a mismatch is `ErrLogCut`). Was: not anchored. Plan: when
   switching, require `entries[0].Prev == st.Hash` if `entries[0].Seq == st.Seq+1`, else the hash at `st.Seq`
   must equal `st.Hash`. A filtered saved state can have `Cursor` past `st.Seq`, so the check needs the entries
   at or before `st.Seq` (read from `st.Seq-1`, or `entryAt`), not the cursor. Avoid: do not mix filtered and
   full reads on one `WatchState` until fixed (no longer needed).
2. **Done:** **flaky test `TestHighestSeqOfAllFilesCountsNotTheNewestByTime`** (`docstore/encrypt`). The cause was
   the test, not timing or the product: `SetProtection` writes a new generation under a fresh name and drops the
   oldest, and the test took the first generation listed afterwards, which could be the new one it had no captured
   bytes for, and overwrote it with an empty file (rightly `ErrUnreadable`). The product never orders files by the
   store's times. The test now picks a captured generation that survived and has no sleep.
3. **Done:** **files in unsigned v1/v2 frames: `Sharing.RepairAll(ctx, RepairAllOptions)`** lists the store once and walks every document (and every other encrypted item: `expiry:`, `collab:`,
   `passkey/`, `public/` ..., sealed again by `Adapter.Rekey`) and
   runs `Sharing.Repair` on each document that has an unsigned file (same lock, same checks against the record, same
   conditional writes), then returns a `RepairAllReport` (`Total`, `Signed` already v3, `Repaired`, `Encrypted`, `Files`, `Next`,
   `Skipped` with a reason each: no signer, no key held, unreadable, forged, downgrade, plaintext, conflict; `Todo`
   for `DryRun`; `Remaining` when cut short). It never downgrades, deletes or uses `AcceptUnsigned`, is idempotent and
   restartable (a rerun starts at the first id, or after `RepairAllReport.Next` given as `RepairAllOptions.After`; a document that failed mid-write may be
   partly rewritten and a rerun finishes it; `Left()` is zero only when every encrypted file `Skip` does not leave readable was signed), and is bounded by the context, `Limit` and a `Progress` callback. There is no command
   for it: the docstore server holds no owner signer or identity, so it is a Go API for the owner's client (see
   [encryption-and-passkeys.md](encryption-and-passkeys.md), "Which files may be unsigned"). Was: only per-document
   `Repair`. Avoid: no longer needed.
4. **Done (reduced):** **the no-signer window is no longer silent; the part that can be closed is closed.** After the
   re-key writes `signed` it looks at the files again and returns `ErrUnsignedAfterFlip` for an unsigned one; an
   unsigned `Save`/`SaveIf` re-reads the record and returns `ErrNoFrameSigner` if it flipped under the writer; an
   `Adapter` with `RequireSigned` and no `Signer` refuses to write an unsigned frame. Every file that enters the
   window is therefore reported to the owner or to its writer, and `Repair(..., AcceptUnsigned())` remains the way out.
   Inherent, so not closed: a client with no `Signer` and no `RequireSigned` can write an unsigned frame until the
   record says `signed` (it cannot sign, and a plain save does not change the record, so the flip cannot be conditional
   on it), and so can anyone who knows a recipient's public key (design, "The window before `signed`"). Avoid: set
   `Adapter.Signer` (or `RequireSigned`) on every client that writes.
5. **Done (reduced):** **revoked owner signer tooling: report and re-sign.** `Sharing.RepairAll` now reports
   `BySigner` (per owner signing key: frames, documents and `keys:<ID>` records it signed; the key is found by
   verifying, since frames carry no key id and the record's `signer` field is only a hint) and `OtherSigner`
   (the documents that have a frame or record by a key other than the Adapter's `Signer`), and with
   `RepairAllOptions{Resign: true}` seals those again: the record first (`Sharing.Resign`), then every file
   (`Repair`/`Rekey`), so everything is signed by the current key (`Resigned` count, `resigned` progress
   outcome; `DryRun` lists them in `Todo`). It is step 2 of the C.5 procedure for a whole store, idempotent,
   restartable, concurrent-save safe like `Repair`; `OtherSigner` empty and `BySigner` showing only the current key
   say that no document, frame or record is left that step 3 (remove the old key from every `OwnerSigners`)
   would refuse. The signed `keys:index` is reported apart (`IndexBy`, `IndexOther`); `Resign` signs it again
   with `Sharing.ResignIndex` (`IndexResigned`, `IndexErr`), and without `Resign` or in a `DryRun` it is only
   reported, so run `Sharing.ResignIndex` before step 3. `Resign` needs the `Sharing`'s `Signer` to be the
   `Adapter`'s key (else `SkipNoSigner`).
   **Not done, needs a format change (design follow-up):** refusing frames by a revoked signer written after a
   revocation point while older ones keep verifying. The data model cannot carry it: (a) a frame has no key id
   and no time, only the record seq, which is per document, so a global cut cannot be stated in it; (b) a
   revocation mark inside the signed `keys:<ID>` record is signed by an owner signer, so the revoked key
   itself can write a newer record without the mark. Plan: frame v4 carrying a signer key id and a signing time;
   a signer set (keys with `notAfter`) kept in a document signed by a root key that is never rotated through
   `OwnerSigners` (or held by the host's config), checked by `verifyFrame` (`ErrForgedFrame` for a frame signed
   after `notAfter`, which also needs a trusted time source or the record seq as the cut); `RepairAll` then
   moves the frames signed before the cut. Avoid: the C.5 procedure, ending with removing the old key from
   every list; until that step the old key's holder can still write.
6. **Done:** **SSH key-tag collision tightening.** `recipientID` (`docstore/encrypt/confidential.go`) was the only
   place that identified an SSH recipient by the 4-byte age key tag (`st.Args[0]`); Sharing add/remove/list,
   the record and its hash, and the directory match by the SHA-256 fingerprint of the full key, so they were
   never affected. No wrong accept existed: a collision could only take a person's key for a server key, so it was
   dropped from a confidential document's recipients (a refusal for that person, never a server key admitted). Now
   `ParseSSHRecipient` returns a recipient that carries the full key's fingerprint, and `ConfidentialKeyring`
   compares tag and fingerprint, so a person whose tag collides with a server key is kept. A raw recipient built
   straight from `agessh` has only the tag, and a tag match with it still counts as the same key (fails closed). No
   stored format changed. Was: tag only. Avoid: no longer needed; build recipients with `ParseSSHRecipient`.
7. **Done (reduced):** **shared `ChallengeStore`: `passkey.StoreChallenges`, `GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES=store`.**
   A passkey ceremony begun on one replica now finishes on another (before: the token was unknown there, so 410).
   Records live under `passkey/challenge/<hash of the token>` (inside the reserved `passkey/` keyspace: hidden from
   `/docs`, no expiry deadline, not a document key). `Put` is a conditional create; `Take` reads, then consumes with
   one conditional step on the digest it read (`DeleteIf`, or `SaveIf` to a used marker on a store that has only
   `SaveIf`, such as s3), so of any number of concurrent Takes on any replicas exactly one wins; expiry is the
   stored `Expires` plus a sweep (`Sweep`, run every minute by the server); the count is bounded (10,000 by default,
   `ErrBusy` past it, a live challenge is never evicted; each replica counts from its last listing, so replicas may
   overshoot by what they write between listings). (Changed by PR B: `store` on a store without conditional writes now warns and falls back to memory, `store-required` stops the server; records are named `passkey/challenge/<expiry minute>/<hash>` and the sweep reads nothing, so the young-record budget and the per-listing cap were replaced, see the queue below.) `store` on a store without working conditional writes stopped the
   server at start in the first version. **Not done, by decision: Redis.** The repo has no Redis client and a
   RESP client with auth, TLS, timeouts and cluster behaviour is not small; the store-backed one works on every
   backend that has conditional writes (memory, localfs, sqlite, postgres, mongo, s3; the blob backend has none, so
   `store` mode refuses to start on it), and the `ChallengeStore`
   interface is unchanged for an embedder who wants Redis `GETDEL`. **Found then, fixed by PR A below (done):
   bearer sessions were per process.** `auth.Manager` keeps sessions (and OpenID Connect login states) in an
   in-memory `tokenStore`, so a token issued by replica A is unknown to replica B whatever `ChallengeStore` is
   used: a passkey sign-in finished on B only helps requests that reach B. Was: sticky routing by the bearer
   token or one replica. **Done (PR A, `auth/sessionstore`, `GOJIBERRY_DOCSTORE_SESSIONS=memory|store|store-required`):**
   `auth.Manager` takes a `SessionStore` (default `MemorySessionStore`, behaviour unchanged). The store-backed one
   keeps `auth/session/<expiry hour>/<sha256 of the token>` (the user, issued-at and expiry; the token is never
   stored) and `auth/state/<expiry minute>/<hash>` (OpenID Connect login states, nonce and PKCE verifier included,
   single use by `DeleteIf` or a conditional used marker). No cache: every authenticated request is one read of the
   store (two under `GOJIBERRY_DOCSTORE_EXPIRY_TTL`, where `ExpiringAdapter` reads the deadline record too), so a
   logout is seen on every replica at once. No sliding expiry (the in-memory one does not slide either): `SessionTTL` is the
   absolute age. Bounded (100,000 sessions, 10,000 pending logins, `ErrStoreFull`/503, a live record is never
   evicted; counts per replica as for challenges). The sweep deletes by record name alone, reading nothing, which
   also removes used markers whatever the listing shows; it lists the login states every minute and the sessions
   (whose buckets are an hour wide) at the first sweep, when the hour changes and at least every 10 minutes, to
   keep the LIST calls of many replicas down. Records are encrypted under an encrypting adapter
   (they hold names and e-mail addresses, so they are not in `encrypt.DefaultSkip`). Left, listed as items 53 to 55
   below. The account store (`AdapterStore`) was already shareable.
8. **Done:** **concurrent double first-passkey UX.** Was: two tabs of a signed-in identity with no passkey account
   begin its first passkey; the account is created by a conditional create, so exactly one won and nothing was
   overwritten or duplicated, but the other got the `410` of an expired challenge. Now it gets `409`
   `account_exists` (`passkey.ErrAlreadyCreated`, `client.ErrAlreadyCreated`) and `components.Passkey` says, in
   de/fr/es too, that another tab or device made the first passkey, to reload and add another, and that the passkey
   the second device made is not used (delete it there). `410` stays for expired, used and unknown challenges.
   Signed-out registrations never collide (random account per begin; the e-mail is a label). Avoid: no longer needed.
9. **Done in part (this PR): s3 `DeleteIf`; blob cannot have one.** The S3 backend implements
   `docstore.ConditionalDeleter` with `If-Match` on `DeleteObject` (read, compare the digest, delete on the ETag
   read; 412 is `ErrConflict`, a missing object `ErrNotFound`), tested against the fake S3 including a version
   swapped in between the read and the delete. The gocloud blob backend stays without: the portable API only has a
   create-only write (`IfNotExist`) and no conditional delete, as for `SaveIf` (public-docstore.md 7.x item 4); for
   object storage use S3. A compatible service that ignores `If-Match` on delete would delete whatever is there;
   the doc comment says so. Avoid: the conditional claim in `SweepRateLimits` (`docstore/limiter.go`) already
   narrows the rate-limit race to a token; it does not cover document expiry (item 10).
10. **Done (this PR): expiry deletes on the digest read.** Was: `ExpiringAdapter.purgeLocked` and `Sweep` deleted
    unconditionally under an in-process lock, so a save by another process in the deadline instant could be
    deleted. Now, where the store is a `ConditionalDeleter`, an expired document is loaded, the deadline record is
    read again, and `DeleteIf` applies to the digest loaded (`deleteExpired`); a changed document is kept
    (`errRenewed`: nothing else is deleted, the sweep counts nothing and reports no error, a save that was clearing
    it carries on over the live one). A user's own delete stays unconditional. Tests: a second process saves
    between the read and the delete (`TestSweepKeepsADocumentAnotherProcessSavedAtItsDeadline`, which fails
    without the change) and the save path; a store without `DeleteIf` expires as before. Not closed: a save with
    the very same bytes, and stores without `DeleteIf` (blob, encrypted ids). `docstore/expiry.go`.
11. **Done (this PR): MongoDB live tests.** They now run in CI against a `mongo:7` service container (`mongo-backend`
    job, which fails on a skip), skip loudly with `SKIPPED, NOT TESTED`, and `docs/testing-workflow.md` has a Docker and a
    no-Docker recipe. They passed against MongoDB 7.0.14 under `-race`; the first real run exposed a test setup bug (the
    conformance factory did not empty its collection between subtests), fixed here. The adapter itself passed.
12. **Log witnesses (directory).** Plan: a witness protocol where independent parties co-sign heads. Avoid:
    compare heads between two auditors by hand.
13. **Done (this PR): older log pseudonyms survive removing the retired directory key.** Was: a log entry names its
    address by a pseudonym keyed by the directory key that made it, so once the retired key left
    `..._DIRECTORY_KEY_PREV` the directory could no longer compute the old pseudonym of an address: the older entries
    stopped matching their address (records needed republishing, owners could not read them, and the entries
    showed up as unmapped). Now every start-up records, while the retired keys are still listed, a signed alias
    record per known address (`directory-log/alias/<current pseudonym>`, pseudonyms only), carries earlier aliases
    forward through later rotations, and `LogAddressesFor` returns them (so `/directory/log/address`, the audit,
    reconciliation and the unmapped count all see them). A record that does not verify is ignored and counted.
    Tests: `TestRemovedDirectoryKeysPseudonymsAreKept` (one and two rotations, the owner's `Previous`, the unmapped
    count) and `TestAnAlteredAliasRecordIsIgnored`. Still true: start the server once with the retired key listed
    before removing it; an address with log entries and no record or key cannot be aliased; a replica started
    earlier reads new aliases only at its next start-up. `docstore/encrypt/directory_alias.go`.
14. **Done (this PR): the dead-room tombstone is also kept in the store.** Was: the hub's `h.gone` entry lived in the
    process for `goneMemory` (one hour), so a restart, eviction at `maxGone` or another replica let a stale tab open
    a new room and write the content back. Now `closeDead` also saves `collab-gone:<id>` (`{"until": ...}`) in the
    hub's store; `load` refuses the id with 410 while it has not lapsed, and an editor whose `Renewer` says the
    document is live again clears it. Left over, by design: a mark nobody asks about again stays in the store (about
    40 bytes; it is deleted when asked about after it lapsed), and a hub without a `Store` keeps only the process
    table. `collab/hub/hub.go`.
15. **Done (this PR): the hub's keys are pinned as system keys; the renewal throttle was already there.** Was: nothing
    tested that the hub's keyspaces stay out of the document rules. Pinning it caught a real miss from #123: its
    new `collab-gone:<id>` mark was not in `docstore`'s system prefixes, so it would have been listed and expired as a
    document. It is now (`collabGonePrefix`), and `TestHubKeysAreSystemKeysNotDocuments` checks
    `StoreKey` and `goneKey` against `IsDocumentKey`. The renewal throttle needed no code: the hub renews a document at
    most once per `RenewInterval` (a minute, at most a quarter of the lifetime) however many tabs edit it, and only on
    an accepted edit, so a stale tab cannot renew faster than that. It still keeps a document alive while it edits,
    by design. `docstore/expiry.go`, `collab/hub/renew_rereview_test.go`.
16. **Done in part (this PR): the per-address daily byte budget is shared through the store.** Was: the budget, the
    sessions and the report counters of the public offer were per process, so behind replicas an address could spend
    its budget once per replica. Now `public.StoreQuota` (a `Quota` with refunds and windows, over a store with
    conditional writes) keeps one counter per address per UTC day under `public/quota/<day>/<hash of the address>`,
    charges with a compare-and-set loop and never lets through more than the limit (tested under races); the
    service's sweep drops ended days. `GOJIBERRY_DOCSTORE_PUBLIC_QUOTA=store|store-required` turns it on (`memory` is
    the default; a store without conditional writes warns and falls back, as for the other shared stores).
    Still per process, now entry 86: the anonymous `SessionStore` counts, the byte budget and the report counters.
    `docstore/public/storequota.go`, `internal/docstorecmd/docstorecmd.go`.
17. **Done (this PR): a snapshot over the byte budget is refused, and a deleted one is subtracted at once.** Was:
    version snapshots were written by the saver, which does not ask the public offer's budget, so the budget could be
    exceeded by the snapshots written after it was reached, and a deleted snapshot came off the count only at the next
    reconcile. Now the public service puts a `Guard` on the `ExpiringAdapter` (asked before every Save and SaveIf,
    nothing is renewed or written if it refuses): a snapshot of a public document that would pass `MaxTotalBytes`
    is refused with `docstore.ErrStorageFull` (the saver reports the error; the document itself is untouched), a
    claim covers the bytes while the write runs, and a stored one is counted. `OnDelete` subtracts a deleted snapshot
    when `Delete` succeeds. Still inexact, by design: a snapshot written or deleted by a path that does not go through
    this `ExpiringAdapter` (`NoteVersionWrite` after the fact, a delete inside a document purge) is counted or
    subtracted by the next reconcile. `docstore/expiry.go`, `docstore/public/service.go`, `guardrails.go`.
18. **Done (this PR): a store that wraps the `ExpiringAdapter` can name it.** Was: the public service put its
    snapshot hooks only on a bare `*docstore.ExpiringAdapter`; `New` accepts any `docstore.Expirer`, so a wrapper
    got no hooks and its snapshots were neither refused over the budget nor counted until a sweep, silently.
    Now a wrapper implements `public.ExpiringHolder` (`Expiring() *docstore.ExpiringAdapter`) and gets them; a
    store that is neither logs a line saying so. Still true, and documented on the hooks: they are plain fields
    set when the `Service` is built, so build the `Service` before the adapter serves requests (setting them under
    load would race). `docstore/public/service.go`.
19. **Done (this PR): the #85 minors.** (1) The report dedupe key is the reporter and the category, not the
    wording: rewording a report no longer buys another slot, so one address holds at most one report per category
    in a document. (2) `GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS_PER_MINUTE`, gone since the per-hour limit, is warned about at
    startup if still set (`Config.Warnings`). (3) `MinUploadRate` has a floor of 1 KiB/s (`MinUploadRateFloor`): a lower
    positive value is raised to it, with a warning; negative still means no minimum. (4) A `MAX_SESSIONS_PER_WIDE`
    that was clamped by the per-net or global cap, or left unset and moved by them, is warned about with the value in
    force. `docstore/public/config.go`, `service.go`.
20. **Closed, not built (this PR): showcase originals stay in memory.** The plan was to keep the journal's originals
    "on disk". In the showcase the only disk is the same `localStorage` the files live in, which is capacity
    limited: a second copy of every file would double what a change needs (the room check would have to count it,
    and a store that fits today would be refused), and it would leave plaintext or old-key copies behind if the
    tab died before cleaning them. What the memory copy buys is the rollback of a failure while the tab is alive;
    after a crash `Recover` already finishes or undoes the change from the state in storage (every step is safe to
    repeat). What neither can do is bring back a file the browser lost, and a second copy in the same storage
    would not survive that either. Revisit if the showcase moves to a store with room to spare (IndexedDB or OPFS):
    then spill the recorder's originals to an `encstore-undo/` area there, counted in `checkRoom`, deleted on
    commit and on `Recover`. The residual limit stays documented in `encryption-and-passkeys.md`.
21. **Directory publish in the showcase and the pick UI. Done (4 slices: #132, #133, #134, this PR).** Slice 1 (#132): the
    showcase's `dirclient` is a thin view over `directoryclient`, and "Publish my keys" proves possession by opening
    the server's challenge with the unlocked key's identity (`encstore.Store.IdentityFor`). Slice 2 (this PR): the
    owner key. The People tab's directory settings can check the address (`GET /auth/me` for who the server knows
    the account as, then the enrollment), make and enroll an owner key, show it for a backup (shown and downloadable,
    with a warning), restore from a backup (checked against the address's enrolled keys, so a stranger's key is
    refused), and forget it in this browser. It was first kept per directory in `localStorage` (the demo does the same); since item 89 it is
    a non-extractable browser key in IndexedDB where the browser can (see there). "Publish my keys" signs with
    it when it is held, and an address that has an owner key but is not held here gets a message that says to
    restore it. Tests: `dirclient` natively (`TestOwnerKeyEnrollmentAndSignedPublish`) and
    `TestOwnerKeyForMyDirectoryEntry` in the browser. Slice 3 (this PR): watching the address. The person pins the log keys the server states (after a
    confirmation that says what that does and does not prove: trust on first use, consistency and not identity),
    and "Watch my address" reads the log through `WatchLog`, keeps the position and what lookups saw across reloads
    (`localStorage`, per directory), and lists entries made by somebody else or breaking the owner rules; the list
    stays until dismissed, a log that was cut back or rewritten is said in words, and a damaged saved position is
    dropped and said. Tests: `TestWatchMyAddress` natively and `TestWatchMyDirectoryEntry` in the browser. Slice 4 (this PR): other people's rows. The picker now carries the marks the client proved itself (the
    key's own attestation; "signed by its owner" only when the owner key is one the person confirmed), never the
    directory's word alone. When a row is signed by an owner key nobody confirmed (first seen, seen again, or
    changed), "Owner keys to confirm" shows the key's fingerprint and why, and a button that asks the person to
    compare it with its owner another way before pinning it; the pins are kept per directory and the lookup is
    redone, so the row earns the mark. Tests: `TestOwnerMarksOfOtherPeoplesRows` natively and
    `TestConfirmAnotherPersonsOwnerKey` in the browser. **Item 21 is done.** Left, small: rotating and resetting
    the owner key from the page (now item 87), and the component's note for an unconfirmed row
    ("not signed by the owner key") is worded for a key published before the owner key existed, which is not the
    reason here.
22. **Done (this PR): `walk()` searches every link.** Was: it took the first link whose `From` matched and whose
    signer was in the list, with no backtracking, so a dead-end first match could hide a reachable link and an
    unlock could fail after many key changes. Now it searches depth first over all links out of each list with a
    visited set of digests (a circle ends), and returns the path to the target.
    `examples/canvas-editor-showcase/encstore/bind.go`.
23. **Done (this PR): a `WatchState` in the old `made` format, or damaged, is refused whole.** Was: the
    `people-keys-demo` ignored the decode error, so `localStorage` state saved before `made` became records half-loaded
    and the half that loaded hid what the other half would have caught. Now `directoryclient.ParseWatchState` returns
    the zero state and `ErrBadWatchState` for anything that does not decode, and the demo forgets the stored value and
    says the next check starts from scratch. Any host that keeps `WatchState` should load it with `ParseWatchState`.
24. **Done (this PR): `ErrFilteredEnrolled` also counts the enrollment this run's audit finds.** Was: `WatchLog`
    refused a filtered answer only when the saved state already said the address was enrolled, so a first run (or a run
    whose new entries held the enrollment) accepted a log that an enrolled address's owner should never be shown
    filtered. Now, after the audit and the cross-check with the directory's own enrollment record, a filtered answer
    is refused when the audit finds the address enrolled, the directory agrees, and the watcher holds one of the
    address's owner keys (`WatchOptions.Owner`); nothing is remembered from that run. It does not fire for a caller
    without the owner key (a filtered log is what anybody else gets), nor when the directory says the address is not
    enrolled (that is the existing violation). `docstore/directoryclient/watch.go`.
25. **Done (this PR): the lint tool no longer drags protobuf into the library's module.** Was: `golangci-lint` was a
    `tool` line in the root `go.mod`, so its whole tree (protobuf and prometheus through `promlinter`, about 200
    `go.mod` lines and 950 `go.sum` lines) was in the module graph every user of the library resolves. Updating the
    tool would not have helped: the dependency is the linter's own. Now it is pinned in `tools/go.mod` (a module
    with only a `tool` line); `make lint`, `make wasm-lint` and `make lint-modules` build the binary from `tools/`
    and run it from the root, `make modules` checks that `tools/` is tidy, and the root `go.mod` and three example
    modules were tidied (`go mod tidy`). `google.golang.org/protobuf` is gone from the root `go.mod`.
26. **Done: committed binaries in history.** The git history was rewritten in 2026-10 and the seven binaries are gone
    (see [history-rewrite.md](history-rewrite.md)). `.gitignore` now blocks them from coming back. Priority: done.

### Queue, status and new entries (added by the second review pass)

Numbers 1 to 26 above are kept as they are, because other documents link them (backlog items 3, 4, 5, 7, 9, 10 and 12).
Items 1 to 8 are done and merged (1 and 2 earlier; 3 `RepairAll`, 4 the no-signer window, 5 revoked-signer tooling,
6 the SSH key tag, 7 the shared `ChallengeStore`, 8 the double first-passkey 409). Items 9 to 26 are open (the sessions part of item 7 is done by PR A; the
follow-ups in item 3 are taken over by the PRs below, all done). New entries start at 27, in order of urgency. **Kind** says `fixable` or
`inherent` (a real limit: it points to the [limits register](limits-register.md) and will not be fixed, only
reduced or documented).

**Queue order (the next PR starts only when the one before is merged):**

1. **PR A: shared sign-in sessions. Done (#99).** `auth.Manager` session and OpenID Connect login-state store, store-backed, so a
   token issued by replica A is valid on replica B (the "found, not fixed" part of item 7; also the last line of the
   passkey infrastructure backlog above, item 2).
2. **PR B: passkey challenge store. Done (#100).**
   1. The sweep no longer reads anything: records are named `passkey/challenge/<expiry minute>/<hash>`, the token is
      `<random>.<expiry minute>` (validated like a session token; a malformed one never reaches the store), and the
      sweep deletes by name after the minute and a minute of grace. Used markers keep the name of their ceremony, so
      the sweep finds them with no `Load`. The young-record budget (100 Loads a pass) is gone.
   2. The cap across replicas: sharded compare-and-set counters were weighed and rejected (a hot record written on
      every Begin and Take, leaks on a crash); the bound is the per-client `Limiter` on the Begin routes, with the
      cap as a backstop counted from the sweep's name listing. Worst-case overshoot: per-client rate x clients x
      sweep interval, never more than (replicas - 1) x cap. At the cap `Put` answers `ErrBusy` (503) with no sweep and
      no `Load`; a live challenge is never evicted.
   3. `store` mode on a store without conditional writes prints a multi-line warning and falls back to per-process
      challenges; `store-required` is fatal (same helper and message shape as `GOJIBERRY_DOCSTORE_SESSIONS`). Also
      warned: `GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES` set with `GOJIBERRY_DOCSTORE_PASSKEY_RPID` unset (ignored), and
      memory mode with `GOJIBERRY_DOCSTORE_REPLICAS` above 1.
3. **PR C: `RepairAll` follow-ups. Done (#101).** Code: `docstore/encrypt/repairall.go`, `docstore/docstore.go`.
   1. Header reads: the optional `docstore.HeadReader` (`LoadHead(ctx, id, n)`, first n bytes) and the helper
      `docstore.LoadHead`, which falls back to `Load` for an adapter without it. Implemented by the memory, localfs,
      sqlite, postgres (`substr`), s3 (ranged GET) and blob (`NewRangeReader`) adapters and by `ExpiringAdapter`
      (same deadline rules as its `Load`, then the inner head). Not by MongoDB (no cheap partial read of a binary
      field), not by the encrypting `Adapter` (it would hand out ciphertext) and not by any wrapper that has not
      thought it through; those are read in full as before. The scan asks for 128 KiB per file (the saving applies only to files larger than that; smaller ones are read whole either way): the frame header is
      inside the age stream, and age authenticates the first 64 KiB chunk whole before releasing a byte, so that is
      the least a file can be judged from. A head that does not settle a file (more recipients than fit) is
      followed by a full read of that file.
   2. `RepairAllOptions.EncryptPlaintext` (default off): items stored in the clear that `Skip` does not leave
      readable are encrypted and signed by the Repair and Rekey paths (needs `AllowPlaintext` and a `Signer`; `Encrypted`
      in the report; `DryRun` lists them in `Todo`). It changes stored bytes that other readers may expect in the
      clear: run `DryRun` first.
   3. Resume cursor: `RepairAllOptions.After` and `RepairAllReport.Next`. Limit and After walk a huge store in slices;
      `Next` is `""` and `Finished` set when the end was reached (a run cancelled before its first group with `After` `""` has `Next` `""` but not `Finished`). Groups before the cursor are not revisited. The caller persists it; `RepairAll` itself saves nothing.

**PRs A, B and C are done (#99, #100, #101):** the git history rewrite (item 26) was done first, so the queue ran one PR at a time.

New entries:

27. **Done (#103): lint failed on main with 37 findings (and as many under `wasm-lint`); it was not a toolchain problem.** Kind: fixable.
    Checked on 2026-10-07 with go1.26.0: `make lint` (which runs `go tool golangci-lint run ./...`, so the linter is
    built by the repo's own go toolchain from the `tool` line in `go.mod`, golangci-lint v2.5.0) started and exited 1.
    Fixed, with no `nolint` and no config change: errcheck (13) by checking the error in tests (`t.Fatal` on a failed
    write) or an explicit `_ =` with a reason for best-effort cleanup; staticcheck (19, plus more hidden by the default
    per-linter cap, so run with `--max-issues-per-linter=0 --max-same-issues=0` to see every one) per its advice
    (QF1008 embedded selectors, QF1001 De Morgan, S1016, S1011, ST1013, ST1018); unused (5) by deleting code with no
    caller on any platform: `auth.loginState`, `(*encrypt.Adapter).seal`, `(*ExpiringAdapter).related` and two unused
    test struct fields. `make lint` and `make wasm-lint` now report 0 issues. CI already runs both
    (`.github/workflows/ci.yml`, steps `make lint` and `make wasm-lint`), `make check` includes both, and
    [testing-workflow.md](testing-workflow.md) lists them. The earlier claim "built with go1.25 vs repo go1.26" is
    only true of the `golangci-lint` found on `PATH` (`/usr/local/bin`, v2.5.0 built with go1.25.1), which `make`
    does not use. Item 25 (protobuf through the tool) is left as it is. The nested modules are now linted too, by
    `make lint-modules` (in `make check` and CI). Priority: done.
28. **Done: unmerged commits on `origin/ccr-f005be71-limits`.** Kind: fixable (housekeeping). The branch no longer exists
    (it was deleted with the other old branches in the history rewrite, see [history-rewrite.md](history-rewrite.md)); its
    content was already on main. Priority: done.
29. **Done: leftover worktrees (`/home/user/gojiberry-*`, `/tmp/rr*`).** Kind: fixable (housekeeping). The stale worktrees
    were removed and pruned. Priority: done.
30. **Done (this PR): a fast clock is a note, not a violation.** Was: an auditor read an entry dated ahead of its own
    clock only as "dated in the future" once past the 10 minute rule, with no difference shown, and nothing below it.
    Now `AuditAddress` with `AuditOptions.Now` reports entries more than `NoteSkew` (a minute) ahead but within
    `FutureSkew` as `AddressAudit.Notes` (with the measured `Ahead`), and the violation past 10 minutes names the
    difference. Still inherent: there is no shared clock ([limits register item 11](limits-register.md#public-docstore-and-operations)),
    so run NTP on every replica and on the auditor. `docstore/encrypt/directory_owner.go`.
31. **Done (this PR): over the cap the sweep evicts the cheapest records first.** Was: `SweepRateLimits` deleted the
    oldest-written records past `MaxRateLimitRecords`, which under a flood are the slow, live callers, whose budget
    was then reset to full. Now `SweepRateLimitsStats` evicts buckets that are already full again first (they carry no
    information), then the ones that will be full soonest (least spent budget), and counts the evictions of a bucket
    that was not yet full (`EvictedLive`); `RunRateLimitSweep` logs that count. `docstore/limiter.go`. A flood from many
    addresses can still make live callers lose a spent budget once the cap is full of live buckets; that is the
    botnet limit, [limits register item 9](limits-register.md#public-docstore-and-operations).
32. **Done (#106): `RepairAll` no longer reports an item as "gone" for a keyring `ErrNotFound`.** Kind: fixable. Was: the
    walk read any `errors.Is(err, docstore.ErrNotFound)` from the repair as "deleted since the scan" (outcome "gone",
    not counted), so a custom `Keyring` returning `ErrNotFound` from `Recipients`, or the built-in `StoreKeyring`
    through its record lookup, made a live, unsigned item look deleted and `Left()` look clean; the re-key itself
    (`rekeyGroupFiles`) also swallowed such an error as "deleted since it was listed", so the item was even counted
    repaired. Reproduced by tests before the fix (`TestRepairAllKeyringNotFoundIsNotGone`,
    `TestRepairAllStoreKeyringNotFoundIsNotGone`). Now: `Adapter.bind`, the one place the adapter calls the keyring,
    cuts the `ErrNotFound` chain of a keyring error (the text is kept), so only a stored file that is really missing
    is `ErrNotFound` to a re-key; and `RepairAll` calls an item gone only if an `ErrNotFound` is followed by none of
    its files being stored any more (a head read of each), otherwise it is a `SkipError` and counted in `Left()`
    (`TestRepairAllNotFoundWithTheFilesStoredIsNotGone`). The `Keyring` comment says `Recipients` must not return
    `ErrNotFound` unless it means a missing record, and what is done with it. A real deletion is unchanged
    (`TestRepairAllIgnoresAnItemDeletedAfterTheScan`). Where: `docstore/encrypt/encrypt.go` (`bind`, `Keyring`),
    `docstore/encrypt/repairall.go`.
33. **Done (#105): `WatchLog` filtered to full: the cursor no longer passes the saved head.** Kind: fixable (narrow). Was: in
    filtered mode the run saved `st.Seq, st.Hash` = the signed head and `st.Cursor = max(read.next, cursor)`, and
    `read.next` can pass the head when the log grows between `c.Head` and the filtered scan. On the switch to the
    full read, entry `st.Seq` was proved to carry the saved hash, but nothing linked it to entry `cursor+1`
    (entries `st.Seq+1` to `cursor` were never fetched), so a server could show a fork consistent from `cursor+1`
    on. Reproduced by a test before the fix (`TestWatchLogDetectsARewriteBetweenTheSavedHeadAndAFilteredCursor`,
    `TestWatchLogFilteredCursorStaysWithinTheSavedHead`). Now: a filtered run saves `Cursor = min(max(read.next,
    cursor), head.Seq)`, so a filtered cursor is never past the signed head and the existing anchoring (`Prev ==
    st.Hash` when the read starts at `st.Seq+1`) covers the switch; the entries past the head are read again by the
    next run (not reported twice: `Reported`; not audited twice: an entry read again is dropped from the
    pending and audited lists when its number and hash match the pending one, and is `ErrLogCut` when the hash
    differs). The switch from a filtered state at the head with nothing past it no longer gives a false
    `ErrLogCut` (the head's entry is fetched when the read starts at or after it). A
    filtered state an older run saved with `Cursor > Seq` is clamped to `Seq` on load, so it stays valid and is
    checked. `WatchState` JSON is unchanged. Where: `docstore/directoryclient/watch.go`.
34. **Done (this PR): the blob backend is named unsupported for the conditional features.** It has neither `SaveIf` nor
    `DeleteIf`, and cannot be given them portably: `gocloud.dev/blob` can create only if the blob does not exist
    (`IfNotExist`) and has no ETag or generation match to send. `docs/public-docstore.md` 7.4a has a table of what each
    backend supports, what each feature needs, and what to use instead (S3 backend, PostgreSQL, MongoDB); the blob
    backend's `Config` comment says so. A provider-specific write hook (S3 `If-Match`, GCS generation, Azure ETag) is
    left until a provider is needed and can be tested against it. Item 9 still covers `DeleteIf` for S3.
35. **Done (this PR): directory log reads have budgets of their own.** Was: `readBudget` took the per-IP and the
    whole-server shard tokens from the lookups' limiters for log reads too, so a client that polled the log hard slowed
    the lookups of everyone behind its address and in its shard, and the reverse. Now a log read spends `dir-log`
    (per user, as before), `dir-log-ip` (120 a minute) and `dir-log-all-N` (1200 a minute over 16 shards), the same
    numbers as lookups but separate keys. `docstore/directory.go` (`readBudget`, `readKind`, `directoryLimits`).
36. **Done (this PR): the user can accept a rotated owner key.** Was: the first owner key a third party saw was kept for
    good, so after a legitimate rotation the lookups of that address showed "changed" forever. Now
    `Client.AcceptOwner(email, ownerPub)` remembers the key the user checked with its owner
    (`SeenAddress.Accepted`); rows signed by it, and by the first key, show as first-seen (remembered, not confirmed)
    instead of changed. A pinned key and the audited chain are untouched. Still to do: a button for it in the
    showcase People tab and a signed rotation chain a third party could follow without asking (entry 13 related).
    `docstore/directoryclient`.
37. **Done (this PR): the showcase explains encrypt errors in words.** Was: a person in the browser read the raw
    Go text, for example "use `Repair(AcceptUnsigned)`", which they cannot call. Now `components.ExplainError` sorts an
    error with `errors.Is` into a group (unsigned file after a key change, file not written by an owner, no signer,
    older state or replayed list, unreadable file, no key opens it, locked, changed elsewhere, safety record not kept,
    storage full, recovery key needed, offline) and gives a translated message that says what happened and what to do,
    with no Go name or API call. The raw text stays behind a native Details disclosure with a Copy button
    (`components.CopyText`); the message is the only `role="alert"`. An error nobody classified gets a generic
    message plus Details; the showcase's own messages (`tErr`, `enc.err.*`) pass through as they were, with the
    wrapped error as the detail. Keys `err.*` in en (the default text), de, fr and es. Where: `components/errors.go`
    (every component's error line and the showcase's key panel), `components/encerr` (the encrypt sentinels; it is a
    package of its own because `docstore/encrypt` cannot be imported by `components`).
38. **Done:** **directory "Unmapped" addresses are listed, warned about once per change, and have a documented
    procedure.** Was: counted and logged as a number, `ReconcileReport.Unmapped` dropped by both callers. Now
    `AdapterDirectory.UnmappedAddresses` returns the count, a capped list (`UnmappedCap`, 100 by default; pseudonyms
    only, first and last entry number and time, age) and the time it was computed; `GET /admin/directory/unmapped`
    serves it with the operator reset's credential and failure budget; the sweep and start-up log one warning per
    change of the count (with the pointer to the fix), not one per tick. An address whose last word is an operator
    reset, or whose entries are all voided, is no longer counted. Procedure: [encryption-and-passkeys.md](encryption-and-passkeys.md) ("Unmapped addresses (operator procedure)").
    Where: `docstore/encrypt/directory_recover.go`, `docstore/directory.go` (`handleUnmapped`).
39. **Done:** **a document that ends in `ErrUnsignedAfterFlip` is listed.** Was: `finishFiles` cleared `Pending`
    before its second look, so after the error `PendingDocs` (pending records only) and every "what is left to do"
    list omitted a document that may hold a late unsigned file, and only the error text named
    `Repair(AcceptUnsigned())`. Now the second look writes the files it found into the signed `keys:<ID>`
    record (`unsignedLate`, `unsignedLateSince`; the record stays `Signed` and not pending), and
    `Sharing.UnsignedDocs` lists those documents with the file ids, the time and the next step, from any client and
    after a restart. `Repair` or `Recover` with `AcceptUnsigned` clears the mark once no file is unsigned. `RepairAll`
    still skips such a document as `SkipForged`, and the list names the same document. `PendingDocs` is unchanged
    (re-keys in flight). Older builds ignore the new fields when they read the record and drop them when they write
    it (the files stay refused; only the listing is lost). Where: `docstore/encrypt/share.go`
    (`finishFiles`, `markUnsignedLate`, `clearUnsignedLate`, `UnsignedDocs`). The mark keeps at most 64 ids and a count,
    is written only onto the record the re-key just wrote (a replayed older record is not signed again), and its
    clearing never fails a re-key. `UnsignedDocs` is a full scan like `PendingDocs`; `Saver.UnsignedCheck`
    (`encrypt.UnsignedHook`) puts it in `VerifyReport.Unsigned`.
40. **Done (this PR): the store mode says what the challenge records hold.** Was: nothing told the operator that
    `StoreChallenges` keeps the pending registration e-mail and name in clear (`auth/passkey/challenge_store.go`),
    for the challenge lifetime (5 minutes by default, plus about 2 until the sweep removes it), under `passkey/challenge/<expiry minute>/<hash of the token>`.
    Now the start-up line of `GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES=store` says it and names the remedy (an
    encrypting adapter under that keyspace; see "Extension points" in
    [encryption-and-passkeys.md](encryption-and-passkeys.md#b1-extension-points)). The record is not changed: the e-mail
    is needed to finish the registration on another replica, and the key is already the hash of the token.
    `internal/docstorecmd/docstorecmd.go`.
41. **Signed-out same-e-mail passkey registration makes two accounts, by design.** Kind: inherent while the e-mail is
    a label ([limits register item 6](limits-register.md#encryption-and-keys)). Why it matters for later work:
    the e-mail verification work must not merge accounts on the bare label. Plan: none now; record it in the
    verification design. Priority: low. Where: `auth/passkey`. Plan to reduce the exposure: [inherent-limits-plan.md](inherent-limits-plan.md#41-signed-out-same-e-mail-passkey-registration-makes-two-accounts). Done in part (this PR), plan steps 1 and 4: the rule is written in `encryption-and-passkeys.md` (A.3, "The rule for e-mail verification") and `TestOneEmailLabelNeverMergesTwoPasskeyAccounts` fails on a merge, a link or a grant by label. Also done (#246): the sending side of e-mail verification, `mail`: one `Sender` interface with an SMTP adapter (STARTTLS that never downgrades, TLS from the first byte, or no TLS to loopback only; sign-in only over TLS; quoted-printable text with an optional HTML alternative; the standard library only) and a Resend adapter (`resend-go/v4`), chosen and checked at start by environment variables with `…_FILE` for secrets (see [configuration.md](configuration.md), which also explains why environment variables rather than TOML, YAML, JSON or protobuf). Tests run the SMTP adapter against a fake server that speaks the protocol (STARTTLS, implicit TLS, sign-in, refusals) and the Resend adapter against a local server. Also done (#247): the SMTP sender is tested against real Mailpit servers (`make mail-test`, `.github/scripts/mailpit-test.sh`; the `axllent/mailpit` image when Docker is running, else the binary): STARTTLS with sign-in, plain on loopback, the message read back through Mailpit's API (accents, long and dot-only lines, the HTML part), refusals. Run here with a build of Mailpit v1.31.5 because this machine has no Docker daemon; the image path and the CI job have not run. Left: the verification flow itself (a token store, the link, the "another account uses this address" notice, the link ceremony), step 2 and step 3 above, which needs a provider and credentials from the operator to try for real.
42. **A revoked signer can keep writing new frames until it is removed from every list.** Kind: inherent without a
    format change; the frame v4 plan is in item 5 above and in the
    [limits register](limits-register.md#encryption-and-keys) (item 1). Priority: low until the format work is
    scheduled. Where: `docstore/encrypt` (`verifyFrame`). Plan to reduce the exposure: [inherent-limits-plan.md](inherent-limits-plan.md#42-a-revoked-signer-can-keep-writing-new-frames-until-it-is-removed-from-every-list). **Design corrected (this PR):** the frame v4 plan named here and in item 5 does not work (a signer-claimed time can be backdated, the sequence number is per document, a revocation mark in a record the removed key can still sign proves nothing); [frame-revocation-design.md](frame-revocation-design.md) replaces it with one resumable `Sharing.RevokeSigner` operation, an optional signed manifest of the old key's frames that makes writes during the window visible, and plain wording about the window. No format change. **Step 1 built (this PR):** `Sharing.RevokeSigner(ctx, key, RevokeSignerOptions{Remove, Progress, MaxPasses})` signs everything again with the signer that stays (`RepairAll` with `Resign`), checks by a dry run that nothing the old key signed is left (running again, up to `MaxPasses`, if it wrote meanwhile; refusing, with `ErrRevokeCannotResign`, if a document cannot be moved, and `ErrRevokeStillWriting` if it keeps writing), calls the host's idempotent `Remove` to take the key out of the lists, and scans once more: a frame of the old key that appeared by then is refused as forged and listed in `Late`. Restartable by running it again. Tests: `TestRevokeSigner...` in `resign_all_test.go`. Left: the signed manifest of the old key's frames (design item 2), the wording in the showcase's removal dialog and the limits register (item 3).
43. **A link can be replayed past the 32-link window or when it was made before links carried a nonce.** Kind:
    inherent (register entry already exists: [item 12](limits-register.md#clients-and-storage)). Plan: none beyond
    a larger `Config.Links` for a store that changes keys often. Priority: low. Where:
    `examples/canvas-editor-showcase/encstore` (`Config.Links`, `walk`).
44. **An SSH-only store with no signed document file yet is undetected.** Kind: inherent, documented residual
    ([limits register item 12](limits-register.md#clients-and-storage); the first owner list is trust on first
    use). Priority: low. Plan to reduce the exposure: [inherent-limits-plan.md](inherent-limits-plan.md#44-an-ssh-only-store-with-no-signed-document-file-yet-is-undetected). Done in part (this PR), plan steps 1 and 2: with `Store.ConfirmFirstUse` (the showcase sets it) the first unlock with an SSH key of a list nothing on this device sealed stops with `*FirstUseError` and shows the list's fingerprint (`ListFingerprint`, 128 bits in eight groups); the person compares it with the one the keys panel shows where the store was made and confirms (`TrustList`; the confirmed digests are kept in this browser, a known-hosts file for stores); a list made or changed on this device, or opened by a passkey or recovery key through the chain of seals, is trusted without the question. Tests: `TestFirstUnlockWithAnSSHKeyAsksForTheListsFingerprint`, `TestFirstSSHUnlockOnANewBrowserAsksForTheListFingerprint`. Left: step 3 (the digest in the key directory log or a second store), and the docs' advice to unlock a new store with a passkey first.
45. **Unenrolled-address entry numbering by a holder of the directory key.** Kind: inherent while the address is not
    enrolled; enrolling an owner key closes it ([limits register item 7](limits-register.md#the-key-directory),
    point (d)). Priority: low. Plan to reduce the exposure: [inherent-limits-plan.md](inherent-limits-plan.md#45-unenrolled-address-entry-numbering-by-a-holder-of-the-directory-key). Done in part (this PR), plan steps 1 to 3: the showcase shows a standing notice while the signed-in address has no owner key; `directoryclient.Client.RequireOwner` makes `Lookup` return `ErrUnenrolled` together with the rows for an address without one (the host then asks the person to confirm the fingerprint another way); `AdapterDirectory.RequireEnrollment` (`GOJIBERRY_DOCSTORE_DIRECTORY_REQUIRE_ENROLLMENT=true`) refuses a publication for an unenrolled address with `ErrDirectoryEnrollmentRequired` (428), while enrolling itself stays open on the session. Tests: `TestRequireEnrollmentRefusesPublicationsForAnUnenrolledAddress`, `TestRequireOwnerRefusesToRelyOnAnUnenrolledAddress`, the notice in `TestOwnerKeyForMyDirectoryEntry`. Step 4, reshaped (this PR): `WatchLog` already lists every entry about the watcher's own address that the watcher did not make (the log shows pseudonyms, so a watcher cannot match the entries of addresses it merely looks up). What a client can detect for those is the number the server names: `Lookup` remembers the first log number shown for each key of an unenrolled address (`SeenAddress.Seqs`) and flags a row whose number later differs (`Key.SeqChanged`; a republication after a revocation looks the same), and the showcase's picker says so. Tests: `TestLookupFlagsAChangedLogNumberOnAnUnenrolledAddress`. The showcase setting is done (this PR): "Do not count a key as verified when its address has no owner key" (kept in this browser; `dirclient.Client.RequireOwner`, `dirclient.ErrUnenrolled` returned with the keys): the picker still lists the keys but without the verified mark and with a note to confirm the fingerprint another way. Tests: `TestRequireOwnerReturnsTheRowsWithErrUnenrolled`, `TestRequireOwnerSettingWithholdsTheVerifiedMark`. Nothing is left for this item that code can do; the rest is inherent (the limits register).
46. **Done (this PR): see item 11.**
47. **Already listed, kept as they are (no duplicate entry):** item 15 (stale-tab renewal throttle), 16 (per-process
    public counters), 17 (snapshot overshoot), 19 (report dedupe key, slow-upload rate floor, old env-var warning,
    wide-cap migration note), 20 (closed, see its entry), 21 (showcase directory publish with proof of
    possession and an owner-signed entry), 22 (`walk` first match), 23 (demo `WatchState` old `made` format;
    clear on decode error, `examples/people-keys-demo/main.go`), 24 (`ErrFilteredEnrolled` and the audited
    state), 25 (protobuf as an indirect dependency).

48. **ARIA attributes across the canvas editor and components. Slices 1 to 4 done; the rest open.** Kind:
    fixable. What: roles, accessible names and labels, live regions, a sensible focus order, and keyboard operation
    of every control (toolbar, menus, dialogs, pickers, the canvas itself). Why it matters: people who use assistive
    technology cannot use controls that have no role, name or keyboard path, and it is a legal requirement in many
    places. **Done:** (3 of the plan, first) axe-core 4.10.2 is vendored (`internal/e2etest/axe`, MPL-2.0, unmodified)
    behind `Tab.Axe`, and `TestNoAccessibilityViolationsAxe` runs it over the showcase's document, every side tab and
    every menu open, with the find bar, and `TestNoAccessibilityViolationsAxeInTheKeyDirectory` over the encryption
    panels and the key directory (owner key, backup, pinned log keys, owner keys to confirm); a new violation fails
    the build. Found and fixed: (a) `aria-readonly` on the `role="document"` copy of the text (not allowed on that
    role); (b) **a menu lost the keyboard focus when it opened** (the bar was re-created because the backdrop was
    inserted before it), so a keyboard user's next key went to the page: the backdrop now follows the bar; (c)
    **Escape did not close a menu** and **Enter and Space did not choose an entry** (entries were focusable and
    click-only): `MenuBar` closes on Escape from anywhere inside it and an entry is chosen with Enter or Space
    (`components/menu.go`, with unit tests and `TestMenuEntryChosenWithTheKeyboard`). **Slice 2 (this PR):** the
    focus primitive and the arrow keys. `xango.Event.MoveFocus(root, items, step)` moves the keyboard focus within a
    widget while an event is handled (`domrt` fills it in; a test or the server gets a no-op), and menus use it: Up,
    Down, Home and End among the entries of a menu (wrapping), Right into a submenu and Left back out, Left and Right
    along the bar (taking an open menu along), Down and Up on a button open its menu at the first or last entry, and
    Escape puts the focus back on the menu's button. Unit tests for every key and `TestMenusAreOperatedWithTheArrowKeys`
    in the browser. **Slice 3 (this PR):** tabs. `components.Tabs` and the showcase's side-panel tabs follow the tabs pattern: a
    `tablist`, `role=tab` with `aria-selected`, only the selected tab in the Tab order (`TabIndex`), and Left, Right,
    Home and End move the focus along the row, wrapping, and open the tab they reach (`TabKeys`, `components/tablist.go`);
    the sixteen side tabs were sixteen tab stops. The toolbar is now inside a labelled `region` landmark (axe found
    the labelled tablist outside any landmark). Tests: `TestTabsKeyboard`, `TestSidePanelTabsAreOperatedWithTheArrowKeys`.
    **Slice 4 (this PR):** dialogs. `Modal` (so `Dialog`, `Confirm`, `Alert` and the Asker's questions) takes the
    focus when it opens (the box has `tabindex=-1`; a field with `autofocus` inside it keeps it), keeps Tab and
    Shift+Tab going round what is inside it (`ModalKeys`, `components/modalkeys.go`), closes on Escape as a click on the
    backdrop does, and gives the focus back to what opened it when it closes. The giving back is `domrt`'s: an element
    with `data-gj-modal` notes the focused element when it is created and, when it is removed, returns the focus there
    one frame after the render (moving a node in the same render would drop it again; found by a test). Tests:
    `TestModalKeyboard` and `TestDialogFocusIsTrappedAndReturned`. **Open:** (1) the audit checklist per component,
    which axe cannot replace: the same arrow-key model for the colour picker, dropdowns and the other pickers; (2) **heading levels**: the components' panel titles are `h4` under a page whose
    headings are in the screen-reader copy, so `heading-order` is disabled in the test (`axeDisabled`) until the
    levels are decided; (3) axe over the other examples and the states it cannot reach by clicking (dialogs, the
    colour picker, a table cell menu); (4) the canvas itself with a screen reader (item 49). Priority: high (first of
    the accessibility group). Where: `components`, `examples/canvas-editor-showcase`, the browser e2e.
    Also done: heading levels in the panels. The side panel now starts with a hidden level 3 heading (the tab's name), so the titles inside it (level 3 and 4) follow the page's level 2 without a skip; the axe test no longer disables `heading-order`, and runs over 21 tabs instead of 7 (the wider run found an empty table header in the statistics table, now a hidden "Measure" label). A long submenu (the display preferences grew past a short window with the 1000-step entry) now scrolls instead of running off the screen (`menu.css`). The one place the rule is still off is the Check tab test with a deliberately skipped heading in the document.
49. **Screen-reader output while typing (voice-over).** Kind: fixable in part (the canvas is not DOM text). What: an
    accessible text mirror of the canvas (a hidden DOM or an ARIA live region) that announces typed characters, caret
    moves, selection and formatting changes. Why it matters: a canvas gives a screen reader nothing to read, so a blind
    person cannot write in the editor. Plan: build the mirror; throttle announcements and add verbosity settings (off,
    words, characters, formatting); verify with NVDA, JAWS and VoiceOver and record the results. Priority: high.
    Where: the canvas editor input layer and `components`. Done in part (this PR): `components.TypingAnnouncer` (a pure state machine) says typed words when they are finished (or after a pause), or each character (a space, a line break and a tab by name), what a deletion removed, a paste or completion as one piece, the line the caret goes to when it changes line (arrow keys, a click) and the characters or words it passes over, and formatting changes at the caret ("Bold on", "Bold off", the paragraph style); it holds what comes within 0.5 s and says it together, keeps at most six sentences, and ignores edits that leave the caret elsewhere (another person's). It speaks through `components.TypingLive`, a polite status region of its own (`#typing-live`) that changes even for a repeated sentence. All of it is off until chosen in View > Display preferences ("Typing: say each word / say each character", "Say formatting changes at the caret", "Read the line when the caret moves"; kept in this browser), because a screen reader may already echo the hidden input. `canvas.TextFlow.CaretFormat` gives the format at the caret. Tests: `components/typingannounce_test.go`, `canvas/caretformat_test.go`, and in the showcase e2e, with `internal/screenreader`'s live-region watcher, `TestTypingSpeaksFinishedWords`, `TestTypingSpeaksCharactersWhenAsked`, `TestTypingSaysNothingUnlessAsked`, `TestTypingReadsTheLineAndFormattingChanges`, `TestTypingPreferencesAreInTheMenu`. Left: only a person with NVDA, JAWS and VoiceOver can say whether the live region is spoken at a good pace, whether it doubles what their reader already echoes from the hidden input, and which default suits (steps 18 to 21 of the manual script in `accessibility-testing.md`); the line read is the line as drawn (a wrapped line is the part on screen), and no "formatting" level beyond the marks listed (colour and size are not said).
50. **More accessibility tools and configs.** Kind: fixable. What: respect high contrast and reduced motion; font size
    and line spacing; a dyslexia-friendly font option; caret and selection visibility; a shortcuts help and key
    remapping; zoom; color-blind safe palettes; clear focus indicators. Why it matters: needs differ by person, and a
    single default fits few. Plan: expose each as a setting with a safe default, honor the matching media queries, and test each in the e2e. Done in part (this PR):
    `components/a11y.css` honours `prefers-reduced-motion`, `prefers-contrast: more` and `forced-colors`, and the same through
    root attributes the page sets (`data-gj-motion="reduce"`, `data-gj-contrast="more"`, `data-gj-text="large|larger|largest"`,
    `data-gj-spacing="wide"`: WCAG 1.4.12 spacing); the showcase applies the kept choice at start (`prefs.go`, key
    `showcase:a11y`; test `TestDisplayPreferencesAreApplied`). Left: the menu that sets them (item 51), a dyslexia-friendly
    font (needs a font asset), caret and selection visibility on the canvas, key remapping, colour-blind palettes, and
    the canvas itself following the text size. Priority: medium. Where: `components`, the canvas editor, `i18n`.
    Also done (this PR): the canvas follows the text size, and key remapping is kept. `canvas.TextFlow.TextScale` (and the xango prop) multiplies the size and line height of every run, whatever sizes the runs carry; the showcase sets it from the text-size setting (125%, 150%, 200%) so the text on the sheets grows with the page around it (`TestTextScaleMakesAllTheTextLargerWhateverItsSizes`, `TestTextSizeSettingReachesTheTextOnTheSheets`). Key remapping was already in the Keys tab (change, clear, reset, search, conflicts said aloud) but was lost on reload; the person's own bindings are now kept in this browser (`showcase:keys`, only what differs from the defaults; `TestKeymap*`, `TestChangedShortcutsAreKept`). Left: a dyslexia-friendly font (needs a font asset), caret and selection visibility on the canvas (width and colour follow the contrast setting), colour-blind safe palettes for remote cursors and comment colours.
    Also done (this PR): colour-blind safe colours and a strong caret. `canvas.TextFlow.PeerPalette` recolours the remote cursors and selections from a palette (`canvas.ColourBlindPalette`, Paul Tol's bright scheme without its two palest colours) by each person's place among the names on show, so each keeps one colour and no two share one until the palette runs out; the name label on each caret stays, so colour was never the only cue. The Preferences tab has "Colour-blind safe colours for other people's cursors". `TextFlow.StrongCaret` draws the caret as a thicker dark bar with a light edge and the selection in a stronger tint, and the high-contrast setting turns it on in the showcase. Tests `TestPeerPaletteGivesEachPersonOneOfTheColours`, `TestStrongCaretIsThickerAndTheSelectionStronger`, `TestParsePrefsColourBlindSafeColours`. Left: a dyslexia-friendly font (needs a font asset).
51. **Preference menu.** Kind: fixable. What: one settings panel for the accessibility options above plus language,
    theme and editor behavior; keyboard accessible, searchable, with reset to defaults and import and export. Why it
    matters: settings scattered over the UI are hard to find, especially with a screen reader. Plan: define a
    preference schema, build the panel on it (it needs item 48 first), store locally, then item 52 syncs it.
    Priority: high. Where: `components`, the showcase. Done in part (this PR): View > Display preferences in the showcase
    (`prefs.go`): text size (default, 125%, 150%, 200%), wide text spacing, more contrast and reduce motion, ticked when
    set, kept in this browser, announced to screen readers, with "Reset to defaults"; keyboard use is the menu bar's own.
    Test: `TestDisplayPreferencesMenu`. Left: language, theme and editor behaviour in the same schema, search, import and
    export, a panel instead of a menu, and a reusable `components` preference schema.
    Also done: import and export of the preferences. The display preferences menu has "Export these preferences…" (a JSON file, one setting to a line) and "Import preferences from a file…"; an imported file keeps only the values the page knows (`parsePrefs`, which the kept copy goes through too), is refused above 16 KB or when it is not JSON, and says so in the live region. Tests `TestParsePrefs*`, `TestEncodedPrefsReadBackTheSame`, `TestPreferencesExportAndImport`; translated in de, fr, es.
    Also done (this PR), the rest of the plan: the preferences are described once (`prefSettings` in the showcase: group, name, toggle or choice) and both the Display preferences menu and the new **Preferences** tab are made from that list. The tab shows every setting in five groups (text and display, language and theme, shared editing, typing and reading aloud, editor) and has a search box that narrows the list by words in a setting's name, its group or its values ("1 preference" / "No preference matches." in a status line); it has the export, import and reset buttons too. The schema gained the interface language (kept, and the language of the next visit unless `?lang=` says otherwise; the View > Interface language menu keeps it too), a dark theme (the chrome's palette through `data-gj-theme` on the root; the sheets of the document stay white) and an editor behaviour, whether typing an opening bracket or quote adds the closing one. Tests `TestPreferencesTab`, `TestAutoClosePreference`, `TestParsePrefsLanguageThemeAndAutoClose`; the axe test covers the new tab; translated in de, fr, es.
52. **Save preferences, styles, templates, fonts and other configs in a central server.** Kind: fixable. What: a
    per-user config store (docstore documents under a system prefix, authenticated, encrypted with the owner keys by
    default, versioned, with conflict handling across devices and a quota); a local fallback that works offline; fonts
    as stored assets with size limits and a licensing note; shareable templates and styles. Why it matters: assistive
    settings follow the person across devices, and they are sensitive (they can reveal a disability), so they must be
    encrypted and never logged. Plan: key layout and schema, encryption by default, version and merge rules, quota,
    offline queue, asset limits, sharing. Depends on PR A (shared sign-in sessions). Priority: medium. Where:
    `docstore`, `auth`, `components`.
    Done in part (#235): the new `userconfig` package, the store's core. A `Store{A, User}` keeps named items under
    `config/<hash of the user>/<name>` (the new `docstore.ConfigPrefix`: refused by the document API in any case,
    never given a lifetime by expiry, and not in the encrypting adapter's skip list, so the owner's keys encrypt every
    item; a test shows the raw store holds no readable setting and no address in the keys). Each item has a revision
    inside the stored bytes: `Put` with a stale revision returns `ErrConflict` and the current item, `Update` reads,
    merges and retries up to five times (a race of three writers keeps every change), writes are conditional where the
    adapter supports `SaveIf`. Limits per item (256 KiB) and per user (2 MiB), names `a-z0-9._-`, `DeleteAll` for the
    right to be forgotten; nothing logs a name or a value. Also done (#236): the HTTP route. With sign-in on, the docstore
    server serves `GET /me/config` (the names), `GET`, `PUT` and `DELETE /me/config/{name}` for the signed-in person
    (`docstore.Server.UserKey`, the address or the provider id); the item's revision is its `ETag`, `PUT` and `DELETE` need
    `If-Match` ("0" for a new item; it also keeps a page on another site from changing settings, since sending the header
    cross-origin needs a CORS preflight), a stale one gets 412 with the current `ETag` to merge from, over the limit gets
    413, nobody signed in gets 401, answers are `no-store` and carry no stored value or store detail. The server keeps
    the bytes it is sent, so a client that wants them hidden from the operator encrypts them with the owner's keys
    first. Also done (#238): `userconfig/client`, the device side. It keeps a local copy of every item in a
    `Cache` (so settings work offline and survive a restart), applies `Set` and `Delete` at once and queues them, and
    `Sync` pushes the queue with `If-Match` and then pulls what other devices wrote: on a 412 it fetches the server's
    copy and calls the app's `Merge` (default: the local change wins), a 401 gives `ErrSignedOut` and a network or 5xx
    failure `ErrOffline`, both leaving the queue intact. It uses `net/http` only, so it runs in the browser's wasm; the
    tests run it against the real `Service`. Also done (#239): fonts as assets. Items named `font.<name>` hold a container of the font file and its
    licence note (`EncodeFont`, `DecodeFont`, `Store.PutFont`/`GetFont`): a font without a licence note, or a file that is not
    a TrueType, OpenType or WOFF font by its signature, is refused (422 over HTTP), and the generic `Put` cannot store an
    unchecked blob under a font name. Fonts have their own limits, 4 MiB each and 16 MiB in all by default, that neither
    use up nor are used up by the room for settings. Also done (#243): sharing. Items named `template.*`, `style.*` and `tokens.*` can be exported as a
    bundle (`Store.Export`: one JSON file with a kind, name, note and the data under a SHA-256, carrying no identity) and
    imported by another person (`Inspect` shows what it holds and checks it, writing nothing; `Store.Import` writes it
    into the importer's own store, never replacing an item: a taken name gets `-2`, `-3`, and the importer's limits
    apply). A changed, foreign, too large or unreadable file is refused with a reason that does not repeat it, and a
    theme is imported only if it passes the `tokens` checks (readable contrast). Nothing is readable across people on the
    server. Fonts and ordinary settings are not shared: a font's licence decides whether it may be passed on. Left:
    wiring the client to the showcase's preferences and a share dialog, and loading a stored font into the editor.

Suggested order of the accessibility group: 48, 51, 49, 50, 52.

**Urgency order of the open work:** the queue above, then 27 (lint), 28, 32, 31, 30, 34, 35, 36, 37,
the older items 9 to 14 and 18, then 38 to 47 and the older minors in 15 to 25. Inherent entries: 41 to 45 and part of
30.

53. **Session store reads once per authenticated request.** Kind: inherent to correct logout across replicas. What:
    `sessionstore.Store.GetSession` has no cache, so each authenticated request is one `Load` of the shared store
    (on a remote backend, a network round trip), and two under `GOJIBERRY_DOCSTORE_EXPIRY_TTL` (`ExpiringAdapter`
    reads the deadline record too). A well-formed random bearer token always reaches the store (one `Load` per
    `/docs` request that carries an `Authorization` header, found or not), and that is not covered by the login
    limiter, which limits only `GET /auth/<provider>/login`. Why: a positive cache would keep a logged-out token working
    on other replicas for as long as it lives. Plan: if the cost matters, add an optional per-process cache with a
    stated maximum staleness (for example 5 seconds) behind an explicit option; off by default. Priority: low.
    Where: `auth/sessionstore`. Plan to reduce the exposure: [inherent-limits-plan.md](inherent-limits-plan.md#53-the-session-store-is-read-once-per-authenticated-request). Done in part (this PR), plan steps 1, 3 and 4: an unknown token is remembered as unknown for `MissFor` (one second by default, bounded at 4096 entries per process), so a flood of random tokens costs one read per token per second; `CacheFor` (off by default; `GOJIBERRY_DOCSTORE_SESSION_CACHE=5s`, at most one minute) keeps a live session in the process, a logout on the process that handles it takes effect at once and on another replica within `CacheFor`, and the cache never outlives the session; `Store.Stats()` counts reads and hits. Tests: `cache_test.go`. Step 2 (this PR): `Manager.SetMissLimiter` (`GOJIBERRY_DOCSTORE_AUTH_MISS_LIMIT=true`, off by default) spends from a per-client budget for each bearer token that is not a session; a client over it is not looked up in the store until its budget is back (its requests are unauthenticated meanwhile, valid tokens included: clients behind one shared address share the budget, hence the option and the advice to set the trusted proxies). Tests: `miss_limit_test.go`.
54. **Done:** **login routes wrote to the store without a rate limit.** Was: `GET /auth/<provider>/login` is
    unauthenticated and, in `store` mode, creates a record (bounded at 10,000 pending logins, then 503), so about 17
    requests a second filled the bound in 10 minutes and kept real logins out until the sweep cleared it (about 10 to
    12 minutes); the in-memory store had the same flaw at 100,000. Now: `Manager.SetLoginLimiter` puts a `Limiter`
    in front of the route, keyed `login|<client address>`, before anything is written; `cmd/docstore` sets it in every
    sessions mode from `GOJIBERRY_DOCSTORE_LIMITER` (the passkey routes' 20 at once and 30 a minute) with the client from
    `GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES` (`passkey.TrustedProxies`) or the remote address. Over budget:
    `429` with `Retry-After`, no state written. **Residual limit:** a flood spread over many addresses can still fill
    the 10,000 states (one address spends only its own budget), and then logins get 503 until the states expire and
    the sweep clears them, up to about 12 minutes. Without `..._TRUSTED_PROXIES` behind a proxy all clients share
    one budget. Where: `auth/auth.go`, `auth/http.go`, `internal/docstorecmd`.
55. **Done in part (this PR): the public offer's anonymous sessions can be shared through the store.** Checked: there are no
    CSRF tokens or cookies in `auth` (the bearer header is the only credential, the OIDC state is the redirect's CSRF
    check and is shared). Was: (a) the public offer's anonymous sessions (`public.SessionStore`, item 16), (b) the
    default `MemoryChallenges` and per-process rate limiters when their `store` modes are off, (c) the session and
    state counts used for the bound. Now (a) and the session counts of (c) are `public.StoreSessions`
    (`docstore/public/storesession.go`; `GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS=store|store-required`): one record per session
    named by the hash of its token, `SetDoc` and `Extend` as compare-and-sets (exactly one of any number of replicas
    sets the one document), the caps and the reserve applied to counts rebuilt from the store at most every 30 seconds
    and kept current by the replica's own writes (so replicas can overshoot a cap by what they create between two
    recounts, never grow without limit), and `Reap` (the service's sweep) deleting the expired. Tests: the lifecycle
    over two replicas, a race on `SetDoc`, each cap and the reserve, the overshoot, and two services over one store.
    Still per process: (b) when their `store` modes are off (a choice, with a warning at start-up), and the
    in-flight byte count (which protects the process's memory). The byte budget is shared since item 16; the report
    counters are item 86. Priority: low. Where: `docstore/public`, `auth/sessionstore`.
56. **Opt-in atypical-speech support in voice to text (Project Relate / Euphonia style).** Kind: fixable in part
    (depends on what exists outside the project). What: let people whose speech standard recognisers handle badly
    dictate into the editor with a recogniser that fits them. **Feasibility finding, to verify at the spike:**
    Project Relate is a consumer Android app (a personal model trained from many recorded phrases; Listen, Repeat and
    Assistant features), and no public developer API or SDK was found. Project Euphonia is a Google Research effort
    that publishes open-source tooling to train personalised speech models for research; its dataset is not available
    externally, Google points to the Speech Accessibility Project (University of Illinois, English) for shared data,
    and no hosted Euphonia API exists. Why it matters: dictation is an access route for people who cannot type, and
    the usual recognisers fail on exactly these users. Plan:
    1. A provider-neutral voice-input interface (start and stop, interim and final transcripts, language, errors) with
       providers: the browser Web Speech API, an on-device model, an optional server recognizer, and an adapter slot
       for a personalised model.
    2. Opt-in and consent: off by default, consent text saying where audio goes, nothing recorded or stored without
       consent, the setting kept in the preference store (sensitive, encrypted).
    3. Research spike: confirm the current availability and licence of Relate and Euphonia output (any API,
       import or export of a personal model, partner programme), and whether a user can bring a model trained with
       Euphonia's open-source tools or a Speech Accessibility Project based model.
    4. If no integration path exists: a bring-your-own recogniser endpoint (user-supplied URL and key) and OS-level
       dictation passthrough (Android keyboard dictation types into the editor through normal input events, so the
       editor must handle IME and composition input well and expose the live transcript to the screen-reader
       mirror).
    5. Privacy and security: no audio leaves the device by default, TLS and short retention for any server path,
       never log audio or transcripts, rate limits.
    6. Testing with users with atypical speech.
    7. Docs of what is and is not supported.
    Depends on the preference menu (51) and the central config store (52); ties to the voice-over item (49).
    Priority: medium, research spike first. Where: `components`, `docs`.
    Spike result and first slice (PR #299): `docs/euphonia-port.md` records the finding. Project Euphonia's app is
    Apache-2.0 (Flutter app that records phrases and uploads them to a Firebase bucket the developer creates, a Colab
    fine-tuning notebook, a Whisper example service); trained weights, the base model, the data set and any backend
    are not in it, and Project Relate has no API. So a personal model cannot be produced here; what is built is the
    flow and the training-free help, as a clean-room Go implementation. Package `speechcoach`: phrase sets (built in
    for en, de, fr, es; a person's own list), a forgiving session (record, retake, skip, back, resume, no time limits),
    quality hints from signal analysis on 16 kHz PCM (silent, too short, too quiet, clipping, noisy, long pause),
    WAV and resampling, a local dataset with a manifest, zip export and a validated import (checksums, size limits,
    no path escapes), sealing (AES-256-GCM) for storage, a vocabulary that becomes the recogniser's initial prompt,
    a correction memory that suggests earlier fixes and lists the person's words for the autocomplete list (79), a
    recogniser client for the voiceserver contract (bring your own endpoint), and the optional contribution to a
    research endpoint (none by default, a confirmed exact package per sitting, no identifiers, revocable by a
    delete request). `voice/listen` and `cmd/voiceserver` take `prompt` and `language` (whisper.cpp `--prompt`,
    `-l`). Tests: `speechcoach/*_test.go`, `voice/listen/whisper_bias_test.go`, `voice/server/server_bias_test.go`.
    Not possible without a trained personalised model (on-device fine-tuning needs a GPU and an ML framework;
    no hosted Euphonia or Relate model exists): recognising an atypical voice better than the base recogniser
    does. Options: export the dataset and fine-tune outside (the Euphonia notebook or whisper.cpp tooling), then
    serve the result behind the voiceserver contract; or run local whisper with the vocabulary bias, which helps
    names and rare words only. Left: the showcase Speech tab, testing with users with atypical speech (step 6),
    steps 1 and 2 as a provider interface in the editor.
    Also done (this PR): [speech-model.md](speech-model.md) (why ONNX and not go-torch or tfgo, how a shared base model and per-person adapters fit, what federation here does and does not guarantee) and package `speechmodel`: the Model/Adaptable/Trainer/Backend contract, a digest-pinned model loader, a checksummed adapter format, and the federated arithmetic (delta, clip, noise, weighted average, apply), with a stub and an http backend so the editor can be wired before a model exists. **Left:** an ONNX backend (own module) and the model repository.
    Also done (this PR): the showcase Speech tab. Record the phrases of the built-in set for the interface language with the microphone (opened only while recording; a forgiving session: retake, skip, previous, next, quality hints that never block a take); the takes stay in the page, with a count and seconds, a zip download (the `speechcoach` export format) and a delete that needs a second press; and dictation into the document through a recogniser address the person types (off while the field is empty; one recording is sent, only when they press stop; the text goes in at the caret in editing mode, and the tab says what to do otherwise). Translated in de, fr and es; axe-clean; `TestSpeechTabRecordsPhrasesAndDictates` uses Chromium's fake microphone. **Left:** keeping the recordings between visits (sealed with the unlocked store's key, `speechcoach.Seal`), learning from corrections as the person edits dictated text (`Corrections`), the contribution flow in the page, the ONNX backend and a model, and trials with people who have atypical speech.
57. **Done in part (PR #285): privacy-first telemetry with no personal data.** Kind: fixable. **Built:** package `telemetry` (closed allowlist of five names with enumerated values, strict decoder that refuses unknown fields, day-granular batches, a `Collector` that keeps nothing and sends nothing until the person opts in, a `Receiver` that only increments counters, 90-day retention, k=5 reporting), `docstore.WithTelemetry` (`POST /telemetry`, rate limited with a hashed in-memory key, totals in `/admin/usage`, `GET /admin/telemetry`), the operator kill switch `GOJIBERRY_DOCSTORE_TELEMETRY=on`, the `telemetry.refused` audit kind, [docs/telemetry.md](telemetry.md), and the showcase Privacy tab (exact-JSON preview, 'send nothing' default, Do Not Track and Global Privacy Control respected, accessible markup). Tests: allowlist, extra-field and bad-day refusals, opt-out sends zero requests (browser e2e with a request counter), exact preview equals request. **Shared sink layer (#302, see 81):** the usage counts and the operator's server metrics are two separate producers (different data, different consent); both write to one `observe.Sink` abstraction, and `telemetry.Receiver.WithBatchHook` hands every accepted batch to it, so the totals can also go as aggregates to the sinks named in `GOJIBERRY_OBS_USAGE_SINKS` (only after the person opted in and the operator enabled the receiver; what the browser may send is unchanged). Done in #302: the abstraction, the hook and the builtin routing; left: the PostHog and OpenTelemetry sinks (next PRs). **Done in PR #304:** step 5 and the leftovers. Client sampling (`Collector.SetSampling`: one visit in 1, 10 or 100, drawn at opt-in and when the rate changes, so a visit sends all of its counts or none; a visit outside the sample keeps, shows and sends nothing and the tab says so). An exportable local debug log (`Collector.SetLog`, `Log`, `LogJSON`, `Withhold`; off by default and only for someone who opted in; in memory, at most 100 entries of minute, outcome `sent`/`failed`/`not-sampled`/`no-server`, day and the allowlisted counts, so it can say no more than the request; never sent; gone on closing the page or choosing to send nothing; 'Download the log'). The Privacy settings entry in View > Display preferences. Persistent totals (`Counters.Persist`, `Snapshot`, `Restore`; `docstore.TelemetryKey` = `telemetry:totals` through the server's Adapter, written a few seconds after a change; reserved from `/docs`, not a document; bounded by the allowlist, 90 days and 1 MiB; read strictly, so a damaged or tampered record is refused whole and replaced; holds no identifier, address or user agent). Noise (`Counters.SetNoise`, `GOJIBERRY_DOCSTORE_TELEMETRY_NOISE=<2..1000>`, `Server.WithTelemetryNoise`): randomized rounding of every reported total to a multiple of the step before the k threshold, keyed by a stored secret seed so repeated asking gives the same answer; differential-privacy-style, not a proven (epsilon, delta) guarantee, and the stored totals stay exact. Tests: `TestSamplingSendsFromOneVisitInN`, `TestTheLocalLogIsOffByDefaultAndHoldsOnlyWhatWouldBeSent`, `TestTheLogIsBounded`, `TestOutsideTheSampleTheLogStillShowsWhatWouldHaveBeenSent`, `TestTotalsSurviveARestartAndABurstIsOneWrite`, `TestRestoreRefusesWhatARequestCouldNotHaveCounted`, `TestAStoreThatCannotBeReadDoesNotStopTheCounting`, `TestNoiseRoundsSmallCountsToTheStepAndIsStable`, `TestNoiseIsRightOnAverage`, `TestNoiseSeedSurvivesARestartSoTheAnswerDoesNotChange`, `TestUsageTotalsSurviveARestartThroughTheAdapter`, `TestADamagedTotalsRecordIsReplacedNotTrusted`, `TestTheTotalsRecordIsNotReachableThroughTheDocumentAPI`, `TestTheReportCanBeRoundedByTheOperator`, and the browser tests `TestLocalLogShowsAndExportsWhatWasSent`, `TestSamplingKeepsAVisitOutAndSaysSo`, `TestPrivacySettingsAreInThePreferencesMenu`. **Open:** step 7 (scrubbed crash reports, separately opt-in); several server processes on one store each write their own totals (last write wins; run one counting process). Original plan: What: a minimal, aggregate-only usage and
    health signal for the operator, designed so that no personal data can be collected. Why it matters: the project
    needs to know which features and browsers matter and where it is slow, without tracking anyone. Plan:
    1. Policy doc first: collect the minimum, aggregate only, no personal data by construction (no user ids, e-mail,
       IPs, document ids, names or contents, keys, free text, file paths, raw error messages or stack traces with
       data), no cross-session or cross-device linking, no fingerprinting, no third-party trackers, no telemetry
       cookies.
    2. Opt-in, default off (alternative to decide with the maintainer: anonymous counters on with a clear notice and
       a one-click opt-out). The setting lives in the preference menu (51) with a plain-language description and a
       "show exactly what would be sent" preview; an operator kill switch (env var); Do Not Track and Global Privacy
       Control respected.
    3. The event schema is an allowlist enforced in code: fixed event names and enumerated values (feature used,
       action counts, error codes, performance buckets such as load time and wasm size class, browser family and
       major version, locale), coarse buckets, nothing finer than a day, no sequence reconstruction. A schema test
       fails if a field outside the allowlist is emitted.
    4. Transport: batched TLS to the operator's own server, a write-only reserved route under a system prefix, rate
       limited with the existing `Limiter`, no IP logging (the server discards the source address), a retention
       limit, aggregation on write, a k-anonymity threshold before any count is reported (optionally noise).
    5. Client-side sampling, and a local-only debug log the user can export themselves for support.
    6. Security: no auth tokens in telemetry requests, separate from sessions, the route cannot probe the store,
       abuse limits against flooding and poisoning.
    7. A crash and error reporting variant that scrubs on the client (error code and component only), separately
       opt-in.
    8. Docs: a table of what is collected, generated from the allowlist, with why, retention, how to turn it off and
       how to self-host; legal notes (GDPR: aggregated anonymous data, still document the lawful basis and the
       opt-in text); a consent UI accessible to screen readers (ties to ARIA item 48).
    Depends on the preference menu (51) and the config store (52). Priority: medium, policy doc and allowlist before
    any code. Where: `docs`, `docstore`, `components`.
58. **Done in part (this PR): small leftovers from the PR #99 and #100 reviews; only the last remains.** (1) the retry
    limit of `StartChallengeSweep` is a named constant (`startSweepAttempts`); (2) the `NewStoreChallenges` and sweep
    docs point to `StartChallengeSweep` (`RunChallengeSweep` stays exported, documented as the variant that blocks and
    does not retry); (3) `StartChallengeSweep` retries only when the listing failed (a `sweepListError`), so a record
    that can never be deleted no longer adds 3.5 s to every start; (4) the net-delta counting drift is documented as
    a known, bounded one on `approx`; (5) a login `clientIP` that answers "" falls back to the connection's address
    instead of putting every client in one budget; (6) the unused login limiter of a passkeys-only manager is
    noted in `withSessions`. Tests: `TestStartChallengeSweepRetriesOnlyAFailedListing`,
    `TestLoginLimiterFallsBackToTheRemoteAddressWhenClientIPAnswersNothing`. (7) done: the login, miss and passkey
    limiters key an IPv6 client by its /64 (`internal/clientkey`, the same rule as `public.ClientKey`, which now calls it); test
    `TestLoginLimiterGroupsAnIPv6SubscriberByItsSlash64`. Nothing open.

## Backlog: accessibility, documents and operations (59 to 84)

Entries 59 to 84 continue the numbering after 58; nothing above is renumbered. Cross-references: 48 is ARIA, 49 is the
screen-reader text output, 51 the preference menu, 52 the central config store, 57 telemetry. **Kind** is `fixable`
unless stated.

59. **Keyboard-only completeness.** Kind: fixable. What: every action and every dialog reachable and operable with the
    keyboard alone; no focus traps; visible focus rings; skip links; a shortcut cheat sheet with remapping. Why it
    matters: people who cannot use a pointer must be able to do everything others can. Plan:
    1. Inventory every action and dialog and mark its keyboard path; list the gaps.
    2. Fix focus order, add focus return after dialogs close, and remove traps (a test tabs through each dialog).
    3. Add visible focus rings that meet contrast rules, and skip links (to the canvas, the toolbar, the comments).
    4. Build the cheat sheet from the one shortcut table, with remapping stored in the preferences (51, 52).
    5. An e2e that drives the whole editor without a pointer.
    Where: `components`, `examples/canvas-editor-showcase`, the browser e2e. Priority: high. Dependencies: 48; remapping
    needs 51. Done in part (this PR): the option lists have a keyboard model (`components/listboxkeys.go`): Down or Up on the button of a `Dropdown` or `FontSelector` opens the list and goes to an option (the search field first, for fonts), the arrows, Home and End move among the options, Enter or Space chooses with the focus back on the button, Escape closes with the focus back, Tab closes and leaves; the options are focusable but out of the Tab order. Tests: `TestDropdownKeyboard`, `TestFontSelectorKeyboard`. The colour and date pickers are native inputs, so the browser already gives them the keyboard. Left: the inventory of every action (step 1), skip links, the cheat sheet and remapping, and the pointer-free end-to-end run (step 5).
    Also done: skip links. `components.DocumentView` starts with a `Skip links` navigation, the first stops for the keyboard: "Skip to the document" and "Skip to the side panel" (out of sight until focused; choosing one moves the focus to the document area or the side panel, by script, because a link to `#id` does not work under the page's `<base href>`). Translated in de, fr and es; `TestSkipLinksAreTheFirstStopAndMoveTheFocus`.
60. **Accessible text mirror of the canvas.** Kind: fixable in part (the canvas is not DOM text). What: a hidden DOM or
    a live region kept in sync with the canvas model, covering paragraphs, tables, lists, images, links, comments,
    tracked changes, structure, alt text, heading levels, table headers and the document outline. Why it matters:
    without it a screen reader reads nothing, so the editor is closed to blind people; this is the base for 61 to 69
    and 79. Highest priority of the group. Plan:
    1. Define the mirror as a pure function of the canvas model (one source of truth, never edited by hand).
    2. Render real elements (`h1`..`h6`, `p`, `ul/ol/li`, `table` with `th scope`, `img alt`, `a`) so the browser
       exposes the structure.
    3. Keep it in sync on every model change with a diff, not a rebuild; keep the caret and selection mapped both
       ways; announce changes through a polite live region with the verbosity levels of 49.
    4. Expose the outline as a navigable landmark list; mark comments and tracked changes with text a reader says.
    5. Tests: model-to-DOM unit tests, e2e with axe-style checks, manual NVDA, JAWS and VoiceOver runs (69).
    Where: the canvas editor input layer and `components`. Priority: highest. Dependencies: extends 48 and 49. Status (this batch): the mirror exists (`components.AccessibleText`: headings, lists, quotes, code, tables with `th`, the selection, a live region), now with the language of a run (item 64) and the description of an image (item 61); the e2e checks it with axe-core (item 69). Done in this batch: links are real `<a>` (safe schemes only, `rel=noopener noreferrer`); the first table row is `thead` with `th scope="col"`; the headings are listed first in a `nav` landmark "Document outline" whose entries focus the heading and move the canvas caret; commented text is marked (`mark`, "Commented text:", `aria-details`) and the comments (author, excerpt, text, replies, resolved) follow in an `aside` region; suggested changes are `ins`/`del` with "Suggested addition/removal by ..." (done before); caret mapping both ways: every block carries its start in the editor's text stream (`export.Block.Start`/`Lead`, `data-gj-start`), the block with the caret is `aria-current="location"`, the mirror's own selection follows the canvas caret or range while the keyboard is not in a field, and a selection made in the mirror (a reader's cursor) moves the canvas caret and selection (`components/a11y_map.go`, the showcase `a11ymirror.go`). Tests: `components/a11y_mirror_test.go`, `a11y_map_test.go`, `export/start_test.go`, and in the showcase e2e, reading the browser's accessibility tree with `internal/screenreader`, `TestMirrorStructureForAScreenReader`, `TestMirrorSaysCommentsAndSuggestedChanges`, `TestMirrorCaretFollowsBothWays`. Left: the mirror is rebuilt from the model on each render and relies on the xango tree diff, with no block-level diff of its own; the caret mapping counts runes of the visible text (a suggested removal or a footnote mark inside a block is one rune each, a table is one position), and in Edit mode the reader follows the editor's own input field, so the mirror's selection is not moved then; and the manual runs with NVDA, JAWS and VoiceOver (steps 14 to 17 of the script in `accessibility-testing.md`), which only a person with a real screen reader can do.
61. **Alt text, image descriptions and structure checks.** Kind: fixable. What: alt text required or prompted when an
    image is inserted; a checker for missing alt text; optional on-device suggestions (no network); checks for reading
    order, heading level skips, empty headings and low colour contrast. Why it matters: an image without a
    description is a hole in the document for a blind reader. Plan:
    1. An insert prompt with "decorative" as an explicit choice; store alt text in the model.
    2. Checker functions over the model, each returning a finding with a location and a fix.
    3. Optional local suggestion hook behind a setting, off by default, with no data sent anywhere.
    4. Contrast check using the document's own colours; heading and reading-order checks.
    Where: the model, `components`, the showcase image insert. Priority: high. Dependencies: 60; feeds 62. Done in part (this PR): steps 1 (model only), 2 and 4 (heading and contrast). An image now carries `Alt` and `Decorative` (`canvas.Image`, `docfile.ImageItem` fields 9 and 10, `canvas/xango` `alt`/`decorative` props), exported as the `alt` of HTML (empty for a decorative image), the alt of Markdown and `[Image: ...]` in plain text, and read in the reading copy ("Image: ..."; a decorative one is left out, one without a description is named as such). The new package `a11ycheck` has `Check(doc, Options) []Finding` (rule, severity, message, fix, block or item, text) with the rules `image-alt`, `heading-skip`, `heading-empty` and `contrast` (WCAG 4.5:1, 3:1 for large text, against the run's background or the page). Tests: `a11ycheck` (per rule), `TestImageAltTextIsExported`, `TestImageDescriptionIsInTheReadingCopy`. Step 1 is finished in the showcase (this PR): Insert > Picture… (or the Pictures tab) takes a png, jpeg, gif or webp file and puts it at the caret with no description; the Pictures tab then asks for one, with "Decorative" as an explicit choice, and lists every picture of the document with a description field, a "Go to it" button and a "No description yet" mark; the Check tab flags a picture with neither. Tests: `TestInsertAPictureAndDescribeIt` (with axe). Left: on-device suggestions (step 3), reading order of floating objects (typed lists and table headers are checked, see 62), heading levels beyond 3 (the export folds them to 3), colour names beyond the basic ones, and the translations of the new messages.
    Also done: colour names. The contrast rule understands every CSS colour name (`golang.org/x/image/colornames`, 148 names) instead of twelve; `rebeccapurple`, added to CSS later, is not in that table and is still skipped.
62. **Document accessibility checker panel.** Kind: fixable. What: a panel that lists findings from 61 and others,
    jumps to each, offers a fix, and can gate export (warn or block, set by the user or the operator). Why it matters:
    people need to learn about a problem before they send the document. Plan: (1) a finding schema (rule, severity,
    location, fix); (2) the panel, keyboard and screen-reader accessible; (3) one-click fixes where safe; (4) an export
    gate with an explicit override; (5) tests per rule. Where: `components`, the showcase. Priority: high.
    Dependencies: 60, 61. Status: the findings and the first rules are the package `a11ycheck` (item 61); `Finding` now carries its position in the text stream (`Offset`, `Item`) and sees heading levels up to 6 (`export.HeadingLevel`). **Done in part (this PR):** `components.CheckPanel` (severity written as a word, the fix beside each finding, a live summary, a "Go to it" button per finding) and the showcase's Check tab, which runs `a11ycheck.Check`, announces the result and moves the caret to a finding. Tests: `a11ycheck` (positions, levels), `TestCheckPanelListsFindingsWithWordsAndAGoButton`, `TestCheckTabListsFindingsAndGoesToThem` (with axe). Steps 3 and 4 (this PR): a finding names its one-click action (`Finding.Action`): make a skipped heading the next level, return faint text to the default colour, or go and describe a picture (nothing is invented); the panel shows the button only where there is one and checks again after a fix. The export gate (`a11ycheck.GateMode`: none, warn, block) is chosen in the Check tab and kept in this browser; both export paths of the showcase go through it; "block" refuses an export with errors in words until the person allows that one export. Tests: `TestGateDecides`, `TestFindingsNameTheirOneClickAction`, `TestCheckPanelFixButtonsAndTheExportGate`, `TestExportGate`. Finished (this PR): (a) the operator setting: `<meta name="gojiberry-min-export-gate" content="warn|block">` in the served page is the least strict gate the person may choose (`GateMode.AtLeast`, `ParseGateMode`); below it the Check tab's choices are disabled with a note, and with `block` there is no "allow this export anyway" (`OverrideAllowed`); a value that is not a gate reads as `block`; it is not read from the query string or local storage; (b) rules `table-header-missing` (error: a table of two rows or more whose first row has no text), `table-header-empty` (warning; cells covered by a merge are not counted) and `list-fake` (warning: two or more paragraphs in a row starting with a typed bullet or number that are not list items); the model has no header flag (row 0 is the header in every export) so "not marked" cannot occur; (c) the empty-heading fix `ActionToParagraph` ("Make it a plain paragraph", then it checks again). Translations (de, fr, es) of the new interface messages. Tests: `TestTableHeaderRow`, `TestTypedMarkersAreNotAList`, `TestEmptyHeadingCanBecomeAParagraph`, `TestOperatorMinimumOfTheGate`, `TestCheckPanelGateFloorCannotBeLowered`, `TestCheckTabFindsTableHeadersAndTypedLists`, `TestCheckTabFixesAnEmptyHeading`, `TestOperatorMinimumExportGate`; see `docs/accessibility-testing.md` section 4. Server-side enforcement (PR #300): package `docstore/exportgate`. `Decide(bytes, mode)` runs `a11ycheck.Check` and `GateMode.Decide` on a stored document (JSON, binary or text format) for any host job that sends documents on; `Wrap(handler, mode)` puts the gate in front of the docstore server's one export-bound route, `GET /docs/{id}?for=export` (or the header `X-Gojiberry-Purpose: export`), which the server runs after sign-in and the access list have allowed the read. `cmd/docstore` reads `GOJIBERRY_DOCSTORE_EXPORT_GATE=warn|block` (a typo stops the server). `block` answers 422 with the error count in `X-Gojiberry-Export-Errors` and no content, and has no override (as `OverrideAllowed(block)` says); `warn` serves with `X-Gojiberry-Export-Gate: warned`; a 401, 403 or 404 passes through untouched; a document the server cannot read as a document (an end-to-end encrypted store's ciphertext) is refused under `block` and served `unchecked` under `warn`. The docstore has no exporter of its own, so this route is the integration point and is documented in [server-access.md](server-access.md); the editor's ordinary load and save are not gated (a document with errors must open to be fixed) and a reader who fetches without the purpose gets the stored bytes as before, so this is a floor for the host's own export and send paths, not copy protection. Tests: `TestDecideByMode`, `TestWhatCannotBeReadAsADocumentIsRefusedUnderBlockAndFlaggedUnderWarn`, `TestBlockRefusesTheExportRouteButNotTheEditorsLoad`, `TestWarnServesAndSaysHowManyErrors`, `TestAnAnswerThatIsNotTheDocumentPassesThroughUntouched`, `TestOnlyGetOfOneDocumentIsExportBound`, `TestExportGateSettingIsParsedAndATypoStopsTheServer`. Left: the page-side minimum is still only the page's (the server floor is the one to rely on); a server floor for an encrypted store needs the host's decrypting job; reading order of floating objects and text boxes (the model has none yet); no one-click fix for `list-fake` or the table header rules (a typed list cannot be converted without guessing the markers); the finding messages are English (only the interface is translated).
    Also done (this PR): a one-click fix for `list-fake`. The finding now carries `ActionToList` (bullets or numbers), the length of the run and the typed markers to remove (`Finding.Spans`, from `markerLength`); the showcase makes the paragraphs a list, then deletes the markers last first so the offsets hold ("Make them a list", translated in de, fr, es). Tests `TestTypedMarkersAreNotAList` (the span arithmetic) and the Check tab test (the fix clears the finding, no `•` is left and the reading copy has `<li>`). No fix for the table-header rules: only the person can name a column.
63. **Accessible exports.** Kind: fixable. What: tagged PDF with language, title, reading order and structure tags;
    semantic HTML with landmarks, headings, table headers and alt text. Why it matters: an accessible editor that
    exports an inaccessible file defeats the purpose. Plan: (1) map the model to PDF tags and to semantic HTML in one
    place; (2) set document language and title; (3) validate with a PDF/UA and an HTML checker in CI; (4) report
    unmapped elements in the fidelity report of 72. Where: the export code, `docfile`. Priority: high.
    Dependencies: 60, 62, 72. Done in part (this PR), plan step 2: the document has a language (`docfile.Document.Language`/`SetLanguage`, kept in Meta under `lang`, validated as a BCP 47 tag) and `export.Snapshot.Lang` carries it to HTML (`<html lang>`), XHTML and the EPUB (`dc:language`, `xml:lang` on its pages), Word (the default run language `w:lang` and the core properties) and puts the title in the PDF information; the showcase sets it in Format > Language of the document and tags the reading copy with it. Tests: `TestExportsCarryTheDocumentLanguageAndTitle`, `TestDocumentLanguageIsInTheReadingCopyAndTheExport`. Left: tagged PDF (structure tags and `/Lang`, which the PDF library cannot write), table header cells and landmarks in the HTML, validators in CI (step 3), the fidelity report (step 4). Tagged PDF done in part (PDF/UA step): `export.PDF` marks what it draws (`pdf_tag.go`) and writes a structure tree as an incremental update (Document, H1 to H3, P, BlockQuote, L/LI/Lbl/LBody, Table/TR/TH/TD, Figure with Alt, Caption; table borders and decorative images are artifacts), with `/MarkInfo`, `/Lang`, `/StructTreeRoot` and `/DisplayDocTitle`; `pdfua.Check` (a catalog and structure-type checker) gates it in the tests, and poppler reads the result as tagged. Then (#280): link annotations are in the tree (each link token is a `Link` element holding its text and an `/OBJR` to the annotation, which gets a `/StructParent` key, `/Contents` and `/F 4`; the `ParentTree` maps the key back), a list item at a deeper `Block.Level` is an `L` inside the `LI` before it, and a run with its own `Format.Lang` is a `Span` (a `Link` for a link) with `/Lang`, a tag `lang.ValidTag` accepts (a new BCP 47 syntax check in `lang`). `pdfua.Check` now reports a link annotation that no `/OBJR` covers (matched by `/StructParent` and `/Rect`, as the reader library exposes no object numbers) and a catalog or element `/Lang` that is not a valid tag. Tests: `TestLinkAnnotationsAreInTheStructureTree`, `TestLinksInCodeBlocksAndAcrossPages`, `TestNestedListsNestInTheStructureTree`, `TestRunLanguagesAreTaggedAsSpans`, `TestPopplerReadsTheTaggedFile` (pdfinfo says Tagged: yes, pdftotext reads the text), `TestPDFUAChecksFailWhenALinkIsLeftOutOfTheTree`, and in `pdfua` `TestLinkAnnotationOutsideTheTreeIsReported`, `TestElementLanguageMustBeAValidTag`. Left: table cells do not yet split their text into language spans or links (they draw no links), code is drawn without links, and a full audit with veraPDF or PAC (not run here). Step 4 (unmapped elements in the fidelity report of 72) is done for what the exporters write: the report names what a format drops that accessibility needs (alt text, language, headings, table cells: a PDF drops row spans and cell alignment, RTF drops alt text and language, Markdown and plain text drop pictures and tables), and Word now writes alt text and run language.
    Also done (this PR), the audit by a Go validator: `make pdfa11y-test` runs pdfa11y (speedata, MIT, the machine-checkable Matterhorn checks) on the exporter's PDFs; its first run found, and this PR fixed, the missing XMP metadata (PDF/UA identifier and title), table header cells without `/Scope`, lists without `/ListNumbering`, pages without `/Tabs`, a nested list that was beside its item's body instead of inside it, and four embedded fonts that all carried the name `go` (each variant is now a family of its own). All checks pass on the test documents (a table, image, lists, links, languages, bold and italic). veraPDF and PAC still have not been run; pdfa11y says itself it is not a replacement.
    Also done (this PR): the exported HTML is checked with axe in the browser tests (`TestExportedHTMLPassesAxe`: a document with headings, a table, lists, a quote, code and a link is exported, shown in the page and checked with every rule). It found two real gaps, now fixed: the exported page had no `<main>` landmark (the content now sits in one, after the contents navigation; `TestHTMLPutsTheContentInOneMainLandmark`), and `<html>` has no `lang` when the document has no language, which stays the person's to declare (the Language tab's "Use as the language of the document" does it, and the test uses it).
64. **Language and direction.** Kind: fixable. What: per-run language tagging so a screen reader switches voice;
    right-to-left and bidirectional text; vertical text; IME and composition input. Why it matters: mixed-language and
    RTL documents are common, and composition input must not break the caret or the mirror. Plan:
    1. Add a language attribute per run and a document default; surface it as `lang` in the mirror (60).
    2. Verify canvas rendering of bidi, RTL and vertical text; fix caret movement and selection across direction
       changes.
    3. Test IME composition (preedit shown, no mirror announcement until commit, no autocomplete popup, see 79).
    4. Browser e2e with scripted composition events plus a manual check with real IMEs.
    Where: the canvas editor text layout and input layer. Priority: medium. Dependencies: 60. Done in part (this PR), step 1: `canvas.Format.Lang` (a BCP 47 tag, checked by `ValidLang`; `TextFlow.SetLanguage` tags the selection or the text typed next), saved in `docfile` (field 18), written by the HTML export as `<span lang>`, and carried into the screen-reader copy (`A11yBlock.Runs`, `A11yRun`: a span with `lang` for each tagged stretch, so a reader switches voice); the showcase has Format > Language of the selection. Tests: `TestValidLang`, `TestSetLanguageTagsTheSelection`, `TestAccessibleTextMarksTheLanguageOfRuns`, `TestHTMLWritesTheLanguageOfARun`, `TestSelectionLanguageReachesTheReadingCopy`. Left: a document default language of its own (the page language is used), setting the language from detection, bidi/RTL/vertical layout checks (step 2), IME composition (steps 3 and 4).
    Also done (this PR): step 3, IME composition, and the language from detection. The editor's input layer cleared its hidden input on every `input` event, which ends an input-method composition and put each preedit step into the document. Now `canvas/bind_js.go` leaves the input alone between `compositionstart` and `compositionend`, inserts the committed text once at the end (taken from the input, or the event, so Chrome's and Safari's different orders of the last events both give one copy), and ignores the keys an input method uses (Enter to choose a candidate, arrows, Backspace: `isComposing` or key code 229) instead of treating them as editor keys. `TestInputMethodCompositionInsertsOnceAtTheEnd` scripts both orders (fails without the change). Not done: drawing the preedit on the canvas at the caret and placing the method's candidate window there (it appears at the offscreen input), and a manual check with real IMEs. The document's own language was already a document setting (Format > Language of the document, saved in the file); the Language tab now has "Use as the language of the document" for the language it detected (`TestDetectedLanguageCanBecomeTheDocumentLanguage`). Still open: bidi, RTL and vertical layout checks (step 2).
65. **Cognitive and motor options.** Kind: fixable. What: distraction-free mode; plain-language labels; forgiving
    undo (long history, undo of the last save); adjustable timeouts; dwell or switch access; large-target mode. Why it
    matters: needs differ, and short timeouts or small targets lock people out. Plan: (1) list every timeout and
    remove or make it adjustable; (2) a large-target mode with a size scale; (3) a single-switch scanning mode and
    dwell click as an experiment; (4) a plain-language label set in `i18n`; (5) settings in the preference menu.
    Where: `components`, `i18n`, the canvas editor. Priority: medium. Dependencies: 50, 51. Done in part (this PR): step 1, the audit: the pages and components have no time limit on what a person must do (no auto-dismissed message, no countdown); the timers in the code are network and storage timeouts (`domrt/browser`, `saver`) that the person does not race. The limits that exist are lifetimes, all stated where they are set: a sign-in session 30 days, a passkey ceremony 5 minutes (the authenticator's own prompt waits on the person), the owner key reset 24 hours (a safety delay, not a deadline), a public-offer session 48 hours idle. Steps 2 and 5: View > Display preferences has "Large targets" (every button, tab, menu entry, field and select at least 44 by 44 pixels, check boxes 24) and "Distraction-free mode" (the toolbar, side panel and status bar are hidden; the menus stay and bring them back), both kept in this browser and set as `data-gj-targets` and `data-gj-focus` on the root (`components/a11y.css`). Test: `TestLargeTargetsAndDistractionFreeMode`. Left: a size scale beyond one step (step 2), switch scanning and dwell click (step 3), the plain-language label set (step 4), a longer history and undo of the last save.
    Also done: the long undo history. The display preferences have "Keep many undo steps (1000)" next to "Save memory" (20 steps); the default stays 200. Kept per browser (`memory: high`), test `TestLowMemoryModeKeepsFewerUndoSteps`, translated in de, fr and es.
66. **Collaboration accessibility.** Kind: fixable. What: announcements for remote cursors, joins and leaves, new
    comments, and conflicts, through a polite live region, with a setting for how much is said. Why it matters:
    collaboration cues are visual (coloured cursors); without text a blind collaborator cannot tell anyone is there.
    Plan: (1) an event-to-message table with throttling and grouping; (2) a presence list that is real DOM; (3) tests
    with two sessions. Where: `collab`, `components`. Priority: medium. Dependencies: 60, 71. Done in part (this PR): step 1: `components.CollabAnnouncer` turns changes of a shared session into short sentences for the live region: people joining and leaving (grouped: "2 people joined: Cy, Di."; someone who comes and goes before anything is said is not announced), the connection lost and back (the first connection is not news), a sharing problem and its end; at most one sentence per two seconds, what comes sooner is held and said together. How much is said is a preference (everything, the connection only, nothing) in View > Display preferences. Step 2 was there: the presence list is real DOM (`components.Presence`). Step 3: unit test of the announcer and the two-browser live test checks that Ana hears "Bo joined.". Left: announcements for remote comments and for conflicts, and a per-person setting for remote cursors. Also done (#191): comments others add to the shared document are announced ("Bo commented.", "3 new comments.") at the "announce everything" level, held and grouped like joins and leaves; `CollabAnnouncer.NoteComment`; the showcase finds the new ones by their replica's id prefix (the author name is not reliable there), e2e in `TestLiveEndToEnd`. Also done (this PR): replies and resolved threads others make are announced ("Bo replied.", "2 new replies.", "1 comment thread was resolved."; `CollabAnnouncer.NoteReply`, `NoteResolved`, same level, hold and grouping as comments); the showcase compares each comment's replies and resolved state with what it saw (`commentSeen`), takes the person's own replies and resolving into that snapshot as they are made so they are not announced to them, and looks again whenever the number of comments, replies or resolved threads changes (before, only the number of comments did). Tests: `TestCollabAnnouncerSaysRepliesAndResolvedThreads`, and `TestLiveEndToEnd` (both transports) checks that Ana hears the viewer's reply, the viewer hears Ana resolve it, and neither is told of their own act. Also done (this PR): conflicts and cursor positions. `collab.Session` counts `Merged` (the changes made while offline, or kept from an earlier visit by the queue, when the connection comes back and meets the server's copy; the client says how many on `Hello`, `Event.Waiting`, counted as they are made so a resend that wins the race with the stream does not hide them) and `Rejected` (the ops the server refused); `CollabAnnouncer.NoteMerged` and `NoteRejected` say "3 changes were merged." and "A change could not be applied." at the levels everything and connection-only (work kept or lost matters whoever is there), grouped and spaced like the rest. Remote cursors stay silent: a per-person setting in Display preferences ("Shared editing: a Where is button for each person") adds a "Where is Bo?" button per collaborator to the presence list, and pressing it says once "Bo is in paragraph 2 of 13, after \"...\"" (`components.WhereSentence`; "has text selected", "at the start of", "has no cursor"). Tests: `TestSessionCountsMergedAndRejectedChanges` (two sessions, network gated, over HTTP and WebSockets), `TestCollabAnnouncerSaysMergedAndRejectedChanges`, `TestWhereSentenceSaysThePlaceOnlyWhenAsked`, `TestPresenceWhereButtonsAreOptIn`, and the browser tests `TestLiveEndToEnd` (both transports: no button until asked for, moves not announced, the answer) and `TestChangesWaitingSurviveAReload` (the merge is announced after the reload). Also done: true conflicts are named. `collab.Bridge` remembers the paragraphs edited here in the last 10 seconds (`OnConflict`, `collab/conflict.go`); when other people's operations arrive and one of those paragraphs is no longer there as it was, the session counts it (`Session.Conflicts`, `ConflictWith`, from the presence list) and the announcer says "Bo changed the paragraph you were editing. Both changes were kept." (or "Someone", or a count; said at the important and all levels; de/fr/es). The merge itself already keeps both changes character by character, so the sentence is not an error. Tests: `TestConflictIsReportedForAParagraphEditedHere`, `TestCollabAnnouncerSaysWhoChangedTheParagraph`, e2e `TestChangedUnderYouIsSaid`. Refined (this PR): a paragraph is found by the ids of its characters, not by its text, so two paragraphs with the same words are not mixed up (an insert placed after one of its characters, or the deletion of one, is a change to it); the window is `Bridge.ConflictWindow` (10 seconds by default); the name of someone who has left is remembered, so "Someone" is only said for a replica that never gave a name. `TestConflictTellsEqualParagraphsApart`, `TestConflictWindowIsConfigurable`, `TestConflictNamesSomeoneWhoLeft`. What stays true, by design: "recently edited here" is a time window (the document is a CRDT with no vector clock to say two changes were truly concurrent, and a change made seconds after yours is worth saying anyway); "merged" counts the person's own offline changes that met the server's copy, which is what it says; and the position is by paragraph and quoted words, not by line or page. Nothing is left in this item but the manual screen-reader run (item 69).
67. **Done (#182, reading aloud in the showcase): read the selection or the document aloud.** Kind: fixable.
    Was: read the selection or the whole document aloud with the browser speech API, with pause, speed and a
    highlighted current sentence. Now: `voice/read.Chunks` (moved out of `components` with the voice packages) turns the screen-reader blocks (the same order and
    wording as 60, with per-run languages from 64) into sentences of at most 200 characters, each with its language;
    the showcase "Read aloud" menu speaks the selection or the document through `speechSynthesis`, a bar shows the
    sentence being spoken with previous, pause or resume, next and stop (keyboard reachable), and the speed (slow,
    normal, fast) is a kept preference. Only voices that run on the device (`localService`) are picked, and the menu
    says so; with none for a language the browser's default voice for it is used, which is the browser's own choice
    and may use a network service. Open: choosing a network voice on purpose, highlighting the sentence in the
    canvas (not only in the bar), and word-level highlighting from the boundary events. Priority: low.
    Where: `components`. Priority: low. Dependencies: 60, 64.
68. **Captions and transcripts.** Kind: fixable, only if media embedding exists or is planned. What: captions and a
    transcript field for embedded audio and video, a caption track editor, and a checker rule. Why it matters: media
    without text excludes deaf readers. Plan: (1) decide whether media embedding is in scope; (2) if so, require a
    caption or transcript link on insert; (3) add the checker rule to 62. Where: the model, `components`. Priority:
    low. Dependencies: 61, 62.
    Done in part (#296), step 1 decided as "the text model, not a player": the editor has no audio or video (nothing in
    `canvas`, `components` or the showcase embeds a clip; the only audio in the code is voice dictation), so this
    adds what makes sense without it. `docfile.MediaItem` (`Item.Media`, protobuf field 11; the goldens `rich.pb`,
    `rich.pbtxt` and `schema.proto` were regenerated with the protobuf library and the codecs match them byte for
    byte) is a clip by reference: URL, kind (audio or video), title, a WebVTT caption track (`Captions`) or its
    address (`CaptionsURL`), a transcript, and size. `CueText` reads the words out of a WebVTT file (no header, notes,
    numbers, timings or cue markup) and `MediaItem.Words` is the transcript, else the captions' words. Step 3, the
    checker (`a11ycheck.MediaFindings`, run by `Check` for every clip, counting a clip as one object in the text
    stream): a video with no captions is an error (`media-captions`), audio with no words is an error and a video
    with no words a warning (`media-transcript`), a clip with no title is a warning (`media-title`), each with what to
    do; so the block gate refuses a video with no captions. Step 2, "require on insert", is the same function: a host
    that inserts a clip calls `MediaFindings` on it first and refuses while it returns an error. The exports carry
    the text: every format (HTML, EPUB, Word, PDF, RTF, Markdown, plain text, braille) writes a clip as a line naming
    it as a link and, under "Transcript", its words (`docfile.MediaText`, used by `export.FromDocument` and the
    Markdown and text exporters), and the fidelity report of 72 says the clip is not embedded (`media`, simplified)
    and the caption track is not written (`media-captions`, dropped, its words are the transcript). The editor opens
    a clip as that text (`ToItems`), so nothing is lost from view. Tests: `TestCueTextIsTheWordsOfACaptionFile`,
    `TestMediaTextNamesTheClipAndCarriesItsWords`, `TestMediaOpensAsTextAndDocumentsKeepTheirClips`,
    `TestMediaItemIsSavedByEveryCodec` and the golden tests in `docfile`; `TestMediaFindingsRequireCaptionsAndTranscripts`,
    `TestCheckFindsMediaInTheDocument`, `TestACheckedClipHasItsWordsInTheExportAndAnUncheckedOneIsRefused` in
    `a11ycheck`; `TestEveryExportCarriesTheTranscriptOfAClip` and `TestFidelityReportSaysAClipIsNotEmbedded` in
    `export`. Left: there is no way to insert a clip in the showcase or the canvas (no player, no element, so the
    Check tab never shows these rules for a document made there; they run for documents made by a host or an
    importer), a caption track editor (the component of the plan), the caption track itself in the exports (a
    `<video>` with a `<track>` in HTML, a media part in Word), importers that read clips (Word and EPUB files with
    embedded media are still dropped, counted as drawings in the import report), audio description of what a video
    shows (only a transcript field to put it in), and the words "Video", "Audio" and "Transcript" in the written text
    are English only.
    Also done (this PR): `<video>`/`<audio>` and `<track>` in the HTML export. A clip is no longer turned into a link for HTML (`export.FromDocumentWithMedia`, used by that exporter alone; the other formats still write the link with the transcript): a `MediaClip` block is written as a `<figure class="media">` with the player, a `<track kind="captions">` (the WebVTT text as a data address, or the caption file's address; `srclang` is the document's language), the title as `<figcaption>` and the transcript under it in `<details>`. An address that is not safe to link to (`javascript:`) leaves the player without a source, the title and transcript staying. The fidelity report no longer lists the clip or its captions as lost for HTML. Tests `TestHTMLEmbedsAClipWithItsCaptionsAndTranscript`, `TestHTMLAudioClipHasNoSizeAndAnAddressedTrack`, `TestHTMLClipWithAnUnsafeAddressHasNoSource`, `TestOtherFormatsStillWriteAClipAsText`. Left: the insert-clip UI and the caption track editor in the showcase (the editor still opens a clip as text, so a clip survives there only as the editor's text, not as a clip).
69. **Accessibility testing as a release gate.** Kind: fixable. What: axe-style checks in the browser e2e tests plus a
    manual NVDA, JAWS and VoiceOver script run before each release. Why it matters: accessibility regresses silently
    unless a test fails. Plan: (1) add axe-core to the e2e for the editor, dialogs and the mirror; (2) fail the build
    on new violations, with a reviewed baseline; (3) write the manual script (open, type, navigate by heading and
    table, comments, dialogs, autocomplete, export) and a results table per release; (4) make the release checklist
    require it. Where: the browser e2e, `docs`. Priority: high. Dependencies: 48, 60. Done in part (this PR): steps 1 to 4. `make a11y-test` runs the axe-core tests (document, every tab, every menu, the find bar, the key directory) and the keyboard and reading-copy tests, and fails without a browser instead of skipping (`GOJIBERRY_REQUIRE_BROWSER`); CI runs it on every push and pull request; a new violation fails it, the reviewed exceptions are `axeDisabled` (each with its roadmap entry); `docs/accessibility-testing.md` has the manual NVDA, JAWS and VoiceOver script with a results table, and `docs/release-checklist.md` requires both before a release. Also done (#244): `make ci-local` (`.github/scripts/local-ci.sh`) runs the workflow's `make` steps on this machine in the workflow's own order, read from `ci.yml` so it cannot drift, with `-k`, a step filter, a report file and a skip list; the gate no longer needs a hosted runner or minutes. `make ci-local STEPS="wasm-size a11y-test"` passed here (a11y-test 76 s with the local Chromium). Left: the other examples are not under the gate, and no one has run the manual screen-reader script yet (only a person with NVDA, JAWS and VoiceOver can; the first results are for the first release).
    Also done (this PR): three more examples are under the gate, each with a `TestNoAccessibilityViolationsAxe` (the style editor demo with the font selector closed and open, the components gallery, the canvas editor app). They found real gaps, now fixed: the font selector's list had no name, was not reachable by keyboard and had a grey on grey under 4.5:1 on its selected row; the date picker, colour picker and slider had no way to give them an accessible name (new `Label` props); the file uploader's progress bar had no name (now "Upload progress", translated); the demo pages had no `lang`, no `main` landmark or no `h1`; the editor app's input and canvas had no names. Left: the passkey and people-keys demos (they need their servers), and the first manual run. The passkey and people-keys demos are under the gate too (`TestNoAccessibilityViolationsAxe` in each, the passkey one signed out and signed in); their page shells gained `lang`, a `main` landmark and an `h1`. Heading order is exempt there: the panel component brings its own `h4` to sit under a host page.
70. **Version history UI.** Kind: fixable. What: a timeline of versions, named versions, a diff between two versions,
    restore, and who changed what, on top of the existing snapshots. Why it matters: people want to see and undo
    changes without asking an operator. Plan: (1) list snapshots with author and time from the store; (2) name and pin a
    version; (3) a text and structure diff view (also readable by a screen reader); (4) restore as a new version, never
    destructive; (5) respect permissions and encryption. Where: `docstore`, `saver`, `components`, the showcase.
    Priority: high. Dependencies: existing snapshots; diff output reuses 60.
    Done in part (#203), step 3: package `docdiff` compares two documents by block (the longest common run of
    blocks, then paired deletes and inserts become edits when they share a third of their words) and, inside an edited
    block, by word; a heading that became a paragraph is a change; `Describe` says a change in words; very large
    middles fall back to delete and insert. `components.VersionDiff` shows it as a list a screen reader can read
    ("Inserted heading 2:", `<ins>` and `<del>` underlined and struck through, a count line, unchanged runs
    collapsed), with de/fr/es texts and plural forms. Also done (#204), step 1 in part: a Compare button per snapshot in the Save tab's version list shows the diff against the document being edited (`Session.CompareVersion`, `VersionsProps.Compare/Diff`), e2e `TestCompareAVersionWithTheDocument`. Also done (#207), step 2 and the author of step 1: a snapshot can be named and kept
    (`Saver.RenameVersion`, `PinVersion`, `SaveVersionBy`; the notes are one small record per document next to the snapshots,
    `versions/<id>/meta.json`, so the snapshots are as saved and go with the document when it is purged); a kept version is
    not dropped when older ones are, does not count toward `MaxVersions`, and cannot be deleted until let go (`ErrPinned`);
    the Save tab's version list shows the author, a name field and a Keep box. Two people changing notes at the same
    moment can lose one change (last write wins). Also done (#208), step 4: Restore on a snapshot saves what is being edited as a new version named "Before restoring ..." and then opens the snapshot, so nothing is lost and the restore can be undone by restoring that version (`Session.RestoreVersion`). Also done: in a shared session the showcase records each person by their name (versions, comments and replies), `TestLiveVersionAuthorIsThePerson`. Left: the permission and encryption checks (steps 4, 5).
71. **Comments, suggestions and tracked changes.** Kind: fixable. What: comment threads, mentions, resolve and reopen,
    suggestion mode and tracked changes with accept and reject. Why it matters: review is the core of shared writing.
    Plan: (1) model for ranges, threads and changes that survive concurrent edits; (2) UI with keyboard paths; (3)
    mentions without leaking addresses to people who lack access; (4) import and export in 72; (5) mirror support
    (60). Where: the model, `collab`, `components`. Priority: high. Dependencies: 60; used by 66, 72.
    Done in part (#209), steps 1 (the model and the core): a suggested change is carried by the characters it concerns, in
    `Format.Proposal` (`i:<id>:<unix>:<author>` for text proposed to be added, `d:...` for text proposed to be removed and
    still there, struck through), so it survives concurrent edits, copies and saving like any other attribute (saved as
    docfile field 19). `TextFlow.Suggesting` makes typing, pasting, cutting and deleting propose instead of change: removed
    text stays struck, replaced text is one proposal (struck and added text share an id), text typed after one's own
    proposal continues it, deleting one's proposed addition removes it for real. `Proposals`, `AcceptProposal`,
    `RejectProposal`, `AcceptAllProposals`, `RejectAllProposals` (one undo step each); drawn in a color per author,
    underlined for additions and struck through for removals. (The names say Proposal because Suggestion was taken by the
    autocomplete list.) Also done (#210), step 2: `canvas.Review` (Ctrl/Cmd+Alt+S turns suggestion mode on and off, Ctrl/Cmd+Alt+Y accepts the suggestion at the caret or the next one, Ctrl/Cmd+Alt+D rejects it; they are in the Edit menu), `components.ProposalPanel` and the showcase's Review tab: a switch for the mode, every suggestion as a sentence ("Ana replaced 'brown' with 'red'") with Accept and Reject buttons that name it, Accept all and Reject all, following the typing live, shown to a viewer without the buttons; announcements; de/fr/es; e2e `TestSuggestionMode`, axe clean. Typing while suggesting does not auto-close brackets. Also done (#211), steps 4 and 5 in part: `export.FromDocument` shows the document as it would be with every suggestion accepted (so plain text, Markdown, PDF and RTF never carry struck text), `FromDocumentTracked` keeps them and HTML, EPUB and DOCX write them as `<ins>`/`<del>` (with author and time) and Word tracked changes (`w:ins`, `w:del` with `w:delText`, which Word offers to accept or reject); the reading copy marks them (`<ins>`/`<del>` with "Suggested addition by Ana:" for a reader) and reading aloud skips the struck text; a test with two replicas proposing at the same time and settling each other's proposals (converges). Also done (#212): reading a Word file's tracked changes back in (`w:ins`, `w:del` with `w:delText`, with the author and date; a deletion followed at once by an insertion of the same person and time is one replacement), so a document from Word keeps its revisions as suggestions to accept or reject here, and what is exported and imported again is the same set of suggestions (round-trip test). Accepting a completion while suggesting (#213) is a proposal too. Export as tracked changes in DOCX is done too (`export/tracked_test.go`), and two people proposing in the same place converge (`collab` `TestSuggestionsFromTwoPeopleConverge`). Left: comment-style mentions that do not
    leak addresses (3), and the same two-people check driven through two real browsers.
72. **Import and export fidelity.** Kind: fixable in part (formats differ). What: DOCX, ODT, Markdown, HTML and PDF in
    both directions, with a fidelity report that lists what was lost or changed. Why it matters: high value for
    adoption; people leave only if their files come over intact, and they need to know what did not. Plan:
    1. Pick a corpus of real documents per format and record a baseline.
    2. Write each importer and exporter against the model; every unsupported feature adds a line to the report.
    3. Show the report in the UI before and after, and keep it with the document.
    4. Round-trip tests on the corpus; fail on a new loss.
    5. Keep accessibility data (alt text, language, headings) across formats (63).
    Where: `docfile`, `docfile/pack`, the Migrate flow, [document-components.md](document-components.md). Priority:
    high. Dependencies: model coverage of 71, 78 for full fidelity; extends the `.opendoc` format.
    Done in part (#293), steps 2 to 5 for the formats that exist: `export.Fidelity(format, doc)` lists, for the
    features the document uses (`export.Usage`, 46 of them: each kind of text formatting, block, table feature,
    picture, comment, suggestion, language, title and the print layout), what that format leaves out (`Dropped`) or
    writes as something else (`Simplified`, with a note: placeholder, character, level 3 heading, ...), from one table
    per format (`exportMatrix`) that must name every feature (an init check panics otherwise). The table is locked to
    the exporters: `TestFidelityMatrixMatchesTheExporters` exports a document with and without each feature in every
    format, and a feature marked Dropped must leave the file unchanged, one marked Kept or Simplified must change it,
    one folded into another construct (deep headings, check boxes, rules, and footnotes outside HTML and EPUB) must be written exactly as that
    construct, so an exporter that starts or stops losing something fails until the table (and the report) is
    updated. Examples it states: superscript, subscript, row spans, shading and cell alignment are not in a PDF;
    comments, colour, font size, columns and page breaks are in no format; plain text keeps characters only;
    Markdown (the editor's own, `transfer.Default`) writes no pictures, tables, footnotes or rules, and for a table or
    footnote not even the text (`content-lost`). `export.ImportFidelity(format, file)` does the same for what an import
    leaves out: `docximport.Inspect` counts the comments, headers and footers, text boxes, charts and shapes,
    equations, endnotes, fields, bookmarks, columns and paragraph indents a Word file holds that `Import` has no place
    for, and a PDF import is text only. Round trips (`TestRoundTripReadsBackWhatTheTableSaysIsKept`,
    `TestPDFRoundTripKeepsTheTextAndNothingElse`) export a fixture per feature, import it again and compare with the
    table: Word and Markdown bring back everything they write except the print layout of Word (headers, footers,
    contents, sheet, recorded in `notReadBack`), PDF brings back text only. Accessibility data is kept (step 5) and
    tested: Word now writes a picture's alt text (`descr`) and a passage's language (`w:lang`) and reads both, and the
    document language, back; HTML, EPUB and PDF carry alt text, language and headings. Fixes found on the way: nested
    list levels and per-level numbering reached no exporter (every nested list was written flat; now nested in HTML,
    Markdown, Word, PDF and RTF); Markdown wrote a heading made with the Heading style as a plain paragraph;
    Markdown and plain text wrote text proposed to be removed as if it were there (now written as accepted). The
    showcase's Files tab shows the report before exporting (`components.FidelityPanel`: a format picker, a live
    summary, a named list, in de/fr/es too), the Download menu and Export buttons announce how many things a format left
    out, and after an import the tab lists what the file held that was not brought over. Tests: `TestUsage...`,
    `TestFidelityReportListsEveryLossAndOnlyThose`, `TestFidelityReportNamedExamples`,
    `TestAccessibilityDataIsInTheFileWhereTheTableSaysKept`, `TestNestedListsKeepTheirLevelAndNumbering`,
    `TestMarkdownAndTextWriteSuggestionsAsAccepted`, `TestInspectCountsWhatImportLeavesOut`, and in the showcase
    `TestFidelityPanelListsWhatAFormatLeavesOut`, `TestFidelityPanelSaysWhatAnImportLeftOut`. Left: ODT does not
    exist (neither an importer nor an exporter), and HTML has no importer; there is no corpus of real documents (step 1)
    so the tests use one fixture per feature, not files from Word, LibreOffice or Google Docs; the report is not yet
    kept with the document (step 3, "keep it with the document") and Markdown and plain-text imports have no
    "left out" report (unknown is shown, not nothing); the showcase's Markdown and plain text exporters
    (`transfer.Default`) drop tables, pictures and footnotes where `export.Markdown` and `export.PlainText` (used by
    braille) write them, which should be made one; and the table is hand-written against the exporters, so a
    feature the exporters get that is not in `export.Features` (a new model field) is not noticed until it is added.
    Also done (this PR): the HTML importer (`htmlimport`, on `golang.org/x/net/html`): headings, paragraphs, lists with nesting, quotes, preformatted text, rules, inline formatting and colours, links (http, https, mailto and in-page only), pictures given as data: URLs (a picture on a server is not fetched), tables with spans, run language and the title; scripts, styles, forms, frames, graphics, media and layout are left out and counted (`htmlimport.Inspect`, shown by `export.ImportFidelity("html", ...)`). It is registered for `.html` and `.htm`. The wasm grew by 0.4 MB (the HTML parser), so the budget is 29.5 MB raw and 7.5 MB gzip (item 82 is where it comes back down). **Left for 72:** ODT, a corpus of real documents, the report kept with the document.
    Also done (this PR): OpenDocument text (.odt) in both directions. `export.ODT` writes ODF 1.2 directly (headings, nested bullet and numbered lists, quotes, code, links, super and subscript, run and document language, tables with spans, fill and alignment, embedded pictures with their alternative text, real footnotes, the page size and margins, and a running header and footer with page-number fields); its parts validate against the ODF 1.2 RelaxNG schema with xmllint (`ODF_SCHEMA=... go test ./export`; no office suite runs on the machine it was written on, so it has not been opened in LibreOffice, and that is the one check still to do). `odtimport` reads what LibreOffice, Collabora and Google Docs write (styles resolved through their parents, runs of spaces, lists with their nesting and kind, footnotes, pictures from the package, cell fills) and counts what it leaves out (comments, headers and footers, text boxes and drawings, tracked changes, endnotes, fields, bookmarks, links it will not keep, columns), shown by `ImportFidelity("odt", ...)`. Both are registered for `.odt`; the fidelity matrix has an `odt` column proved by the same with-and-without test as the others. The wasm grew 0.4 MB, so the budget is 30.0 MB raw and 7.7 MB gzip. **Left for 72:** a corpus of real documents, the report kept with the document. (The contents page in ODT is done, see below.)
    Also done (this PR): the contents page in ODT. `export.ODT` writes the table of contents as a real ODF index (`text:table-of-content`: the title, an entry template per level and an index body with an entry for each heading, linked to a bookmark on it, in dotted-leader paragraph styles `Contents 1` to `3`), followed by a page break, when the document asks for one. Page numbers are placeholders until the person updates the index in LibreOffice (Tools > Update > All Indexes): only it knows its pages, as with Word's fields. The part validates against the ODF 1.2 RelaxNG schema with xmllint (`ODF_SCHEMA=... go test ./export`, schema from docs.oasis-open.org) and `TestODTHasATableOfContentsOfItsHeadings` checks the links; the fidelity matrix lists `page-toc` as kept for ODT. Still not opened in LibreOffice.
73. **Search and replace across the document and the store.** Kind: fixable. What: find and replace in a document and
    across documents, with a client-side encrypted index for the encrypted store. Why it matters: search is expected,
    and a server-side index would leak contents. Plan: (1) in-document search with case, word and regular expression
    options, announced to screen readers; (2) a local index built after decrypt, stored encrypted with a key derived
    from the owner keys, never sent in clear; (3) update on save; (4) replace across documents with a preview and
    undo. Where: `docstore/encrypt`, `components`. Priority: medium. Dependencies: 60 for announcements.
    Done in part (#221), step 2 as a library: the new `docsearch` package is a local index of many documents (every
    query word must match, the last may be a start, title words count more, snippets around the match) that is
    stored only sealed (AES-256-GCM under `DeriveKey`, an HKDF of a secret the person holds), with `Open` refusing a
    tampered or foreign-key index so it is rebuilt. Also done (#222), step 3 as a library:
    `docsearch.Refresh` brings the index in line with a `docstore.Adapter` (pass the decrypting one) reading only the
    documents that are new or newer, dropping deleted ones and skipping what cannot be read, and `FromDocfile` extracts
    title, text and table cells from any docfile format. Left: calling it in the showcase (after unlock and after a
    save) with the sealed index kept in browser storage, and the search box across documents. Also done (#223), step 4
    as a library: `docsearch.PlanReplace` previews a replace across documents (count and a before/after of the first
    match per document, nothing written), `Plan.Apply` writes exactly the previewed bytes (optionally only the ticked
    documents; one saved since the plan is skipped, never overwritten), and `Applied.Undo` puts the old text back unless
    the document was saved again. It replaces in the flowing text like the editor's Replace all (a table cell is not
    touched). The showcase panel is done (this PR): the Find tab has "Search all documents" (a local index of the documents of the place the page saves to, read through that place's own adapter, so an unlocked encrypted store is searched on this device; `docsearch.Subset` limits it to the documents' own files, never old versions or the catalog) with the hits and an Open button, and "Replace in the other documents" with the preview, a tick per document, Apply and Undo (the open document is left to the editor's Replace all). Tests: `TestSubsetShowsOnlyItsKeys`, `TestSearchAndReplaceAcrossDocuments`, `TestSearchAllDocumentsInTheEncryptedStore`. Also done (this PR): the sealed index is kept in the browser's IndexedDB between visits, so the next visit reads only the documents that are new or changed (`indexCache` in `searchkeep.go`; the Find tab says how many documents are indexed and how many were read this time). It is only ever kept sealed (`docsearch.Index.Seal`): the encrypted store's index is sealed under `docsearch.DeriveKey` of `encstore.Store.SearchSecret`, a hash of the unlocked owner key's signature over the owner list, so it is neither read nor written while the store is locked, a changed owner list cannot open an older index, and nothing the server or the storage holds can derive the key; any other place uses a per-device non-extractable Ed25519 key of the browser (a browser without one keeps nothing and rebuilds). A changed or foreign index is refused by `Open`, removed, and built again. Replace across documents now goes through the saver (`Plan.Bytes`, `applyReplace`): the catalog and checksums stay right, the text as it was is kept as the named version "Before replace in all documents" so the history has it, and Undo restores it unless the document was saved again. Tests: `TestSearchAllKeepsTheSealedIndexBetweenVisits`, `TestSearchAllKeepsTheEncryptedStoresIndexSealedUnderTheOwnerKey` (nothing readable in IndexedDB, locked store untouched, altered index rebuilt), `TestSearchAndReplaceAcrossDocuments` (history keeps the old text), and the unit tests in `searchkeep_test.go`, `encstore/searchsecret_test.go`, `docsearch/replace_test.go`. Done too (this PR): the kept index is written incrementally. A refresh that changed documents keeps only that change, `docsearch.Delta` (`Index.DeltaFor`, `SealDelta`, `Index.ApplyDelta`; `Result` now names the ids that changed or went), sealed as the next record after the base and bound to that base and its place in the sequence; a visit opens the base and applies the records in order. The base is sealed whole again when there is none, after 32 records or when the records have grown to half of it, and the old records go. A record that cannot be applied (altered, lost, from another base) ends the chain there and later ones are dropped; nothing depends on it, since the refresh compares the index with the store and reads again what is missing. Tests: `TestDeltaCarriesOneRefreshAndIsBound`, `TestOneChangeKeepsOnlyThatChange` (the base is untouched, the record is small and holds no readable text, a new visit reads nothing, an altered record costs only a re-read), `TestManyChangesAreFoldedIntoANewBase`. Nothing is left in this item.
74. **Offline-first with conflict visibility.** Kind: fixable. What: a visible sync state, the queue of pending
    changes, and what will merge or conflict. Why it matters: people lose trust when they cannot tell whether their
    work is saved. Plan: (1) a status model (saved, saving, offline, conflict) exposed in the UI and to screen readers;
    (2) a durable local queue with retry; (3) a pre-merge view of the remote changes; (4) tests for offline edits and
    reconnect. Where: `saver`, `collab`, `components`. Priority: medium. Dependencies: 66.
    Done in part (#214), step 1 for shared documents: `components.SyncStatus` and a status bar item that says whether the
    person's work has reached the others ("All changes shared", "Sending 3 changes…", "Connecting to the shared
    document…", "Offline: 2 changes waiting to be sent", "Not shared: <reason>"), with a `data-state` for tests; only
    offline and problem are announced as they happen (sending and shared come and go with every keystroke); de/fr/es
    with plural forms; e2e checks the shared state. Left: the same for a document saved to a store (that already has
    saved/saving/unsaved/failed), a pre-merge view of
    what others changed while offline (3), and an e2e that goes offline (the e2e harness cannot cut an open stream yet).
    Also done, step 2 in the client: `client.Options.Queue` (a `Queue` with `Load` and `Save`) keeps the ops waiting to be
    sent, `Dial` sends what it loads, and a unit test restarts a connection with the server down. Also done: the WebSocket
    connection takes the same `Queue`, and the showcase keeps the waiting changes of a shared document in localStorage
    (`showcase-queue:<doc>:<name>`); the e2e cuts the hub, types, reloads the page, brings the hub back and checks that a
    second browser gets the text. Also done: `Tab.Offline` in the e2e harness cuts the browser's network (with the open connection dropped from the server side, since emulated offline stops new requests only) and `TestGoingOfflineInTheBrowser` goes offline, types, comes back and checks a second browser gets the text. Also done, step 3: when the connection comes back, the showcase keeps the person's own text, waits for what the others did meanwhile to arrive, and lists the difference as "While you were offline" in the sharing panel (`docdiff` list, announced, Dismiss; `away.go`, `TestWhatOthersChangedWhileOffline`; de/fr/es). It is shown after the merge, not before it: a shared document is merged as operations and nothing is lost, so there is nothing to accept or refuse; what the person gets is the sight of what changed under them. A document saved to a store has its own state in the status bar (saved, saving, unsaved, failed), which is what step 1 asks for there. Done for a document saved to a store too (this PR): the saver session remembers the store's date of the copy it opened or saved; when it saves and the stored copy is newer (someone else, another tab) and differs, that copy is first kept as the version "Saved elsewhere, before your save", the save goes on, and the comparison of that copy with the saved text is ready in Versions with the status "... It replaced a newer copy saved elsewhere; that copy is kept as a version" (`Session.keepElsewhere`, `TestSaveKeepsACopySavedElsewhere`; de/fr/es). Nothing is lost and nobody is blocked. Nothing is left in this item.
75. **Done in part (PR #289): sharing UX.** Kind: fixable. **Built:** share links in the access list (`docstore.ACL.Links`, `docstore/sharelinks*.go`): a link has a role (viewer or editor), a mandatory end (default 7 days, at most 90, at least a minute, at most 20 live per document), is made by `POST /docs/{id}/acl` (owner only; the token is shown once and only its SHA-256 is stored) and revoked by `DELETE /docs/{id}/acl?link=` (owner only). A request carries the token in `X-Gojiberry-Share` (`HTTPAdapter.UseShareLink`, `CreateLink`, `RevokeLink`, `LoadACL`). A view link reads, an edit link reads, writes and renews; no link deletes, reads or changes the access list, or makes links; a named person's own grant is unchanged and a link only adds access. `ACL.WhoCanSee` is the 'who can see this' list (owner, people, live links with their ends). The showcase People tab has the section: role and lifetime pickers, the link shown once, a 'check a link' box, and the list as a real `ul`. Tests: expired refused at the deadline (view and edit), revoked refused at once while others keep working, view link cannot write, renew, delete or touch the access list, an edit link cannot delete or manage, only the owner makes or revokes, forged and truncated tokens, a `links` field smuggled into a PUT of the list, the token never in the list, limits, and a browser test (including a moved page clock). **Done in PR #298:** per-person expiry (`ACL.Until`, `ACL.GrantFor(email, role, now, life)` between a minute and `MaxGrantLife` of a year, `GrantEnds`, `ACL.Active(now)`): the server reads every list through `Active`, so a grant whose end has passed opens nothing (a person at the deadline is refused, as a link is), the hub's `ACLRoles` honours it, `WhoCanSee` lists a person's end and drops an ended one, a change of role keeps the end, `PUT /docs/{id}/acl` keeps an end only for someone who is granted and cuts it to `MaxGrantLife`, and only the owner can set it. The People tab has 'They keep access' (until I remove them, 1, 7 or 30 days) for the people added next, and the list says 'until <time>'. The showcase reads `#share=<token>` on load (`takeFragment`): the secret is taken out of the address bar at once, the People tab opens on a notice that asks 'Use this link?' and does nothing until the person chooses; 'Use this link' makes the confirmed server target send the token (`HTTPAdapter.UseShareLink`), switches the saver to the server and opens the document named by `/d/{doc}`; without a confirmed server (or with the encrypted store on the server) it says why it cannot; 'Stop using it' takes the token back. Wording review: shorter sentences in the Share/People copy, and 'What encryption protects' with what it does not (the server keeps scrambled text; it does not hide a document from someone let in, nor that it exists, when it changed or its size; a link works for anyone who holds it); de/fr/es updated. Tests: `TestAPersonsAccessEndsAtTheirDeadline`, `TestAPersonCannotGiveThemselvesALongerEnd`, `TestAnEndFarAwayIsCutAndAStrayEndIsDropped`, `TestWhoCanSeeShowsPeopleEndsAndDropsEndedOnes`, `TestGrantForRefusesBadLivesAndGrantKeepsTheEnd`, `TestACLRolesHonourTheEndOfAGrant`, and the browser tests `TestOpenAShareLinkFromTheAddress` (nothing sent before the choice, ignore, no server, use, stop, axe) and `TestAPersonsAccessEndsInThePeopleTab` (moved page clock). **Not built, and why:** a comment-only role. Comments live inside the document (item 71), and with the encrypted store the server holds only ciphertext, so a server cannot tell a write that adds a comment from a write that edits the text; a role it cannot enforce would only look protective, so the People tab says there is none. It needs either comments stored as separate records the server can see (which gives up their privacy from the server) or a signed per-field write the server can check; neither is done. **Still open:** a sharing dialog that reaches a real server for the access list itself (the People tab edits an in-memory list; `HTTPAdapter.CreateLink`, `RevokeLink` and `LoadACL` are the calls it would use). Original plan: What: link sharing, per-person permissions, expiring links, view-only versus
    comment-only, and a plain explanation of what encryption protects and what it does not. Why it matters: people
    share wrongly when the options are unclear. Plan: (1) a permission model with roles view, comment, edit; (2)
    expiring links using the existing expiry support; (3) a sharing dialog with a plain-language summary of who can
    see what; (4) wording reviewed for non-experts; (5) accessibility checks. Where: `docstore/encrypt`, `auth`,
    `components`. Priority: medium. Dependencies: 71 for comment-only.
76. **Key and recovery UX.** Kind: inherent in part (lost passkey plus lost recovery key means lost data; see the
    [limits register](limits-register.md)); the guided flows are fixable. What: recovery-key printing, a periodic
    recovery check, a nudge when there is only one key, device management, and a revoke and re-key walk-through. Why it
    matters: the limit cannot be removed, only made hard to hit by accident. Plan:
    1. A printable recovery sheet and a "test my recovery key" step without exposing the key.
    2. A periodic reminder to run the check, and a nudge while only one key is enrolled.
    3. A device list with last use and a revoke action.
    4. A guided re-key after revoke, with progress (builds on `RepairAll`).
    5. Wording that states the limit plainly; all steps keyboard and screen-reader accessible.
    Where: `docstore/encrypt`, `auth/passkey`, `components`, the showcase. Priority: high. Dependencies: none hard. Plan to reduce the exposure: [inherent-limits-plan.md](inherent-limits-plan.md#76-key-and-recovery-ux-lost-passkey-plus-lost-recovery-key-means-lost-data). Done in part (this PR), plan steps 1 and 2 (steps are those of [inherent-limits-plan.md](inherent-limits-plan.md#key-and-recovery-ux-lost-passkey-plus-lost-recovery-key-means-lost-data)): the keys panel states the limit plainly ("nobody can open its documents, including us"), warns while the store has one key, and "Check your recovery key" tells the right recovery key from another key and from rubbish with `encstore.Store.CheckRecoveryKey` (nothing unlocked, nothing written, the typed secret cleared), keeping the time of the last good check in this browser. Tests: `TestCheckRecoveryKey`, `TestCheckTheRecoveryKeyWithoutUsingIt`. Steps 3 and 4 (this PR): "Print a sheet" beside the secret of a new recovery key opens a printable page built in the browser (`encstore.RecoverySheet`: large plain text, high contrast, the key as text so it can be typed from paper, its fingerprint, the limit in words, no external file); the keys panel reminds the person to check the key until they have and every 90 days (`encstore.CheckDue`), with "Remind me in a week" (kept in this browser). Tests: `TestRecoverySheetHoldsTheKeyAsPlainEscapedText`, `TestCheckDue`, `TestRecoverySheetAndReminder`. Step 5 (this PR): the keys list says when each key last unlocked the store *in this browser* ("Last used in this browser: 2030-05-01", or "Not used in this browser"; `Store.OnUnlock`, `RecipientRow.Detail`; it cannot know about other devices), removing a key was there already (with the question and the reasons it is blocked), and the re-encryption after a removal or replacement reports "Encrypting again: 3 of 12 documents" (`encrypt.Adapter.RekeyProgress`, `Store.Progress`). Tests: `TestRekeyAllReportsProgress`, `TestHooksNameTheUnlockingKeyAndReportProgress`, `TestKeyListShowsWhenEachKeyWasLastUsedHere`. Also done (#245): the printed sheet carries a QR code of the key beside the text (`encstore.RecoveryQR`, an inline SVG from `rsc.io/qr`, black on white with the quiet zone, a text alternative for a screen reader, no outside reference; the key stays as text too, so the code is only a shortcut to move it to another device). A test reads the drawing back and checks every module against the code. Also done (#265): a "Stop and put everything back" button while the store is encrypted again (`Store.Stop`, `ErrStopped`): the running change is cancelled between two documents (`RekeyAll` checks the context), everything it wrote is put back with an uncancelled context and the keys are as they were; test `TestStopPutsEverythingBackAndKeepsTheStoreUsable`. Last use on other devices, library part (this PR): `encrypt.Adapter.RecordDeviceUse` / `DeviceUses` keep one small record per key under `deviceuse/` (`docstore.DeviceUsePrefix`; not a document, never expires), named by a hash of the fingerprint, holding the time and a device label the person chose (cut to 60 characters), encrypted to the store's keys like a document, so the server sees only that a key has a record and when it was written; `encstore.Store.RecordUse` and `DeviceUses` wrap them (current keys only, the store must be unlocked), and removing a key re-encrypts the records so the removed key cannot read them. Tests: `TestDeviceUseIsKeptEncryptedPerKey`, `TestDeviceUseRefusesBadFingerprintsAndCleansLabels`, `TestDeviceUseOfARemovedKeyIsNotReadableByIt`, `TestDeviceUseSurvivesAKeyChangeAndNamesOnlyCurrentKeys`. The showcase can now also save to a server (this PR): the docstore server confirmed in the People tab (the key directory address and token, set with encryption on or off) is a "Save to" target, "Server <host> (the server can read these documents)" (`bearerTransport` sends the sign-in token; `Store` is `docstore.NewHTTPAdapter`); forgetting the server removes it and moves the saver back to the browser. Test: `TestTheConfirmedServerIsASaveTarget` (a real docstore server behind the page). The end-to-end encrypted store can now live on the server (this PR): with a confirmed server, "Keep the end-to-end encrypted store on this server instead of in this browser" (applies after a reload) runs the same screens over the server's documents (target "server", "Server <host> (end-to-end encrypted)"; its key list, passkey slot and the lists the person confirmed are kept apart from the browser store's by a suffix naming the server, `encscope.go`). The files on the server are ciphertext; a reload starts locked. A second device takes the key list from the first (`encstore.ExportKeyList` / `ParseKeyList`: public text with a short code both devices show; the second device shows the code in a question before it uses the list, and still checks the list against the store's seals at unlock), then unlocks with any of the store's keys and sees the documents. Each unlock leaves a record in the store (`Store.RecordUse`), the keys list says "Last used on <device>: <time>" for every device, and a field names this device (60 characters). Tests: `TestEncryptedStoreOnTheServer` (browser: ciphertext on the server, locked reload, unlock, device name, a second browser joining with the key list), `TestKeyListMovesToAnotherDevice`, `TestParseKeyListRefusesWhatIsNotOne`. Nothing is left on this item that code can do; the rest is inherent (the limits register).
77. **Templates, styles and a design-token system.** Kind: fixable. What: document templates, named styles, and a
    token set (colours, type scale, spacing) that themes the editor and the exports. Why it matters: consistent
    documents and accessible contrast defaults. Plan: (1) a token schema; (2) style and template documents stored in
    the central config store; (3) apply tokens to the canvas and to exports; (4) contrast validation on edit. Where:
    `components`, `docfile`, `docstore`. Priority: medium. Dependencies: ties to the central config store (52); 50.
    Done in part (#233), steps 1 and 4 as a library: the new `tokens` package. A `Set` has named colours (text,
    background, muted-text, link, accent, accent-text, danger and any others), a type scale and a spacing scale.
    `Validate` lists problems in plain words: a colour that is missing or cannot be read, a text pair under the WCAG 2
    contrast of 4.5:1 (text, muted text, link and danger on the page, accent text on the accent), a body size under
    16px, a scale that does not grow. `CSS` writes a valid set as `--gj-color-*`, `--gj-font-*` and `--gj-space-*` custom
    properties and refuses an invalid one; names and values from a stored file cannot break out of the declaration.
    `a11ycheck.Contrast` is exported for it. Also done (#234), step 3 for HTML: `export.HTML{Theme: css}` writes the
    tokens' CSS in a `<style>` in the head, with body, page and link rules that use the tokens (each with a fallback); it
    accepts only one `:root{--name:value;...}` block and fails on anything that could close the style or open another
    rule. (`export` cannot import `tokens`, which depends on it through `a11ycheck`, so the CSS is passed as text.) Left:
    storing sets in the central config store (2), applying them to the canvas, the other exports and the showcase (3),
    and a theme editor that shows the problems as they are typed. Also done (this PR): the theme editor, `components.ThemeEditor` (colour fields with a swatch, type scale and spacing as lists; `tokens.Set.Validate` runs on every keystroke and its problems are listed in words, the field with the problem is `aria-invalid` and described by the list, a sample shows the colours; the fields cannot write more CSS than a colour), in the showcase's Styles tab. A theme without problems goes with the document (`docfile.Document.SetTheme`, kept in its metadata, read back by `tokens.FromCSS` when the document is opened) and into the HTML export (`export.Snapshot.Theme`; the exporter's own `HTML.Theme` wins, and an unsafe stored theme is left out rather than failing the export), also when the export is done by the file worker. Until the person edits the theme the document has none and exports are as before; a theme with a problem is not used. Tests: `TestThemeEditorSaysWhatIsWrongAsItIsTyped`, `TestFromCSSReadsWhatCSSWrote`, `TestHTMLCarriesTheDocumentsOwnTheme`, e2e `TestThemeEditorChecksAndThemesTheExport`; de/fr/es. Also done (this PR): the document's theme in the other exports. EPUB takes the same style element as HTML; Word takes the text colour and body size as the default run (`w:rPrDefault`), the link colour for hyperlinks, and the page colour (`w:background`, with a `settings.xml` that asks Word to show it); OpenDocument the default text colour and size, the link style (`Internet_20_link`, now used by every link) and the page background; RTF the colour table (text, link; links in colour 2) and body size; PDF the text colour and the page colour on every page (links are not coloured in the PDF, which draws no colour of its own). Read by `export/theme.go` from the same `:root` block as HTML, an unsafe or unreadable block is ignored. Tests: `theme_formats_test.go`, and the ODF schema test now checks a themed document. Also done (this PR): the theme is drawn on the sheets (`canvas.FlowTheme`: text colour, page colour, link colour; `TextFlow.LinkColor`; the `theme` prop of the TextFlow element), and named themes are kept in the settings store of package userconfig as `tokens.<name>` items (`themelib.go`): on this device always (localStorage through `userconfig/client`, so they work offline) and on the server too when a docstore server is confirmed in the People tab (queued while offline or signed out, merged with another device's). The Styles tab lists them with Use and Delete and a field to save the one being edited; a theme with problems cannot be saved or used. Tests: `TestThemeLinkColour`, e2e `TestThemeOnTheCanvasAndSavedThemes` (page colour drawn, save, reload, use, delete) and `TestSavedThemesReachTheSettingsServer`; de/fr/es/pt/ar/he. Left: nothing in this item. (The canvas keeps its own header, footer and desk colours; the theme is the document's text, page and links.)
78. **Equations, diagrams, code blocks and footnotes.** Kind: fixable. What: these elements with accessible output:
    MathML for equations, text alternatives for diagrams, readable code blocks with a language, and footnotes that
    link both ways. Why it matters: technical documents are common and these elements are usually the least
    accessible. Plan: (1) model elements; (2) mirror output (60); (3) export to MathML and semantic HTML (63); (4)
    checker rules for a missing diagram alternative (62). Where: the model, `components`. Priority: low.
    Dependencies: 60, 63.
    Done in part (PR #290), steps 1 and 3 for the export model: footnotes already existed in `docfile` (marks and
    notes) and now link both ways in the HTML, XHTML and EPUB export (`Format.NoteRef` on the mark, `Block.Note` on the note: the
    mark is a `<sup id="fnref-N"><a href="#fn-N">` link and the note `<li id="fn-N">` ends with a labelled back link);
    a code block has a language (`Block.CodeLang`, a short lower-case name; HTML `class="language-go"`, Markdown fence info
    string; anything but letters, digits and `+-#` is dropped); an equation is a paragraph block with a linear formula
    (`Block.Equation`, built by `export.EquationBlock(formula, alt)`) whose segments are its text alternative, so every format
    shows the words, and the HTML export writes MathML (`export.MathML`: numbers, identifiers, operators, `^` and `_`,
    `{}` groups, `sqrt{}`, `frac{}{}`, a few names) with the alternative as `alttext` and as a text/plain annotation;
    the output is well formed for any input. A diagram is an image with its description, which the image-alt rule of
    the accessibility checker (62) already requires. Tests: `TestMathMLFromTheLinearNotation`,
    `TestMathMLIsWellFormedWhateverTheFormula`, `TestEquationHasATextAlternativeInEveryExport`,
    `TestFootnotesLinkBothWaysInHTML`, `TestCodeBlockLanguage`.
    Done in part (PR #301), the saved document and the editor: `docfile` holds an equation (`Item.Equation`: formula and
    text alternative), a diagram's long description (`ImageItem.LongDesc`, and `canvas.Image.LongDesc`) and a code
    block's language (`Para.CodeLang`, and `canvas.ParaFormat.CodeLang`, on a paragraph of the Code style), in the JSON,
    protobuf text and protobuf binary encodings (the golden files `docfile/testdata/rich.pb`, `rich.pbtxt` and
    `schema.proto` were regenerated with the protobuf library, as for `MediaItem`); `canvas.EquationObject` shows an
    equation in the canvas as a block (its formula as typed: the editor has no typesetter). The exports read them:
    the snapshot has the equation as `export.EquationBlock`, the lines of a Code-style paragraph form one code block
    with its language, and a long description is a paragraph after the picture (`Block.DescribesImage`) that the HTML
    and EPUB export link with `aria-describedby`. The Pictures tab of the showcase inserts an equation (it is refused
    until it has a text alternative, with the focus sent to that field), lists and edits the equations, has a long
    description field for each picture and sets the language of the code block at the caret; the reading copy says
    "Equation: ..." in words; `a11ycheck` has the rule `equation-alt` (an error without an alternative, a warning
    when the alternative is the formula typed again), also used by the insert button (`a11ycheck.EquationFindings`);
    the search index has the words of an equation. Footnotes now link both ways outside HTML where the format has
    links: Word (a bookmark on the mark and on the note with internal links each way) and the Markdown export
    (`[^1]` and `[^1]: text`); the fidelity report knows the three new features (equation, code language, long
    description) and Word now keeps footnotes. Tests: `TestElementsSurviveEveryEncoding`,
    `TestElementsSurviveTheEditorsFlow`, `TestEquationWordsAndText`, `TestBinaryMatchesProtobufLibrary`,
    `TestEquationFindingsRequireAWordedAlternative`, `TestCheckFindsAnEquationWithoutAnAlternativeWhereItIs`,
    `TestSavedEquationIsMathMLInHTMLAndWordsEverywhereElse`, `TestCodeParagraphsAreOneBlockWithTheirLanguage`,
    `TestDiagramLongDescriptionFollowsThePictureAndIsLinkedInHTML`, `TestFootnotesLinkBothWaysInWordAndMarkdown`,
    `TestFidelityMatrixMatchesTheExporters` (three new features), and the browser test
    `TestInsertAnEquationRequireItsAlternativeAndExportIt`. Left: footnotes do not link in the PDF (needs internal
    destinations in the PDF writer) or in RTF, and the Markdown and plain text of `transfer.Default` still write
    them as the numbered list (they have no footnote syntax of their own; see 72); equations are not MathML in Word
    (OMML), only their text alternative; the long description of a diagram is a paragraph in Word, PDF and RTF, not
    linked to the picture; the editor shows a formula as typed, not typeset; a code block's language is not written
    outside HTML and EPUB (Markdown of `transfer.Default` writes code as inline marks, which carry no language).
    Also done: footnote links in RTF. The mark is a link to its note (bookmark `_fnN`) and the note ends with a link back to the mark (`_fnrefN`), the same names the Word export uses; `TestFootnotesLinkBothWaysInRTF`, and the fidelity matrix lists footnotes as kept for RTF. Still open: footnote links in PDF, equations as OMML in Word.
79. **Autocomplete word picker list.** Kind: fixable. What: keep Tab accepting the inline suggestion and its ghost
    text for the top candidate; add a suggestion popup at the caret with the top N candidates (default 5), opened after
    a short delay or on demand (Ctrl+Space), and closed on Esc, click-away or the caret leaving the word. Keys: Up and
    Down move, Tab or Enter accept the highlighted word, Esc dismisses, number keys 1 to 9 optional; a row can be
    clicked or tapped. Why it matters: one guess is often wrong, and a list is faster; it must work for screen-reader
    and keyboard users from the start. Plan:
    1. Accessibility: the WAI-ARIA combobox and listbox pattern (`role=combobox` on the accessible mirror or the
       focused editable, `aria-expanded`, `aria-controls`, `aria-activedescendant`, `role=option` with
       `aria-selected`, `aria-setsize` and `aria-posinset`); a polite live region announces the number of suggestions
       and the highlighted word; the popup never takes focus.
    2. Sources: the editor word list plus words already in the document (recency weighted), per-language dictionaries
       through the `spell` package (`spell/dict`), and the user dictionary in the encrypted preference store.
    3. Privacy: no network calls, no telemetry.
    4. No popup during IME composition (64).
    5. Performance: a prefix index, a cap on candidates, a debounce and no layout thrash.
    6. A provider interface for custom sources (snippets, mentions).
    7. Tests: unit tests for ranking, e2e for the keyboard model and the ARIA attributes, and a screen-reader script
       (69).
    First slice (popup plus keyboard model) can ship earlier if ARIA is built in. Where: the canvas editor input layer,
    `spell`, `components`. Priority: high. Dependencies: ARIA 48, mirror 60, preference menu 51.
    Done in part (#185), plan steps 2, 3, 5 and 6: the new `complete` package is a pure in-memory index (no network, no
    telemetry) with a sorted prefix index, a cap, recency- and count-weighted words from the text, a vocabulary with
    weights (for a dictionary or the user dictionary), custom `Provider`s, the typed capital kept ("Hel" gives
    "Help"), and `PrefixAt`/`WordEnd` for the caret. Also done (#186), steps 1 and 7 in part:
    `components.Suggestions` (a `role=listbox` popup with `aria-selected`, `aria-setsize`, `aria-posinset`; pressing a row
    does not move the focus), `SuggestionsControl` (the combobox attributes with `aria-activedescendant` for the editing
    element), and `SuggestState` (Up/Down wrap, Home/End, Tab or Enter accept, Escape closes, digits 1 to 9 optional, a
    polite announcement of the count and the highlighted word), unit tested, with de/fr/es texts. Also done (#187): the `canvas.CompleteFeature` command
    (Ctrl/Cmd+Space; `Chord` now names the space bar "Space"), and the showcase wiring: the list is built from the
    document's own words around the caret, opens at the caret, keeps the focus in the input element (marked as the
    combobox with `aria-activedescendant`), takes Up, Down, Home, End, Tab, Enter and Escape while open, narrows as
    typing goes on, closes on leaving the word, and never opens during IME composition; e2e `TestWordSuggestions`.
    Also done (#192): the preference "Suggest words as I type" (View > Display preferences) opens the list by itself 0.6 s after typing stops, and
    says nothing when there is nothing to offer. Also done (#193): with spell check on, the dictionary's words and the user's own fill the list after the
    document's own (`spell.Checker.CompleteMany`, `Dictionary.WithPrefix`). Done (this PR, closing the old Left line): the inline ghost-text suggestion. `components.GhostState` holds the rules (only Tab accepts and only Escape dismisses; Enter, the arrows and every other key never insert it; Escape hides it until the prefix changes; it is announced once per word, politely: "Suggestion: zebra. Press Tab to accept.") and the showcase draws the rest of the best word faintly after the caret (`ghost.go`, an `aria-hidden` element outside the xango tree) when the preference "Show a suggestion after the caret (Tab accepts)" (View > Display preferences, off by default, kept in this browser) is on, the caret ends a word that ends its line (so it never covers text), no selection is held, the list is closed and no input method is composing; a click or any key that changes the text hides it. Dictionaries for other languages: `complete/wordlist` has small lists (a few hundred words each) for German, French and Spanish, written for this repository (the function words of `lang.Stopwords`, everyday verbs and nouns, words of a text editor), so they carry no third-party licence; the showcase adds the list of the document's language (`Language of the document`) after the document's own words, in the list and in the ghost text. There are no German, French or Spanish Hunspell dictionaries in the repository (only `spell/dict/en_US`), so these are word lists for completion, not spelling dictionaries. The screen-reader script has steps 22 to 24 in `accessibility-testing.md`. Tests: `TestGhost*` (components), `TestEveryListIsUsable`, `TestListsFeedCompletion` (wordlist), and in the showcase `TestGhostTextIsOffByDefault`, `TestGhostTextIsAcceptedByTabAlone`, `TestGhostTextEscapeAndEnter`, `TestGhostTextIsAnnouncedPolitely`, `TestGhostTextNeverCoversText` (with `internal/screenreader`), `TestWordSuggestionsUseTheLanguageWordList`. Left: the ghost text is a DOM element positioned over the canvas at the caret (the canvas does not draw it, so it is not in a canvas screenshot or print), it uses the page font at 80 % of the line height rather than the run's font, it shows only at the end of a line, the language lists are small (common words only, no inflected forms, no per-region spelling) and use the document language, not the language of the run at the caret, and the three steps of the script (22 to 24) have not been run with NVDA, JAWS or VoiceOver.
    The two completion lists are merged (#219): `canvas`'s popups (`TextFlow`, `FloatingText`) rank with package
    `complete` (count, then recency, then the vocabulary), and the showcase has only the accessible list (its text flow
    no longer sets `Vocabulary`; the words go into the list's index). The canvas-drawn popup stays for apps without an
    accessible mirror.
80. **Print layout.** Kind: fixable. What: pagination, headers and footers, page numbers and a table of contents.
    Why it matters: documents are still printed and exported to paged formats. Plan: (1) a page model with size and
    margins; (2) layout with page breaks, widow and orphan control; (3) headers, footers and fields; (4) a table of
    contents from the heading outline with links; (5) use the same layout for PDF export (63). Where: the canvas
    editor layout, export code. Priority: medium. Dependencies: 60 for the outline, 72, 77.
    Done in part (this PR): steps 1, 3, 4 and 5 and the page-number part of step 2. `docfile.PageLayout` (kept in Meta under `page.*`) is the page model: header and footer text with the fields `{page}`, `{pages}` and `{title}` (a tab separates left, centre and right parts), a table of contents switch and title, the sheet and the margins. `export.Snapshot` carries it (`Header`, `Footer`, `TOC`, `TOCTitle`) and `Snapshot.Outline` lists the headings. PDF draws the header and footer on every page as artifacts with the real page and page count, and a table of contents with dotted leaders and the real page of each heading (the document is drawn again until the pages settle); it also keeps a paragraph from leaving one line alone on a page (widow and orphan control: a paragraph of fewer than four lines that would split moves whole, a longer one keeps two lines on each page). Word gets header and footer parts with PAGE and NUMPAGES fields and a TOC field (dirty, so Word fills in the page numbers) with a bookmarked heading and a PAGEREF per entry; RTF gets `\header`/`\footer` groups with PAGE and NUMPAGES fields and the sheet and margins; HTML gets a print stylesheet (`@page` size and margins, header and footer in the margin boxes, headings kept with the text after them, no splitting of rows, figures and code, widows and orphans) and a linked contents list that shows page numbers where the print engine knows `target-counter`. The showcase Page tab has Header, Footer and "Start with a table of contents", saved with the document and used by every export. Tests: `TestPageLayoutRoundTripsThroughMeta`, `TestPageLayoutIgnoresWhatCannotBeLaidOut`, `TestPDFHeadersFootersAndPageNumbersOnEveryPage`, `TestPDFTableOfContentsHasTheRealPageNumbers`, `TestPDFTableOfContentsThatRunsToSeveralPages`, `TestPDFNeverLeavesAWidowOrAnOrphanLine`, `TestHTMLPrintStylesheetAndContents`, `TestDOCXHeaderFooterAndContents`, `TestRTFHeaderFooterAndSheet`, and in the showcase `TestPrintLayoutReachesTheExports`. Left: the canvas editor does not paginate on screen with the header, footer and contents (the sheets show no running header), the contents of a Word or RTF file show placeholder page numbers until Word updates the field (RTF has no contents), the HTML contents have page numbers only in print engines with `target-counter` (Prince, WeasyPrint), the showcase does not read the sheet back from a saved file (the Page tab keeps its own setup), widow and orphan control covers paragraphs, lists and code, and (PDF) table rows: the header row is never alone at the foot of a page and the last row never alone at the head of one (`TestPDFTableNeverLeavesALoneRowAtAPageEdge`); not yet in other formats. The RTF export now has the table of contents too (title, entries with dot leaders, bookmarks on the headings and `PAGEREF` fields marked dirty, so Word fills the page numbers in on opening, as it does for the Word export; `TestRTFContentsListHeadingsWithPageReferences`; the fidelity matrix now lists `page-toc` as kept for RTF). Page numbers are written only by Word itself: the PDF is the one format that knows its pages. Still open: a different header on the first page or on odd and even pages.
    Also done (this PR): the sheets on the screen show the running header and footer the exports print. `canvas.HeaderFooter.Parts` (left, centre and right parts of one line, also `Parts` on the xango `Header` and `Footer`), `export.RunningParts` (the tab-separated text with `{page}`, `{pages}` and `{title}` filled in for a sheet), and the showcase draws each sheet's own header and footer from the Page tab (the sample's own header and page number stay while the layout has none). Tests: `TestHeaderPartsAreDrawnLeftCentreAndRight`, `TestSheetsShowTheRunningHeaderAndFooter`. **Left for 80:** odd and even headers, table-row widow control, the contents page on the screen, and restoring the sheet size of an opened document (done: `components.PageSetupOf` finds the paper and orientation a document was saved with, within two points, and the margins; a size that is none of the papers on offer leaves the sheet as it is; `TestPageSetupAndStatusBar` opens a saved Letter landscape document).
81. **Admin side.** Kind: fixable. What: a PII-free operator audit log, docstore backup and restore, quota dashboards
    and health pages. Why it matters: operators need to run and recover a store without seeing user data. Plan: (1) an
    audit event schema with an allowlist (no names, addresses, document ids or contents); (2) backup and restore of
    the encrypted store as opaque blobs, with a restore test; (3) quota and usage pages from counters (ties to the
    telemetry rules of 57); (4) health and readiness routes. Where: `docstore`, `internal/docstorecmd`, `docs`.
    Priority: medium. Dependencies: 57 for the privacy rules.
    Done in part (#224), step 1: the new `audit` package. An `Event` has no free text: a time, a count and five fields
    that are members of closed sets (kind, outcome, actor class, reason code, and a `Ref` pseudonym that is a keyed
    HMAC of the subject, 16 hex characters). `Logger.Log` writes one JSON line, or refuses a value off the allowlist and
    counts it (`Rejected`) instead of writing it; a test fails when anyone adds a free `string` field to `Event`. Left:
    calling it from the stores and servers (a reviewed change per call site), steps 2 and 3.
    Also done (#225), step 4 in part: `GET /ready` on the docstore server answers 200 `{"ready":true}` when the store
    lists, else 503 `{"ready":false}` with nothing else (the store's error is only logged), for a load balancer to
    decide on traffic; `/health` stays the "process is up" answer. Left: the same route on the other servers.
    Also done (#226), step 2: `docstore.Backup` writes every stored document of an adapter as opaque blobs (a tar with a
    manifest of ids, sizes and SHA-256) and `docstore.Restore` checks the whole archive against the manifest before it
    writes anything, then saves each document; a damaged, cut or foreign archive writes nothing (`ErrBadBackup`). Give
    them the adapter under the encrypting one: the test `TestBackupOfAnEncryptedStoreIsOpaqueAndRestoresReadable` shows
    the archive holds only ciphertext, needs no key, and the owner reads the restored documents. Also done (#231): the commands
    `docstore backup <file>` and `docstore restore [-force] <file>` (the file is written beside its final name and
    renamed, mode 0600; a store that already has documents is restored into only with -force), each writing one `audit`
    event (`BackupMade`, `BackupRestored`) to stderr, without any id. Left: scheduling and rotation of backups, and
    calling `audit` from the other places that change a store.
    Also done (#227), step 3 in part: `GET /admin/usage` (only when the operator guard `WithDirectoryAdmin` is set;
    a failed guard is rate limited like the other admin routes) reports `documents`, `records` (the access-list
    bookkeeping), total and largest `bytes`, and the backend quota, computed from the listing's sizes alone: it reads no
    document and names none (a test checks that the report holds none of the ids). Also done (this PR): usage over time. The counters above (documents, records, total and largest bytes, quota in use, and the usage batches the telemetry receiver took and refused) are sampled into a bounded series (`docstore/usageseries.go`): 48 hourly and 90 daily points, each the last sample of its period, seven numbers and a time and nothing else (a test fails if a field that could hold an identifier is added), in memory only so a restart starts it again and the gap shows as missing rows. `GET /admin/usage/history` is the JSON and `GET /admin/usage/page` a plain HTML page with two tables (real headers, `scope`, `<time>`, no script, a strict CSP, light and dark), both behind the operator guard and sampling on each read; `docstore` samples hourly in the background when the operator token is set (`Server.RunUsageSampling`). Tests in `usageseries_test.go`. Left: the series is not persisted across restarts, and the sampler runs only with the operator token set.
    Also done in part (#302), operator observability: package `observe` and [docs/observability.md](observability.md).
    Traces and metrics about the server's own behaviour (requests per route template, store operation timings,
    sweep timings, sign-in, passkey and limiter outcomes, errors by fixed code), **off by default**
    (`GOJIBERRY_OBS=on`, unset is the kill switch), with closed allowlists for metrics, labels, values, spans and
    span attributes (anything else is dropped and counted), route templates only, and a middleware that never reads
    the address, a header, the query or the body. A `Sink` abstraction shared with the usage producer of 57 with
    fan-out that isolates a failing sink, a builtin sink, `GET /admin/observe` (the allowlist, routing and sink
    status, also while off), env validation that names the variable, flush on graceful shutdown. Tests include a
    no-PII fuzz of the middleware. Also done (this PR): the PostHog sink (`observe/posthog.go`, standard library, in every build: aggregate
    events, `$process_person_profile` false, `$geoip_disable` true, US/EU cloud or a self-hosted https host, key from env or file
    and never shown, bounded buffer, failures counted; tests with a fake server). Also done (this PR): the OpenTelemetry sink (`observe/otlp.go`), OTLP over HTTP with JSON and the standard `OTEL_EXPORTER_OTLP_*` variables, standard library only so it is in every build (no nested module was needed; gRPC and protobuf go through a Collector). Also done (this PR): the collaboration hub, the sync relay and the font, translation, speech and braille servers use the same switches, sinks and allowlists (`observe.Setup`, `observe.Serve`, the middleware with their route templates, flush on a stop signal). **Left:** metrics for their own work (rooms, merges, scans, translation calls); persistent builtin totals.
82. **Performance and size.** Kind: fixable. What: the showcase wasm is about 26.7 MB (6.7 MB gzipped; the 21.7 MB first written here was out of date); streaming compile, code
    splitting, caching and a low-memory mode. Why it matters: slow first load excludes people on slow links and weak
    devices. Plan: (1) measure size and load per build and record a budget; (2) `WebAssembly.compileStreaming` with
    correct headers and compression; (3) split rarely used parts; (4) service-worker caching with versioned keys; (5)
    a low-memory mode that limits history and caches. Where: `examples/canvas-editor-showcase`, the build, `domrt`.
    Done in part (#230), step 1: `make wasm-size` (`.github/scripts/wasm-size.sh`, a CI step) builds the showcase and
    fails when its raw or gzip size is over the budget in `docs/wasm-budget.txt` (now 28,000,000 and 7,200,000 bytes,
    about 5% above today's 26.7 MB and 6.7 MB); raising a number is a deliberate edit. Also done (#232), step 2 as a library:
    the new `wasmserve` package is the static handler for a built wasm app. It serves `.wasm` as `application/wasm` (needed
    for `WebAssembly.instantiateStreaming`), sends a precompressed `.br` or `.gz` beside the file when the browser accepts
    it, otherwise gzips text-like files and wasm once and keeps the result, sets an `ETag` (so a repeat visit is a 304) and
    `Vary`, can mark hashed names immutable, and can answer app paths such as `/d/report` with `index.html` (a missing
    file with an extension stays a 404). Then (#284): the examples' dev servers use it (`passkey-demo`, `people-keys-demo`, `collab-editor-app` and `canvas-app` serve their wasm with `wasmserve.Handler`; `Options.GzipLevel` lets a dev server pick `gzip.BestSpeed`), `wasmserve.Precompress` and `cmd/wasmdist` write a `.gz` (and, with the brotli tool, a `.br`) beside each compressible file, `make showcase-dist` builds the showcase into `dist/showcase` with them, the showcase's Dockerfile does the same and nginx sends them (`nginx.conf`: `gzip_static`, the wasm type, `no-cache` with the ETag, 404 for missing scripts), and [deploy.md](deploy.md) is the recipe (nginx, Caddy, a Go server, how to check). Tests: `TestPrecompressWritesSiblingsThatHandlerSends`, `TestPrecompressKeepsRefreshesAndRemoves`, `TestPrecompressLeavesWhatDoesNotShrink`, `TestPrecompressBrotliThroughTheTool`, `TestPrecompressReportsAToolThatFails`, `TestGzipLevelOfTheHandler`, the negotiation tests `TestPrecompressedNegotiationHeaders` (gzip, br, identity and q=0 each get their own body, ETag and length, HEAD, the body decodes to the file), `TestPrecompressedNotModifiedPerEncoding` (304 by ETag and by date, an ETag of one encoding does not match another), `TestPrecompressedRanges` (206, `Content-Range`, suffix ranges, `If-Range`, 416) and `TestStalePrecompressedSiblingIsNotSent` (a `.gz` older than a rebuilt wasm is ignored), and, for the command, `TestRunWritesGzipSiblingsAndReportsThem`, `TestServedDirectoryAfterCompressing`; the dev server was also run by hand (type, gzip, 304). Left: steps 3 to 5 (splitting the wasm, service-worker caching with versioned keys, a low-memory mode); the nginx, Caddy and Docker settings in the recipe were not run here (no daemon, nginx or Caddy), and `.br` files need the brotli tool, which this machine lacks (tested through a stand-in).
    Priority: medium. Dependencies: none. Size check (this PR): `make wasm-size` failed on main (28.2 MB raw against the 28.0 MB budget) after the privacy, search-index, usage and print-layout work; the shipped build is now measured and built with `-ldflags="-s -w"` (no symbol table or DWARF: 27.7 MB, 6.96 MB gzipped), and the budget was raised on purpose to 29.0 MB raw and 7.4 MB gzipped, then to 29.5 MB and 7.5 MB when the HTML parser came in (item 72), and to 30.0 MB and 7.7 MB with the OpenDocument reader and writer. The real fix is steps 3 to 5 (splitting the wasm, caching it with versioned keys, a low-memory mode).
    Also done (this PR), step 4: service-worker caching with versioned keys. `wasmserve.ServiceWorker` makes the script of a worker that keeps `index.html`, `wasm_exec.js` and `main.wasm` in a cache named for a hash of their contents (a new build is a new script, the cache fills when it installs and the old one is deleted when it takes over); it touches nothing else, so the API, sign-in and other origins go to the network, and an app path comes from the network with the cached start page only as the offline fallback. `Options.ServiceWorker` serves it as `/sw.js`, `cmd/wasmdist` writes it, the showcase page registers it (`?nosw` leaves it out), and [deploy.md](deploy.md) says how. `TestServiceWorkerKeepsTheAppForRepeatVisits` runs it in Chromium and counts the server's requests for the program: none on a repeat visit. Found on the way: Chromium cut off a stream of the 30 MB program into the cache and a second large download at once, so the worker reads each file whole and one at a time. **Left for 82:** step 3 (splitting; Go's wasm has no dynamic loading) and step 5 (a low-memory mode).
    Measured (splitting the file worker by format, switch in `files-worker.js`): not worth it yet, nothing merged. PDF alone built to 10.4 MB raw (3.0 MB gzipped) and Word/OpenDocument/HTML/RTF/EPUB to 8.8 MB (2.4 MB), against 11.0 MB (3.1 MB) for both together: the programs share a floor of about 8.7 MB, because `docfile` imports `fonts`, which imports `net/http` (and `fontserver`), and `canvas`. A visitor who needs one format would save at most 0.7 MB gzipped, one who needs both would download 5.4 MB instead of 3.1. The way to make the switch pay is to take `net/http` out of the worker first: move the font types (`fonts.Font`, `Variant`) that `docfile` uses into a leaf package, so `docfile` and `export` no longer link the HTTP client. Then per-format programs would be a few MB each and the JS side is a switch on the format that loads `pdf.wasm` or `office.wasm` in the same worker, fidelity answered by whichever is loaded.
    Done after that measurement: the font types (`Font`, `Variant`, `Closest`) moved to a leaf package, `fonts/fontdata` (`fonts` aliases them, so no API changes), and `docfile` imports that instead of `fonts`. The file worker no longer links `net/http`, `crypto/tls` or `fontserver`: `files.wasm` went from 11.0 MB to 9.5 MB raw and 3.1 MB to 2.7 MB gzipped (budget now 9.8 MB / 2.8 MB). The split by format is still not done; the next step is to see what else the floor holds (about 7 MB: the Go runtime, `canvas`, `gopdf`, fonts) before judging it again.
    Also done (this PR), step 5: a low-memory mode. Each undo step is a snapshot of the whole text and its formatting, 200 of them, which is what weighs on a large document; `canvas.SetHistoryLimit` (`HistoryLimit`, `DefaultHistoryLimit` 200, least 5) sets how many are kept, and the showcase's View > Display preferences > "Save memory" keeps 20. With no choice it turns itself on where the browser reports 2 GB of memory or less (`navigator.deviceMemory`, Chromium only); the person's choice, either way, wins and is kept. Tests: `TestHistoryLimitCanBeLowered`, `TestLowMemoryModeKeepsFewerUndoSteps`. Not measured: how much memory it saves on a real large document on a real small device. **Left for 82:** step 3 only (splitting; Go's wasm has no dynamic loading, so it means a second binary or a worker for the importers and exporters).
    Also done (this PR), step 3: the file formats are a second program. `export/remote` is the light side (the format tables, proxies that fill the transfer registry, the fidelity calls, and `WordImport` for the migration) and `export/remote/serve` the heavy side that holds all of package `export`; they speak four operations (import, export, fidelity, import-fidelity) with documents as the editor's JSON. In the showcase the heavy side is `files.wasm` (`./files`) in a web worker (`files-worker.js`), started the first time a file is imported or exported (a request blocks, so the Files tab, the document list's download and the migration run it in a goroutine; the fidelity panel says "checking…" until the worker answers). The main program no longer registers or links Word, OpenDocument, PDF, RTF, EPUB or HTML in or out (`migrate.DefaultImport` is no longer set by the package, for the same reason): 29.4 MB became 26.1 MB raw and 7.4 MB 6.4 MB gzipped for every visitor, and the worker costs 11.0 MB (3.1 MB gzipped) once, when first used. The service worker keeps the second program once it has been fetched, not ahead of time. Budgets in `docs/wasm-budget.txt` are now per program. Tests: `TestEveryFormatThroughTheProtocolMatchesTheDirectCall`, `TestTheFormatTablesAreWhatExportRegisters`, `TestReportsThroughTheProtocolMatchTheDirectCall`, the Files-tab tests unchanged and passing through the worker, and the service-worker test extended. Found on the way: `TestShareAndStats` failed about half the time on main. The mirror's selection, written at the canvas selection, collapses when the copy is drawn again under it, and the late `selectionchange` was read as the reader moving to the start; a collapse onto an end of the selection the page wrote a moment ago now puts the selection back (item 60). **Left for 82:** memory saved by low-memory mode is not measured; the importers and exporters could be split further (one program for PDF alone, 3 MB) if the worker's 11 MB matters.
83. **i18n depth.** Kind: fixable. What: only de, fr and es packs exist; add a translation workflow, pluralisation,
    locale-aware dates and numbers, and a contribution path. Why it matters: reach, and plain-language labels (65)
    need real translations. Plan: (1) message catalogue format with plural rules; (2) a check that finds missing and
    stale keys; (3) locale-aware formatting through one helper; (4) a short contributor guide; (5) right-to-left
    packs once 64 lands. Where: `i18n`, `docs`. Priority: low. Dependencies: 64 for RTL. Done in part (this PR), plan steps 2 and 4: the 177 messages added since the packs were last filled are translated into de, fr and es; `TestEveryMessageInTheSourcesIsInThePacks` fails on any `T`, `tr`, `trf` or `Translate` call whose id is in no pack (so a feature cannot leave a language behind), and [translating.md](translating.md) is the contributor guide. Plural rules (step 1) are done too (#188): `i18n.PluralCategory` (CLDR cardinal rules for whole numbers in English, German and the Germanic default, French, Spanish, Italian, Catalan, Portuguese, Russian, Ukrainian, Polish, Czech, Slovak, Arabic, Hebrew and the single-form Asian languages), `Catalog.Plural` (text per `id#category`, falls back to `#other`, then to the English in the code), `components.TranslatePlural`/`trn` and the showcase's `Tn`; the test needs `id#other` in the packs for every `Tn`/`trn` call, and `suggest.count` is the first message to use it. Step 3 is done for en, de, fr and es (#189): `i18n.Number`, `Percent`, `Date` (short, medium, long; plain "en" and "en-US" month-first, other English day-first) and `Time`, plain functions of the language tag, and `Catalog.Plural` writes its number the way the language does (12.345 in German); the status bar's word and character counts use both. `TestNoStaleMessagesInThePacks` (#190) fails on a pack id no source asks for (ids built at run time count when their prefix is a literal ending in "." or "-"); two stale ids were removed. Relative times are done (#194): `Catalog.Ago(lang, d)` ("just now", "5 minutes ago", "vor 2 Stunden") as plural messages in the packs; nothing in the showcase shows one yet. Time zones are done (this PR): `i18n.Zone` ("UTC", "UTC+2", "UTC+5:30", the offset at that moment) and `i18n.DateTime` ("14. Okt. 2026, 16:05 (UTC+2)"); the components write every time through the hook `components.FormatTime` (versions, comments, suggestions, a document's last change; ISO until the app sets it), and the showcase sets it to the viewer's language and own zone, so a time says which zone it is in. Fixed offsets only: Go's time zone database would add about 450 KB to a wasm that has 2% of its budget left, and the browser already knows the offset. More languages and right-to-left packs are done (this PR): Portuguese, Arabic and Hebrew, all 1204 messages with their plural forms (Arabic six categories, Hebrew three), as files fetched when the language is chosen (`static/lang-<code>.json`, `langload.go`; the wasm does not grow), the page turned around for Arabic and Hebrew, `TestLazyPacksAreComplete`, `TestLazyLanguagesAndRightToLeft` (also chosen from the preferences) and an axe run in Arabic. **They are model-drafted, not reviewed by a native speaker; that review is the one thing left for them.** Left: converting the other older messages that use neutral wording, a native review of pt/ar/he, and the ids built at run time, which the test cannot see.
84. **Done (this PR): lint the examples.** Kind: fixable. The root `.golangci.yml` excluded the path `examples`, which
    hid every example from `make lint` and `make lint-modules`. The exclusion is removed (no path exclusions remain)
    and every finding is fixed with no `nolint`: 40 in all, found by running all three targets natively and as
    js/wasm with `--max-issues-per-linter=0 --max-same-issues=0`. errcheck (9): `rows.Close`, `db.Close`,
    `Encoder.Encode`, `db.Exec` and `h.Close` are checked (logged, or `t.Errorf` in tests), and the best-effort
    `os.Remove` of a scratch binary and `conn.CloseNow` on a connection that is going away are an explicit `_ =` with a
    reason. In `canvas-app`'s end-to-end test the database now closes in `t.Cleanup` after the `DELETE FROM documents`
    cleanup, which previously ran after a deferred `Close` and so could never succeed. staticcheck (13, SA1019):
    `collab-editor-app` moved from the deprecated `nhooyr.io/websocket` to its successor `github.com/coder/websocket`
    v1.8.15 (same API; the same module and version as the root `go.mod` and `collab/hub`). unused (18, native pass only): one test helper with no caller was deleted; the rest are used only by
    the js/wasm entry points, so they moved into js/wasm-only files (`canvas-editor-app/model_browser.go`,
    `canvas-editor-showcase/sample_wasm.go`, `canvas-xango-demo/lorem_wasm.go`) with no change to their code. A
    planted unchecked error in a root-module and a nested example makes `make lint` and `make lint-modules` fail.
    Where: `.golangci.yml`, `Makefile`, `examples`. Dependencies: none.

**Order for 59 to 84:** 60, 59, 61 and 62, 79 (first slice), 72, 70, 76, then the rest.
85. **Done (this PR): the findings of an outside review of main (#90 to #123).** A fresh reviewer read the merged
    work and ran its tests with `-race`: no races, no silent skips, and these were wrong, all fixed here.
    1. `AuditAddress` computed the gap to a far-future entry as a `time.Duration`, which overflows past about 290
       years and hid the "dated in the future" violation (a hostile directory could also push the settle boundary
       forward); the gap is now compared in seconds. 2. `WatchReport` dropped `AuditAddress` notes, so entry 30's
       fast-clock note never reached a watcher; `WatchReport.Notes` now carries them. 3. The start-up line of entry 40
       said "two minutes"; the default ceremony lifetime is 5 minutes plus the sweep's grace, and the line and the
       entry say so. 4. `sessionstore.Sweep` treated any name under its prefixes as its own (a short hash or a
       non-canonical number was deleted and counted toward the cap); it now takes only
       `<prefix><canonical bucket>/<64 hex>`, as the passkey store does, and keeps the Puts and removals that
       happened during its listing, as that store does. 5. Unreadable rate-limit records no longer count as
       evicted "live" callers. 6. `ParseWatchState` refuses `null` and negative positions. Not deeply re-audited by
       that review, for lack of budget: the filtered/full chaining of `WatchLog` (#105) and the signed-frame
       verification internals (their tests pass); a second pass over them is worth a later review.
86. **Public offer: the report counters are still per process (the sessions and their counts are shared since item
    55).** Kind: fixable. Why: behind replicas each process counts the reports it has seen, so each cap can be reached
    once per replica; the daily budget per address is shared since entry 16 and the sessions since entry 55. Plan:
    report counters kept in the report records. Priority: low to medium for several replicas, none for one. Where:
    `docstore/public` (`reportwatch.go`). Avoid: one replica. **Done in part (this PR):** each reporter's allowance of priority reports can be shared: `Options.PriorityQuota` (a `StoreQuota` of `MaxPriorityReportsPerReporter` a day; `GOJIBERRY_DOCSTORE_PUBLIC_REPORT_COUNTERS=store|store-required`) is charged one unit per priority report under `prio|<reporter>`, refunded if the report is not stored, with the fixed UTC days of a `StoreQuota`. Tests: `TestPriorityAllowanceIsSharedByReplicasThroughTheStore`, `TestPublicPriorityQuotaModes`. Still per process: the total number and bytes of reports (the global cap) and the burst watch over networks, which are bounds on the process's own memory and an operator alarm rather than a per-person allowance.
87. **Done (this PR): the showcase page rotates and resets the owner key.** Was: the owner key could be made, backed up,
    restored and forgotten, but not replaced, so a copied key or a lost key and backup left the address refusing
    entries. Now `dirclient.Client` has `Rotate`, `RequestReset`, `CompleteReset` and `CancelReset`, and `Enrollment`
    says whether a reset waits (`ResetSince`, `ResetDue`) and which key is current. The panel (`encowner_reset.go`):
    **Replace the owner key** while it is held (a new key signed in by the old one, its backup shown at once, the old
    backup refused from then on); **I lost my owner key** asks for a reset, makes the new key at once and keeps it
    apart (`<url>#reset`, non-extractable where the browser can) with its backup shown, and **Complete the reset**
    works once the directory's 24 hours are over (before, the directory's refusal is shown); **Cancel the reset** with
    the held key, and **Give up the reset here** to drop the new key. A waiting reset is stated in the address's
    state line. Tests: native (`TestRotateAndResetTheOwnerKey`: wrong current key, early completion, cancel, old key
    refused afterwards) and a browser test (`TestRotateAndResetTheOwnerKeyFromThePage`). Then (#283): "Watch my address"
    names a reset as its own kind of alert: `dirclient.WatchResult.Alerts` are `Alert{Kind, Seq, Text}` (`AlertReset` for
    a completed reset that somebody else made, `AlertResetRequest` for a request while it waits, `AlertViolation`, else
    `AlertEntry`), and the panel words the first two for what they mean and what to do (`dir.alert.reset`,
    `dir.alert.resetRequest`, translated in de, fr and es) and marks each list entry with `data-kind`; kept alerts carry
    their kind, and ones kept as bare text by an older version still load. Tests: `TestWatchNamesAResetOfTheOwnerKey`
    (native, the demo server's clock moved: request, reset after 25 hours, the person's own reset is no alert, a
    publication stays a plain entry) and `TestWatchNamesAResetOfTheOwnerKeyOnThePage` (browser, kinds and wording, kept
    over a reload). Left: the demo server's clock is moved in the tests, not by the page; a cancelled reset and a
    rotation by somebody else are still plain entries.
88. **Done (this PR): the API dumps under `docs/*.txt` are regenerated, and `make check` fails when they go stale.**
    Was: `make docs` (`go doc -all`) had not been run for a long time (about 2,200 lines of difference across eight
    dumps, the `components` dump alone about 1,900), so the dumps did not show the real public API. Now they are
    regenerated, and `make check` runs `make docs` and a new `docs-check` (as `generate-check` does for `*.xg.go`)
    that fails when a tracked dump changed. `Makefile`, `docs/*.txt`.
89. **Security review of the keys, ceremonies and directory: what was fixed and what is open.** Kind: mixed. A
    read-through of the code (not an external audit, which should come before anyone relies on this for secrets).
    **Fixed (this PR):** `encrypt.OpenChallenge` decrypted whatever the directory sent as a "challenge" with the
    unlocked key and returned up to 1 KiB of it to the directory, so a malicious or compromised directory could have
    read the start of any envelope made for that key (a stored document's, say) by presenting it as a challenge. It
    now reads only a nonce's length and refuses anything that is not exactly a nonce (`ErrNotAChallenge`), sending
    nothing back (`TestOpenChallengeIsNotADecryptionOracle`). **Open:** (1) ~~the showcase's owner key in `localStorage`~~ **fixed (this PR)**: it is now a
    non-extractable WebCrypto Ed25519 key in IndexedDB (the directory client signs through a `crypto.Signer`, so it
    never needs the key's bytes: `encrypt.SignOwnerOpWith`, `SignOwnerPublishWith`, `directoryclient.OwnerSigner`), so
    a script in the page can ask it to sign while the page is open but cannot copy it away. Its backup is shown once,
    when it is made; a key an earlier page kept as text is moved on the next visit; a browser without Ed25519 in
    WebCrypto falls back to text and the panel says so. Still in `localStorage`: the directory session token typed
    into the page (it belongs to the provider session, which the person can end), and a signature is still possible
    for as long as a hostile script runs in the page; (2) ~~the log keys are pinned on first use~~ **improved (this PR)**: the keys are shown with
    fingerprints, and a fingerprint typed from another source (the operator's page, a call) must match one of them or
    nothing is pinned; without one it is still trust on first use, and the panel says so; (3) the
    expiry race is closed to the content, not to a save with identical bytes; (4) an S3-compatible service that
    ignores `If-Match` on delete would delete a changed document (the doc says so; there is no live endpoint here
    to test the real services); (5) the passkey PRF path, the age and SSH usage and the signed-frame format have
    tests but no independent review. Priority: (1) medium, the rest low. Where: `examples/canvas-editor-showcase`
    (`encowner.go`, `encdir.go`), `docstore/directoryclient`, `docs/encryption-and-passkeys.md`.
