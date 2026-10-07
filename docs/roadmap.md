# Roadmap

What is planned beyond the [opendocs port plan](opendocs-port-plan.md) and the
[known limits plan](known-limits-plan.md). Ordered by what is needed next.

## Merges since the last full test run

5 of 10 since the last full run (#88, #89 which sets the tenth-merge rule, #90 `Sharing.RepairAll`, #91 the no-signer window, and #92 `RepairAll` with `Resign`; the full run on 40b6644 passed except the flake that #88 fixed; the full run is now every tenth merge, see [testing-workflow.md](testing-workflow.md)).

## Notes for upgraders

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
  default and nothing is required: `GOJIBERRY_DOCSTORE_LIMITER` (`memory`, the default, or `store`),
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
2. **A shared challenge store** (`passkey.ChallengeStore`): Redis or a database, with an atomic
   Take, so a ceremony begun on one replica finishes on another. Default today: memory.
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
   conditional writes), then returns a `RepairAllReport` (`Total`, `Signed` already v3, `Repaired`, `Files`,
   `Skipped` with a reason each: no signer, no key held, unreadable, forged, downgrade, plaintext, conflict; `Todo`
   for `DryRun`; `Remaining` when cut short). It never downgrades, deletes or uses `AcceptUnsigned`, is idempotent and
   restartable (no saved cursor: run it again and it starts at the first id; a document that failed mid-write may be
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
   is the signal that step 3 (remove the old key from every `OwnerSigners`) loses nothing it could still read.
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
6. **SSH key-tag collision tightening.** The code is `recipientID` in `docstore/encrypt/confidential.go`, which
   keys an SSH recipient by `st.Args[0]`, the 4-byte age short key tag. A collision fails closed (the recipient
   is taken for a server key: `ErrUncomparableRecipient` or the server-key outcome), never open. `age.Recipient`
   has no key accessor, so "compare the full key" means threading the key text through to where tags are matched.
   Plan: do that. Avoid: do not add two SSH keys with the same tag to one list.
7. **Shared `ChallengeStore` and Redis implementations for replicas.** A store-backed limiter already ships
   (`docstore.StoreLimiter`, `GOJIBERRY_DOCSTORE_LIMITER=store`). Plan: a shared `ChallengeStore` and Redis
   implementations behind the existing interfaces. Avoid: run one replica, or sticky routing.
8. **Concurrent double first-passkey UX (410).** Plan: tell the person the other tab won and offer to continue.
   Avoid: set up the first passkey in one tab.
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
26. **Committed binaries in history.** The largest blobs reachable from `origin/main`: `docsh` 54 MB, `collabhub`
    50 MB, `examples/gin-ssr-demo/gin-ssr-demo` 21 MB, `examples/echo-ssr-demo/echo-ssr-demo` 10 MB,
    `translateserver` 9.7 MB, `examples/collab-editor-app/server` 8.9 MB. Plan: needs a user decision, since
    removing them means rewriting history. Avoid: nothing is lost by leaving them.
