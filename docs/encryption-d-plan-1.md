# D. Plan, steps 1 to 4

This is part of the design [Encryption with SSH keys and passkeys](encryption-and-passkeys.md), which is split into parts so that each can be read on GitHub (one file of 147 KB was cut off). The index says where everything is.

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
   - **Show different people different logs** (a split view), unless a quorum of independent witnesses cosigned the log (roadmap, backlog item 12):
     a client that pins the witnesses refuses another log at the cosigned entry; without witnesses two auditors must compare the heads they saw.
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
     different clients (a split view; witnesses, when the host runs them and the client requires a quorum, catch it from the cosigned entry back). A record written straight into
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
     keeps their update; and at most `MaxRateLimitRecords` (100,000) are kept, the cheapest to lose evicted first (buckets that are
     full again, then the least spent budget, never a slow live caller just for being written first), so a flood of
     different IP-keyed callers cannot grow the keyspace without bound between sweeps; the sweep logs how many
     live callers got a fresh bucket when that happens.
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
     it, enroll an owner key (made in the browser and kept out of reach as a non-extractable key where the browser can), and watch my address
     ("someone published a key for your address"). Its e2e (headless Chrome, virtual authenticator with
     PRF) covers the second-key message, the recovery key shown once, add my passkey, the verified mark
     from a lookup, and the whole owner flow: publish, the foreign publication reported, enroll, the
     owner-signed mark, the session alone refused, a forged log entry reported, and revoke.
     **The showcase (5.7)** has the People tab with a directory picker and "Publish my keys" through its own
     `dirclient` (session-authenticated publish with proof of possession, through `directoryclient`; no
     owner-signed mark yet). *Left:* the rest of this section's `directoryclient.Client` there (the signed-in provider session's token and the pinned log keys, `Lookup` with
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
      has nothing to compare with; witnesses (a quorum of independent parties' cosignatures that a client requires) close this for the entries they cosigned.
   3. **Owner keys are the owner's to keep.** A lost owner key means a reset (a day's wait, in the log);
      a leaked one is the owner's own key leaking: rotate it. Records that predate the enrollment stay
      `Verified` but not `OwnerSigned` (and say so) until republished. An auditor needs the address to check
      the owner signatures (they name it); the log itself shows only pseudonyms.
   4. **Rotating the directory key**: a log pseudonym changes with it. While the retired key is listed, every
      start-up records, for each address the store holds a record or key for, a signed **alias** record
      (`directory-log/alias/<current pseudonym>`, pseudonyms only, no e-mail) from the current pseudonym to the
      older ones, and carries the aliases of earlier keys forward; so after the old key is removed from
      `..._KEY_PREV` the older entries still match their address, the owner still gets the older pseudonyms
      from `/directory/log/address`, and the entries do not show up as unmapped. Start the server once with
      the retired key still listed before removing it (a replica started later reads the aliases at its own
      start-up). An alias record that does not verify (altered, or signed by a key no longer listed) is
      ignored and counted in a start-up log line. An address the store holds no record or key for cannot be
      aliased and stays unmapped. If the old key leaked, remove it from `..._KEY_PREV` and republish, do not
      rotate through it.
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
