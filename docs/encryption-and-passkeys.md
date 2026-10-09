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
| `ChallengeStore` | started ceremonies; `Take` must be atomic so a challenge is good once | `MemoryChallenges` (10,000 at most, swept every 5 s) | `StoreChallenges` (over a `docstore.ConditionalAdapter`: replicas on one store share ceremonies; `GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES=store`), Redis `GETDEL`, a table with a delete-returning |
| `auth.SessionStore` (`auth.Config.Sessions`, `Manager.SetSessionStore`) | the bearer sessions and OpenID Connect login states of the session manager that passkey sign-in also mints its sessions through; `TakeState` is single use | `auth.MemorySessionStore` (100,000 of each, in the process) | `sessionstore.Store` (over a `docstore.ConditionalAdapter`: replicas on one store share sessions; `GOJIBERRY_DOCSTORE_SESSIONS=store`), Redis |
| `Limiter` | per client and route group: allowed now, or wait this long | `RateLimiter` (token bucket per address and group: 20 at once, 30 a minute) | Redis, a gateway; `Unlimited{}` to switch off |
| `auth.Limiter` (`Manager.SetLoginLimiter`) | a budget per client address in front of `GET /auth/<provider>/login` (key `login|<address>`), which writes a login state without a sign-in; over it 429 with `Retry-After` and nothing is written | none for an embedder; `cmd/docstore` sets one in every `GOJIBERRY_DOCSTORE_SESSIONS` mode (the passkey routes' 20 at once, 30 a minute) | any `docstore.Limiter` (`docstore.StoreLimiter` shares it between replicas), a gateway |
| `Config.ClientIP` | who the client is for the limiter | the remote address; `TrustedProxies(cidrs...)` reads `X-Forwarded-For` only from your proxies | your own function |

Over the limit the routes answer 429 with `Retry-After`. `cmd/docstore` reads
`GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES` (comma separated CIDRs) for the last row, and the same list names the
client for the login limiter: **behind a reverse proxy it must be set, or every client shares the proxy's address and
one login budget** (the server logs a warning at start). The login budget lives where `GOJIBERRY_DOCSTORE_LIMITER`
says. It exists because a flood of unauthenticated logins would otherwise fill the 10,000 pending-login bound and
keep real logins out (503) until the sweep clears it; a flood from many addresses can still do that, for up to
about 12 minutes.
**Two tabs, one first passkey.** A passkey made while signed in (an OpenID Connect user with no passkey account yet)
makes the account `Create`d under the identity's own ID, so two ceremonies begun before either finished (two tabs or
devices) name the same account. `Creator.Create` is a conditional create (`SaveIf` with no digest on an `AdapterStore`
over a `ConditionalAdapter`; the `MemoryStore` lock; without a `Creator` the `Service`'s own lock, one process
only; an `AdapterStore` over a non-conditional adapter falls back to a process-local lock too, so there the guarantee is per process), so exactly one wins and nothing is overwritten: the loser's credential is not attached to the account (its
authenticator holds a user handle the server never stored, so it could not sign in anyway). The loser gets `409`
`{"code":"account_exists"}` (`passkey.ErrAlreadyCreated`, `client.ErrAlreadyCreated`), no longer the `410` of an
expired, used or unknown challenge, which stays `410`. `components.Passkey` shows it in the interface's language
(`passkey.alreadyCreated`: reload and add another passkey from the account; the passkey the second device made is not
used and can be deleted there). Signed out, every begin draws its own random account ID and handle, so two
ceremonies for one e-mail make two accounts (the address is an unverified label, not a key); nothing collides.
When e-mail verification arrives it must not merge or link passkey accounts on the bare e-mail label: passkey
sessions carry no e-mail, and two signed-out registrations with one address make two accounts by design.
**Shared ceremonies.** `StoreChallenges` keeps each ceremony as a record `passkey/challenge/<hash of the token>`
(the token is not in the name). `Put` is a conditional create. `Take` loads the record and consumes it with one
conditional step on the digest it read, `DeleteIf` or, on a store with only `SaveIf`, a swap to a used marker
(then deleted), so of any number of concurrent Takes, on one replica or many, exactly one succeeds; the rest, and
a replay, get `ErrBadChallenge` (410). Records are named `passkey/challenge/<expiry minute>/<hash>` and the token is
`<random>.<expiry minute>`; a token that is not exactly that (canonical digits, one dot, at most 128 bytes, a minute in
range) never reaches the store. The deadline is the stored `Expires`, checked on every Take (a replica's own
clock, so skew moves it by seconds). `Sweep` (every minute in the server, and once at start) lists the prefix and
deletes by name what is a minute past its bucket, used markers included (they keep their ceremony's name), reading
nothing (the server makes the first sweep before it serves, retrying a failed listing a few times). Only names that
are exactly `<prefix><minute>/<64 lowercase hex>` are swept this way; a record of the earlier layout
(`passkey/challenge/<64 hex>`) is deleted once the store says it is over 24 hours old (or, if the store gives no time,
once a read shows it unparseable or expired) and is not counted; every other name is foreign and never touched.
   The cap (10,000 by default) and replicas. Three strategies were weighed: (a) count names from the listing the
sweep already makes; (b) sharded counters moved by compare-and-set, rejected: a hot record written on every Begin
and Take, retries under load, and a crashed replica leaks its increments; (c) the per-client `Limiter` on the Begin
routes as the real bound. The server uses (c) with (a) as the backstop: each replica counts from its last listing
plus its own writes, so at the cap `Put` answers `ErrBusy` (503) with no sweep and no `Load`, and a live challenge is
never evicted. **Worst-case overshoot** of the cap = the ceremonies all replicas can write between two listings =
(per-client rate per minute) x (distinct clients) x (sweep interval in minutes), and never more than
(replicas - 1) x cap; with the default budget (30 a minute on average per client and route group, burst 20) and the
one-minute sweep a client can write about 50 records in the first interval and 30 in each later one above the cap
(per replica when the limiter is per process, `GOJIBERRY_DOCSTORE_LIMITER=memory`), and the next listing stops it on
every replica. The count a sweep stores keeps the Puts and Takes of its own process that happened while it listed.
   `GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES=store` on a store without conditional writes prints a multi-line warning
(cause, effect, fix) and keeps ceremonies in each process; `store-required` makes the server refuse to start instead,
like `GOJIBERRY_DOCSTORE_SESSIONS`. Memory mode with `GOJIBERRY_DOCSTORE_REPLICAS` above 1 warns that ceremonies are
not shared.
**Shared sessions.** The bearer session a sign-in returns (a passkey one, minted through `Manager.StartSession`, or
an OpenID Connect one) is held by the `auth.Manager`'s `SessionStore`. By default that is in the process, so behind
several replicas a token is known only to the replica that issued it. With `GOJIBERRY_DOCSTORE_SESSIONS=store`
(`auth/sessionstore`) the records are in the store: `auth/session/<expiry hour>/<sha256 of the token>` holds the
user (ID, name, username, picture, verified e-mail and its flag), the issue time and the expiry; the token is never
stored, so someone who reads the store cannot sign in with it. A request reads its record (no cache: a logout on
any replica is seen by the next request on every other; a cache would keep a logged-out token alive for as long
as it lives): one store read per authenticated request, two under `GOJIBERRY_DOCSTORE_EXPIRY_TTL` (the
`ExpiringAdapter` reads the deadline record too). A well-formed random bearer token always reaches the store (one
read per `/docs` request that carries an `Authorization` header); the login limiter does not cover that. Expiry is the stored `Expires` checked on every read and does not slide (as in memory), so
`SessionTTL` is also the absolute age. `auth/state/<expiry minute>/<hash>` holds a login state (provider, return
URL, PKCE verifier, nonce), consumed once by `DeleteIf` or, where that is missing (an encrypting adapter has none
for an encrypted id), by a conditional swap to a used marker, so a replayed `state` fails on every replica and of
any number of concurrent callbacks exactly one wins. The sweep deletes by the expiry number in the
record name without reading anything; used markers go the same way. It lists the login states every minute and the
sessions (hour-wide buckets) at the first sweep, when the hour changes and at least every 10 minutes, so many
replicas make few LIST calls on an object store. At most 100,000 sessions and 10,000 pending
logins wait (`auth.ErrStoreFull`, 503); a live record is never evicted, and each replica counts from its last
sweep, so replicas can overshoot by what they write between sweeps. A store that cannot answer signs nobody in
(401), and a logout it cannot do is a 503, never a silent success. **Limit:** the PKCE verifier and the nonce sit in the state record, so anyone who can read the store (or decrypt it, in encrypted mode) can complete a stolen authorization code until the state is consumed or expires (10 minutes); the store is already inside the trust boundary for session records, which name the user. The records are not in `encrypt.DefaultSkip`
(they hold names and e-mail addresses and the PKCE verifier): under an encrypting adapter they are encrypted like
any file (`passkey/` and `auth/` are not skipped), `RepairAll` treats each as an item of its own, and the document
API refuses both prefixes in every mode. `store` on a store without conditional writes prints a warning that
names the fix and keeps sessions in memory; `store-required` refuses to start. Clock skew moves expiry by the
skew (limits register item 11). What remains per process is listed in the roadmap, backlog item 55.
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
  Step 2 for a whole store is `Sharing.RepairAll(ctx, RepairAllOptions{Resign: true})` (with the new
  `Signer` on the `Adapter` and the `Sharing`, both keys listed): for every document, and every
  other encrypted item, with a frame or a record signed by a key other than the `Signer`'s it signs
  the record again (`Sharing.Resign`) and then seals every file again, so the new key signs all of
  it; it is idempotent and restartable, and `DryRun` lists what it would do. The report says what
  each key signed (`BySigner`: frames, documents, records per key, found by trying each listed key,
  as the record's own `signer` field is only a hint) and which documents were not yet moved
  (`OtherSigner`, empty after a `Resign` run): when it is empty and `BySigner` shows only the new
  key, no document, frame or record is left that step 3 would refuse. The signed `keys:index` is not
  one of the documents: it is reported apart (`IndexBy`, `IndexOther`) and `RepairAll` with `Resign`
  signs it again (`Sharing.ResignIndex`, `IndexResigned`; `IndexErr` says if that failed), but a run
  without `Resign`, or a `DryRun`, only reports it. So run `Sharing.ResignIndex` (or `RepairAll`
  with `Resign`) before step 3 and check `IndexOther` is false: an index still signed by the old key
  stops verifying once that key is removed, and the strict reads that use it fail. `Resign` needs
  the `Sharing`'s `Signer` to be the same key as the `Adapter`'s (a document is otherwise skipped,
  `SkipNoSigner`). Do not use `Resign` in a store with several legitimate owners: it moves
  everything to the one signature. What it does not do is refuse the old key's frames written after
  a revocation while older ones still verify: a frame has no key id or time and a revocation mark in
  the record could be rewritten by the revoked key, so that needs a format change (roadmap, backlog
  item 5).
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
     **Upgrading a whole store.** `Sharing.RepairAll(ctx, RepairAllOptions{Progress, Limit, After, DryRun, Resign, EncryptPlaintext})` is
     `Repair` over every document: it lists the store once, groups the files by document, reads the head of each file
     (up to 128 KiB, which saves reads only for files larger than that: a smaller file is read whole either way; through the optional `docstore.HeadReader` that the memory, localfs, sqlite, postgres, s3 and blob
     adapters and `ExpiringAdapter` implement; a store without it, such as MongoDB, is read in full, a read of the whole
     store per run; the frame header is inside the age stream, so the first 64 KiB chunk is the least that can be read,
     and a file whose head does not settle it is read in full), and for each document with a v1/v2 file calls `Repair`, so a
     document is sealed again signed as a unit (and its record then says `signed`; a never-shared document with no
     `keys:` record gets one, signed, with `Signed` set, so a later unsigned frame in it is forged). Any other encrypted
     item (`expiry:`, `collab:`, `passkey/`, `public/` ...) is sealed again signed by `Adapter.Rekey`, one by one. A
     document is repaired as a unit or not at all; one that fails or conflicts while being written may be left partly
     rewritten (some files v3, the record not yet flipped), which loses nothing, and a rerun finishes it. It returns a report: `Total`, `Signed` (all v3 already), `Repaired`, `Files`, and `Skipped`
     with a reason per document: `no-signer` (the Adapter has no `Signer`; nothing is written), `no-key` (a file is
     encrypted to keys the keyring does not hold, for example a protected document of another owner),
     `unreadable`, `forged` (a bad v3 signature, or an unsigned file under a record that already says `signed`),
     `downgrade`, `plaintext`, `conflict`, `error`. It does not use `AcceptUnsigned`: vouching for a file stays a
     per-document decision of the owner. It never downgrades or deletes, is idempotent (a second run repairs 0)
     and restartable (documents go in id order: a rerun starts at the first id and finds
     the finished ones `Signed`, or carries on after the cursor: the report's `Next` is the last group it went through
     (`After` itself when it went through none, so a run cancelled before its first group with `After` `""` also has
     `Next` `""`: `Finished` is true only when the end was reached, and `Next == ""` with `Remaining == 0` means finished),
     and `RepairAllOptions.After` starts after it, so `Limit` and `After` walk a huge store
     in slices (groups sorting before the cursor are not revisited, so a file saved there afterwards is missed until a run
     from the start; `Left` covers only the groups after `After`); the caller keeps the cursor, nothing is saved; run it again after a cancel, `Limit`, or once the missing key or signer is available), and a Save by another writer meanwhile is safe for the reasons `Repair` is (the lock and
     the conditional write). `DryRun` lists what it would do (`Todo`); `Report.Left()` is zero when every encrypted file that `Skip` does not leave readable (documents and the other
     items above) was in a signed frame when it looked; files `Skip` leaves in the clear are not looked at. A file saved unsigned after the scan by a client with no `Signer` is found by the next run.
     **Files stored in the clear.** By default they are reported (`plaintext`) and left alone. With
     `EncryptPlaintext` (which needs `Adapter.AllowPlaintext` and a `Signer`) the same run encrypts them, signed, by the
     same Repair and Rekey paths (same lock, conditional writes, all or none per document; ids `Skip` leaves readable are
     never touched; `DryRun` lists them in `Todo`; the report counts them in `Encrypted`; a second run finds them `Signed`).
     This changes the stored bytes of documents that another reader (a tool, another service, an older release) may
     expect in the clear, and nothing reads them afterwards without the keys; anything that does not start with the age
     header counts as clear, including a damaged file that is not age. A host that reads `passkey/`, `auth/`, `public/` or
     `collab:` records raw from the same backend would break, because those are encrypted too (they are not in
     `DefaultSkip`); `Skip` is the control that keeps an id out. Run it with `DryRun` first and read `Todo`.
     There is no command: the docstore server has no owner signer or identity, so the owner's client calls it.
     **The window before `signed`, and the way out of it.** A re-key writes signed files and only
     then changes the record to say `signed`. A client with no `Signer` that read the record before
     that can save an unsigned (v2) file of a server-level document in between, and a plain `Save`
     does not change the record's seq, so the change to `signed` cannot be made conditional on it.
     Two things handle it. (a) Immediately before setting `signed`, the re-key looks at every file
     again; if one is not v3 (it was saved meanwhile) it is sealed again (another pass) first.
     That leaves only the few steps between that look and the write of the record. (a2) Nothing
     in that window is silent. After it writes `signed` the re-key looks at the files once more:
     one still unsigned makes `Repair` (and `Add`, `Remove`, `SetProtection`, `Recover`) return
     `ErrUnsignedAfterFlip` (wrapping `ErrForgedFrame`, naming the files). A client with no `Signer`
     that wrote its file and then finds the record says `signed` gets `ErrNoFrameSigner` from its own
     `Save`/`SaveIf` (it re-reads the record after an unsigned write). Any such file is after the
     owner's last look and before the flip (found by the second look) or before the writer's re-read
     (reported to the writer), so one of the two is told. With `Adapter.RequireSigned` a client
     that cannot sign does not write an unsigned frame at all (`ErrNoFrameSigner`), which closes the
     path for every client the host runs that way. What stays inherent: a client with no `Signer` and
     no `RequireSigned` can still write an unsigned frame until the record says `signed` (it cannot
     sign, and a plain `Save` does not change the record, so the flip cannot be conditional on it),
     and anyone who knows a recipient's public key can write one in that window; it is reported,
     not prevented, and the owner resolves it with `AcceptUnsigned` or by deleting the file.
     The second look also writes the file ids into the signed record (`unsignedLate`, with a time; the record stays
     `signed` and is not pending), so `Sharing.UnsignedDocs` lists the document for any client and after a restart
     (`PendingDocs` does not: nothing is pending; `Saver.UnsignedCheck`, `encrypt.UnsignedHook`, puts the ids in
     `VerifyReport.Unsigned`). Like `PendingDocs` it is a full scan: it loads and verifies every `keys:` record, so
     run it rarely (Verify, an admin page), not per request. The record keeps at most 64 file ids (the first by id)
     and the total (`UnsignedDoc.Count`); the error text names the same 64 and "... and K more". A later mark
     replaces the earlier one and keeps its time. The mark is written only onto the record the re-key just wrote: if
     another record is there by then (an older one put back, or a concurrent change) it is refused, the error says the
     document "could not be marked", and the replayed record is not signed again. The refusal wraps `ErrDowngrade` because a replayed record and a concurrent legitimate seq bump are indistinguishable at that point, so a caller seeing `ErrDowngrade` together with `ErrUnsignedAfterFlip` should rerun the finishing operation or `Repair` before treating it as an attack. Clearing the mark is best effort: if the
     files cannot be read when the re-key ends, the mark stays and the re-key still succeeds. `RepairAll` skips it as forged, naming the same document. `Repair`
     or `Recover` with `AcceptUnsigned` clears the mark once no file is unsigned. The field is covered by the record's
     signature, so a store writer cannot add or clear it; older builds ignore it when they read and drop it when they
     write the record, which loses the listing but not the refusal of the file.
     (b) What a save
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
     remembering what it saw); the showcase (5.7) keeps an in-memory floor, which cannot degrade, so it needs
     neither; a host with a `LocalStorageSeqFloor` must set the handler or poll `Err()`.
     **Runbook, a damaged floor file** (`FileSeqFloor` says "damaged" and names the file; nothing is
     overwritten): move the file away or delete it, then run `Sharing.VerifyIndex`: the floor starts
     empty and the index raises it for every listed end-to-end document. What only the lost file knew
     (a document the index does not list) has no floor until its record is read again; that is the
     protection a fresh client has.
  4. `Saver.Save` of a document its catalog says is confidential calls `Saver.Rekey` (e2ee) first,
     which asserts the level on every save of a document whose files are still frame v1.
  **Frame v1, v2 and the upgrade.** Files written before frame v2 have no level or seq; they are read as
  before (no check), and the first `Save` or re-key of a document writes a signed v3 (`Sharing.Repair(doc)`
  upgrades all of its files at once, `Sharing.RepairAll` every document of a store and reports what it could not). Frame v1 cannot detect a downgrade: the first save after upgrading
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
   fingerprint is typed. *Left:* wiring it into the real application (the showcase has it, 5.7: the owner's keys, how they
   unlock, the People tab) and the "verified e-mail" mark of C.4 (done, see step 4b).
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
   without `OnRecoveryKey`. The showcase supplies these callbacks (5.7), like `examples/people-keys-demo` (see 4b);
   `CheckRecoverableOwner` is called when a document is made confidential (step 5, `ConfidentialCheck`).
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
   - **A crash between an owner-state log entry and its record is mended from the log.** Enroll, rotate and
     the reset calls append the entry first and write the enrollment record second. Before every publish,
     revoke and owner operation, and from `ReconcileEnrollments` (start-up, then `RunSweep` hourly, and again
     once `ReconcileGrace` (2 minutes) has passed after a pass that found an entry too young to judge, so a
     restart inside the grace does not leave the mismatch standing), the directory replays the address's
     entries with `AuditAddress` and completes the record when it is behind (younger entries answer 503:
     they may be in flight on another replica). The log names addresses by pseudonym and the owner
     signatures name the e-mail, so the pass finds the addresses to replay from every e-mail the store
     holds a record for, an enrollment record **or a published key**; an address with entries and neither
     (a first enrollment that crashed for an address with no key) is *unmapped*: it is listed (see
     *Unmapped addresses* below), warned about once per change of the count, and mended when somebody
     operates on it, or by the operator reset.
   - **Unmapped addresses (operator procedure).** The warning in the log reads `N address pseudonyms have
     owner-state entries in the log and no record or key the directory can name them by`; it is logged at
     start-up and again only when N changes (the start-up counts as a change from none, so a first count of
     0 is silent and a first count above 0 warns; later, going back to 0 logs a one-line all-clear). List them, with the credential of the operator reset
     (`GOJIBERRY_DOCSTORE_DIRECTORY_ADMIN_TOKEN`, sent as the header `X-Gojiberry-Admin-Token`; a wrong one
     counts against the same failure budget, 10 a minute per client, then 429):
     `curl -H "X-Gojiberry-Admin-Token: $TOKEN" https://HOST/admin/directory/unmapped` answers
     `{"count", "addresses": [{"address", "first_seq", "last_seq", "first_at", "last_at", "age_seconds"}],
     "truncated", "computed_at"}`. The list is computed on each call from the log and the store, holds at
     most 100 addresses (`AdapterDirectory.UnmappedCap`; `count` is whole and `truncated` says it was cut),
     oldest first, and holds only the pseudonymous hash, never an e-mail. In Go: `AdapterDirectory.UnmappedAddresses`.
     To resolve an address, either (1) the owner operates on it: any of Enroll, RequestReset or CancelReset
     (`POST /directory/enroll/challenge` then `/directory/enroll`, `/directory/enroll/reset`, `/directory/enroll/cancel` for the signed-in
     owner) first completes the record from the log, after which the address drops off the list; or (2)
     the operator, who cannot know the owner, resets it by its hash:
     `curl -X POST -H "X-Gojiberry-Admin-Token: $TOKEN" https://HOST/admin/directory/reset-enrollment/<address>`
     with `<address>` the 64 hex characters from the list. That logs a signed `operator-reset` entry that
     watchers are told about, and the address then counts as settled. Leaving it is safe: nothing breaks, the
     audit of that address keeps showing the enrollment the log recorded, and the owner's next operation mends it.
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
     again, and the tombstone is pruned (by `Sweep` and by a later publication) **together with the copies
     the revoked key left under a retired directory key's name**, unless one was published again after the
     revocation: an older live copy there would otherwise become the newest record the moment the tombstone
     went, and bring the revoked key back. The fingerprint in the path is the one lookup shows, or its path
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
     draining the service. (The OpenID Connect login routes' budget, `login|<address>`, is one of these and uses the
     same variable.) `GOJIBERRY_DOCSTORE_LIMITER=memory` (default) keeps the budgets in each process;
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
   - **Client.** `docstore/directoryclient` (native and js/wasm; net/http, age and Ed25519, plus the
     `docstore` and `docstore/encrypt` packages for the wire types, formats and audit, so it is not a
     minimal standalone client; `GOOS=js GOARCH=wasm go vet` covers it):
     `Publish` (challenge, decrypts the nonce with the caller's identity, attests an ssh-ed25519 key, signs
     as owner when enrolled), `Revoke`, `Lookup` (re-checks attestations and owner signatures itself; `OwnerSigned`/`OwnerPinned` only for a key
     somebody confirmed: pinned in `Client.Pins` by the host or user, enrolled by this client itself, or in the
     audited chain of its own address; the first key seen for an address is remembered in `WatchState.Seen`
     and shown `OwnerUnpinned`, and the same key on later lookups `OwnerFirstSeen`, which is consistent
     but **not** confirmed: a server that showed the wrong key the first time shows it every time; a different
     key, also a second one in one answer, is `OwnerChanged`), `Enroll`, `Rotate`, `RequestReset`,
     `CompleteReset`, `CancelReset`, `Enrollment`, `LogAddress`, `Head` and `WatchLog` (fetches the log
     since the last position kept in a `WatchState`, checks the head's signature against pinned keys, the
     head against what it remembers, the chain and every signature, audits the entries of the address and
     reports those it did not make). With no saved position (a first run, or after a filtered read) the
     answer must begin at the entry asked for, and at the zero hash when that is the start of the log, or
     it is `ErrLogCut` (the start was withheld). An entry the client made (`WatchState.Made`: the server's
     number plus the operation, fingerprint and signature the client sent) is held to what the client did:
     another operation or key at that number is a violation and the entry is not taken as the client's own
     (its pseudonym is not pinned), so the server cannot choose the address the watcher is held to by
     choosing a number. A filtered answer for an address the audited state says is enrolled is
     `ErrFilteredEnrolled` (the owner of an enrolled address is shown the whole log). A filtered run never
     saves a cursor past the signed head, so the switch to the full read chains every entry after the saved
     head; an entry read again that is pending with another hash than the one remembered is `ErrLogCut`. A
     void that arrives in a later run than the entry it cancels is shown (`WatchReport.LateVoided`).
     `Enroll`, `Rotate` and `CompleteReset` pin the key the client made, never the one in the server's
     answer. Tested against the server with `httptest`.
   - **Host.** `examples/people-keys-demo` uses it: `AddMyPasskey`, `CreateRecoveryKey` (shown once) and
     `Directory` (`directoryclient.Lookup`, so the marks are the client's own), plus a "my directory entry"
     section: publish my passkey's key (proof of possession with the passkey's own PRF identity), revoke
     it, enroll an owner key (made in the browser and kept in `localStorage`), and watch my address
     ("someone published a key for your address"). Its e2e (headless Chrome, virtual authenticator with
     PRF) covers the second-key message, the recovery key shown once, add my passkey, the verified mark
     from a lookup, and the whole owner flow: publish, the foreign publication reported, enroll, the
     owner-signed mark, the session alone refused, a forged log entry reported, and revoke.
     **The showcase (5.7)** has the People tab with a directory picker and "Publish my keys" through its own
     `dirclient` (plain, session-authenticated publish, no owner-signed mark). *Left:* wiring this section's
     `directoryclient.Client` there (the signed-in provider session's token and the pinned log keys, `Lookup` with
     `DirRow.OwnerSigned` and `OwnerEnrolled` copied from the `Key` rows, the three buttons of the demo's `myEntry`
     section, a `WatchLog` on sign-in with its `WatchState` in storage); publishing with proof of possession needs
     the owner's signing key, and the showcase holds only the owners' public keys (a passkey would need another
     assertion for each signature).

   *Limits that remain, stated plainly.*
   0. **What the client can and cannot confirm.** `OwnerPinned`/`OwnerSigned` rest on a pin somebody
      confirmed (out of band, or by the client's own enrollment) or on the owner's own audit; trust on first
      use (`OwnerFirstSeen`) only shows that the answer has not changed since the first lookup. A client with
      no `WatchState` has no memory at all (every lookup is a first sight). An entry the client made is
      matched to the log by the number the server gave, the operation, the key and the signature; for an
      address that is not enrolled the signature is absent, so a server holding the directory key could still
      answer with the number of another address's entry for the *same* public key (it can publish any public
      key under any address), which pins that entry's pseudonym: enroll an owner key (its signature is
      checked too) to close it.
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
      narrows it to a domain); reads of the log spend the same per-IP and whole-server shard budgets as
      lookups (only their per-user budget is separate), so heavy log polling also spends the lookups of
      everybody hashed to the same shard. The per-IP lookup budget and the **failed-admin budget of the
      operator route are per client address**: the connection's address (`RemoteAddr`) by default, which
      behind a reverse proxy is the proxy's for everybody, so one budget is shared and an attacker's wrong
      admin tokens can lock the operator out for a minute at a time. Set
      `GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES` (the same list the passkey routes use; embedders call
      `Server.WithDirectoryClientIP(passkey.TrustedProxies(...))`) to have `X-Forwarded-For` believed from
      those proxies only. Unauthenticated floods are the gateway's to stop.
   7a. **A replica whose clock is ahead hurts availability.** Entry times never go back along the chain, so
      one entry written by a replica whose clock is ahead (by more than the 10-minute owner-signature skew)
      dates every later entry at least that late, whichever replica writes it: owner signatures made with
      the right time then look "dated far from the entry" to an auditor, entries read as "dated in the
      future" until real time catches up, and resets measure from the late times. Keep the replicas' clocks
      in step (NTP); a replica whose clock is behind is harmless (it writes the predecessor's time).
   8. A store without conditional writes carries the directory for **one** process only (the directory
      refuses to start with `GOJIBERRY_DOCSTORE_REPLICAS>1` there, and warns with one). The cap of 20 keys can
      be overshot by a key or two by racing replicas even on a conditional store.
   9. A revoked key can be published again after the window (24 hours by default), and revoking does not
      touch documents already encrypted to it.
   10. The real app does not wire these controls yet (the demo and the showcase do, the showcase without
       proof of possession, see 5.7; the follow-up for the real app is the note above). The browser adapters do not implement
       `PrefixLister` (they fall back to listing the store). `ConditionalDeleter` is not implemented by the
       s3 backend, and the blob backend has no conditional writes at all (`GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES=store`
       warns and falls back to per-process ceremonies on it; `store-required` refuses to start). On s3 `Take`
       consumes through the used marker, and the sweep deletes by name.
       Shared passkey ceremonies (`store` mode) keep records under `passkey/challenge/`. Under an encrypting
       adapter they are age-encrypted (`DefaultSkip` does not exempt `passkey/`); on a plain store anyone who can
       read the store sees the pending registration's email and name. `Put` at the cap (10,000) returns busy without
       scanning or reading; the background sweep (every minute) frees room by name and reads nothing, so a used marker
       that outlives a failed delete goes with its bucket. Across replicas the cap can be exceeded by at most the
       writes of one sweep interval (see B.1); the per-client limiter is the real bound.

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
      files, see C.5 and 5.8). A confidential
      document's files are wrapped **only** for people's keys (`People`: the owner's keys and the
      signed recipient list); `AtRest` (C.1) is never consulted for them, and `ServerRecipients`
      are removed from the list even if a deployment put the server key among the owners, an age
      key or an SSH key (compared by key, not identity; a recipient of a type that cannot be
      compared is `ErrUncomparableRecipient`. An SSH key is matched by its 4-byte age key tag plus, when the recipient came from `ParseSSHRecipient`, the SHA-256 fingerprint of the full key, so two distinct keys with the same tag are not taken for one; a raw recipient has only the tag, and a tag match then counts as the same key, which only ever drops a key, never admits a server key; none left: `ErrNoRecipients`, nothing is saved). Server-level documents and non-document
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
      level and seq the record had (5.8), `Save`, `Load` and the re-keys report `ErrDowngrade` and
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

   7. **The showcase's encrypted store (done): `examples/canvas-editor-showcase`.**
      Encryption is **off by default**. In the Save tab, "Encrypt this store" wraps the browser target
      (`localStorage`) with `encrypt.Adapter` over a `ConfidentialKeyring`; the code is the `encstore` package (no
      browser code, tested natively: `encstore.go`, `transaction.go`, `bind.go`), `encpanel.go` (the screens),
      `encdir.go` and `dirclient` (the key directory). The final state, in one place:
      - **Owner keys, chosen at setup.** (a) a **passkey** through PRF, with no server: `client.CreateLocal` makes a
        discoverable credential for the origin (a random challenge, the credential ID kept in `localStorage`, `ErrNoPRF`
        when the authenticator cannot do PRF) and `client.DeriveKey` asks it for the secret, which becomes an age key
        (`PRFIdentity`) and, through `SignerFromAge`, the key that signs the recipient lists; (b) a pasted **SSH
        public key** (ed25519 only: it also signs the lists, an RSA key cannot), unlocked later with the private key
        file; (c) a generated **recovery key**, shown once (copy, download) and never kept.
      - **A passkey alone puts EVERYTHING at risk, and the form says so.** In this store every document is
        encrypted to the owner keys (there is no server to hold another copy), not only the confidential ones, so
        losing the only passkey loses the whole store. `Store.Enable` refuses passkeys alone
        (`ErrPasskeyOnly`) unless the person ticks an explicit acknowledgement (`Options.AcceptPasskeyOnly`); the
        form strongly recommends a recovery key. A *confidential* document additionally needs a key that is not a
        passkey (`CheckRecoverableOwner`), which the People tab offers ("Create a recovery key").
      - **Every change of the keys is a transaction** (`encstore.Store.run`): turning encryption on, adding a key,
        removing one, replacing a passkey, turning encryption off. The order is: (1) check the room (age adds a
        header to every file and a stanza per key: `NoRoomError`, before anything is written) and that every file
        opens with the keys held (`UnreadableError`); (2) write a **journal record** into `Config.Pending` (the
        operation and the owner list it is making current, signed by the keys that make the change); (3) convert each
        file through a recorder that keeps the original of every file before it is written and **reads each write
        back** (a store that says "written" and keeps something else is caught); (4) verify: every file is in the new
        state and opens with the new keys to the content it had; (5) commit: only now is the new key list made
        current in `Config`. Any failure in 3 to 5 puts the originals and the old `Config` back; a store is never
        half on. The page holds the storage's own lock and a gate that `Adapter()` writes pass through while a
        change runs, so a save made meanwhile waits (Disable: it then finds encryption off, and nothing is left as
        ciphertext in a store that no longer reads it). Failure injection tests (`transaction_test.go`) fail each
        write, each journal write, each commit, a lying store and a quota, in each operation.
      - **A crash in the middle is recoverable on the next load.** If the page dies (or the rollback itself fails:
        `ErrRollbackFailed`), the journal is what is left. The next load is *interrupted*: `Adapter()` reads (a file
        still in the clear is read as it is: the plaintext-tolerant adapter) and refuses writes, nothing can be made
        confidential, and the Save tab offers **Finish re-encrypting** (`Store.Recover(ctx, true)`) or **Undo the
        change** (`Recover(ctx, false)`). Each step is safe to repeat. Sealing files that are still plain needs no key;
        re-keying needs the store unlocked (a key of either list opens the files). Finishing checks the journal's
        signature first (`ErrConfigChanged` for a journal that none of the store's keys signed).
      - **A recovery key is shown BEFORE it is committed** (Enable and AddOwner alike). The page makes the key and
        shows its secret (copy, download); it becomes an owner key only after the person ticks "I have saved this
        recovery key" and presses the commit button. `encstore` enforces it: a recovery key without
        `NewKey.SecretSaved` is refused (`ErrSecretNotSaved`), so a key whose secret nobody saw never counts as
        recoverable (`CheckRecoverable`). A cancelled or failed commit leaves no trace in `Config`.
      - **Passkey ceremony failures are told apart.** `client.classify` maps the browser's rejections:
        `ErrTimedOut` and `ErrNotVerified` (both are also `ErrCancelled`, so old callers still work),
        `ErrInsecureContext`, `ErrUnsupportedAuthenticator`; a generic `NotAllowedError` is `ErrCancelled` and the page
        says "closed or refused" instead of staying silent. A passkey that was made and then not used (the derive
        prompt closed) is remembered in memory (`pendingCred`): the page offers **Try the passkey again** (no
        second passkey is made) or **Forget it**; an authenticator that makes a passkey with no PRF
        (`ErrNoPRF`, which still returns the credential ID) gets an explicit message naming the passkey to delete.
        The page cannot delete a passkey from the authenticator and says so. The WebAuthn timeout is set (60 s).
      - **What is kept where.** The owner keys' *public* halves, their signing keys, the seals and the journal are in
        `localStorage` (`showcase-enc:config`, outside the store's own prefix); the private side (the unlocked
        identity and signer) is **in memory only**. `Config` holds no secret (a native test greps its JSON for
        `AGE-SECRET-KEY`, the recovery secret, the PRF identity and the raw secret bytes). The secrets given to
        `UnlockPasskey` and `UnlockSSH` are cleared by them on every path (success, not a key, not a key file); the page
        clears the SSH file it was handed inside the asynchronous run.
      - **Lock closes the open document.** Lock forgets the keys **and** clears the editor (the text, the title, the
        document's comments and styles, find/spelling/outline/statistics caches) and the session (`Session.Clear`:
        the saved document's ID and name, the tag filter, the selection, the revealed names, the status), and the lists
        show the placeholders again. With changes that are not saved it asks first: **Save and lock**, or Cancel to keep
        working; "Lock and discard unsaved changes" is the one deliberate way to drop them. The note beside the button
        says exactly this. A reload starts locked.
      - **Config trust: seals, links and the anchor.** The owner list sits next to the store, so anyone who can write
        this origin's storage could replace it with one that adds their own key (and every later save would be
        encrypted to them). Two things bind it. (1) *Seals and links* (`bind.go`): each key that was unlocked when a
        list was made signs it (`Seal`: the list, signed with the key's own signing key: the one derived from the PRF
        output, the recovery key, or the SSH key itself), and each change is a signed `Link` (the list before, the list
        after, signed by a key that was an owner before). At unlock the key's seal must lead to the current list by a
        chain of links signed by owners of the list each leaves; otherwise the unlock is refused with
        `ErrConfigChanged` ("the key configuration on this device was changed ..."). A passkey or a recovery key always
        has a seal, so a list without one is refused whatever `Version` says; an SSH key is sealed at its first unlock.
        (2) *The anchor* (`anchor.go`): the store writes into the signed frame of every document file the hash of the
        owner list it was sealed under (`Binding.Hash`), and at unlock every verifiable frame (the newest 300 files are
        looked at) must carry the hash of the list in `Config` (or of the list a journal is moving from or to). That
        makes stripped seals, a first-generation list, an older valid config put back and a replaced list visible
        even to an SSH key with no seal yet. A deleted or unreadable config over encrypted files is not "encryption
        off": `Inspect` says `ErrConfigMissing` or `ErrConfigCorrupt`, and `Store.Restore` rebuilds a configuration
        from a key that opens every file.
        **The journal is authenticated too** (`authJournal`): a journal is whatever is in the storage, so finishing
        one (`Recover(ctx, true)`) needs a held key that is vouched for by its own seal chain, the Config's owners to
        be the list the journal says it started from, and the `Link` from that list to the journal's new list, signed
        by a key of the old list (its signature covers both lists, so a seal over an older list, or a link made for
        another direction, cannot be replayed as the journal of going back to it). Finishing with only the new key
        (the change cut short after every file was converted) needs the same link, signed by an owner of the
        Config, and the new key's own seal over the new list; the link is kept so the old keys still open the store.
        Undoing goes back to the Config's own list and needs no signature.
        A link already recorded in `Config.Links` is refused for finishing (it was completed once; the list may have
        come back to the same content since, as after adding and removing a key).
        **Residual limits, stated plainly:** (1) a store with no signed document file yet (an SSH-only store before
        its first save) has nothing to anchor, so its first-generation list and an SSH key with no seal are trusted on
        first use; (2) a rollback of the files together with the config to an older consistent snapshot is not
        detected, and a journal replay is refused only while the Config still records its link (a link carries a
        signed random nonce, and a finished change keeps it in `Config.Links`, the last 32): a replay of a link
        made before the nonce existed over a list that came back to the same content, or of one that has aged out
        of those 32, still passes; (3) an attacker who replaces every file as well as the config leaves nothing to protect, only what
        is saved afterwards is exposed; (4) nothing here protects against code running in this origin. A real
        application keeps the signers and the list out of the reach of the store's writers.
        **Another tab** of the page can change the keys: before every write the stored config is compared with the one
        this tab loaded (`ErrStaleConfig`), and a tab that finds it changed locks itself and, at unlock, says the page
        is out of date.
      - **Removing and replacing keys.** The People tab lists each owner key with **Remove this key** and, on the
        passkey, **Replace this passkey**, each asking first (what it does, and that whoever held the key keeps what
        they already read or copied and any copy of old ciphertext). `RemoveOwner` encrypts everything again to the
        keys that remain and signs every document's list again with one of them (`Sharing.Resign`: the list a removed
        signer signed would no longer verify), as one transaction. It is refused for the last key
        (`ErrLastOwner`), when it would leave only passkeys where there was a way back that is not one
        (`ErrWouldLoseRecovery`), and while the store is unlocked with the key being removed alone
        (`ErrUseAnotherKey`: another key's signing key is needed); `CanRemoveOwner` says why, and the button shows the
        reason. `ReplaceOwner` adds the new passkey and removes the old in a single transaction (the page restores the
        slot that remembers which passkey to ask if the swap fails, `client.SetLocalCredential`).
      - **Erase the encrypted store.** When every key is lost nothing can bring the documents back; the only way
        out is to start again. "Erase the encrypted store..." in the Save tab explains that, asks a question, then asks
        for the word typed (ERASE, in the interface's language), and only then deletes everything the browser store
        holds (documents, versions, folders, the catalog, the lists) and forgets its keys. It touches no other target
        (memory, a folder on this computer, a server) and nothing else in the browser's storage, needs no key, and
        can be run again after a failure. It cannot be undone.
      - **Everything saved is encrypted, not only confidential documents.** There is no server, so the "at rest"
        level is the owner's own keys (`ConfidentialKeyring.AtRest`); a document made confidential also hides its
        name and tags and is wrapped for people's keys only, and can be shared with another key from the People tab.
        The catalog is NOT encrypted: the names, tags, sizes and times of ordinary documents stay readable to anyone who
        can read this browser's storage (the hint says so). Turning encryption off decrypts everything back
        (`ErrConfidentialLeft` is enforced by `Store.Disable` from the signed lists, as well as by the page).
      - **Locked** the store does not write: a save fails with "Unlock the store..." (an error that still wraps
        `docstore.ErrLocked`), opening a confidential document does the same, and its list entry is the placeholder
        with a Reveal button; unlocking shows the real names (auto-reveal), opening one reveals it too.
      - **Lock marks, switches and the placeholder's language.** `components.DocList` draws a lock with the label
        "Confidential document" on confidential documents (`Doc.Confidential`, `NameHidden`) and "Confidential folder"
        on folders, and each row has "Make confidential" / "Make not confidential" with a question that says what
        changes. The buttons have accessible names of their own (`Make Budget confidential`; two hidden rows get a
        short ID) and `#enc-status` is a polite live region. The placeholders `saver.ConfidentialName` /
        `ConfidentialFolderName` are **stored in English** (catalogs, packs, other clients read them) and **drawn
        translated**: the components render the placeholder through `tr()` whenever `NameHidden` is set (es, fr, de
        tested), and never store a translated string. `encstore` and `client` errors are sentinels; the page puts them in
        the interface's language (`tErr`, `passkeyFailed`), and the passkey prompt's relying party and user names
        (`enc.passkeyRP`, `enc.passkeyUser`) are translated too.
      - **Packs.** `encrypt.PackSealer(owners, identities)` returns `PackOptions.Seal` and `ImportOptions.Open` over a
        scratch store per call; `Session.PackKeys` hands them out while unlocked. The pack is sealed for the owner keys
        only. The e2e checks the exported bytes for the name and the text, then deletes the document and imports the
        pack (locked: "needs a key", left out; unlocked: confidential again). A pack sealed for **another** store's key,
        imported while this one is unlocked, says "sealed for another key" (`encrypt.ErrWrongPackKey`,
        `docstore.ErrWrongKey`), not "unlock the store".
      - **The key directory.** With `?docstore=https://host` on the page's address (or the settings field in the People
        tab, kept in `localStorage`, with an optional session token) the People tab has a **directory picker**: it finds
        people by e-mail through `components.Recipients`' `Directory` (`GET /directory/lookup`), marks a key verified only
        as the server answered (and names the host that vouches), and offers **Publish my keys** (`POST
        /directory/publish`: a passkey's and an SSH key's public keys, never the recovery key). The calls go through the
        showcase's own `dirclient.Directory`, a small interface over the routes. **The address is a trust decision:** a
        link only *offers* an address, nothing is sent to it until the person confirms it in a dialog that names the
        host; it must be https (http only for a host on this machine), it is confirmed again whenever it changes, and
        a session token belongs to one address and is dropped when the address changes (the token field is a password
        field). A server without the routes gets a clear message (`dirclient.ErrNoDirectory`). Tested natively
        against `examples/people-keys-demo/demoserver` (`dirclient_test.go`) and in a browser
        (`TestPeopleTabUsesTheKeyDirectory`). The page has no sign-in: a server that needs a provider session needs its
        token pasted (the demo server hands one out at `/demo/provider-session`, for demo servers only); the page must
        be allowed to call the server (same origin, or CORS on the server).
        *Left:* publishing **with proof of possession** and the owner-signed entry of the signed directory (#84:
        `directoryclient.Client`, the owner key, `WatchLog`) are not wired in the showcase. It holds only the *public*
        keys of its owners, and a passkey owner would need another WebAuthn assertion (PRF) for every signature, so
        the showcase publishes through its own `dirclient` as the plain, session-authenticated route and shows no
        "owner-signed" mark. The demo `examples/people-keys-demo` has the whole flow.
      - **Left (residual limits).** The trust limits of the key list are above. A `SeqFloor` here lasts the page
        (`encrypt.MemorySeqFloor`, which cannot degrade, so the showcase does not need `SetFloorDegradedHandler`: a
        host that moves to `LocalStorageSeqFloor` must set it or poll `Err()`). One local passkey per origin, two
        prompts to set it up (create, then derive). Another tab can write while a change runs: the store's own lock
        covers this page, the browser's web lock (where it has one) the rest. The journal's originals are in memory:
        after a crash the change can only be finished or undone from the state on disk (both are safe, neither can
        recover a file the browser lost). **Quota and capacity:** the room check estimates what age adds and what the
        storage can hold (`Store.Capacity`, in the storage's own units: `localStorage` keeps strings, base64 costs 4
        characters for 3 bytes, some browsers count 2 bytes a character), and a browser's accounting is its own, so a
        write can still fail (it is then rolled back); `NoRoomError` is raised before anything is written. The anchor
        looks at the newest 300 files only. A file no unlocked key opens blocks a change (`UnreadableError`): the way
        out is another key or Erase. RSA SSH keys cannot be owners, and a passphrase-protected SSH key must be opened
        by the person first. Directory publishing with proof of possession is not wired (above). The real app has none
        of this yet.

   8. **Downgrade and deleted-record detection from inside the envelope (done).** The design and the rules are
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
   1. ~~The showcase wiring and the key management UX~~ Done (5.7): the owner's keys, the "make
      confidential" switch, the recovery key shown before it is committed, the lock mark in the document list.
      Left there: the real application, and the points listed under 5.7's "Left".
   2. ~~Folder names are not hidden~~ Done, see 5.6 below (confidential folders). The showcase has the
      switch (5.7). Left there: the real app's switch, and the pack-count wording in `Session.DownloadPack`, which counts a folder whose
      name could not be sealed with the documents left out.
   3. What someone already opened or copied before a switch cannot be taken back (as with any
      re-key); with the hook the stored copies themselves are re-keyed. A store writer who
      deleted the `keys:` record first is caught from inside the files (5.8).
   4. ~~Key management for `Seal`/`Open` in the pack UI~~ Done in the showcase (5.7): `Session.PackKeys`
      gives the pack the owner keys while unlocked. Left: the real app. Without it
      `Session.DownloadPack` leaves confidential documents out and says how many, and
      `Session.ImportPackData` reports the ones it could not open.
