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
   it does not prove the key is not the account's compromised one. Proof of possession, the
   key's own attestation and the transparency log narrow what the server alone can claim; the
   table of what is proven and what is not is in step 4b.

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
- `StoreKeyring.Recipients` (what every save asks) verifies the record and **fails closed**: an
  unsigned record (`ErrUnsignedRecord`), a record signed by a key that is not an owner's, or a
  changed or moved one (`ErrBadSignature`) makes the save fail, never a longer or shorter list.
  Fingerprints and types are recomputed from the stored key text, not read from the record.

Threat model, store write access only (no owner key):
- **Cannot** add themselves, or anyone, as a recipient of future saves: the record they write does
  not verify, and saves fail until the owner repairs it.
- **Cannot** edit a signed list to drop a recipient or change a key.
- **Can** delete a record (the document then reads as shared with no one besides the owner and, at
  the confidential level, as server level: its later saves are wrapped for the server keyring).
  Detecting this needs owner-held state (a signed index of which documents have records); not
  built. `Sharing` keeps a record rather than deleting when the last key goes, and
  `SetProtection` never deletes one, but a reader that has seen nothing before cannot tell a
  deleted record from a document that never had one.
- **Can** put back an older validly signed record (a replay: for example one that still lists a
  person the owner removed, or one from before the document was made confidential, whose
  `protection` is empty), and future saves then follow it. A signature cannot tell it from the
  current record. What is done about it, in layers, none absolute:
  1. `Saver.Save` of a document its catalog says is confidential calls `Saver.Rekey` (e2ee)
     first, with one record read when settled: a record that says less is re-signed to e2ee and
     the files re-keyed before a byte is written. The same call also covers a new confidential
     document, and a document made confidential before this was in place. Files written by
     someone else between the replay and the next save are not covered, and the replayed
     recipient list itself is kept (it is re-signed with the old list).
  2. `StoreKeyring.SeqFloor` / `Sharing.SeqFloor` (optional; `MemorySeqFloor` is the in-memory
     one): the highest `seq` seen per document. A record below it is refused
     (`ErrStaleRecord`), and so is a missing record where one was seen. It must be kept where
     store writers cannot write (the client's memory or device storage); a fresh client has no
     floor and a replay before its first read is not detected. A floor that is ahead of a
     record the owner must restore needs a deliberate reset by the owner's client.
  So a store writer cannot forge a level or a list, but is not stopped from replaying or deleting
  a record where no floor is known; the saver's re-assertion limits the effect to the window
  before the next save of the document by an owner client.
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
   component keeps no copy and drops it from the DOM when done (the host holds it in memory while it is
   shown, and Go strings cannot be wiped). The button is left out when the host sets `CreateRecoveryKey`
   without `OnRecoveryKey`. *Left:* the showcase does not supply these callbacks yet (`examples/people-keys-demo` does, see 4b);
   calling `CheckRecoverableOwner` when a document is made confidential is step 5.
   4b. **Done (library, server routes, component and a demo host): the verified e-mail mark of C.4,
   and the limits of the first version removed.**
   `encrypt.KeyDirectory` (`Lookup(ctx, email) ([]PublishedKey, error)`) is injected; a `PublishedKey` is
   the key text, label, type, fingerprint and the marks below. `encrypt.AdapterDirectory` is one over a
   `docstore.Adapter`. The rule of the whole feature: **only the signed-in owner of an address can
   publish, replace or revoke the keys of that address**, and only with a provider session whose e-mail
   the provider verified.

   *What the directory proves, and what it does not.* The directory proves exactly this: **"the provider
   says this account's verified e-mail published this key, and (with the proof) held it at publish
   time"**, and, for an address whose owner has enrolled an owner key, **"the holder of that owner key
   signed this publication"**. Each mark is a separate claim:

   | Mark | Proven by | What it proves | What it does not prove |
   |---|---|---|---|
   | `Verified` | the server's Ed25519 signature on the record (ID, key ID, key, label, publisher, provider, address, time, proof, attestation, owner signature, log number); for a record that a *retired* directory key signed, or that belongs to an owner-enrolled address, also a log entry that backs it (same address, key and operation, validly hashed and signed) | the server wrote the record for a provider account whose e-mail the provider called verified, and the address in it is the one asked for | that the key is safe; that the provider's claim is true; anything about a record the server's key did not sign. A record signed by a retired key with no log entry behind it is **not** `Verified` (a leaked retired key cannot make one) |
   | `Held` | a challenge, a random nonce encrypted to the key, was decrypted and returned once, within 2 minutes, by the same session user, for that key; the token is honoured only if the **current** directory key issued it | whoever published could decrypt with the private key at that moment | that the key is still held, or held by the person the address names; `Held` is **the server's word** (it is inside the server's signature) |
   | `Attested` | the key's own signature over `directory-binding\|address\|fingerprint\|time`, checked on every lookup (ssh-ed25519 keys only) | the holder of that private key signed the binding to that address. The signature is in the row: a client checks it itself (`encrypt.VerifyAttestation`; `directoryclient.Lookup` does) | that the holder is the person the address names: whoever generates a key can attest it to any address. age and passkey keys cannot sign, so for them proof of possession (`Held`) is what the key itself contributes, and **the owner signature (`OwnerSigned`) is what attests them** |
   | `OwnerSigned` (the strongest mark) | the owner's Ed25519 signature over `directory-op\|publish\|address\|fingerprint\|time\|log position`, under an owner key enrolled for the address, in the row (`Owner`: key, time, position, signature) and in the log entry; a client checks it itself (`encrypt.VerifyOwnerEvidence`; `directoryclient.Lookup` sets its own `OwnerSigned` only if it verifies) | the holder of the address's owner key (held client-side, never by the server) consented to publishing this key under this address, at that time, naming that log position. It works for any key type: age, passkey and SSH keys alike | that the owner key's holder is the person the address names (it was enrolled on a provider session: trust on first use, below); that the key is safe |
   | `OwnerEnrolled`, `OwnerResetPending` | the enrollment record (signed by the server) and the log | the address has an owner key (a client that saw it enrolled treats a later "not enrolled" as an attack); a reset of the owner key is waiting out its delay | |
   | `Seq` | the number of the record's entry in the transparency log (0: none) | the publication was appended to the log, where an auditor and the address's owner can see it; a tombstone carries the number of its **revoke** entry | that the log everybody sees is the same one (there are no witnesses) |
   | `Revoked` | a signed tombstone, shown only when asked for | the owner revoked the key at that time | anything about documents already encrypted to the key (remove it from their recipient lists) |

   *What each mark proves and what the server can still do.* "The server" below is whoever holds the
   directory key and the store (and can open a provider session of its own for an address).

   | What a client shows | Rests on | Who could have made it | What the server can still do |
   |---|---|---|---|
   | *label only* | nothing | anybody | nothing is claimed |
   | *verified e-mail* | the server's signature (and, for a retired-key record or an enrolled address, a log entry) | the server, for any address that has **no owner key** | publish a key it holds under any address nobody has enrolled: the mark ends at the server |
   | *signed by the key* (`Attested`) | the key's own signature, checked by the client | whoever holds that key (any address) | it cannot attest a key it does not hold, but it can publish and self-attest a key *it* holds |
   | *signed by its owner* (`OwnerSigned`) | the owner's signature, checked by the client, under the enrollment | **only the holder of the address's owner key** | nothing that adds a key: it cannot fabricate a publication, only withhold (an address that looks empty, a key not shown), show an older state, or show different people different logs |
   | *not signed by the owner key* (a note on a verified key of an enrolled address) | the enrollment says the address has an owner key, this record does not carry its signature | a record that predates the enrollment (grandfathered, still `Verified`); after the enrollment the key is **not** `Verified` at all | the owner republishes it with their key to get the strong mark |

   **The exact guarantee of owner enrollment.** (1) Once an address has an owner key, every publish and
   every revoke for it needs a valid owner signature *and* the provider session; the server checks the
   signature (current owner key, within 10 minutes, naming a log position that exists), and a
   publication without it is refused (428, or 403 for a bad one). (2) The owner signature is recorded in
   the log entry and in the record. (3) Anyone who knows the address can audit the log for it:
   `encrypt.AuditAddress` replays the entries of the address and reports every entry that the owner key
   then current did not sign (no signature, a wrong key, a stale time, an enrollment over an owner key, a
   reset that took effect before its wait, a rotation the new key did not accept). A server that
   fabricates an entry for an enrolled address produces a log that fails the audit. (4) A lookup marks a
   key `OwnerSigned` only with valid evidence, and a record written after the enrollment without the owner's
   signature is not `Verified`. (5) The owner key is held by the owner (generated in the browser or CLI, `directoryclient.GenerateOwnerKey`, kept in
   `localStorage` or a file); the server only ever sees its public half.

   **What the server can still do, stated plainly.**
   - **Withhold and show an old state.** It can answer a lookup as if a key were not there, or with
     yesterday's rows; it can serve a log that stops short. A client that remembers the last log position it
     saw (`WatchState`) detects a log cut back or rewritten from that point on
     (`encrypt.VerifyLogHead`, `CheckHead`, `GET /directory/log/head`, signed); a client that never saw
     the log before cannot.
   - **Show different people different logs** (a split view), because the log has no witnesses or gossip:
     two auditors must compare the heads they saw.
   - **Enroll first.** The first owner key for an address is accepted on the provider session plus a
     signature by the new key over a server challenge, nothing more: **trust on first use**. Whoever is signed in as the
     address first (the provider, or the server with a provider session of its own) can enroll first; the
     enrollment is in the log, so an owner who watches the address sees "someone enrolled an owner key for
     your address", but a rightful owner who finds an attacker's key enrolled cannot take it back by
     themselves: a reset (`POST /directory/enroll/reset`) is logged and waits 24 hours, and the enrolled key
     can cancel it. Recovering needs an operator who deletes the enrollment record. Enrolling early is the
     defence.
   - **Replace a lost owner key.** By a provider-session *reset*: the request is logged at once (visible to
     everybody who watches the address), it takes effect only after the waiting period
     (`ResetWait`, 24 hours; auditors hold the server to 24 hours whatever it is set to), and the current
     owner key can cancel it (`reset-cancel`, signed). A rotation signed by the current key
     (`rotate`, the new key accepts) has no wait.
   - **Delete records.** Someone with store access can delete a record or an enrollment record (a lookup
     then finds nothing; a damaged enrollment record makes publish, revoke and lookup for the address fail
     closed rather than look un-enrolled). The log still shows the history, and `WatchLog` flags a server
     that says an address is not enrolled when the log shows it is.

   - **Records are signed.** `PublishWith(ctx, user, key, PublishOptions)` (and `Publish`, without a
     proof) refuses (`ErrNotPublishable`) unless `user.VerifiedEmail` is set (and `user.Email` with it)
     and the user came from a provider session (a `passkey:` user carries no e-mail, B.3), and refuses
     an address that is not exactly one local part, one `@` and one domain (400; the same for lookup). It
     writes `directory/<HMAC of the e-mail>/<fingerprint>` holding the key, label, the publisher's ID and
     provider, the address, the time, the signing key's ID (`kid`), the proof flags and the owner
     signature, signed by the server's `encrypt.DirectorySigner` (Ed25519; the signature covers the record's
     own ID, so a record copied to another name fails). `Lookup` recomputes type and fingerprint from the
     key text and sets the marks above; any other record is returned unverified ("label only") and an
     unreadable one is skipped. The record's name is a keyed hash, so addresses cannot be guessed or
     enumerated from the store's IDs. At most 20 keys per address (`MaxKeys`; keys revoked within the
     window count). `Lookup` lists only the address's prefix (`docstore.PrefixLister`: memory, localfs,
     sqlite and the nested postgres, mongo, s3 and blob adapters, each natively; `docstore.ListPrefix`
     falls back to `List` for an adapter without it, and the browser adapters still do).
   - **Publish and revoke are atomic across replicas.** Both read the address, check (tombstones within the
     window, the cap, the owner signature) and write with `saveIf` on the digest that was read: a create-if-absent
     for a key with no record, a compare-and-set otherwise. If another replica changed the record in between
     (a revoke, say, racing a republish) the write conflicts, the log entry that was already appended is
     **voided** (a compensating entry, `op: "void"`, `ref` the cancelled number, so the log never claims a
     publication that did not happen; `AuditAddress` ignores both) and the operation re-reads and checks
     again, up to 8 rounds. A record whose write fails for another reason leaves a void entry too. Appending
     to a contended log retries with backoff and jitter, up to 16 lost rounds, then answers **503** with
     `Retry-After` (`docstore.ErrDirectoryBusy`). On a store with no conditional write all of this holds only
     inside one process: see *Replicas and non-conditional stores*.
   - **Publishing again re-signs.** Publishing a key that is already there replaces its record, signed by
     the current key and named under the current key's hash; that is how a record moves to a new key.
   - **Proof of possession** (`AdapterDirectory.Challenge`, `OpenChallenge`; routes below).
     `POST /directory/challenge {"key"}` answers `{token, encrypted, expires}`: a random nonce encrypted
     to the key as an age file (age, SSH and passkey-derived keys alike), and a token the server signed
     that holds the nonce's keyed hash (HMAC under the server secret, so the token does not reveal it),
     the session's user ID and address, the key's fingerprint and an expiry 2 minutes out. The client
     decrypts the nonce with the private key and publishes with `"proof": {"token", "nonce"}`. The server
     checks that the **current** directory key signed the token (a challenge of a retired key is refused:
     it lives two minutes, and a retired key may be the one that leaked), the expiry, user, address,
     fingerprint and nonce, then **uses the challenge up**: it creates `directory/pop/<id>`
     (create-if-absent, atomic on a conditional store) so a second publish with the same answer is
     refused (403). Nothing is stored until a challenge is answered, and `Sweep` deletes used-challenge
     records a minute after they could no longer be answered. `RequireProof` (env
     `GOJIBERRY_DOCSTORE_DIRECTORY_REQUIRE_POP=true`, **off by default**) makes a publish without a proof
     428; a proof that is sent is always checked, and recorded as `Held`.
   - **Attestation.** An ssh-ed25519 key signs `directory-binding|<address>|<fingerprint>|<unix seconds>`
     (`encrypt.AttestSSH` or `Attest` make it; the address lower-cased and trimmed) and sends it as
     `"attestation": {"at", "sig"}`. The server verifies it (the key must be ssh-ed25519, the time within
     10 minutes of now, the signature the key's) before it stores it; a bad one is refused (400). On every
     lookup the row's `Attested` is recomputed, and `Attestation` carries the signature for a client that
     wants to check it without the server. **age and passkey keys cannot sign**: proof of possession
     covers them, and the **owner signature attests them** (it binds any key, of any type, to the
     address).
   - **Owner keys** (`directory-enroll/`, `AdapterDirectory.Enroll`, `RequestReset`, `CancelReset`,
     `Enrollment`; `GET /directory/enroll?email=`, `POST /directory/enroll/challenge`,
     `POST /directory/enroll`, `POST /directory/enroll/reset`, `POST /directory/enroll/cancel`; the Go and
     browser client is `docstore/directoryclient`). The owner key is an Ed25519 key the owner holds
     (`encrypt.GenerateOwnerKey`). Every owner operation signs
     `directory-op|<op>|<address>|<fingerprint>|<unix time>|<log position>` with `op` one of `publish`,
     `revoke`, `enroll`, `rotate`, `accept` (the new key's acceptance), `reset-cancel`, and the log
     position (`<seq>:<hash>`, from `GET /directory/log/head`) the owner last saw. The server accepts a
     signature dated within 10 minutes of now naming a position that exists in the log, so a signature
     cannot be replayed later and is bound to a point in the log. **Enroll:** `POST
     /directory/enroll/challenge` gives a time and log position and a single-use token (2 minutes, current
     key only); `POST /directory/enroll {"mode": "enroll", owner_pub, token, time, prev, sig}` where `sig` is the
     new key's own signature over the enroll operation. It needs the provider session **and** that
     signature. **Rotate:** `mode: "rotate"`, `sig` by the current owner key over `rotate` naming the new key,
     `proof` by the new key over `accept`. **Reset:** `POST /directory/enroll/reset` (provider session
     only) logs a `reset-request`; after 24 hours `mode: "reset"` with `proof` by the new key installs it;
     the current owner key cancels with `POST /directory/enroll/cancel` (signed). The enrollment record keeps
     the chain of owner keys (each with the number of the entry that installed it and the one that replaced
     it), so a publication made under an earlier key stays `OwnerSigned` after a rotation. Every step is a log entry
     carrying the keys and signatures, so `AuditAddress` can replay it from the log.
   - **Revoking** (`DELETE /directory/keys/<fingerprint>`, body `{"owner": {...}}` when the address has an
     owner key, `AdapterDirectory.Revoke`): the signed-in owner of the address only (the record is found
     under the session's own address, so another user's request finds nothing, 404). The record is
     replaced at once by a signed tombstone carrying the **revoke entry's** number, so `Lookup` stops
     returning the key immediately; `GET /directory/lookup?email=&revoked=1` (or
     `LookupWith(..., IncludeRevoked)`) returns it flagged `revoked`. The tombstone is kept for the
     **revoke window** (24 hours; `RevokeWindow`, `GOJIBERRY_DOCSTORE_DIRECTORY_REVOKE_WINDOW`): within it
     the same key cannot be published again (409), so a replayed publish request (a captured request, a
     stolen session) cannot silently bring a revoked key back; after it the owner can publish the key
     again, and the tombstone is pruned. The fingerprint in the path is the one lookup shows, or its path
     form (`docstore.DirectoryFingerprintSegment`). An unsigned tombstone (written by someone with store
     access but not the key) is ignored.
   - **The key is configuration, and it rotates.** `GOJIBERRY_DOCSTORE_DIRECTORY_KEY` is a 32-byte seed in
     hex that signs (and, derived with a separate label, keys the HMAC that names records, the log's
     address pseudonyms and the enrollment records); `GOJIBERRY_DOCSTORE_DIRECTORY_KEY_PREV` is a comma
     separated list of retired seeds that **only verify and name**. Each record carries the ID of its
     signing key (`kid`). To rotate: put the new seed in `..._KEY` and the old one in `..._KEY_PREV`;
     records signed by the old key keep verifying and stay findable. **Before the server listens**,
     `AdapterDirectory.Startup` runs `Resign`, which moves a record to the new key only if it is worth
     endorsing: its signature verifies and **a log entry stands behind it** (same address, key and
     operation). A record with no entry (`Seq` 0, or an entry that is not its own) is left under the old
     name, counted as *skipped* and reported, and stays unverified: **a leaked retired key can sign records
     for keys of the attacker's choosing, and rotating through it would launder them into records the
     current key signed. If the old key may have leaked, do not rotate through it: remove it from
     `..._KEY_PREV` instead** (what it signed is then neither found nor believed) and have people
     republish. Each record is moved under its address's lock and written conditionally on what was read
     (the newest copy across all names decides: a tombstone that a replica wrote meanwhile is never
     written over; the old copy is deleted only if it is still the one read), so a rolling deploy cannot
     bring a revoked key back. The start-up log line counts records re-signed, already current, skipped
     and unverified, and **named under unknown keys** (a wrong `..._KEY`, or a retired one missing from
     `..._KEY_PREV`, leaves records no lookup can find: the server logs a WARNING and `/health` shows
     `directory.unknown_keys`). When it reports nothing left to move the old seed can be removed. After
     it is removed, log entries signed under it can no longer be checked by the server: a pre-enrollment
     record of an enrolled address then needs republishing to be `Verified` again (owner-signed records
     keep their strong mark). Unset, the directory is off. A key per process is not generated.
   - **Transparency log** (`directory-log/`, `AdapterDirectory.ReadLog`, `GET /directory/log?since=<n>`,
     at most 200 entries a call, ask again from the last number for more). Every publish, revoke and owner
     operation appends an entry `{seq, prev, at, op, address, fingerprint, kid, ref, owner_*, hash, sig}`:
     `prev` is the hash of the entry before, `hash` the SHA-256 of the entry's fields, `sig` the server
     key's signature of the hash; `address` is a **keyed** pseudonym of the address (an HMAC under the
     directory's key material, domain-separated: `AdapterDirectory.LogAddressFor`), so a guessed address
     cannot be tested against the log and the log does not list addresses. The signed-in owner gets theirs
     from `GET /directory/log/address` (`{address, previous}`: the pseudonyms under retired keys, for
     entries made before a rotation) and filters the log with it. The log, its head and the address route
     need a **provider session** (a passkey session is 403), apply the lookup `DirectoryPolicy` (asked
     about the user's own address) and are limited per user, per client IP and for the server. The
     publication is logged first and its record carries the entry's number. Appends take turns across
     replicas with create-if-absent on a conditional store (a head record is a hint that catches up after
     a crash). `encrypt.VerifyLog(entries, prevHash, since, keys)` checks a run of entries against the
     pinned public keys by key ID: numbering, chain, hashes and signatures (`ErrLogBroken`).
     `GET /directory/log/head` answers the **signed head** `{seq, hash, at, kid, sig}` (`VerifyHead`);
     `CheckHead(prevSeq, prevHash, head)` and `VerifyLogHead(prevSeq, prevHash, entries)` compare it, and
     the entries read from the entry last seen, with what the client remembers: a lower number, or another
     hash where it saw an entry, is `ErrLogTruncated` (tail truncation or a rewrite). The response also
     lists the server's public keys (`keys`), which an auditor pins out of band, not from this field.
     *What it is for:* an auditor that keeps the last entry's number and hash detects any later change of
     what it has seen, and a person who watches the entries for their own address sees every key published
     or revoked for it, including one the server published on its own (`directoryclient.WatchLog`:
     "someone published a key for your address"); with an owner key, every entry must also carry the
     owner's signature, which the server alone cannot make. *What it is not:* it does not stop a
     publication, it does not cover lookups, and the server's key signs it, so a server can start a new
     history (the auditor sees the break only if it kept the old head), or show different histories to
     different clients (a split view; there are no witnesses or gossip). A record written straight into
     the store has no entry; a lookup does not trust one for an enrolled address or a retired key.
   - **The server rule.** The `directory/`, `directory-log/`, `directory-enroll/` and `ratelimit/`
     prefixes are reserved in `docstore.Server` like `acl:` and `collab:`, **compared without case** (a
     backend that folds case would alias them): they cannot be read, written, listed or deleted through
     `/docs`; the expiry allowlist leaves them alone (they are system keyspaces, covered by the
     prefix-scan test, as are the public offer's `public/` and `report/`) and encryption skips them. With an
     `auth.Manager` and `Server.WithDirectory(encrypt.DirectoryService(dir))` (the docstore server does it
     when the key is set) the server mounts `POST /directory/challenge`, `POST /directory/publish` (body
     `{"key", "label", "proof", "attestation", "owner"}`, at most 4 KB), `DELETE /directory/keys/<fingerprint>`,
     `GET /directory/lookup?email=[&revoked=1]`, `GET /directory/log?since=`, `GET /directory/log/head`,
     `GET /directory/log/address` and the owner routes above. **Every route needs a signed-in
     *provider* session**; a passkey session is refused (403). Publishing, challenging, revoking and the
     owner routes need an explicitly verified e-mail: `auth.User.VerifiedEmail`, set only when the
     provider's ID token says `email_verified: true` (an absent claim is **not** verification; `auth`
     then leaves `User.Email` empty too). A provider that really verifies every address but omits the
     claim can be opted in with `auth.Provider.AssumeEmailVerified` (default false; in the
     `GOJIBERRY_DOCSTORE_AUTH_PROVIDERS` JSON, `"assumeEmailVerified": true`); an explicit
     `email_verified: false` still wins. They act on that address only: a different `email` in the body
     is refused (403). Errors: 401 signed out, 403 refused or bad proof or bad owner signature, 400 bad key,
     address or attestation, 404 no such key, 409 revoked recently or an enrollment conflict, 428 proof or
     owner signature required, 429 over a budget (with `Retry-After`), 503 the log is busy.
     **`/health`** also shows `directory` (`records`, `resigned`, `skipped`, `unknown_keys`) after the
     start-up check.
   - **Limits on abuse, shared by replicas** (`docstore.Limiter`; the same interface as
     `passkey.Limiter`, which is an alias). Per minute on average and at once (a token bucket per key):
     publish, revoke and the owner changes 10 per user; challenges 20 per user; lookup 60 per user and log
     reads 30 per user, then 120 per client IP and 1200 for the whole server. **A lookup or a log read
     spends the user's own budget first and the shared IP and server tokens only after it**, so a user
     over their own limit is refused without draining what everybody shares.
     `GOJIBERRY_DOCSTORE_DIRECTORY_GLOBAL_BUDGET=domain` (`Server.WithDirectoryGlobalPerDomain`) makes the
     whole-server budget one per e-mail domain of the caller (with the `domain` lookup policy: one per
     policy domain, so one company cannot spend another's). **The IP is the connection's address: behind a
     proxy every client shares it, so real per-client limiting, and any limiting of unauthenticated
     floods, belongs to the gateway in front of the server**; the budgets here only keep one tenant from
     draining the service. `GOJIBERRY_DOCSTORE_LIMITER=memory` (default) keeps the budgets in each process;
     `=store` keeps them in the store (`docstore.StoreLimiter`), so replicas on one store share them (the
     public offer's request budgets too: `public.Options.LimiterFactory`). Each budget is a record
     `ratelimit/<name>/<hash of the caller's key>` changed with a compare-and-set loop over
     `docstore.ConditionalAdapter.SaveIf` (read, refill, take a token, save with the digest read, start
     over on a conflict; a denial writes nothing; the caller's address or user ID is not in the record's
     name). With a store that is not conditional it falls back to the in-process limiter (same numbers,
     per process); when the store errors, that one decision is made in process and the failure is logged;
     a hot key that loses 64 rounds in a row is refused for a second. **Clocks:** a record dated more
     than 5 seconds ahead of this replica's clock is re-dated to it, keeping its tokens, so a replica
     with a clock hours ahead cannot lock the others out. A background sweep (`SweepRateLimits`, every 10
     minutes) deletes the records of callers idle for over a minute; one that cannot be read or deleted is
     skipped and the sweep goes on (the errors are reported at the end); the delete is conditional on the
     record being the one read (`docstore.ConditionalDeleter`: memory, sqlite, localfs, postgres and
     mongo; elsewhere a conditional write claims the record first), so a caller who came back in between
     keeps their update; and at most `MaxRateLimitRecords` (100,000) are kept, the oldest evicted first,
     so a flood of different IP-keyed callers cannot grow the keyspace without bound between sweeps.
   - **Bounded probing.** Besides the budgets above, `docstore.DirectoryPolicy{CanLookup(user, email)}`
     decides who may look up an address: `AllowAllLookups` (default: any signed-in provider user),
     `SameDomain()` (the address's domain is the user's own verified domain;
     `GOJIBERRY_DOCSTORE_DIRECTORY_LOOKUP=domain`), `Contacts(func(auth.User) []string)` and `AnyOf(...)`; a
     refusal is 403 and does not depend on whether the address has keys, so it leaks nothing. The policy
     is asked after the budgets, so probing a refused address costs budget too; the log routes ask it
     about the user's own address. **Lookup reveals the published public keys of any address the policy
     allows to every signed-in provider user, by design**; it does not reveal whether an address has an
     account.
   - **Replicas and non-conditional stores.** The log's and the challenges' create-if-absent, and the
     conditional writes of publish, revoke and the owner operations, are atomic across processes only on a
     store that is a `docstore.ConditionalAdapter` (sqlite, localfs, postgres, mongo and s3 are;
     `docstore.SupportsConditional` probes it, since a wrapper may have the method and not the
     capability). `GOJIBERRY_DOCSTORE_REPLICAS` (default 1) says how many server processes share the
     store: **with more than one on a store without conditional writes the directory refuses to start**
     (`AdapterDirectory.CheckStore`), and with one it logs a warning. The 20-key cap is a list-then-save, so
     a few racing replicas can still overshoot it by a key or two (the records themselves cannot clobber
     each other); route publishes through a gateway that serialises them per address, or accept it.
   - **UI.** `RecipientRow.Verified` shows "verified e-mail", and a draft that is not shows "label only:
     confirm the fingerprint"; `Attested` shows "verified e-mail, signed by the key"; **`OwnerSigned` (on
     `RecipientRow` and `DirRow`) shows the strongest mark, "verified e-mail, signed by its owner"**, with
     its help text, and a verified key of an enrolled address that is not owner-signed carries the note
     "not signed by the owner key" (`OwnerEnrolled`). `Recipients` can look an address up (`Directory`,
     `OnDirResults`, `DirResults`), and picking a row fills the draft. The typed fingerprint stays for
     label-only keys, and is skipped for a verified one only when the host sets `TrustVerified`.
   - **Client.** `docstore/directoryclient` (native and js/wasm, net/http, age and Ed25519 only):
     `Publish` (challenge, decrypts the nonce with the caller's identity, attests an ssh-ed25519 key, signs
     as owner when enrolled), `Revoke`, `Lookup` (re-checks attestations and owner signatures itself, pins
     the first owner key seen for an address, flags a different one), `Enroll`, `Rotate`, `RequestReset`,
     `CompleteReset`, `CancelReset`, `Enrollment`, `LogAddress`, `Head` and `WatchLog` (fetches the log
     since the last position kept in a `WatchState`, checks the head's signature against pinned keys, the
     head against what it remembers, the chain and every signature, audits the entries of the address and
     reports those it did not make). Tested against the server with `httptest`.
   - **Host.** `examples/people-keys-demo` uses it: `AddMyPasskey`, `CreateRecoveryKey` (shown once) and
     `Directory` (`directoryclient.Lookup`, so the marks are the client's own), plus a "my directory entry"
     section: publish my passkey's key (proof of possession with the passkey's own PRF identity), revoke
     it, enroll an owner key (made in the browser and kept in `localStorage`), and watch my address
     ("someone published a key for your address"). Its e2e (headless Chrome, virtual authenticator with
     PRF) covers the second-key message, the recovery key shown once, add my passkey, the verified mark
     from a lookup, and the whole owner flow: publish, the foreign publication reported, enroll, the
     owner-signed mark, the session alone refused, a forged log entry reported, and revoke.
     **For the showcase (another agent owns it):** the People wiring there needs a `directoryclient.Client`
     with the signed-in provider session's token and the pinned log keys, `Lookup` as `Recipients.Directory`
     (copy `DirRow.OwnerSigned` and `OwnerEnrolled` from the `Key` rows), the three buttons of the demo's
     `myEntry` section, and a `WatchLog` on sign-in with its `WatchState` in storage.

   *Limits that remain, stated plainly.*
   1. **Trust on first use.** The first owner key for an address is accepted on the provider session and
      the new key's signature, nothing more. Whoever is signed in as the address first can enroll first
      (including a server with a provider session of its own) and then locks the rightful owner out of
      owner-signed publishing until an operator deletes the enrollment record: a reset is logged and delayed
      24 hours, but the enrolled key can cancel it. Before an address enrolls, the server can still put a
      key it holds under it (`Verified`, `Held` and self-attestation do not tell it from the owner's key);
      detection then relies on the owner watching the log, `Seq`, and the fingerprint check. Enrol early.
   2. **A server can withhold, show an old state and show different people different logs.** Truncation
      and rewriting are detected only by a client that remembered an earlier head; a first look at the log
      has nothing to compare with, and the log has no witnesses.
   3. **Owner keys are the owner's to keep.** A lost owner key means a reset (a day's wait, in the log);
      a leaked one is the owner's own key leaking: rotate it. Records that predate the enrollment stay
      `Verified` but not `OwnerSigned` (and say so) until republished. An auditor needs the address to check
      the owner signatures (they name it); the log itself shows only pseudonyms.
   4. **Rotating the directory key**: a log pseudonym changes with it (the owner gets the older ones from
      `/directory/log/address` while the retired key is listed); after the old key is removed, entries made
      under it can no longer be matched or checked by the server. If the old key leaked, remove it from
      `..._KEY_PREV` and republish, do not rotate through it.
   5. Attestation covers ssh-ed25519 keys only; age keys (recovery keys, passkey-derived keys) get proof of
      possession (`Held`, the server's word) and, for an enrolled address, the owner signature.
   6. Proof of possession is optional on the wire unless `RequireProof` is on (off by default, because it
      changes publishing); attestation and the owner signature cannot be required of an address that has
      not enrolled.
   7. The global lookup budget can be used up by a handful of accounts (a denial of service on lookups,
      bounded by the per-user and per-IP budgets and by what an account costs; `GLOBAL_BUDGET=domain`
      narrows it to a domain); per-IP limits behind a proxy are per proxy, and unauthenticated floods are
      the gateway's to stop.
   8. A store without conditional writes carries the directory for **one** process only (the directory
      refuses to start with `GOJIBERRY_DOCSTORE_REPLICAS>1` there, and warns with one). The cap of 20 keys can
      be overshot by a key or two by racing replicas even on a conditional store.
   9. A revoked key can be published again after the window (24 hours by default), and revoking does not
      touch documents already encrypted to it.
   10. The showcase and the real app do not wire these controls yet (the demo does; the follow-up is the
       note above and the key-management UX of step 5.1). The browser adapters do not implement
       `PrefixLister` (they fall back to listing the store). `ConditionalDeleter` is not implemented by the
       s3 and blob backends (the sweep claims the record with a conditional write first, which narrows the
       race to a token at most).

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
      signed record or deleting it is not prevented by the signature, see C.5 for what is done). A confidential
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
      `SetProtection` has returned nil. Remaining weakness: deleting a document's `keys:` record
      or replaying an older one (a store writer can) makes later saves server level until the next
      save of that document by an owner client re-asserts e2ee (or at once, with a `SeqFloor`
      that has seen the newer record, which refuses it); a signed index of documents with records
      (C.5) would close the deletion case on a fresh client.

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

   *Left:*
   1. The showcase wiring and the key management UX (where the owner's key lives, the "make
      confidential" switch, the recovery key shown once, the lock mark in the document list).
   2. ~~Folder names are not hidden~~ Done, see 5.6 below (confidential folders). Left there: the
      UI switch, and the pack-count wording in `Session.DownloadPack`, which counts a folder whose
      name could not be sealed with the documents left out.
   3. What someone already opened or copied before a switch cannot be taken back (as with any
      re-key); with the hook the stored copies themselves are re-keyed. A store writer who
      deleted the `keys:` record first is the weakness named in item 5.
   4. Key management for `Seal`/`Open` in the pack UI ("As .opendoc", "Import .opendoc"): until
      then `Session.DownloadPack` leaves confidential documents out and says how many, and
      `Session.ImportPackData` reports the ones it could not open.
