# Roadmap

What is planned beyond the [opendocs port plan](opendocs-port-plan.md) and the
[known limits plan](known-limits-plan.md). Ordered by what is needed next.

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
      provider session with a verified e-mail) and a demo host, `examples/people-keys-demo`. Left: wiring
      it in the showcase/app, a publish button, `PrefixLister` in the nested backends, revoking a published key.
   4. Hiding folder names of confidential folders.
3. **Public docstore.** An anonymous offer: no account, one document per session, a link
   and a download always, deleted 24 hours after the last edit (viewing does not renew).
   Design, wording of the promise, and the abuse controls that must exist before launch:
   [public-docstore.md](public-docstore.md). Needs expiry in `docstore` first.
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
