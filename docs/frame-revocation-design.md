# Design: refusing the frames of a revoked signer (roadmap item 42)

Status: **design, no code.** The first plan (in `inherent-limits-plan.md` and in roadmap item 5) was a
frame v4 with a signer key id and a "revoked at sequence N" cut. Looking at it against the code it does not
work, so this page records why and what to build instead. Nothing here is shipped.

## The situation

An owner signing key (`OwnerSigners`) signs the frames of the files it writes (v3: Ed25519 over the header and
the content hash) and the `keys:<ID>` records. Someone removes a device: its key should stop being able to
write. Today the procedure (C.5 in `encryption-and-passkeys.md`) is: re-sign everything with a key that stays
(`RepairAll` with `Resign`), then remove the old key from every list (`OwnerSigners`). Until the last step the
old key's holder, if they can write the store, writes frames that verify.

The adversary is *a holder of the removed key who can write the store* (a stolen laptop with its storage
synced). The store cannot refuse bytes; the question is whether a reader can tell.

## Why "signer id, time and a sequence cut" does not work

1. **A time in the frame is the signer's claim.** The removed key signs whatever time it likes, so "refuse
   frames signed after the revocation" is refused by writing an earlier date. A time source nobody on the
   writing side controls (a witness, a timestamping service) is a second party this design does not have.
2. **The sequence number is per document** (the `keys:<ID>` record's `Seq`). A revocation is global, so
   "revoked at sequence N" has no meaning across documents; a per-document cut is possible (see below) but
   it is then a list of cuts, not a number.
3. **A revocation mark inside a record signed by an owner signer** can be rewritten by the revoked signer, which
   is still one of the owner signers until the end of the procedure. The mark must be vouched for by something
   the removed key does not hold, and the only such thing is a key that stays: a revocation signed by it can
   be forged by it only if it is the attacker, which is the case the owner already lost.
4. **Two keys revoking each other.** If a revocation is "a record signed by an owner signer", an attacker
   holding X writes one revoking Y. Which of the two is right cannot be told from the store alone; it needs the
   record the *owner* signed last (a chain of owner-signed lists, as the key directory has), so the list of
   owner signers has to be a hash-chained, owner-signed object of its own. `encstore` already has one (seals
   and links); `docstore/encrypt` does not for `OwnerSigners` (the host passes them in).

So a cut that a reader enforces *without a second party* is not possible. What is possible is to shrink the
window and to make what happens in it visible.

## What to build instead

1. **One operation, `Sharing.RevokeSigner(ctx, key)` (M).** It does what the procedure says, as one resumable
   step with a journal (like `encstore`'s): (a) re-sign every file and record with the current signer
   (`RepairAll{Resign}`), (b) write the new `OwnerSigners` (the host's config or the signed list) without the
   key, (c) scan again and report any frame the old key signed *after* step (a) began: it is not in the set that
   step (a) saw. A frame written by the old key after (b) is refused like any frame of an unknown signer
   (`ErrForgedFrame`), as today. The window is the duration of the operation, not "until someone remembers
   step 3".
2. **A manifest for the window (S to M, optional).** At the start of (a) the owner's current signer signs the
   list of content hashes of the old key's frames as scanned (`revoked-signer/<key id>`; one record,
   bounded by the number of frames, which `RepairAll` already counts). In (c), and on any later audit, a
   frame by that key whose content hash is not in the manifest is *reported*, with its document, as "written
   after the removal began". This detects; it does not prevent, and a backdated time is irrelevant because the
   test is the content hash. The manifest is signed by a key that stays, so the revoked key cannot replace it
   with its own (it can still delete it, which is the same as deleting any record: the store is not tamper
   proof against its writer, see the limits register).
3. **Say so (S).** The limits register item for revoked signers states the window in words: "until the
   operation finishes, the removed key's holder can still write frames that verify; afterwards they are
   refused; frames written during the operation are reported".

Frame v4 is *not* needed for this. If a later change wants the signer's key id in the frame (so a reader does not
try every owner signer), it can be added then, as an optimisation with no security claim.

## Tests to write with the code

- Revoke with 50 documents; a goroutine with the old key keeps writing while it runs; after it, none of the old
  key's frames verify and every frame written during the run is in the report.
- A crash after each step, resumed, ends in the same state (the journal).
- A manifest signed by the removed key is not accepted for itself.
- The old golden files (v1 to v3) still open: nothing in the format changes.

## What stays true

A holder of a removed key who can write the store can write bytes until the operation finishes, and can delete
or roll back records at any time (the limits register, "a writer of the store"). This design makes the
window short and the writes noticed; it does not make the store tamper proof.
