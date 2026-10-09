# Roadmap

What is planned beyond the [opendocs port plan](opendocs-port-plan.md) and the
[known limits plan](known-limits-plan.md). Ordered by what is needed next.

## Merges since the last full test run

4 of 10 since the last full run (the full suite passed on main 1ab7677; #108, #109, #110, this PR); the full suite runs on main after the
10th merge and the counter resets to 0 after it. PRs A, B and C are done (#99, #100, #101).

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
9. **s3/blob `DeleteIf`.** Plan: implement `ConditionalDeleter` for both (postgres and mongo have it). Avoid: the
   conditional claim in `SweepRateLimits` (`docstore/limiter.go`; the sweep that design step 4b, "Limits that
   remain" item 10, means) already narrows the rate-limit race to a token; it does not cover document expiry.
10. **Expiry delete on the digest read (depends on 9 for s3 and blob).** `ExpiringAdapter.purgeLocked` and `Sweep`
    (`docstore/expiry.go`) delete unconditionally under an in-process lock. Plan: where the store is a
    `ConditionalDeleter`, delete the document with `DeleteIf` on the digest read, and keep a changed document.
    Avoid: one process per store.
11. **MongoDB live tests.** Plan: run the backend tests against a live server in CI (`TEST_MONGO_URI`; see the
    note on store backends above). Avoid: run them locally against a server before relying on the backend.
12. **Log witnesses (directory).** Plan: a witness protocol where independent parties co-sign heads. Avoid:
    compare heads between two auditors by hand.
13. **Directory key rotation versus the log pseudonym.** Plan: keep older entries matchable after the retired
    key is removed. Avoid: do not remove the retired key until owners have fetched their older pseudonyms.
14. **Dead-room tombstone in memory.** The hub's `h.gone` entry lasts `goneMemory` (one hour) and is evicted at
    `maxGone` (`collab/hub/hub.go`). Plan: keep it in the store, not the process. Avoid: it is lost after one hour,
    on a restart, or by eviction at the cap; restart during quiet hours.
15. **Expiry-prefix test pin and stale-tab renewal throttle (#82 minors).** Plan: pin the prefix in a test and
    throttle renewals from a stale tab. Avoid: close old tabs.
16. **Public per-process counters.** Plan: shared `SessionStore` and `Quota` (see
    [public-docstore.md](public-docstore.md#75-what-is-left) 7.5 item 1 and the extension-points table in
    [7.4](public-docstore.md#74-extension-points)). Avoid: one replica.
17. **Snapshots cannot be refused (overshoot); a deleted snapshot is subtracted only at the next reconcile.**
    Plan: the saver must ask the budget before writing a snapshot, so it can be refused or reserved. Avoid: leave
    headroom under the byte budget.
18. **`OnSave` auto-wiring, the real remainder.** `docstore/public/service.go` already wires
    `ExpiringAdapter.OnSave` when the store is an `*ExpiringAdapter`. Left: a Service built after the adapter is
    already in use (the hook is set at construction), and stores wrapped so they are not an `*ExpiringAdapter`.
    Plan: build the Service first, or expose the hook through the wrapper. Avoid: call `NoteVersionWrite` by hand.
19. **#85 minors:** report dedupe key (reporter and category only), old env-var warning, slow-upload rate
    floor, wide-cap migration note. Plan: one small change each. Avoid: see the upgrade notes in
    [public-docstore.md](public-docstore.md#75-what-is-left) 7.5 item 6.
20. **Showcase: in-memory originals during a change.** Plan: keep the journal's originals on disk. Avoid:
    keep the tab open until a change finishes.
21. **Directory publish in the showcase and the pick UI.** The showcase has its own `dirclient`, while the demo
    (`examples/people-keys-demo`) has the controls. Plan: move the showcase to `directoryclient`, ask the person
    for a private key or another passkey assertion per signature (proof of possession and an owner-signed entry),
    and finish the app-side controls (publish and pick). Avoid: publish from `examples/people-keys-demo`.
22. **`walk()` takes the first matching link, no branch search** (`examples/canvas-editor-showcase/encstore/bind.go`).
    It takes the first link whose `From` matches and whose signer is in the list, with `break` and no
    backtracking, bounded by `steps > len(cfg.Links)`; it does not loop forever, but a dead-end first match can
    hide a reachable link. Plan: search all links from each list (BFS or DFS) with a visited set of digests.
    Avoid: none known; report an unlock that fails after many key changes.
23. **`directoryclient` `WatchState` in the old `made` format no longer loads** (the `people-keys-demo`
    `localStorage`): an ignored decode error half-loads state. Plan: fix it in the demo's load path
    (`examples/people-keys-demo/main.go`, `newPage`): clear the state on a decode error. Avoid: clear the demo's
    saved state by hand.
24. **`ErrFilteredEnrolled` should also check `audit.Enrolled` after the audit.** Plan: add the check; it must not
    fire for a legitimately filtered non-owner caller. Avoid: use a full read for an address that may be enrolled.
25. **protobuf is still an indirect dependency through the lint tool.** Plan: update or replace the tool.
    Avoid: none needed at runtime.
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
30. **Clock-ahead replica causes false "dated in the future" directory violations.** Kind: inherent in part (no shared
    clock, [limits register item 11](limits-register.md#public-docstore-and-operations)), fixable in the report. Why: a replica whose clock is ahead writes entries a correct replica
    then reads as dated in the future, and an auditor reports a violation that is skew. Plan: report a small skew as
    a warning with the measured difference, not a violation, keep a hard violation past the 10 minute rule, and
    document NTP in the operator notes. Priority: medium. Where: the log checks in `docstore/directoryclient` and
    `docstore/encrypt` (`directory_adapter.go`), `docstore/limiter.go` for the 5 second re-dating.
31. **Rate-limit flooding resets legitimate callers.** Kind: fixable, reduced by a cap. Why: `MaxRateLimitRecords`
    (100,000 in `docstore/limiter.go`) keeps the newest budget records; a flood from many addresses fills it and
    `SweepRateLimits` evicts the oldest, which are live, slow callers, whose budget is then reset to full. Plan:
    evict records that have refilled first (they carry no information), then the least recently used, and count
    the evictions of a live record in a metric. Priority: medium. Where: `docstore/limiter.go`
    (`SweepRateLimits`, `MaxRateLimitRecords`). See also the botnet limit,
    [limits register item 9](limits-register.md#public-docstore-and-operations).
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
34. **Blob backend has neither `SaveIf` nor `DeleteIf`.** Kind: fixable only in part. Confirmed:
    `docstore/backends/blob/blob.go` implements `Save`, `Load`, `List`, `ListPrefix` and `Delete` only (lines 92-138);
    the S3 backend has the conditional write (`docstore/backends/s3/s3.go:156`), as do postgres and mongo. The
    service API: the blob backend sits on `gocloud.dev/blob` v0.46.0 (`docstore/backends/go.mod:16`), whose portable
    `WriterOptions` has `IfNotExist` (create only, `PreconditionFailed` when the blob exists) and no `If-Match`, ETag
    or generation match; `Attributes.ETag` can be read, but not sent as a precondition. So a create-only `SaveIf` with
    an empty `prev` is possible, a replace or a delete guarded by content is not, without the driver-specific
    `BeforeWrite`/`As` hook (for S3 the `PutObjectInput.IfMatch`). The ChallengeStore and limiter need the guarded
    replace and delete, so `store` mode stays unsupported on this backend. Not verified: whether each driver
    (fileblob, memblob, s3blob, gcsblob, azureblob) honours `IfNotExist`. Plan: name the backend unsupported for the
    conditional features in its docs and in `ErrConditionalUnsupported`'s text now; item 9 covers `DeleteIf` for s3
    and blob. Priority: medium. Where: `docstore/backends/blob/blob.go:92-138`, `docstore/conditional.go:12-47`
    (the interfaces) and `:94` (`SupportsConditional`).
35. **Directory log reads share the lookup budgets for client IP and whole server.** Kind: fixable. Confirmed in
    `docstore/directory.go`: `readBudget` (`:846-855`) takes three tokens per read: the user's own limiter (separate:
    `lookup` for lookups, `logRead` for the log), then `lookupIP` and `lookupAll[lookupShard(u.ID)]`, which both
    lookups (`:896`) and log reads (`:928`) spend, as the comment at `:572-579` says. Why: a user who polls the log
    hard uses their IP budget and their shard's whole-server budget, so lookups of everyone hashed to that shard (and
    behind that IP) slow down, and the reverse. Plan: separate keys for log reads (for example `dir-log-ip` and
    `dir-log-all-<n>`), or a larger budget for the log with its own shards. Priority: medium. Where:
    `docstore/directory.go:572-605` (limiters), `:846-860` (`readBudget`), `:896` and `:928` (callers).
36. **Sticky `FirstOwner`: after a legitimate owner key rotation, third-party lookups show "changed" permanently.**
    Kind: fixable. Why: the first owner key a third party saw is kept; a rotation is indistinguishable from an
    attack, so the warning never clears. Plan: a user action to confirm the new key (a signed rotation chain
    from the owner, or a one-click acknowledgement that stores the new first key). Priority: medium. Where:
    `docstore/directoryclient` (the lookup state and its `FirstOwner` field). Related to
    [limits register item 7](limits-register.md#the-key-directory).
37. **The showcase shows the raw `ErrUnsignedAfterFlip` text with the Go-API hint.** Kind: fixable. Why: a person in
    the browser reads "use `Repair(AcceptUnsigned)`", which they cannot call. Plan: translate the error in the
    showcase into a plain, actionable message (which files, what the owner can do, in de/fr/es too). Priority:
    medium. Where: `examples/canvas-editor-showcase` (the error display).
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
    (`finishFiles`, `markUnsignedLate`, `clearUnsignedLate`, `UnsignedDocs`).
40. **Passkey challenge records show the pending registration e-mail and name on a plain store.** Kind: fixable
    by configuration. Confirmed: `StoreChallenges.Put` (`auth/passkey/challenge_store.go:132-146`) stores the whole
    `Challenge` as JSON (`storedChallenge` embeds it, `:126-129`), and `Challenge` (`auth/passkey/challenge.go:14-22`)
    holds `Email` and `Name` (for a new account) plus the WebAuthn session data, under the key
    `ChallengePrefix` plus the SHA-256 of the token (`:120-122`). Why: a person who can read the store reads them (see
    "Extension points" in [encryption-and-passkeys.md](encryption-and-passkeys.md#b1-extension-points)). Plan: use
    an encrypting adapter for the `passkey/` keyspace, say so in the `store` mode start-up line, and consider
    hashing the label. Priority: low. Where: `auth/passkey/challenge_store.go:126-146`,
    `auth/passkey/challenge.go:14-22`.
41. **Signed-out same-e-mail passkey registration makes two accounts, by design.** Kind: inherent while the e-mail is
    a label ([limits register item 6](limits-register.md#encryption-and-keys)). Why it matters for later work:
    the e-mail verification work must not merge accounts on the bare label. Plan: none now; record it in the
    verification design. Priority: low. Where: `auth/passkey`.
42. **A revoked signer can keep writing new frames until it is removed from every list.** Kind: inherent without a
    format change; the frame v4 plan is in item 5 above and in the
    [limits register](limits-register.md#encryption-and-keys) (item 1). Priority: low until the format work is
    scheduled. Where: `docstore/encrypt` (`verifyFrame`).
43. **A link can be replayed past the 32-link window or when it was made before links carried a nonce.** Kind:
    inherent (register entry already exists: [item 12](limits-register.md#clients-and-storage)). Plan: none beyond
    a larger `Config.Links` for a store that changes keys often. Priority: low. Where:
    `examples/canvas-editor-showcase/encstore` (`Config.Links`, `walk`).
44. **An SSH-only store with no signed document file yet is undetected.** Kind: inherent, documented residual
    ([limits register item 12](limits-register.md#clients-and-storage); the first owner list is trust on first
    use). Priority: low.
45. **Unenrolled-address entry numbering by a holder of the directory key.** Kind: inherent while the address is not
    enrolled; enrolling an owner key closes it ([limits register item 7](limits-register.md#the-key-directory),
    point (d)). Priority: low.
46. **Mongo live tests skip without `TEST_MONGO_URI`.** Kind: fixable. Same as item 11, which holds the plan: run
    in CI, or document how to run them locally (a one-line `docker run` and the variable). Priority: low.
47. **Already listed, kept as they are (no duplicate entry):** item 15 (stale-tab renewal throttle), 16 (per-process
    public counters), 17 (snapshot overshoot), 19 (report dedupe key, slow-upload rate floor, old env-var warning,
    wide-cap migration note), 20 (showcase in-memory originals), 21 (showcase directory publish with proof of
    possession and an owner-signed entry), 22 (`walk` first match), 23 (demo `WatchState` old `made` format;
    clear on decode error, `examples/people-keys-demo/main.go`), 24 (`ErrFilteredEnrolled` and the audited
    state), 25 (protobuf as an indirect dependency).

48. **ARIA attributes across the canvas editor and components.** Kind: fixable. What: roles, accessible names and
    labels, live regions, a sensible focus order, and keyboard operation of every control (toolbar, menus, dialogs,
    pickers, the canvas itself). Why it matters: people who use assistive technology cannot use controls that have no
    role, name or keyboard path, and it is a legal requirement in many places. Plan: (1) write an audit checklist per
    component; (2) fix the gaps found; (3) add automated checks (for example axe-core) to the browser e2e so a
    regression fails the build. Priority: high (first of the accessibility group).
    Where: `components`, `examples/canvas-editor-showcase`, the browser e2e.
49. **Screen-reader output while typing (voice-over).** Kind: fixable in part (the canvas is not DOM text). What: an
    accessible text mirror of the canvas (a hidden DOM or an ARIA live region) that announces typed characters, caret
    moves, selection and formatting changes. Why it matters: a canvas gives a screen reader nothing to read, so a blind
    person cannot write in the editor. Plan: build the mirror; throttle announcements and add verbosity settings (off,
    words, characters, formatting); verify with NVDA, JAWS and VoiceOver and record the results. Priority: high.
    Where: the canvas editor input layer and `components`.
50. **More accessibility tools and configs.** Kind: fixable. What: respect high contrast and reduced motion; font size
    and line spacing; a dyslexia-friendly font option; caret and selection visibility; a shortcuts help and key
    remapping; zoom; color-blind safe palettes; clear focus indicators. Why it matters: needs differ by person, and a
    single default fits few. Plan: expose each as a setting with a safe default, honor the matching media queries, and
    test each in the e2e. Priority: medium. Where: `components`, the canvas editor, `i18n`.
51. **Preference menu.** Kind: fixable. What: one settings panel for the accessibility options above plus language,
    theme and editor behavior; keyboard accessible, searchable, with reset to defaults and import and export. Why it
    matters: settings scattered over the UI are hard to find, especially with a screen reader. Plan: define a
    preference schema, build the panel on it (it needs item 48 first), store locally, then item 52 syncs it.
    Priority: high. Where: `components`, the showcase.
52. **Save preferences, styles, templates, fonts and other configs in a central server.** Kind: fixable. What: a
    per-user config store (docstore documents under a system prefix, authenticated, encrypted with the owner keys by
    default, versioned, with conflict handling across devices and a quota); a local fallback that works offline; fonts
    as stored assets with size limits and a licensing note; shareable templates and styles. Why it matters: assistive
    settings follow the person across devices, and they are sensitive (they can reveal a disability), so they must be
    encrypted and never logged. Plan: key layout and schema, encryption by default, version and merge rules, quota,
    offline queue, asset limits, sharing. Depends on PR A (shared sign-in sessions). Priority: medium. Where:
    `docstore`, `auth`, `components`.

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
    Where: `auth/sessionstore`.
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
55. **Per-process auth state that remains.** Kind: fixable. Checked: there are no CSRF tokens or cookies in `auth`
    (the bearer header is the only credential, the OIDC state is the redirect's CSRF check and is now shared). What is
    still per process: (a) the public offer's anonymous sessions (`public.SessionStore`, item 16); (b) the default
    `MemoryChallenges` and per-process rate limiters when their `store` modes are off; (c) the session and state
    counts used for the bound, which each replica refreshes from its own sweep. Priority: low. Where: `docstore/public`,
    `auth/sessionstore`.

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
57. **Privacy-first telemetry with no personal data.** Kind: fixable. What: a minimal, aggregate-only usage and
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
58. **Small leftovers from the PR #99 and #100 reviews.** Kind: fixable. Why it matters: none is urgent, but each
    is a small trap for the next reader. Priority: low. Where: `auth`, `auth/passkey`, `internal/docstorecmd`.
    1. `auth/passkey/challenge_store.go` `StartChallengeSweep`: the retry limit 4 is hard-coded twice (the "of 4" log
       and the `attempt==4` check). Use a named constant.
    2. The `NewStoreChallenges` doc still says to run `RunChallengeSweep`: point it to `StartChallengeSweep`, and
       decide whether `RunChallengeSweep` stays exported.
    3. `StartChallengeSweep` retries the whole sweep when the listing worked but a Delete failed, which adds up to
       3.5 s of startup delay on every start if one record can never be deleted. Retry only when the listing failed.
    4. Net-delta counting has off-by-one races (a Put or Take straddling the before and after reads; a Put between
       `net.Load` and `approx.Store`). The count drifts by at most the operations in the window and the next listing
       repairs it. Document it as a known, bounded drift.
    5. Auth login limiter: with a custom `clientIP` that returns "", all clients share one key `login|` (the comment
       says `clientIP` must be correct). Add a guard that falls back to the `RemoteAddr` host when `clientIP`
       returns empty.
    6. `internal/docstorecmd`: the login limiter is installed even when the manager has only passkeys and no OpenID
       Connect provider (harmless, no login route is mounted). Note it or skip it.
    7. Per-/64 IPv6 budgets for the login routes (each address is its own key, as on the passkey routes): reuse the
       /64 grouping if the public store has one.

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
    needs 51.
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
    Where: the canvas editor input layer and `components`. Priority: highest. Dependencies: extends 48 and 49.
61. **Alt text, image descriptions and structure checks.** Kind: fixable. What: alt text required or prompted when an
    image is inserted; a checker for missing alt text; optional on-device suggestions (no network); checks for reading
    order, heading level skips, empty headings and low colour contrast. Why it matters: an image without a
    description is a hole in the document for a blind reader. Plan:
    1. An insert prompt with "decorative" as an explicit choice; store alt text in the model.
    2. Checker functions over the model, each returning a finding with a location and a fix.
    3. Optional local suggestion hook behind a setting, off by default, with no data sent anywhere.
    4. Contrast check using the document's own colours; heading and reading-order checks.
    Where: the model, `components`, the showcase image insert. Priority: high. Dependencies: 60; feeds 62.
62. **Document accessibility checker panel.** Kind: fixable. What: a panel that lists findings from 61 and others,
    jumps to each, offers a fix, and can gate export (warn or block, set by the user or the operator). Why it matters:
    people need to learn about a problem before they send the document. Plan: (1) a finding schema (rule, severity,
    location, fix); (2) the panel, keyboard and screen-reader accessible; (3) one-click fixes where safe; (4) an export
    gate with an explicit override; (5) tests per rule. Where: `components`, the showcase. Priority: high.
    Dependencies: 60, 61.
63. **Accessible exports.** Kind: fixable. What: tagged PDF with language, title, reading order and structure tags;
    semantic HTML with landmarks, headings, table headers and alt text. Why it matters: an accessible editor that
    exports an inaccessible file defeats the purpose. Plan: (1) map the model to PDF tags and to semantic HTML in one
    place; (2) set document language and title; (3) validate with a PDF/UA and an HTML checker in CI; (4) report
    unmapped elements in the fidelity report of 72. Where: the export code, `docfile`. Priority: high.
    Dependencies: 60, 62, 72.
64. **Language and direction.** Kind: fixable. What: per-run language tagging so a screen reader switches voice;
    right-to-left and bidirectional text; vertical text; IME and composition input. Why it matters: mixed-language and
    RTL documents are common, and composition input must not break the caret or the mirror. Plan:
    1. Add a language attribute per run and a document default; surface it as `lang` in the mirror (60).
    2. Verify canvas rendering of bidi, RTL and vertical text; fix caret movement and selection across direction
       changes.
    3. Test IME composition (preedit shown, no mirror announcement until commit, no autocomplete popup, see 79).
    4. Browser e2e with scripted composition events plus a manual check with real IMEs.
    Where: the canvas editor text layout and input layer. Priority: medium. Dependencies: 60.
65. **Cognitive and motor options.** Kind: fixable. What: distraction-free mode; plain-language labels; forgiving
    undo (long history, undo of the last save); adjustable timeouts; dwell or switch access; large-target mode. Why it
    matters: needs differ, and short timeouts or small targets lock people out. Plan: (1) list every timeout and
    remove or make it adjustable; (2) a large-target mode with a size scale; (3) a single-switch scanning mode and
    dwell click as an experiment; (4) a plain-language label set in `i18n`; (5) settings in the preference menu.
    Where: `components`, `i18n`, the canvas editor. Priority: medium. Dependencies: 50, 51.
66. **Collaboration accessibility.** Kind: fixable. What: announcements for remote cursors, joins and leaves, new
    comments, and conflicts, through a polite live region, with a setting for how much is said. Why it matters:
    collaboration cues are visual (coloured cursors); without text a blind collaborator cannot tell anyone is there.
    Plan: (1) an event-to-message table with throttling and grouping; (2) a presence list that is real DOM; (3) tests
    with two sessions. Where: `collab`, `components`. Priority: medium. Dependencies: 60, 71.
67. **Text-to-speech read-aloud.** Kind: fixable. What: read the selection or the whole document aloud with the
    browser speech API (on-device voices where the browser offers them), with pause, speed and a highlighted current
    sentence. Why it matters: it helps people with low vision or reading difficulty and needs no assistive software.
    Plan: (1) read from the mirror text so the order matches 60; (2) per-run language voices (64); (3) controls with
    keyboard access; (4) no text sent to a network service unless the browser does so, and say so in the UI.
    Where: `components`. Priority: low. Dependencies: 60, 64.
68. **Captions and transcripts.** Kind: fixable, only if media embedding exists or is planned. What: captions and a
    transcript field for embedded audio and video, a caption track editor, and a checker rule. Why it matters: media
    without text excludes deaf readers. Plan: (1) decide whether media embedding is in scope; (2) if so, require a
    caption or transcript link on insert; (3) add the checker rule to 62. Where: the model, `components`. Priority:
    low. Dependencies: 61, 62.
69. **Accessibility testing as a release gate.** Kind: fixable. What: axe-style checks in the browser e2e tests plus a
    manual NVDA, JAWS and VoiceOver script run before each release. Why it matters: accessibility regresses silently
    unless a test fails. Plan: (1) add axe-core to the e2e for the editor, dialogs and the mirror; (2) fail the build
    on new violations, with a reviewed baseline; (3) write the manual script (open, type, navigate by heading and
    table, comments, dialogs, autocomplete, export) and a results table per release; (4) make the release checklist
    require it. Where: the browser e2e, `docs`. Priority: high. Dependencies: 48, 60.
70. **Version history UI.** Kind: fixable. What: a timeline of versions, named versions, a diff between two versions,
    restore, and who changed what, on top of the existing snapshots. Why it matters: people want to see and undo
    changes without asking an operator. Plan: (1) list snapshots with author and time from the store; (2) name and pin a
    version; (3) a text and structure diff view (also readable by a screen reader); (4) restore as a new version, never
    destructive; (5) respect permissions and encryption. Where: `docstore`, `saver`, `components`, the showcase.
    Priority: high. Dependencies: existing snapshots; diff output reuses 60.
71. **Comments, suggestions and tracked changes.** Kind: fixable. What: comment threads, mentions, resolve and reopen,
    suggestion mode and tracked changes with accept and reject. Why it matters: review is the core of shared writing.
    Plan: (1) model for ranges, threads and changes that survive concurrent edits; (2) UI with keyboard paths; (3)
    mentions without leaking addresses to people who lack access; (4) import and export in 72; (5) mirror support
    (60). Where: the model, `collab`, `components`. Priority: high. Dependencies: 60; used by 66, 72.
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
73. **Search and replace across the document and the store.** Kind: fixable. What: find and replace in a document and
    across documents, with a client-side encrypted index for the encrypted store. Why it matters: search is expected,
    and a server-side index would leak contents. Plan: (1) in-document search with case, word and regular expression
    options, announced to screen readers; (2) a local index built after decrypt, stored encrypted with a key derived
    from the owner keys, never sent in clear; (3) update on save; (4) replace across documents with a preview and
    undo. Where: `docstore/encrypt`, `components`. Priority: medium. Dependencies: 60 for announcements.
74. **Offline-first with conflict visibility.** Kind: fixable. What: a visible sync state, the queue of pending
    changes, and what will merge or conflict. Why it matters: people lose trust when they cannot tell whether their
    work is saved. Plan: (1) a status model (saved, saving, offline, conflict) exposed in the UI and to screen readers;
    (2) a durable local queue with retry; (3) a pre-merge view of the remote changes; (4) tests for offline edits and
    reconnect. Where: `saver`, `collab`, `components`. Priority: medium. Dependencies: 66.
75. **Sharing UX.** Kind: fixable. What: link sharing, per-person permissions, expiring links, view-only versus
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
    Where: `docstore/encrypt`, `auth/passkey`, `components`, the showcase. Priority: high. Dependencies: none hard.
77. **Templates, styles and a design-token system.** Kind: fixable. What: document templates, named styles, and a
    token set (colours, type scale, spacing) that themes the editor and the exports. Why it matters: consistent
    documents and accessible contrast defaults. Plan: (1) a token schema; (2) style and template documents stored in
    the central config store; (3) apply tokens to the canvas and to exports; (4) contrast validation on edit. Where:
    `components`, `docfile`, `docstore`. Priority: medium. Dependencies: ties to the central config store (52); 50.
78. **Equations, diagrams, code blocks and footnotes.** Kind: fixable. What: these elements with accessible output:
    MathML for equations, text alternatives for diagrams, readable code blocks with a language, and footnotes that
    link both ways. Why it matters: technical documents are common and these elements are usually the least
    accessible. Plan: (1) model elements; (2) mirror output (60); (3) export to MathML and semantic HTML (63); (4)
    checker rules for a missing diagram alternative (62). Where: the model, `components`. Priority: low.
    Dependencies: 60, 63.
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
80. **Print layout.** Kind: fixable. What: pagination, headers and footers, page numbers and a table of contents.
    Why it matters: documents are still printed and exported to paged formats. Plan: (1) a page model with size and
    margins; (2) layout with page breaks, widow and orphan control; (3) headers, footers and fields; (4) a table of
    contents from the heading outline with links; (5) use the same layout for PDF export (63). Where: the canvas
    editor layout, export code. Priority: medium. Dependencies: 60 for the outline, 72, 77.
81. **Admin side.** Kind: fixable. What: a PII-free operator audit log, docstore backup and restore, quota dashboards
    and health pages. Why it matters: operators need to run and recover a store without seeing user data. Plan: (1) an
    audit event schema with an allowlist (no names, addresses, document ids or contents); (2) backup and restore of
    the encrypted store as opaque blobs, with a restore test; (3) quota and usage pages from counters (ties to the
    telemetry rules of 57); (4) health and readiness routes. Where: `docstore`, `internal/docstorecmd`, `docs`.
    Priority: medium. Dependencies: 57 for the privacy rules.
82. **Performance and size.** Kind: fixable. What: the showcase wasm is about 21.7 MB; streaming compile, code
    splitting, caching and a low-memory mode. Why it matters: slow first load excludes people on slow links and weak
    devices. Plan: (1) measure size and load per build and record a budget; (2) `WebAssembly.compileStreaming` with
    correct headers and compression; (3) split rarely used parts; (4) service-worker caching with versioned keys; (5)
    a low-memory mode that limits history and caches. Where: `examples/canvas-editor-showcase`, the build, `domrt`.
    Priority: medium. Dependencies: none.
83. **i18n depth.** Kind: fixable. What: only de, fr and es packs exist; add a translation workflow, pluralisation,
    locale-aware dates and numbers, and a contribution path. Why it matters: reach, and plain-language labels (65)
    need real translations. Plan: (1) message catalogue format with plural rules; (2) a check that finds missing and
    stale keys; (3) locale-aware formatting through one helper; (4) a short contributor guide; (5) right-to-left
    packs once 64 lands. Where: `i18n`, `docs`. Priority: low. Dependencies: 64 for RTL.
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
