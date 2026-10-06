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
  `pending` flag, the document's `protection` level, the recipients), and `sig` is an **Ed25519 signature** over a fixed context, the
  document ID and the payload bytes. The ID in the signed message means a valid record cannot be
  moved to another document.
- The signing key is the owner's: `encrypt.SignerFromAge` derives it from an age identity (HMAC of a
  fixed context under the identity's secret, so a PRF-derived or recovery identity works),
  `SignerFromSSH` uses an ed25519 SSH key. Every owner key that may change lists has its public
  signing key in `StoreKeyring.OwnerSigners` (and `Sharing.OwnerSigners`) of whoever reads them.
  `Sharing` needs the `Signer` to Add or Remove (`ErrNoSigner` without it).
  **Revoking an owner signer.** A signer stays trusted for as long as its public key is in a
  reader's `OwnerSigners` (`StoreKeyring`, `Sharing`, `Adapter`): there is no revocation list, and
  a key that is revoked in any other sense (lost, left the organization) can still write records
  and frames that verify, everywhere it is still listed. Removal is removing it from every one of
  those lists, on every client; nothing else switches it off. Frames carry no key id (a reader
  tries each listed key), so there is nothing per file to change, and the procedure is by
  re-signing first: (1) add the new signer to the `OwnerSigners` of every client, beside the
  old; (2) with the new `Signer` set as `Sharing.Signer` and `Adapter.Signer`, run
  `Sharing.Recover(doc, level, shared)` for each document (the level and the list from
  `Sharing.List`/the index), which writes a new record signed by it and re-keys every file with
  frames it signs, then `Sharing.VerifyIndex`; (3) remove the old key from the lists. After that
  anything the old key signed and was not re-signed (a document missed in step 2, a stray old
  file) is refused (`ErrBadSignature` for a record, `ErrForgedFrame` for a frame), which is also how
  a missed one is found: run `Saver.Verify` and `Sharing.PendingDocs`. Until step 3 is done
  everywhere, the old key's holder can still write; a replay of anything the old key signed before
  step 3 is refused after it, not before.
- `StoreKeyring.Recipients` (what every save asks) verifies the record and **fails closed**: an
  unsigned record (`ErrUnsignedRecord`), a record signed by a key that is not an owner's, or a
  changed or moved one (`ErrBadSignature`) makes the save fail, never a longer or shorter list.
  Fingerprints and types are recomputed from the stored key text, not read from the record.

Threat model, store write access only (no owner key):
- **Cannot** add themselves, or anyone, as a recipient of future saves: the record they write does
  not verify, and saves fail until the owner repairs it.
- **Cannot** edit a signed list to drop a recipient or change a key.
- **Can** delete a record, or put back an older validly signed one (a replay: for example one that
  still lists a person the owner removed, or one from before the document was made confidential,
  whose `protection` is empty). A signature cannot tell either from the current record, and the
  keyring on its own would then wrap later saves for the server (a deleted or replayed
  server-level record) or for a removed person. What stops it, in layers (the first keeps no state on
  the client; it does need the client to be configured with the owners' public signing keys,
  `OwnerSigners`, which is configuration and not something it remembers):
  1. **The level and the record's seq are inside the encrypted frame, and the frame is signed.**
     Every file is framed inside the encryption: frame v3 is `GJE\x03`, the stored id, a level byte
     (server / e2ee), the `seq` of the signed record the file was sealed under, a hash of that
     record's recipient list, the SHA-256 of the data, and an **Ed25519 signature by an owner signer
     over all of that**, then the data (v2, `GJE\x02`, is the same without the hash and signature).
     **age has no sender authentication.** Anyone who knows a recipient's *public* key (an SSH key
     on a profile, the server's key) can mint a file that opens for that recipient, with any
     content and any frame, so "the AEAD authenticates the frame" proves nothing about who wrote
     it. The signature does: `Adapter.Signer` signs every file it writes for a document with a
     record, the readers verify against `StoreKeyring.OwnerSigners` (plus `Adapter.OwnerSigners` and
     the adapter's own signer), and a v3 frame whose signature is missing, is by no owner signer,
     or does not match the content it came with (a genuine header put on other content: the
     content hash is signed) is **`ErrForgedFrame`**, which is an `ErrDowngrade`-class failure
     (`errors.Is(err, docstore.ErrDowngrade)`): no data is returned and nothing is written over it.
     So a writer who knows only public keys can no longer forge a frame, a level, a seq or content
     of a document an owner signer wrote. A confidential document is not saved without the signer
     (`ErrNoFrameSigner`, also from the re-keys): `Adapter.Save` fails closed instead of writing an
     unsigned frame. The header is signed (not just the content) so the checks that scan a document's
     files verify it from the first chunk of each file without reading the rest.
     The adapter (`encrypt.Adapter`, with a `Binder` keyring: `StoreKeyring` and
     `ConfidentialKeyring`) cross-checks a file with the record read for the same operation, with
     `Fs`/`Fl` the seq and level in the file and `Rs`/`Rl`/`Rp` the record's seq, level and pending flag:
     - **no record** but the file says `Fs > 0` or e2ee: the record was deleted: `ErrDowngrade`;
     - `Fs > Rs`: the record is older than one the file was sealed under, a replay: `ErrDowngrade`;
     - `Fs == Rs`: level and recipient hash must agree, else `ErrDowngrade`;
     - `Fs < Rs` is normal (the record's seq only rises, and clearing `pending` raises it again after
       every file was sealed), except on **Load**, where data would be returned under the wrong
       level: a file wrapped at a lower level than the record's with `Rp` false (an old server-level
       ciphertext put back into an e2ee document) is `ErrDowngrade`. While `Rp` is set the files the
       re-key has not reached are exactly such files, so it is tolerated: this is how a switch cut
       short, and the saves and loads in its window, never read as a downgrade. A file wrapped
       above the record's level (people only, in a document the record calls server) is the safe
       direction and accepted.
     Where it runs: `Adapter.Save` and `SaveIf` look at **every** stored file of the document (its
     generations `<ID>~...`, or a confidential folder's `folder-secret/<ID>`; one prefix listing, and
     for each file one read whose first chunk is all that is decrypted) and check each against the
     record the recipients are about to come from **before sealing**: a deleted, replayed or older
     record is `ErrDowngrade` and **nothing is written**. What counts as evidence and what stops the
     operation is exact, because the store reports the times and order of its files and both are
     the store writer's to choose:
     - a file that opens and verifies is evidence, whatever its time: the check does not look at the
       "newest" file, it checks all of them (so the highest seq among them is the one that counts);
     - a file that is **provably someone else's** (a well-formed age file none of our keys matches,
       `ErrNoKey`) or is gone is skipped: it says nothing, and it cannot hide the files that do;
     - a file that is forged (`ErrForgedFrame`) fails the operation with that error;
     - any other file that cannot be opened (the age header but malformed or damaged, a file that
       opens with a key of ours and says something else, not an age file at all unless
       `AllowPlaintext`) is **`ErrUnreadable`**, naming the file, and stops the operation: it could be
       hiding what the record should say, and "decoy" files like this (three junk files that start
       with the age header, planted next to a deleted record) used to make a save succeed. Restore
       the file or delete it (a version snapshot: `Saver.DropVersions`; a generation is deleted in
       the store; `Sharing.Recover` does not need this check, but see below: it re-keys every file);
       **which operation a junk or unreadable file blocks depends on where it is**:

       | file | `Save`/`SaveIf` | change to the list (the check before the record is written) | re-keys (`Add`, `Remove`, `SetProtection`, `Repair`, `Recover` finish by one) | `highestSeq` (`Recover`) | `Load` |
       |---|---|---|---|---|---|
       | generation `<ID>~...`, or `folder-secret/<ID>` | blocks | blocks | blocks | skipped | only if it is the file read |
       | version snapshot `versions/<ID>/...` | **not scanned** | **not scanned** | blocks (all or none) | skipped | only if it is the file read |
       | provably foreign (`ErrNoKey`) | skipped | skipped | blocks (it cannot be sealed again) | skipped | only if it is the file read |

       So a junk snapshot does not stop saving or loading the document, but it stops every re-key,
       and an `Add`, `Remove` or `SetProtection` then has already written the record (pending) when
       it stops: `Repair` after dropping the snapshot (`Saver.DropVersions`) finishes it;
     - a document none of whose files opens (none stored, or all provably foreign) has no evidence and
       passes: what the record says is all there is.
     The cost is one read and one first-chunk decryption per file of the document (generations, not
     version snapshots, which only the re-keys and the scan for the highest seq open) on each save.
     **A client that raced a switch is not told it saw a downgrade.** `Save` reads the record and
     then the files; if another client completed a switch in between, the files are sealed under a
     newer record than the one just read. A downgrade finding is therefore made again once with the
     record (and for `Load` the file) read again; a record that is really missing or replaced is the
     same the second time. `Sharing`'s changes do the same. `Load` checks the file it returns (one extra
     record read). Every re-key (`Rekey`, `RekeyDocument`, `RekeyAll`, `SetProtection`, `Repair`) checks
     each file as it opens it, so a re-key never seals the document under a replayed or missing record;
     and `Sharing` refuses to change a record (`Add`, `Remove`, `SetProtection`, the clearing of
     `pending`) that is missing or older than its files say, so the owner's own client never signs over
     a replay. `SetProtection` order is unchanged and is what makes the rules above hold: the record is
     written first (seq N+1, pending), then the re-key writes frames at N+1, then `pending` is cleared
     (N+2); a retry after a failure at any file finds the record pending and finishes. The message
     says which case it was (deleted, older, wrong level) and how to go on: `Sharing.Recover` when
     the record was lost or replaced and the owner knows what it should say, `Sharing.Repair` when
     a file was left wrapped below the document's level by a switch that was cut short; the saver
     returns that text as it is (`Save`, `SetProtection`, `Load`, `Reveal`), and `Verify` lists the
     document in `VerifyReport.Downgraded`.
     **A stuck `pending` flag.** While `pending` is set, a file wrapped below the document's level
     is tolerated on `Load` (it is where the re-key has not reached yet), so a flag that stays set
     switches that one check off for the document. The record says since when (`pendingSince`, the
     clock of the client that wrote it: a client whose clock is behind or ahead of the one that
     checks makes "since when" and "stuck" off by that skew, so a flag can look younger or older than
     it is; `PendingWarnAfter` is a warning threshold, not a guarantee);
     `Sharing.PendingStatus` and `Sharing.PendingDocs` report it, and a flag older than
     `Sharing.PendingWarnAfter` (30 minutes by default) is *stuck*. `Saver.PendingCheck`
     (`encrypt.PendingHook`) puts them in `VerifyReport.Pending` and `PendingStuck`: the owner runs
     `Sharing.Repair`. Once it is not pending the same file is refused with an error that names
     `Repair`.
     **Which files may be unsigned.** Files written before frame v3 are read as before (v1: no level,
     no check; v2: level and seq, unsigned), with a warning logged once, and **until they are
     upgraded they can be forged by anyone who knows a recipient's public key**: that is the one
     place the claim above does not hold, stated plainly. The first save or re-key of a file writes
     v3; when a re-key by a signing adapter finishes (`Sharing.Repair(doc)` does it for a document
     that has nothing pending, `Add`, `Remove`, `SetProtection` and `Recover` as they finish), the
     record says `signed`: from then on **every unsigned frame in that document is `ErrForgedFrame`**
     (so the forger cannot choose the older format), and whoever saves it must sign.
     `Adapter.RequireSigned` makes an adapter refuse unsigned frames for every document id the
     keyring binds (every id `DocID` finds with a `Binder` keyring, whether or not its record
     exists; ids it does not bind are not checked), for a deployment whose files were all upgraded.
     **The window before `signed`, and the way out of it.** A re-key writes signed files and only
     then changes the record to say `signed`. A client with no `Signer` that read the record before
     that can save an unsigned (v2) file of a server-level document in between, and a plain `Save`
     does not change the record's seq, so the change to `signed` cannot be made conditional on it.
     Two things handle it. (a) Immediately before setting `signed`, the re-key looks at every file
     again; if one is not v3 (it was saved meanwhile) it is sealed again (another pass) first.
     That leaves only the few steps between that look and the write of the record. (b) What a save
     in that remaining window (or an older build that wrote v1/v2 frames under a record that already
     says `signed`) leaves is a document where `crossCheck` refuses the file as `ErrForgedFrame`
     and `Save`, `Load`, `Repair` and `Recover` all refuse, so there is an explicit way out:
     `Sharing.Repair(doc, encrypt.AcceptUnsigned())` (and `Sharing.Recover(..., encrypt.AcceptUnsigned())`)
     opens the unsigned files, still checks their level, seq and recipients against the record, and
     seals them again signed, with the owner's `Adapter.Signer` (`ErrNoFrameSigner` without it).
     Without the option they refuse, with an error that names the file and the option. The option
     is for **unsigned frames only**: a v3 frame whose signature is not an owner's or does not match
     its content is a forgery and is `ErrForgedFrame` with or without it, never sealed again. It
     cannot tell an unsigned file the owner's own client wrote from one minted by someone who
     knows a recipient's public key (an unsigned frame carries no proof), so the owner uses it
     after checking that the file is theirs (what it holds, who could have written it): accepting
     it is the owner vouching for it, which is the limit of the old format and no more.
  2. **The owner-signed index** (`keys:index`, `encrypt.IndexKey`): `{seq, docs: {id: {level, seq}}}`,
     signed by the owner signer under its own context, listing the end-to-end documents and folders and
     the record seq when each was switched; `SetProtection` writes it after the record (and again on
     a retry; removing the entry when a document goes back to server). `Sharing.VerifyIndex` compares it
     with the records of every document it lists, **including ones never opened on this client**: a
     missing record, one that does not verify, an older one, or one that disagrees at the same seq is a
     finding (`ErrDowngrade`), and when a `SeqFloor` is set it is raised for every listed document, so a
     fresh client has a floor for the documents that matter after one call (call it after sign-in,
     the floor is raised in one batch, `RaiseMany`). It also covers files still in frame v1, which
     carry no level. The index can itself be deleted or replaced by an older one; when it lists nothing
     and no floor has seen an index (no floor, or an empty one), `VerifyIndex` returns a finding with
     `ErrIndexUnverifiable` (`Doc` is `IndexKey`; a warning, not a downgrade): a deleted index
     cannot be told from none. On a store with compare-and-swap (`SaveIf`) concurrent changes to the
     index retry; on one without, `Sharing` takes the store's `Lock` for the change if it has one, and
     otherwise two clients changing the index at the same moment can lose an entry (serialize them: one
     owner client at a time), which leaves a confidential document unlisted, not unprotected: the
     frames and the record still protect it.
  3. **`SeqFloor`** (optional, client state): the highest `seq` seen per document and for the index. A
     record below it is refused (`ErrStaleRecord`), and so is a missing record where one was seen.
     `MemorySeqFloor` (process lifetime), `FileSeqFloor` (a JSON file for a server or CLI, written
     atomically, mode 0600, the file and its directory synced, errors reported; read once and kept in
     memory, written through, re-read only if the file changed on disk, and `RaiseMany` writes any
     number of documents at once, so `VerifyIndex` is one read and one or two writes, not one per
     document) and `LocalStorageSeqFloor` (the browser, js/wasm build, one `localStorage` entry per
     document under a per-account prefix; every access runs inside a JavaScript try block, because a
     thrown `localStorage` getter would otherwise end the Go program). Keep a floor where store
     writers cannot write.
     **A floor that cannot be kept says so.** The browser's floor keeps working from memory when
     storage is blocked or full (a private window), as availability is not traded for this layer, but
     `Get` and `Raise` return the value together with an error wrapping `ErrFloorDegraded`; the
     keyring and `Sharing` go on and report it through `SetFloorDegradedHandler` (logged when it
     changes by default), and `StorageSeqFloor.Err()` keeps the last failure for a page that polls.
     An application that offers this layer should show it (a banner that this browser is not
     remembering what it saw); the showcase is not wired to the encrypting adapter yet, so when it is
     it must set the handler or poll `Err()`.
     **Runbook, a damaged floor file** (`FileSeqFloor` says "damaged" and names the file; nothing is
     overwritten): move the file away or delete it, then run `Sharing.VerifyIndex`: the floor starts
     empty and the index raises it for every listed end-to-end document. What only the lost file knew
     (a document the index does not list) has no floor until its record is read again; that is the
     protection a fresh client has.
  4. `Saver.Save` of a document its catalog says is confidential calls `Saver.Rekey` (e2ee) first,
     which asserts the level on every save of a document whose files are still frame v1.
  **Frame v1, v2 and the upgrade.** Files written before frame v2 have no level or seq; they are read as
  before (no check), and the first `Save` or re-key of a document writes a signed v3 (`Sharing.Repair(doc)`
  upgrades all of its files at once). Frame v1 cannot detect a downgrade: the first save after upgrading
  pins the level, and a record deleted *before* that first save is seen by layers 2 and 3 but not by the
  frame. Frame v2 files (level and seq, unsigned) are read with the same limits as v1 for forgery until
  they are re-keyed. Older builds cannot read v2 or v3 files.
  **When the record was restored on purpose** (or after an attack, to carry on): `Sharing.Recover(doc,
  level, sharedKeys)` writes a new signed record with exactly the level and shared keys the owner
  gives (the replayed record's list is not carried over, so a person the owner had removed does not come
  back), numbered above every seq any file was sealed under and above the floor, then re-keys the
  files as `SetProtection` does and updates the index. It needs the owner's signer.
  **What cannot be defended.** A store writer who can replace *all* of the store consistently, files,
  records and index together, with an older snapshot of it, hands every client an older but internally
  consistent past: the frames, the records and the index all agree, so nothing inside the store can
  call it a downgrade. That is a rollback of data (the store can always return an older version of what
  it held; the saver's checksums notice a file that does not match its catalog, not a catalog that was
  rolled back with it), not a lowering of protection: a document that was e2ee in the snapshot is e2ee
  in it. A client-side floor (layer 3) that outlives the rollback (`FileSeqFloor`,
  `LocalStorageSeqFloor`) refuses records older than the client has seen, and so does notice it on a
  client that has been here before; a fresh client cannot. Whoever holds an owner key can of course
  change lists, and what someone already opened or copied cannot be taken back.
  **What is and is not defended, after the signed frame** (a store writer who knows the recipients'
  public keys but holds no owner key):
  - *Defended:* forging a file (any content, level, seq or recipient hash) for a document whose
    files an owner signer wrote and whose record says `signed`: `ErrForgedFrame`. Forging a *record*
    or the *index*: signed, refused. Deleting a record, or putting an older one back, while the files
    that were sealed under the newer one are in the store: `ErrDowngrade` on every read and write,
    with decoy files and doctored times not changing that (any unreadable file stops the operation
    instead of being skipped; the highest seq among all files counts).
  - *Not defended, and no layer inside the store can:* (1) **rollback to a genuine older consistent
    snapshot** (files, records and index together): every signature in it is genuine, so only a
    client-side floor that outlives it notices, and a fresh client has none; (2) documents whose
    files are still unsigned (v1, v2) and whose record does not yet say `signed`, which can be
    forged until `Repair`; (3) deleting files (availability), including all of a document's files
    together with its record, which leaves no evidence of the lost document except the index and a
    floor; (4) a document with no record at the server level, which has no owner signature to
    authenticate; (5) anything an owner key can do.
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
   strict reads by default (a file without the age header is refused; `AllowPlaintext` is the opt-out for migrating a clear store), ciphertext bound to its id, and from frame v2 to its protection level and record seq, by a framed header inside the encryption (`ErrWrongDocument`, `ErrDowngrade`; C.5), `SaveIf` on encrypted ids compared against the plaintext, serialized and conditional re-keys that continue past failures, catalogs / folder records / ACL records / fonts left readable
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
   component keeps no copy and drops it from the DOM when done (the host holds it in memory while it is
   shown, and Go strings cannot be wiped). The button is left out when the host sets `CreateRecoveryKey`
   without `OnRecoveryKey`. *Left:* the showcase does not supply these callbacks yet (`examples/people-keys-demo` does, see 4b);
   calling `CheckRecoverableOwner` when a document is made confidential is step 5.
   4b. **Done (library, server rule, component and a demo host): the verified e-mail mark of C.4.**
   `encrypt.KeyDirectory` (`Lookup(ctx, email) ([]PublishedKey, error)`; a `PublishedKey` is the key
   text, label, type, fingerprint and `Verified`) is injected; `encrypt.AdapterDirectory` is one over a
   `docstore.Adapter`.
   - **Records are signed.** `Publish(ctx, user, key)` refuses (`ErrNotPublishable`) unless
     `user.VerifiedEmail` is set (and `user.Email` with it) and the user came from a provider session (a
     `passkey:` user carries no e-mail, B.3), and
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
     `{"key", "label"}`, at most 4 KB) and `GET /directory/lookup?email=`. Both need a signed-in
     *provider* session: a passkey session is refused (403) on publish and on lookup. Publishing needs
     an explicitly verified e-mail: `auth.User.VerifiedEmail`, set only when the provider's ID token
     says `email_verified: true` (an absent claim is **not** verification; `auth` then leaves
     `User.Email` empty too). A provider that really verifies every address but omits the claim can be
     opted in with `auth.Provider.AssumeEmailVerified` (default false; in the
     `GOJIBERRY_DOCSTORE_AUTH_PROVIDERS` JSON, `"assumeEmailVerified": true`); an explicit
     `email_verified: false` still wins. Publishing is under that address only: a different `email` in
     the body is refused (403). Each user is limited to 10 publishes and 60 lookups a minute, and a
     client address to 120 lookups (429 with `Retry-After`). **Lookup reveals the published public
     keys of any address to every signed-in provider user, by design** (a public key is public, and
     finding it by address is the feature); it does not reveal whether an address has an account. The
     20-key cap is a list-then-save, serialised per address inside one process (a keyed mutex); with
     several server replicas on one store, route publishes through a gateway that serialises them per
     address, or use a store with a conditional write, or the cap can be overshot.
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

   5. **Keys per protection level (done).** `encrypt.ConfidentialKeyring` chooses the recipients by the
      document's level, read from a signed `protection` field in the `keys:<ID>` record (C.5; the
      same signature and fail-closed rules: a store writer cannot forge it, and an unsigned or
      altered record fails the save instead of falling back to the server level; replaying an older
      signed record or deleting it is not prevented by the signature, but is caught from inside the
      files, see C.5 and 5.7). A confidential
      document's files are wrapped **only** for people's keys (`People`: the owner's keys and the
      signed recipient list); `AtRest` (C.1) is never consulted for them, and `ServerRecipients`
      are removed from the list even if a deployment put the server key among the owners, an age
      key or an SSH key (compared by key, not identity; a recipient of a type that cannot be
      compared is `ErrUncomparableRecipient`; none left: `ErrNoRecipients`, nothing is saved). Server-level documents and non-document
      ids keep using `AtRest`. Identities are the union of both, for every document: opening is not
      where the level is enforced, and a re-key between levels must open the old wrapping.
      `Sharing.SetProtection(doc, level)` writes the record (level and `pending`) first, then
      re-keys every stored file of the document (current copy, previous copy, every version
      snapshot) and clears `pending` only when all are done; settled, it reads one record and writes
      nothing (and needs no signer). A failure is an `*encrypt.RekeyError` naming the files still wrapped to the old set. The re-key
      is all or none, so one file that cannot be opened blocks the switch; it is never skipped
      silently: `RekeyError.Unreadable` names it, `saver.UnreadableKeys(err)` returns the ones that
      are version snapshots, and after the person confirms, `Saver.DropVersions` deletes them and
      `SetProtection` is run again (a current or previous copy is not droppable).
      `encrypt.RekeyHook(sharing)` is the function for `Saver.Rekey`, which the saver calls (it
      does not import encrypt): `SetProtection` to e2ee re-keys **first** and only then moves the
      name and tags into the document, so no file holding them is ever wrapped for the server; back
      to server it moves the name out first (still wrapped for people) and re-keys last, so a failure
      exposes nothing. Calling `SetProtection` again with the same level retries (the hook runs even
      when the catalog already says so). `Saver.Save` of a confidential document (new: an import, a
      copy, a document marked so; or existing) runs the re-key before its first byte is written, and
      if the document turns out confidential only inside the catalog lock (a concurrent delete or
      switch) the save starts over re-keyed, never writing it first.
      A document with `Saver.Rekey` set is therefore never left wrapped for the server key once
      `SetProtection` has returned nil. A store writer who deletes a document's `keys:` record or
      replays an older one no longer makes later saves server level: the files themselves prove the
      level and seq the record had (5.7), `Save`, `Load` and the re-keys report `ErrDowngrade` and
      write nothing, with no state kept on the client (it needs the owners' public signing keys as
      configuration: the frame is signed, so that someone who only knows a public key cannot
      forge one, `ErrForgedFrame`, and a confidential document is not saved without
      `Adapter.Signer`); the signed index and a persistent `SeqFloor` are the belt and braces. Left:
      the whole-store rollback to an older consistent snapshot described in C.5, which no layer
      inside the store can tell from the truth, and unsigned (v1, v2) files until they are re-keyed.

   Without a `Saver.Rekey` hook, `SetProtection(E2EE)` still does not erase the past: the previous
   generation and the version snapshots keep the wrapping they had until the store's keyring
   re-keys them (`Sharing.Repair`). `ImportPack` with Replace writes the confidential document in
   one step (the check and the re-key run first, and the record is never stored as an ordinary
   document); the copies of the document it replaced are re-keyed by the same hook.

   6. **Confidential folders (done): C.2's "folder names inside the envelope".**
      - `Saver.SetFolderProtection(ctx, target, id, level, recursive)`, with the same
        `ConfidentialCheck` and `Rekey` hooks as documents and the same order (to e2ee: check,
        re-key, then the name moves; back to server: the name moves out, then re-key; the same level
        again retries the hook). A confidential folder lists `saver.ConfidentialFolderName`
        ("Confidential folder") in the catalog, in `Folders`, `Overview`, breadcrumbs (`FolderPath`) and
        the move menu; `Saver.RevealFolder` returns the real name (an error wrapping
        `docstore.ErrLocked` when no key opens it).
      - **Where the name lives.** In `folder-secret/<folder ID>` (`docstore.FolderSecretPrefix`,
        `saver.FolderSecretKey`), JSON `{"name": ...}`, written through the adapter. The folder's
        plain record (`catalog:folder/<ID>`) keeps the placeholder, the parent and the level, so the
        tree's shape stays visible (C.2) and a rebuild needs no key to find the folder in its place.
        The rule for the encrypt layer: `DefaultSkip` skips `catalog:` (the plain records) but NOT
        `folder-secret/`; `encrypt.DocID("folder-secret/<ID>")` is the folder's ID (a UUID like a
        document's), so the folder's own signed `keys:<ID>` record and `ConfidentialKeyring` choose
        the recipients: people's keys only at the e2ee level, never the server's; `Sharing.SetProtection`
        works on a folder ID as on a document's. `saver.reserved` lists the prefix so it is never read
        as a document. (Under `RequireACL` `folder-secret/<ID>` is an ordinary id with its own ACL: the
        first writer owns it, and it is not tied to the folder's keys record.)
      - **Folder and document IDs are disjoint, enforced by the catalog.** Both are UUIDs minted
        separately, so nothing but the catalog stops a crafted pack from giving a folder a document's ID,
        which would share `keys:<ID>` and make the Rekey hook act on the document. `createFolderLevel`,
        `RenameFolder`, `SetFolderProtection` and `RevealFolder` refuse a document's ID (`ErrFolderID`),
        and `ImportPack` gives such a folder (and a document that has a folder's ID) a fresh ID.
      - **The plain folder record is not authenticated.** Its level and name are not signed (the same
        trust model as documents: the `keys:<ID>` record is signed, the catalog is not). A store writer
        can flip a plain record's level or rename it; that can make a confidential folder list as a
        server one or the reverse, or hide/show it under another placeholder, but it cannot reveal the
        real name (that stays in the encrypted `folder-secret/<ID>`) nor change who can open it (the
        signed keys record decides).
      - The docstore expiry (docs/public-docstore.md) exempts `folder-secret/<ID>`: it lives as long
        as the folder (removed by `DeleteFolder`), never on a TTL.
      - Rename of a confidential folder asserts the keys (the hook) and writes only the record: the
        catalog does not change. Move keeps the name hidden; delete removes the record.
      - **Documents inside are NOT made confidential by their folder**, and a folder does not become
        confidential because its documents are. `recursive` applies the level to the folders below and to
        every document in the subtree (the trash included), reporting the ones that failed together
        (run again to finish).
      - `Rebuild`/`Repair`/`Verify` with no key: the plain record is read as is, the folder is listed under
        the placeholder, whatever name a record claims. A store whose folder records are themselves
        encrypted and locked lists the folder as confidential under the placeholder, with its place in
        the tree from the old catalog (the top level if there was none), like locked documents.
      - Packs: a confidential folder is in the index under the placeholder; with `PackOptions.Seal` its
        real name travels as an encrypted record whose ID is the folder's (a small `.open` message
        named with it, index name `ConfidentialFolderName`); without `Seal` it is exported with the
        placeholder and reported to `Warn` (`PackNeedsKey`). `ImportPack` with `Open` makes the folder
        confidential again (through `ConfidentialCheck`; a refusal is that folder's `Failed` and its
        documents go to the parent); without it (or when the name cannot be opened, or the pack lists the
        folder under the placeholder with no sealed name) the folder is still made confidential, under the
        placeholder (never a plain folder called that), `NeedsKey` names it and `RenameFolder` sets a name.
        `ExportPack` treats a missing `folder-secret/<ID>` like a locked one: a warning, the placeholder.
        ZIP archives draw a confidential folder as a directory named with the placeholder and the first
        eight characters of its ID, so two of them never merge.
      - `Session`: `SetFolderProtection`, `RevealFolder`; unrevealed confidential folders show the
        placeholder in the list, breadcrumb and move menu (`components.DocFolder.Confidential` lets a UI
        draw a lock); the pack's file name is never the real one. Tests with the real adapter
        (`docstore/encrypt/confidential_folders_test.go`): no real name in the store, a stranger's rebuild,
        passkey PRF alone and recovery key alone, rename, move, delete, breadcrumbs, no leak in the lists.

   7. **Downgrade and deleted-record detection from inside the envelope (done).** The design and the rules are
      in C.5; what was built, in `docstore/encrypt` unless noted:
      - `encrypt.go`: frame v3 (`GJE\x03`: id, level, record seq, recipient hash, content hash and an owner
        signature; v2 is the same unsigned, v1 has no level), `Adapter.Signer`, `ErrForgedFrame`,
        `ErrNoFrameSigner`, `ErrUnreadable`, `Binding`, the `Binder`
        interface (`Bind`, `Binding`: one read of the record gives both the recipients and what the frame
        carries), `crossCheck`, `ErrDowngrade` (= `docstore.ErrDowngrade`), checks in `Save`, `SaveIf`, `Load` and
        every re-key's plan. `StoreKeyring` and `ConfidentialKeyring` are Binders; `Static` is not, and writes
        frames with a zero binding that are never checked. `checkFiles` scans every file of the
        document (foreign ones skipped, unreadable ones `ErrUnreadable`), and a downgrade finding is
        made again once with the record read again, in `Save`, `SaveIf`, `Load` and `Sharing`'s changes.
      - `share.go`/`confidential.go`: `Sharing` checks the record against the files before it signs
        (`mutate`), `SetProtection` refuses "settled at server" for a document whose files say a record
        existed, and updates the index.
      - `index.go`: the owner-signed `keys:index`, `Sharing.Index`, `Sharing.VerifyIndex`, and
        `Sharing.Recover` (the owner's way forward, also for a confidential folder).
      - `floor.go`, `floor_js.go`: `FileSeqFloor` (cached, `RaiseMany`, directory sync), `StorageSeqFloor`
        over a `KV`, `LocalStorageSeqFloor`, `ErrFloorDegraded`, `SetFloorDegradedHandler`.
      - `share.go`/`index.go`: `Sharing.PendingStatus`, `PendingDocs`, `PendingHook` (`saver.Saver.PendingCheck`,
        `VerifyReport.Pending`/`PendingStuck`), the record's `signed` flag, `ErrIndexUnverifiable`.
      - `saver`: `VerifyReport.Downgraded`; Save, SetProtection, Reveal and the folder calls return the
        error wrapped (nothing is written or re-asserted over it); a catalog rebuild fails on a
        downgraded document instead of listing it as a server one.
      - Behaviour changes (also in the roadmap's upgrade notes): a client with a Binder keyring now reads
        the record on `Load` (so it needs `OwnerSigners` to read, as it did to save); `Save` costs one prefix
        listing and one read of the document's newest file more; frame v2 is not readable by older builds.
      - Tests with the real adapter and saver (`downgrade_test.go`, `signed_frames_test.go`, `floor_test.go`,
        `floor_js_test.go`;
        each fails when its check is taken out): replayed record then save/load/re-key/change refused with
        the store unchanged; deleted record at e2ee and at server-shared; legitimate switches both ways
        cut short at a file and retried, with saves and loads in the window; a server-level file put
        back into an e2ee document; v1 files read, upgraded by the first save or `Repair`, and the v1
        limit stated; two clients, the second one fresh; the index finding deletions and replays of
        documents never opened, and its own signature, deletion and replay; `Recover` after a replay and a
        deletion; folder records; `Verify` and rebuild; file and `localStorage` floors (the wasm test runs
        under node: `GOOS=js GOARCH=wasm go test -exec <wrapper> -run LocalStorage ./docstore/encrypt`,
        where the wrapper clears the environment before calling `$(go env GOROOT)/lib/wasm/go_js_wasm_exec`
        if node reports "total length of command line and environment variables exceeds limit"); and a race
        test of saves, versions and loads against repeated switches that must never see `ErrDowngrade`.
        `signed_frames_test.go`: decoy files (junk with the age header, well-formed foreign files, a restored old
        file that is the newest by time) cannot hide a deleted or replayed record; forged frames (unsigned, wrong
        signer, a genuine header on other content) are refused, and the unsigned limit before `Repair` is
        stated by a test; a confidential save needs the signer; same-seq files that disagree with the record
        (level, recipients); a switch completed between the reads of `Save`, `SaveIf`, `Load` and a `Sharing`
        change (deterministic interleaving hooks); a stuck `pending` in `Verify`; the index warning and the
        store lock; the floor's read-once, batch write and directory sync, its damaged-file runbook, and a
        degraded floor reported through the keyring.

   *Left:*
   1. The showcase wiring and the key management UX (where the owner's key lives, the "make
      confidential" switch, the recovery key shown once, the lock mark in the document list).
   2. ~~Folder names are not hidden~~ Done, see 5.6 below (confidential folders). Left there: the
      UI switch, and the pack-count wording in `Session.DownloadPack`, which counts a folder whose
      name could not be sealed with the documents left out.
   3. What someone already opened or copied before a switch cannot be taken back (as with any
      re-key); with the hook the stored copies themselves are re-keyed. A store writer who
      deleted the `keys:` record first is caught from inside the files (5.7).
   4. Key management for `Seal`/`Open` in the pack UI ("As .opendoc", "Import .opendoc"): until
      then `Session.DownloadPack` leaves confidential documents out and says how many, and
      `Session.ImportPackData` reports the ones it could not open.
