# Limits register

The limits that are **inherent**: no code change inside this repository removes them. Each entry says
what the limit is, why it is inherent, what we ship to narrow it, and who has to handle what is left
(the **host** that embeds the libraries, the **operator** who runs a server, the **user**). Limits that
are only unfinished work are not here: they are the [backlog from the limits review](roadmap.md#backlog-from-the-limits-review).

Every claim points at the design text that states it. Where this register and that text differ, the
design text is the one tested against the code; fix this page.

Sources: [encryption-and-passkeys.md](encryption-and-passkeys.md) (called "the design" below; its step
4b is "Limits that remain, stated plainly" for the key directory, step 5.7 the showcase),
[public-docstore.md](public-docstore.md) (section 7.5 "What is left", section 4 for expiry),
[saving.md](saving.md), [opendoc-format.md](opendoc-format.md), [testing-workflow.md](testing-workflow.md).

## Encryption and keys

1. **age has no sender authentication.**
   1. *What:* anyone who knows a recipient's public key can make a file that opens for that recipient,
      with any content and any frame.
   2. *Why inherent:* the format authenticates the payload to whoever holds the recipients' keys, not to a
      writer; public keys are public (an SSH key on a profile, the server's key).
   3. *Shipped:* every frame and every recipient record is **signed by an owner signer** (frame v3,
      `ErrForgedFrame`, `ErrBadSignature`, `ErrNoFrameSigner`); a confidential save without the signer
      fails closed. See [C.5](encryption-and-passkeys.md#c5-who-may-change-a-recipient-list), layer 1.
   4. *Who:* the host keeps the owner signers out of the store writers' reach and lists them in
      `OwnerSigners`; files still in unsigned v1/v2 frames stay forgeable until re-keyed; `Sharing.RepairAll` re-keys a whole store at once (documents, and the other encrypted items such as `expiry:` or `passkey/`) and reports what it could not; `Left()==0` covers the ids `Skip` does not leave readable, not files saved unsigned after the scan (backlog item 3, done). *Retired signer:* a revoked owner signer stays trusted until it is removed from every `OwnerSigners` list (there is no revocation list and no cut that lets older frames keep verifying: frames carry no key id or time, and a mark in the signed record could be rewritten by the revoked key); `RepairAll` reports frames and records by signer (`BySigner`, `OtherSigner`) and with `Resign` moves them all to the current signer before the old key is removed (backlog item 5, done reduced; the cut is a design follow-up there). *Window before `signed`:* a client with no `Signer` can still write an unsigned frame until the record says `signed` (inherent: it cannot sign and a plain save does not change the record); since backlog item 4 such a file is never silent (the re-key's look after the flip returns `ErrUnsignedAfterFlip`, the writer's own `Save` returns `ErrNoFrameSigner` if the record flipped under it) and an `Adapter` with `RequireSigned` and no `Signer` refuses to write one; the owner resolves it with `Repair(AcceptUnsigned)`.

2. **A rollback of the whole store is undetectable without a client floor.**
   1. *What:* replacing files, records and index together with an older genuine snapshot.
   2. *Why inherent:* every signature in the snapshot is genuine and every check inside it agrees; the
      store can always return an older version of what it held.
   3. *Shipped:* `SeqFloor` (`FileSeqFloor`, `LocalStorageSeqFloor`, `MemorySeqFloor`), the owner-signed
      `keys:index` and `Sharing.VerifyIndex` that raises the floor for every listed document, and a
      degraded-floor report (`ErrFloorDegraded`). See C.5, layer 3 and "What cannot be defended".
   4. *Who:* the host keeps a floor where store writers cannot write and wires `VerifyIndex` at sign-in;
      a fresh client has no floor and cannot notice.

3. **Whoever can write the store can block or delete.**
   1. *What:* delete or replace files and records, or damage them (availability), and read the catalog
      and the recipient lists (not secret).
   2. *Why inherent:* a store is somebody's disk; encryption protects content, not existence.
   3. *Shipped:* deletion of a record or of files that were sealed under a newer one is `ErrDowngrade`
      (not silent), the signed index and a floor leave evidence, `Sharing.Recover` restores on purpose.
      A document deleted together with its record and not listed anywhere leaves no evidence.
   4. *Who:* the operator (backups, access control on the store); the user (copies of what matters).

4. **Removing a key does not revoke what that key already read.**
   1. *What:* the holder keeps plaintext they opened, copies of old ciphertext and the data keys they
      unwrapped.
   2. *Why inherent:* secrets cannot be taken back once shown.
   3. *Shipped:* re-keying on removal (a new data key for what remains, `Sharing.RemoveEverywhere`), a
      warning that names the number of documents, the same warning in the showcase. See
      decision 6 of [C](encryption-and-passkeys.md#c-decisions) and the showcase's "Removing and
      replacing keys" in step 5.7.
   4. *Who:* the user decides whether a document that key saw must be considered disclosed.

5. **A lost passkey and a lost recovery key lose the data.**
   1. *What:* with no remaining recipient key, nothing opens the documents.
   2. *Why inherent:* the keys never leave the person; there is no escrow by design.
   3. *Shipped:* a passkey alone is refused as the only way in (a second recipient, a recovery key
      shown once), `ErrWouldLoseRecovery`, "Erase the encrypted store" to start again. See
      [A.3](encryption-and-passkeys.md#a3-passkeys-as-a-key) and decision 3 of C.
   4. *Who:* the user keeps the recovery key offline; the host shows the warning and does not hide it.

6. **A verified e-mail proves the account, not that the key is safe.**
   1. *What:* the directory says "the provider says this account's verified e-mail published this key"
      (and, with the proofs, held it); it does not say the key is not the account's compromised one.
   2. *Why inherent:* the e-mail is a label from a provider; only the person can confirm a fingerprint.
   3. *Shipped:* separate marks (`Verified`, `Held`, `Attested`, `OwnerSigned`), the fingerprint
      confirmed by the owner when adding a recipient, the transparency log. See decision 4 of C and
      the tables in step 4b ("What the directory proves, and what it does not").
   4. *Who:* the user confirms fingerprints out of band for anything sensitive; the host shows the
      weakest mark honestly.

## The key directory

7. **The directory server can withhold, show an old state, split views, and is trust-on-first-use.**
   1. *What:* (a) answer as if a key were absent, serve yesterday's rows or a log that stops short;
      (b) show different people different logs (no witnesses); (c) the first owner key for an address
      is accepted on the provider session plus the new key's signature (trust on first use), so
      whoever is signed in as the address first can enroll first; (d) on an **unenrolled** address a
      server that holds the directory key can publish a key it holds, and can answer a client with the
      number of another address's log entry for the same public key (no owner signature to compare).
   2. *Why inherent:* a single operator holds the key and the store; a first contact has nothing to
      compare with; the log has no second party.
   3. *Shipped:* a signed, hash-chained transparency log with a signed head, `WatchLog` and
      `WatchState` (a client that remembers a head detects a cut or rewritten log from then on),
      `AuditAddress`, owner enrollment (every later publish and revoke needs the owner's signature,
      checked by the client), a logged 24 hour reset the enrolled key can cancel, an operator reset
      that watchers are told about. See step 4b, "What the server can still do" and "Limits that
      remain" items 0 to 2.
   4. *Who:* the user enrolls early and watches the address; the operator keeps the directory key and
      the store separate from provider sessions it should not use; the host may add witnesses
      (backlog item 12).

8. **Attestation exists only for ed25519 SSH keys.**
   1. *What:* only those keys can sign a binding to an address; age and passkey-derived keys get
      proof of possession (`Held`, which is the server's word) and, for an enrolled address, the owner
      signature.
   2. *Why inherent:* age and passkey keys cannot sign.
   3. *Shipped:* proof of possession (`RequireProof` off by default), the owner signature. See step 4b,
      "Limits that remain" items 5 and 6.
   4. *Who:* the operator may turn `RequireProof` on; the user enrolls an owner key.

## Public docstore and operations

9. **Botnets, shared addresses, the admin token, legal and abuse operations.**
   1. *What:* (a) a flood spread over thousands of networks is a crowd of newcomers that can fill the
      reserve and hold the in-flight cap; (b) visitors behind one /24 or /16 (campus, ISP,
      carrier-grade NAT) share a cap; (c) a distributed guesser can try the admin token, which no limiter
      holds back; (d) the terms text, who reads reports and who answers takedowns are not code; (e)
      reports sent from many addresses each take their share of the reserved report slots, so the layers
      make a flood cost one address per report slot without removing it; (f) without trusted proxies the
      per-client address budgets (sessions, the directory's per-IP lookups, the failed-admin budget) key on
      the connection's `RemoteAddr`, which behind a reverse proxy is the proxy's for everybody, so one
      budget is shared and wrong admin tokens can lock the operator out for a minute at a time.
   2. *Why inherent:* an anonymous offer cannot tell a crowd from visitors by address alone; the
      limits can slow and make visible, not out-guess; the legal side is people.
   3. *Shipped:* per-client, per-network and per-wide caps with a reserve, an exact byte ledger that
      fails closed, a minimum upload rate, deduplicated reports with a category enum, failed-only admin
      limits, a 16 character minimum for the token, the `Challenger` hook (no implementation ships), and
      `..._TRUSTED_PROXIES` (the server will not start with it unset when an admin token is set) so
      `X-Forwarded-For` is believed from named proxies only. See
      [section 7.5, item 7](public-docstore.md#75-what-is-left) and the design's step 4b, "Limits that
      remain" item 7 (the lookup budget, `RemoteAddr` behind a proxy).
   4. *Who:* the operator fronts the server with a gateway, sets higher net caps behind shared
      addresses, uses a long random admin token, names the trusted proxies, and adds a `Challenger` when the offer is busy; the
      operator or host owns the legal terms and the abuse process.

10. **Races at the instant of expiry.**
    1. *What:* two server processes on one store can race at the instant of a document's deadline (a
       save in that second may be deleted).
    2. *Why inherent:* a lock serializes one process, not two. Partly an implementation gap, though:
       `ExpiringAdapter.purgeLocked` and `Sweep` delete with an unconditional `Inner.Delete` under an
       in-process lock, so on a store with conditional deletes the race could be closed with `DeleteIf` on
       the digest that was read (backlog item 10, which depends on item 9 for s3 and blob). Until then
       the limit holds on every store.
    3. *Shipped:* a lock inside a process, and the sweep re-reads the deadline record under it before
       deleting. Conditional deletes exist today only for the rate-limit sweep (`SweepRateLimits`), the
       directory adapter and system-record `DeleteIf`, not for document expiry. See
       [public-docstore.md](public-docstore.md) section 4 ("Residual limits, stated plainly").
    4. *Who:* the operator runs one process per store, or accepts that a save in the deadline second may
       be lost.

11. **Clock skew between replicas.**
    1. *What:* deadlines and the other time rules are compared with each process's own clock; a replica
       whose clock is ahead dates directory-log entries late, which hurts availability and makes owner
       signatures look far from their entry.
    2. *Why inherent:* there is no shared clock.
    3. *Shipped:* a replica's record dated more than 5 seconds ahead is re-dated for the rate limiter,
       owner signatures are accepted within 10 minutes. See the design's step 4b, limit 7a.
    4. *Who:* the operator keeps clocks in step (NTP) and, for several replicas, uses a store with
       conditional writes.

## Clients and storage

12. **A client-only store cannot resist a writer of its own storage; first unlock may be trust on first use.**
    1. *What:* in the browser-only showcase, whoever can write the page's `localStorage` can replace
       files, config and signers together; beyond the signed frame anchor nothing stops it. A store with
       no signed document yet (an SSH-only first unlock) has nothing to anchor, so its first owner list is
       trust on first use. A journal's link is replayable only past the 32 links kept in `Config.Links` or
       if it was made before links carried a nonce.
    2. *Why inherent:* the code and the keys run in the same origin as the writer; there is no second
       party.
    3. *Shipped:* signed frames, seals, links and an anchor in the frames, an authenticated journal, a
       stale-tab check (`ErrStaleConfig`). See step 5.7, "Residual limits, stated plainly".
    4. *Who:* the host of a real application keeps signers and the list out of the store writers' reach;
       the user unlocks a new store with a passkey first where possible.

13. **Browser folder permissions lapse.**
    1. *What:* the browser may withdraw write permission to a chosen folder, after a restart or mid
       session.
    2. *Why inherent:* the permission belongs to the browser, and `Request` needs a user gesture.
    3. *Shipped:* `docstore.ErrPermission`, "Reconnect folder", the folder remembered across visits.
       See [saving.md](saving.md) (the folder target).
    4. *Who:* the user reconnects; the host shows the error rather than a silent failure.

14. **Pack signatures have no trust anchor beyond the optional `-pubkey`.**
    1. *What:* a pack's signature record names a key; a reader reports it, but who that key belongs to is
       outside the file.
    2. *Why inherent:* a file cannot vouch for its own signer.
    3. *Shipped:* `opendoc verify -pubkey <hex>` requires a given Ed25519 key; hashes cover every
       document. See [opendoc-format.md](opendoc-format.md#6-decisions-and-what-is-left-open) and the
       header comment of `cmd/opendoc/main.go` (anyone can sign a pack with a key of their own).
    4. *Who:* the user or host gets the public key by another route and passes it.

## Project infrastructure

15. **CI is manual, and hosted runners can fail.**
    1. *What:* the workflow runs only from the Actions tab (`workflow_dispatch`); the free hosted runners
       are outside this repository's control and can fail or be unavailable.
    2. *Why inherent:* a free-tier, third-party service (the header of `.github/workflows/ci.yml`: plain
       GitHub-hosted `ubuntu-latest` runners only, no paid or self-hosted ones).
    3. *Shipped:* the local checks before each push and a full run on `main` after every fifth merge. See
       [testing-workflow.md](testing-workflow.md) and the merge counter in
       [roadmap.md](roadmap.md#merges-since-the-last-full-test-run).
    4. *Who:* the maintainers run the checks locally and re-run or work around a failed hosted run.
