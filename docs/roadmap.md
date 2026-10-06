# Roadmap

What is planned beyond the [opendocs port plan](opendocs-port-plan.md) and the
[known limits plan](known-limits-plan.md). Ordered by what is needed next.

## Next

1. **Migrate: done** (see [document-components.md](document-components.md)); left: skipping what an earlier run already brought (needs the source ID kept on the document), Google Docs comments and revision history.
2. **Button icons: done** (`components.Material`, see [document-components.md](document-components.md)); left: a toolbar of icon buttons for the formatting commands.
3. **Fresh review** of the grown PR before it merges.

## Later

1. **Download submenu.** "Download as" with a submenu: Docx, the other export
   formats, and *Open*, opendocs' own `.open` file (a document with its history and
   comments, for moving between opendocs installations). `docfile.Open` exists
   (protobuf, `.open`); what is left is the submenu and checking that versions and
   comments are fields of the document message.
2. **`.opendoc` packs.** Many documents and their folders in one file: framed
   protobuf records (the `.open` message), a footer index, a SHA-256 per document and
   an optional signature; opens from the end of the file, appends in place, recovers
   from a cut file. Spec: [opendoc-format.md](opendoc-format.md). Done: `docfile/pack`,
   `Saver.ImportPack` / `ExportPack` (with injected sealing for confidential documents), and
   `cmd/opendoc` with a `.docx` converter; left: the UI for packs (download a folder as
   `.opendoc`, import, convert in Migrate) and the opendoc README, to be offered as open source.
3. **Encryption and passkeys.** Encrypt documents to SSH keys and passkeys (envelope
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
   3. The People-tab control that adds "my passkey" as a key, and the verified-e-mail mark
      (C.4).
   4. Hiding folder names of confidential folders.
4. **Public docstore.** An anonymous offer: no account, one document per session, a link
   and a download always, deleted 24 hours after the last edit (viewing does not renew).
   Design, wording of the promise, and the abuse controls that must exist before launch:
   [public-docstore.md](public-docstore.md). Needs expiry in `docstore` first.
5. **Console mode.** A command line for work that is clumsy with a mouse: bulk
   rename, move and tag by pattern, import and export of a folder, repair and
   rebuild of a catalog. The operations already exist as `saver.Saver` methods; the
   work is a command grammar (`mv 'Report *' Archive/`, `tag +draft *.docx`), a
   dry-run mode that says what would change, and the confirm step for destructive
   ones. A `cmd/` CLI comes first; the same engine can then drive a panel in the
   editor.
6. **Terminal app demo.** A text-mode demo of the same engine (a file manager and a
   plain-text editor over `saver.Session`) to show the components are not tied to
   the browser. It reuses the console grammar above.
7. **Store backends with conditional saves.** SQLite, local files, memory and the
   HTTP store now save the catalog conditionally; PostgreSQL, MongoDB and S3 (which
   has `If-Match` writes) still write unconditionally and keep the old limit that two
   processes can lose a catalog update.
8. The rest of the [known limits plan](known-limits-plan.md): i18n breadth,
   accessibility depth, right-to-left, Hunspell dictionaries on demand.

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
