# C. Decisions

This is part of the design [Encryption with SSH keys and passkeys](encryption-and-passkeys.md), which is split into parts so that each can be read on GitHub (one file of 147 KB was cut off). The index says where everything is.

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
