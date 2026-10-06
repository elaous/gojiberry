# Roadmap

What is planned beyond the [opendocs port plan](opendocs-port-plan.md) and the
[known limits plan](known-limits-plan.md). Ordered by what is needed next.

## Merges since the last full test run

0 of 5 (full run on `main` after the 5th merge in a row; see [testing-workflow.md](testing-workflow.md)).

## Notes for upgraders

- **Reserved document IDs.** IDs starting with `directory/` are reserved for the key directory
  (like `acl:` and `collab:`): `docstore.Server` refuses to read, write, list or delete them
  through `/docs`, so an existing document with such an ID becomes unreachable through the
  server. Rename it in the store before upgrading.
- **E-mail now needs an explicit `email_verified: true`.** `auth` used to treat an absent
  `email_verified` claim as verified; it now leaves `User.Email` empty unless the provider sent
  `true` (new `User.VerifiedEmail` says so). Access lists that match by e-mail stop matching users
  of a provider that omits the claim. If such a provider does verify every address, set
  `Provider.AssumeEmailVerified` (`"assumeEmailVerified": true` in
  `GOJIBERRY_DOCSTORE_AUTH_PROVIDERS`). The key directory publishes only for `VerifiedEmail` users
  and its lookup is closed to passkey sessions.

- **Key directory (this release).** New reserved ID prefixes `directory-log/` (the transparency log) and
  `ratelimit/` (shared rate limit budgets) join `directory/`: `docstore.Server` refuses them through `/docs`,
  so a document with such an ID becomes unreachable; rename it before upgrading. Everything new is off by
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
  `PrefixLister` (no schema change).

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
   1. The showcase wiring, which needs the key management UX (where the owner's key lives, how
      it is unlocked, the "make confidential" switch, the recovery key shown once, a lock mark
      in the document list).
   2. A keyring that wraps a confidential document's files for people's keys only, and re-keys
      its older copies on the switch.
   3. **Done (components and library):** the People-tab "my passkey" control with the second-key rule and
      the recovery key shown once, and the verified-e-mail mark with `encrypt.KeyDirectory` /
      `AdapterDirectory` (C.4); the server rule (the `directory/` prefix is reserved from `/docs`, records
      are signed by a configured directory key and written only by `POST /directory/publish`, which needs a
      provider session with a verified e-mail) and a demo host, `examples/people-keys-demo`. Also done:
      revoking (`DELETE /directory/keys/<fingerprint>`) and rotating the directory key (`KEY_PREV`, `Resign`),
      proof of possession and ssh-ed25519 attestation (`Held`/`Attested`, a stronger mark), a signed
      transparency log (`GET /directory/log`), shared rate limits (`docstore.StoreLimiter`,
      `GOJIBERRY_DOCSTORE_LIMITER=store`), lookup budgets and a lookup policy, and `PrefixLister` in the
      postgres, mongo, s3 and blob backends. Left: wiring it in the showcase/app, UI to publish, revoke and
      watch the log, a Go client for the routes, the owner-signed keyring that would close the substitution
      by a server that holds the key (see the limits in step 4b), witnesses for the log.
   4. Hiding folder names of confidential folders.
3. **Public docstore.** An anonymous offer: no account, one document per session, a link
   and a download always, deleted 24 hours after the last edit (viewing does not renew).
   Design, wording of the promise, and the abuse controls that must exist before launch:
   [public-docstore.md](public-docstore.md). Expiry in `docstore` is done (`ExpiringAdapter`: TTL policy, renewed by writes, sweeper, `X-Gojiberry-Expires`, `GOJIBERRY_DOCSTORE_EXPIRY_TTL`/`_SWEEP`/`_ADOPT`); left: collaboration-hub edits renewing a document, anonymous sessions, view-only links, size/rate limits, takedown and the terms.
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
