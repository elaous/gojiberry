# Roadmap

What is planned beyond the [opendocs port plan](opendocs-port-plan.md) and the
[known limits plan](known-limits-plan.md). Ordered by what is needed next.

## Merges since the last full test run

0 of 5 (full run on `main` after the 5th merge in a row; see [testing-workflow.md](testing-workflow.md)).

## Notes for upgraders

- **Reserved document IDs.** IDs starting with `directory/` are reserved for the key directory
  (like `acl:` and `collab:`): `docstore.Server` refuses to read, write, list or delete them
  through `/docs`, so an existing document with such an ID becomes unreachable through the
  server. Rename it in the store before upgrading. The same holds for IDs starting with `public/`
  or `report/` (the public offer's records and reports, any case), and for a provider named
  `anon` in `GOJIBERRY_DOCSTORE_AUTH_PROVIDERS` (reserved for anonymous users, like `passkey`).
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
  whose storage fails reports `ErrFloorDegraded` (`encrypt.SetFloorDegradedHandler`; the showcase,
  once it uses the encrypting adapter, must set it or poll `StorageSeqFloor.Err()`). A client that reads or saves with a `StoreKeyring` or
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
      turned on): "Encrypt this store" in the Save tab (a local passkey through PRF, an SSH public key, a
      recovery key shown once; unlock state in memory only, a Lock button), lock marks and "Make
      (not) confidential" with a question for documents and folders, Reveal, sealed `.opendoc` packs
      (`encrypt.PackSealer` through `Session.PackKeys`), and the People tab's keys control. Also done
      there (PR #81 review): every change of the keys is a transaction with a journal (a failure puts everything
      back, a crash is finished or undone on the next load), a recovery key's secret is shown and confirmed before
      it is committed, a passkey alone needs an explicit acknowledgement, Lock closes the open document (and asks
      about unsaved edits), Erase the encrypted store for lost keys, removing a key and replacing a passkey, seals
      that make a replaced key list detectable at unlock, passkey failures told apart, the directory picker and
      "Publish my keys" behind `dirclient`, and placeholders drawn in the interface's language. Left: the real
      app (the owner signers must live out of reach of the store's writers: the seals cannot stop someone who
      strips them or puts back an older list, see section 5.7; a passkey from a server session rather than a
      local one), and the publish route's proof of possession.
   2. **Done:** a keyring that wraps a confidential document's files for people's keys only, and re-keys
      its older copies on the switch (`ConfidentialKeyring`, `Sharing.SetProtection`, `RekeyHook`).
   3. **Done (components and library):** the People-tab "my passkey" control with the second-key rule and
      the recovery key shown once, and the verified-e-mail mark with `encrypt.KeyDirectory` /
      `AdapterDirectory` (C.4); the server rule (the `directory/` prefix is reserved from `/docs`, records
      are signed by a configured directory key and written only by `POST /directory/publish`, which needs a
      provider session with a verified e-mail) and a demo host, `examples/people-keys-demo`; the showcase's People tab
      now has the same key controls, with the directory picker and a publish button when a docstore server is
      configured. Left: the directory in the real app, `PrefixLister` in the nested backends, revoking a
      published key.
   4. **Done:** hiding folder names of confidential folders (and the showcase's switch for them).
   5. **Downgrade and deleted-record detection: done** (library; C.5 and step 5.7 of
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
      into the showcase sign-in.
3. **Public docstore.** An anonymous offer: no account, one document per session, a link
   and a download always, deleted 24 hours after the last edit (viewing does not renew).
   Design, wording of the promise, and the abuse controls that must exist before launch:
   [public-docstore.md](public-docstore.md). Expiry in `docstore` is done (`ExpiringAdapter`: TTL policy, renewed by writes, sweeper, `X-Gojiberry-Expires`, `GOJIBERRY_DOCSTORE_EXPIRY_TTL`/`_SWEEP`/`_ADOPT`); **server side done** (`docstore/public`, `GOJIBERRY_DOCSTORE_PUBLIC=true`: anonymous sessions, one document per session, size/version/rate/daily-byte caps, view-only IDs, reports, takedown with tombstones, `Blocklist`/`Challenger`/`AdminGuard`/`SessionStore`/`Quota` hooks; see [public-docstore.md](public-docstore.md) section 7); hardened after review (per-client session cap with a 1 h idle TTL and an hourly issue rate, limits keyed by IPv6 /64, a global storage budget and report bound, `/docs` closed in public mode, request deadlines, charge-before-body with refunds, `/public/health`, takedown that bans after the records are swept; section 7.2 items 6-15); left: the legal terms text, real abuse operations (who reads reports), a captcha or proof of work behind `Challenger`, shared stores for replicas (the counters and limits are per process, section 7.5), the editor side (links, countdown, report link, creating the document within the idle hour); collaboration-hub edits renew a document (`hub.Config.Renewer`, `ExpiringAdapter.Touch`, `POST /docs/{id}/renew`, never for public sessions), a room whose document expired is closed with 410 after a confirming check, hub-only state is swept (`GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS=collab`, on by default in public mode) and `MaxAge` caps a document's life.
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

1. **A shared rate limiter** (`passkey.Limiter`): Redis or a gateway's decision, so replicas share
   one budget. Default today: a per-address token bucket in the process.
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
