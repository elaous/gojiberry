# Encryption with SSH keys and passkeys; signing in with a passkey (design, draft 1)

Two features that share a vocabulary (public keys, who holds which private key) but
not code: **encrypting documents to keys** and **signing in with a passkey**.

## A. Encrypting documents

### A.1 Model

1. **Envelope encryption.** A document is encrypted with a random 256-bit **data key**
   (an AEAD: XChaCha20-Poly1305). The data key is **wrapped** (encrypted) once for each
   **recipient**: the owner, and anyone the document is shared with. Sharing is adding a
   wrapped copy; the document bytes are not re-encrypted.
2. **Recipients are public keys.** A recipient is `{label (an e-mail, a name), key
   type, public key, fingerprint}`. Supported keys: an **SSH key** (`ssh-ed25519`,
   `ssh-rsa`) and a **passkey** (see A.3). The e-mail is a *label*: it says who the key
   is meant for and matches the owner/ACL records already in the store, but it is not
   what makes the key trusted. Trust is the fingerprint, confirmed by the owner when
   adding a recipient.
3. **Format.** The envelope follows the shape of [age](https://age-encryption.org)
   (a header of recipient stanzas, then the encrypted payload), and the reference
   implementation uses `filippo.io/age` and `agessh` rather than new cryptography.
   The payload is a `.open` message, so everything above the envelope is unchanged.
4. **Where the key lives.** The private key never goes to a server. Encrypting and
   decrypting happen where the person is (the browser, in wasm, or the CLI); the store
   only ever holds ciphertext. The "private key for your docstore" is the owner's key:
   it is the recipient every document of that store is wrapped for, so the owner can
   always open their own.
5. **Revoking** removes a wrapped copy and **re-keys** the document (a new data key,
   re-encrypted, wrapped for who is left). That stops them getting new versions; nothing
   can take back what someone already decrypted, and the UI says so.

### A.2 Where it plugs in

1. `docstore.EncryptedAdapter` wraps any adapter: it encrypts on `Save` and decrypts on
   `Load`, with the keys from a `Keyring` the host supplies. Every backend gets it, and
   the saver, folders and versions work unchanged because they see plain `Adapter`
   calls.
2. **What stays readable.** The catalog (names, tags, folders, sizes) is what lists and
   searches a store, and stays readable except for confidential items (C.2).
3. **`.opendoc` packs.** A record's data may be an envelope (a record flag, see
   `opendoc-format.md`). The index hashes are of the stored bytes, so a pack's integrity and
   signature are checked **without** any key; a document is decrypted only when opened. The
   pack and the saver import no cryptography: sealing and opening are functions the host
   passes in (`PackOptions.Seal`, `ImportOptions.Open`).
4. **Sharing.** The People tab's Add/Remove gains a key: adding someone takes their
   public key (pasted `ssh-ed25519 …`, a `.pub` file, or a passkey enrolled by them) and
   the owner confirms the fingerprint.

### A.3 Passkeys as a key

A passkey does not hand out a private key to decrypt with: WebAuthn only *signs*. What
can be used is the **PRF extension** (`hmac-secret` on the authenticator), which gives a
secret derived from the passkey and a salt we choose. That secret is a **key-encryption
key** that wraps a key pair or the data key, so:

1. The passkey is a recipient through a stanza that holds the wrapped data key, the
   salt and the credential ID. Unlocking is one passkey prompt.
2. PRF is not available everywhere (it depends on the browser, the OS and the
   authenticator, and on whether a synced passkey keeps it). It is feature-detected;
   where absent, the person uses an SSH key.
3. **Losing the passkey loses the documents** unless another recipient exists. Adding
   a passkey as the only recipient is refused; the UI asks for a second one (a recovery
   SSH key, kept offline).

### A.4 What encryption costs

1. Anything the server does with content stops working on ciphertext: server-side
   search, the spelling and grammar servers, the translate server, exporting on the
   server, and the **collaboration hub** (it merges edits it can read). Encrypted
   documents are therefore single-user or use clients as the only mergers; a hub that
   relays encrypted operations is a separate project and is not promised here.
2. Metadata that is not encrypted (see A.2, option 1) still says who edited what and
   when.
3. Encryption is only as good as the endpoint: a compromised browser sees plaintext.

## B. Signing in with a passkey

1. **WebAuthn**, not a password: the server stores each person's credential (ID, public
   key, signature counter, transports, label, created) and verifies a signed challenge.
   Library: `github.com/go-webauthn/webauthn` (BSD), not hand-written verification.
2. **Package.** `auth/passkey`: `BeginRegistration`/`FinishRegistration`,
   `BeginLogin`/`FinishLogin`, a `CredentialStore` interface (memory and docstore-backed
   implementations), and handlers that sit beside the OIDC ones. The relying party ID
   and allowed origins are configuration (`opendocs.io`, not guessed from the request).
3. **One session format.** A successful passkey login issues the same bearer token as
   OIDC (the package's rule: one token format). The user is `passkey:{credential owner
   id}`, **with no e-mail**: the address typed when registering is stored on the account as a
   label (it names the passkey in the browser) but nothing has verified it, and the rest of the
   library (access lists, `User.Email`) treats an e-mail as verified, so putting it in the
   session would let anyone register as `victim@corp.com` and be matched by an ACL that names
   that address. Marking an address verified (a link mailed to it, then a signed marker on the
   account) is future work. Linking a passkey to an existing OIDC identity is done while
   signed in with that identity (the e-mail in the request body is ignored then), and the
   registration token can only be finished by the session that began it.
   Sign-in refuses a credential whose signature counter did not go up (`CloneWarning`: logged,
   401, the credential stays flagged: remove it and register again). A server keeps at most
   10,000 unfinished ceremonies (503 beyond), and the provider name `passkey` is reserved.
4. **Discoverable credentials** allow signing in without typing a username. A person can
   register several passkeys, list them and remove one (not the last, unless another way
   to sign in exists).
5. **UI.** `components.Passkey`: register, list, remove, "Sign in with a passkey"; the
   browser calls `navigator.credentials.create/get` through `syscall/js`.
6. **Tests.** The server side against recorded ceremonies; the browser side with
   Chrome's virtual authenticator (the CDP WebAuthn domain), which `internal/e2etest`
   can drive. Replay is covered by the signature counter and one-time challenges.

### B.1 Extension points

Everything a second replica or a larger deployment needs to share is an interface, with a
default that works in one process. Pass your own in `passkey.Config` (or the store you give
`passkey.New`):

| Interface | What it does | Default | Plug in |
|---|---|---|---|
| `Store` (+ optional `Updater`, `Creator`) | accounts and credentials; `Updater` changes one account atomically (the function may run again on a conflict), `Creator` makes one only if none exists | `MemoryStore`; `AdapterStore` (compare-and-set over a `docstore.ConditionalAdapter`, else this process's lock) | SQL tables, anything with a transaction or conditional write |
| `ChallengeStore` | started ceremonies; `Take` must be atomic so a challenge is good once | `MemoryChallenges` (10,000 at most, swept every 5 s) | Redis `GETDEL`, a table with a delete-returning |
| `Limiter` | per client and route group: allowed now, or wait this long | `RateLimiter` (token bucket per address and group: 20 at once, 30 a minute) | Redis, a gateway; `Unlimited{}` to switch off |
| `Config.ClientIP` | who the client is for the limiter | the remote address; `TrustedProxies(cidrs...)` reads `X-Forwarded-For` only from your proxies | your own function |

Over the limit the routes answer 429 with `Retry-After`. `cmd/docstore` reads
`GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES` (comma separated CIDRs) for the last row.
A store without `Updater`/`Creator` still works in one process; behind several replicas it
needs them (a conditional write), or two sign-ins can overwrite each other's counter update.

## C. Decisions

1. **Both modes, chosen per document or folder: a protection level.** Convention (the
   way Google Drive and Dropbox differ from Proton Drive and Tresorit, in one product):
   - **Everything is encrypted at rest** with a key the server holds (`EncryptedAdapter`
     with a server keyring). This protects disks, backups and copies of the store; it
     does not protect against whoever runs the server. Search, spelling, translation and
     collaboration work.
   - **Confidential** documents and folders are **end to end**: the data key is wrapped
     only for people's keys; the server holds ciphertext. No server-side features and no
     live collaboration for them (A.4).
   - The level is a field of the document's envelope and the catalog (`protection`:
     `server` or `e2ee`), not something inferred from the name.
2. **The catalog stays readable, except for confidential items, and confidentiality is
   a setting, never a word in a name.** A marker in a name defeats itself: the name is in
   the catalog, so the server has already read it. A document or folder is made
   confidential with the protection level (a switch in the UI and the `protection` field).
   Doing so moves its real name, tags and folder names inside the envelope, and the
   catalog holds a placeholder ("Confidential document") that the client replaces after
   decrypting. Sizes, times and the folder tree's shape stay visible. There is no
   `[CONFIDENTIAL]` shortcut: names are never interpreted.
3. **A passkey is never the only way to unlock an end-to-end document.** Making one
   confidential requires a second recipient: a recovery key generated in the browser
   and shown once to download (or an SSH key). Exception: a document marked disposable
   (the public store, `public-docstore.md`) may skip it, with the warning. Passkeys for
   **signing in** are different (B): losing one costs a login, not data, so they only
   need another way back in (another passkey, an OIDC identity or a recovery e-mail).
4. **A recipient's e-mail is "verified" when it is the verified e-mail of an account on
   the store** and the key was registered by that account while signed in. Picking
   people from the store's directory shows a verified mark and their registered keys; a
   pasted key for an address with no account stays "label only" and the owner confirms
   its fingerprint. Verified e-mail proves the account, and the key was published by it;
   it does not prove the key is not the account's compromised one.

5. **MFA: no separate second factor.** A passkey with user verification (required by default) is
   already two factors in one: the device and a PIN or biometric. We do not add TOTP or SMS codes
   (weaker than the passkey they would guard). For OIDC sign-in, MFA is the provider's policy. Left,
   and not built: an optional **step-up** (a fresh passkey assertion within a few minutes) before
   sensitive actions: removing a key, purging, sharing.
6. **Removing a key warns and re-encrypts.** The warning names how many documents use the key and
   that anything already opened or copied cannot be taken back. Confirming decrypts and encrypts
   again, without that key, every document that used it (`Sharing.Usage` counts them, `Sharing.RemoveEverywhere` does it; `components.Recipients` shows "opens N documents" and a "Remove from all N documents" button with that warning). A failure stops it naming the document, and running it again finishes the rest.

### C.5 Who may change a recipient list

The `keys:<ID>` record decides who a document is encrypted to, so it is authenticated, not just
stored. Design:

- Each record is `{payload, sig}`: the payload is JSON (`v`, the document ID, a growing `seq`, a
  `pending` flag, the recipients), and `sig` is an **Ed25519 signature** over a fixed context, the
  document ID and the payload bytes. The ID in the signed message means a valid record cannot be
  moved to another document.
- The signing key is the owner's: `encrypt.SignerFromAge` derives it from an age identity (HMAC of a
  fixed context under the identity's secret, so a PRF-derived or recovery identity works),
  `SignerFromSSH` uses an ed25519 SSH key. Every owner key that may change lists has its public
  signing key in `StoreKeyring.OwnerSigners` (and `Sharing.OwnerSigners`) of whoever reads them.
  `Sharing` needs the `Signer` to Add or Remove (`ErrNoSigner` without it).
- `StoreKeyring.Recipients` (what every save asks) verifies the record and **fails closed**: an
  unsigned record (`ErrUnsignedRecord`), a record signed by a key that is not an owner's, or a
  changed or moved one (`ErrBadSignature`) makes the save fail, never a longer or shorter list.
  Fingerprints and types are recomputed from the stored key text, not read from the record.

Threat model, store write access only (no owner key):
- **Cannot** add themselves, or anyone, as a recipient of future saves: the record they write does
  not verify, and saves fail until the owner repairs it.
- **Cannot** edit a signed list to drop a recipient or change a key.
- **Can** delete a record (the document then reads as shared with no one besides the owner: the
  owner can still open everything, and the people it was shared with lose access to later saves,
  silently). Detecting this needs owner-held state (a signed index of which documents have lists);
  not built. `Sharing` keeps an empty signed record rather than deleting when the last key goes,
  but a reader cannot tell a deleted record from a document that was never shared.
- **Can** put back an older signed record (for example one that still lists a person the owner
  removed), and future saves then go to them. `seq` is in the signed payload so a client that
  remembers the highest it has seen per document can refuse a lower one; no client does yet.
- **Can** still delete or replace files (availability), and read the catalog and the recipient
  lists: fingerprints, labels and public keys are not secret.
Someone who holds an owner key (it opens the documents anyway) can change lists.

Re-keying after a change is crash-safe: the list is saved first with `pending` set, then every file
of the document is encrypted again, and `pending` is cleared only when all of them are. `Remove` of a
key that is already gone from the list finishes a pending re-key (it is `ErrNotFound` only when
nothing is pending), `Add` of a listed key encrypts again, and `Sharing.Repair` does the same
without naming a key. `Adapter.RekeyDocument` opens all the files first and writes none if one
cannot be opened.

## D. Plan

1. **Done:** `docstore/encrypt` (`encrypt.Adapter`, `Keyring`, `Static`) using `filippo.io/age`
   and `agessh`: SSH (ed25519, RSA), age and recovery-key recipients, `Rekey` and `RekeyAll`,
   strict reads by default (a file without the age header is refused; `AllowPlaintext` is the opt-out for migrating a clear store), ciphertext bound to its id by a framed header (`ErrWrongDocument`), `SaveIf` on encrypted ids compared against the plaintext, serialized and conditional re-keys that continue past failures, catalogs / folder records / ACL records / fonts left readable
   (`DefaultSkip`), conditional catalog writes and the store lock passed through. Tests: round
   trip, wrong key, tampered ciphertext, sharing and re-key on revoke, a recovery key, bad keys,
   and the whole saver (folders, versions, `Verify`) on an encrypted store. It is its own
   package, so a build that does not import it does not link `age` (about 3.8 MB of wasm when it
   does).
2. **Done (library and component):** `encrypt.Sharing` (`Add` needs the fingerprint the owner
   confirmed, `Remove`, `List`) over `encrypt.StoreKeyring`, which keeps each document's
   recipients in a readable, **owner-signed** `keys:<ID>` record (public keys only, see "Who may
   change a recipient list" below) beside the owner's keys; every
   file of the document (its copies and versions) is encrypted again to the new list
   (`Adapter.RekeyDocument`). `components.Recipients` is the People tab's control: the owner's keys,
   the shared keys with Remove (which says it cannot take back what was already opened), and a form
   that parses a pasted key, shows its type, label and fingerprint, and keeps Add off until the
   fingerprint is typed. *Left:* wiring it into an application (the showcase has no encrypted store
   yet: it needs the key management of steps 3 and 4, where the owner's key lives and how it is
   unlocked) and the "verified e-mail" mark of C.4 (done, see step 4b).
3. **Done:** `auth/passkey` (go-webauthn; discoverable credentials, user verification required,
   one-time challenges with a TTL, `Store` with `MemoryStore` and `AdapterStore`), the
   `components.Passkey` control, `Server.WithRoutes`/`Hide` on the docstore server (`cmd/docstore`
   mounts `/auth/passkey/` when `GOJIBERRY_DOCSTORE_PASSKEY_RPID`, `_ORIGINS` and `_NAME` are set and
   hides the `passkey/` records from the API), `auth.Manager.StartSession` and a provider-less
   manager (a passkey-only deployment). `examples/passkey-demo` has the e2e against Chrome's virtual
   authenticator. Linking to an OIDC identity: a passkey registered while signed in uses that
   identity's ID, so both ways reach the same account.
4. **Done (library, browser client, e2e):** a passkey as an encryption recipient through the WebAuthn PRF
   extension. `encrypt.PRFSalt`, `PRFIdentity` (HKDF of the 32 PRF bytes into an age X25519 identity),
   `PRFRecipient` (type `passkey-prf`, the credential ID kept as the `Hint` of which passkey to ask),
   `CheckRecoverableOwner(owner, shared)` (`ErrNeedSecondRecipient` unless one of the **owner's** keys is
   not a passkey: a recovery key must be one the owner controls, not someone else's; two passkeys are not
   enough, they can be lost together; `CheckRecoverable(keys...)` counts whatever it is given, so prefer
   the owner form). Known-answer tests pin the derivation (fixed PRF output to the exact
   `AGE-SECRET-KEY-1...` and `age1...` strings, and the encoding checked against the age library and
   `crypto/ecdh`), because changing it would lock people out. The secret is the root of the key: callers
   `clear(secret)` after `PRFIdentity`/`PRFRecipient` (the demo does), and `DeriveKey` zeroes the browser's
   buffer once copied. The credential ID kept as the `Hint` is readable in the signed recipient list: it
   shows that a passkey is in use and which, and links documents that share it (leave it empty to avoid
   that, at the cost of the browser asking which passkey). Registration asks for PRF
   (`WithExtensionPRFSupport`), and `client.DeriveKey` asks a passkey for the bytes (`ErrNoPRF` when the
   authenticator does not do PRF: passkeys made before this, some security keys and password managers;
   `ErrPRFLength` when it answers with the wrong size).
   The e2e derives the same key twice in Chrome.
   **Done (component):** `components.Recipients` has the "Add my passkey as a key" control
   (`AddMyPasskey func(ctx) error`: the host calls `client.DeriveKey`, `encrypt.PRFRecipient`, clears the
   secret and calls `Sharing.Add`; `PasskeySupported` false shows a note instead of the button; the
   owner's own key needs no typed fingerprint, and `MyPasskeyAdded` shows the fingerprint that was added).
   For a `Confidential` document whose owner keys are all passkeys the button is off and the control shows
   `SecondKeyMessage` (the host passes `encrypt.ErrNeedSecondRecipient.Error()`, the same rule as
   `CheckRecoverableOwner`) with "Create a recovery key": `CreateRecoveryKey` generates one
   (`encrypt.GenerateRecoveryKey`) and adds its public part, and the secret is shown **once**, copyable and
   downloadable, with "keep it offline; we cannot recover it" until the person says they saved it; the
   component never keeps it. *Left:* the showcase does not supply these callbacks yet (`examples/people-keys-demo` does, see 4b);
   calling `CheckRecoverableOwner` when a document is made confidential is step 5.
   4b. **Done (library, server rule, component and a demo host): the verified e-mail mark of C.4.**
   `encrypt.KeyDirectory` (`Lookup(ctx, email) ([]PublishedKey, error)`; a `PublishedKey` is the key
   text, label, type, fingerprint and `Verified`) is injected; `encrypt.AdapterDirectory` is one over a
   `docstore.Adapter`.
   - **Records are signed.** `Publish(ctx, user, key)` refuses (`ErrNotPublishable`) unless `user.Email`
     is set and the user came from a provider session (a `passkey:` user carries no e-mail, B.3), and
     writes `directory/<HMAC of the e-mail>/<fingerprint>` holding the key, label, the publisher's ID and
     provider, the address and the time, signed by the server's `encrypt.DirectorySigner` (Ed25519; the
     signature covers the record's own ID, so a record copied to another name fails). `Lookup`
     recomputes type and fingerprint from the key text and sets `Verified` only when the signature
     verifies for that record and address and the publisher is a provider user; any other record is
     returned unverified ("label only") and an unreadable one is skipped. The record's name is a keyed
     hash, so addresses cannot be guessed or enumerated from the store's IDs. At most 20 keys per
     address (`MaxKeys`). `Lookup` lists only the address's prefix (`docstore.PrefixLister`, which the
     memory, localfs and sqlite adapters implement; `docstore.ListPrefix` falls back to `List` for an
     adapter without it).
   - **The key is configuration.** `GOJIBERRY_DOCSTORE_DIRECTORY_KEY` is a 32-byte seed in hex
     (`DirectorySignerFromEnv`; also the HMAC key, derived with a separate label). Unset, the directory is
     off: no routes are mounted, `Publish` returns `ErrDirectoryOff`. A key per process is not generated:
     records must verify after a restart. Changing the key unverifies and hides every record.
   - **The server rule.** The `directory/` prefix is reserved in `docstore.Server` like `acl:` and
     `collab:`: it cannot be read, written, listed or deleted through `/docs`. Records are written only
     by the publish route. With an `auth.Manager` and `Server.WithDirectory(encrypt.DirectoryService(dir))`
     (the docstore server does it when the key is set) the server mounts `POST /directory/publish` (body
     `{"key", "label"}`, at most 4 KB) and `GET /directory/lookup?email=`. Both need a signed-in session;
     publishing needs a provider session with a verified e-mail (`User.Email`, which auth leaves empty for
     an unverified one) and publishes under that address only: a different `email` in the body is
     refused (403), and a passkey session is refused. Each user is limited to 10 publishes and 60
     lookups a minute (429 with `Retry-After`).
   - **UI.** `RecipientRow.Verified` shows "verified e-mail" and a draft that is not shows "label only:
     confirm the fingerprint"; `Recipients` can look an address up (`Directory`, `OnDirResults`,
     `DirResults`), and picking a row fills the draft. The typed fingerprint stays for label-only keys,
     and is skipped for a verified one only when the host sets `TrustVerified`. Verified e-mail proves the
     account, and that the account published the key; it does not prove the key is safe.
   - **Host.** `examples/people-keys-demo` implements `AddMyPasskey` (`client.DeriveKey`,
     `encrypt.PRFRecipient`, `Sharing.Add`, no typing), `CreateRecoveryKey` (shown once) and `Directory`
     (`/directory/lookup`), and sets `Parsed.Verified` and the shared rows' mark from the directory's
     answer for that fingerprint, never from a pasted key. Its e2e (headless Chrome, virtual authenticator
     with PRF) covers the second-key message, the recovery key shown once, add my passkey, and the
     verified mark from a lookup.
   *Left:* the showcase and the real app do not wire these controls yet (they need the key-management
   UX of step 5.1); no UI publishes the owner's keys (the route exists and the demo's test calls it); the
   rate limits are per process (a gateway or a shared limiter for several replicas); the nested backends
   (postgres, mongo, s3, blob) and the browser adapters do not implement `PrefixLister` yet (they fall
   back to listing the store); revoking a published key (a `DELETE` route) and rotating the directory
   key are not built; whoever holds the directory key can publish anything.

5. **Done (library):** encrypted records in packs and placeholder catalog entries for confidential
   documents.
   1. `docfile/pack`: a record flag and an index field for an encrypted record, whose data is an
      opaque envelope (`Writer.AddEncrypted`, `Entry.Encrypted`, `Reader.Read` gives
      `ErrEncrypted`, `Reader.ReadWith(id, open)`); hashes and signature are of the stored bytes,
      so `Verify`, `Recover` and `Compact` need no key. Format backward compatible.
   2. `saver`: a protection level on catalog entries (`server`, the default, or `e2ee`) and, for
      the latter, `saver.ConfidentialName` as the name and no tags; the real ones are inside the
      document (`docfile.Document.Protection`, the meta key `protection`, also marks it so that
      a rebuild keeps the level). `Saver.Reveal` reads them (the document must be openable),
      `Saver.SetProtection` moves name and tags in or out, calling the injected
      `Saver.ConfidentialCheck` before enabling it; `encrypt.ConfidentialCheck(sharing,
      owners...)` is the hook that refuses with `ErrNeedSecondRecipient` (C.3). `Session` shows
      the placeholder, and the real name and tags only after a successful open, save or reveal
      (so searches and tag filters over the list see nothing of an unopened document).
      Trash, versions, folders, copies and archives keep working; a copy of a confidential
      document is confidential.
   3. A rebuild (`Rebuild`, `Repair`, a lost catalog) reads each document: one that carries the
      mark, or that the old catalog listed as `e2ee`, is listed under the placeholder with no
      tags. A file no key here opens (the adapter's error wraps `docstore.ErrLocked`;
      `encrypt.ErrNoKey` does) is **not read**: it is listed as confidential under the
      placeholder, the old catalog's folder, trash flag and level kept if there was one, else
      the top level and not trashed (they live inside the file). `Verify` counts such documents
      as `Locked` and leaves them untouched.
   4. `Saver.ExportPack` / `ImportPack` (one API, `opendoc-format.md` §7.2, with its
      `PackOptions`, `ImportOptions` and `ImportReport`): `PackOptions.Seal` and
      `ImportOptions.Open` are injected. On export a confidential document is sealed into an
      encrypted record (the index lists `ConfidentialName` and no tags; name and tags are inside
      the envelope); with no `Seal` it is left out and reported to `PackOptions.Warn`, never
      written in the clear, and a `Seal` that fails aborts the export. On import a record with
      no working `Open` is skipped with a needs-key warning (`ImportReport.NeedsKey`,
      `Warnings`) and the rest come in; one that is opened is saved confidential again, through
      `Saver.ConfidentialCheck` (a refusal is that document's `Failed`), under any
      `ConflictPolicy` (Replace turns a server document into a confidential one in a single end
      to end save: the check runs before anything is overwritten, a refusal leaves the
      existing document as it was and is that document's `Failed`).

   **Warning: `SetProtection(E2EE)` does not erase the past.** The previous generation of the
   document (`prev`) and its version snapshots still hold the pre-switch plaintext, with the
   real name and tags, under the wrapping they had, until the store's keyring re-keys them
   (`Sharing.Repair`). Until then, whoever could read those copies still can. `ImportPack`
   with Replace writes the confidential document in one step (the check runs first, and the
   record is never stored as an ordinary document), but the copies of the document it
   replaced are the same case.

   *Left:*
   1. The saver records the level but does not choose the keys: which recipients a confidential
      document's files are wrapped for (people's keys only, not a server key) is the store's
      keyring, and re-keying copies and versions written before the switch is `Sharing.Repair`.
      A keyring that does this per protection level is not built.
   2. The showcase wiring and the key management UX (where the owner's key lives, the "make
      confidential" switch, the recovery key shown once, the lock mark in the document list).
   3. Folder names are not hidden (they live in the catalog and the folder records); only
      documents are.
   4. A saved document keeps its older copies readable to whoever had access to them: the
      switch changes the catalog and the document, not what was already stored (see 1).
   5. Key management for `Seal`/`Open` in the pack UI ("As .opendoc", "Import .opendoc"): until
      then `Session.DownloadPack` leaves confidential documents out and says how many, and
      `Session.ImportPackData` reports the ones it could not open.
