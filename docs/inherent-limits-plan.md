# Plan: the six roadmap items marked inherent

Roadmap items 41, 42, 44, 45, 53 and 76 are marked "inherent". That word is true of the *limit* (the
underlying fact cannot be removed) and not of the *exposure* (how often a person meets it, how soon it is
noticed, how bad it is when it happens). For each item: what stays, what can change, the steps in
order, their size (S ≤ half a day, M ≈ 1 to 2 days, L > 2 days), and how to tell it is done. Nothing
here promises to remove a limit; every step makes it rarer, earlier noticed or cheaper.

Suggested order: **53** (cheap, self-contained), **42** (a format change, so it should be decided
early), **76** (the largest effect on real people), **45**, **44**, **41** (waits for e-mail verification).

## 53. The session store is read once per authenticated request

*What stays:* a logout on one replica must end the token on all of them, so a positive cache would let a
logged-out token work for as long as it is cached.

*What can change:* the cost of reads that are *not* logins of a real person, and a bounded staleness that the
operator chooses.

1. **Negative cache for misses (S).** A token that the store did not know is remembered as unknown for
   one second per process (a small fixed-size map, no growth under a flood of random tokens). A flood of
   random bearer tokens then costs one store read per token per second instead of one per request.
   Safe: a *new* login on another replica takes at most one second to be seen, and an unknown token
   gains nothing by being retried.
2. **A budget for authenticated misses (S).** Count misses per client address with the existing limiters
   (`docstore.Limiter`), so a client sending tokens that are never found is refused before the read.
3. **An optional positive cache with a stated maximum staleness (M).** `sessionstore.Store.CacheFor`
   (zero by default = off, as today; the documentation says "a logout can take up to this long to reach
   the other replicas"). Logout on the replica that handles it drops its own entry at once, so one-replica
   deployments see no staleness.
4. **Metrics (S).** Reads, hits, misses, so the operator can tell whether the cost matters.

Done when: a test with a fake clock shows a miss is read once per second and a logout is seen after at
most `CacheFor`; with the option off, behaviour is byte-for-byte today's.

## 42. A revoked signer can keep writing new frames until it is removed from every list

*What stays:* whoever can write the store can write bytes; the store cannot refuse them, and a holder of a
removed key can delete or roll back records at any time.

*What can change:* the window in which the removed key's frames still verify, and whether what is written in it
is noticed. **The first draft of this section (a frame v4 with a revocation sequence number) was wrong** and is
withdrawn: a time in a frame is the signer's own claim, the sequence number is per document, and a mark in a
record the removed key can still sign proves nothing. The reasons and the design that replaces it are in
[frame-revocation-design.md](frame-revocation-design.md). Steps, in order:

1. **`Sharing.RevokeSigner` (M).** Re-sign everything with the current signer, write the new list of owner
   signers without the key, and scan again, as one resumable operation with a journal; the window is the
   duration of the operation.
2. **A manifest of the old key's frames (S to M, optional).** Signed by a key that stays, at the start; frames
   of the old key that are not in it are reported as written after the removal began.
3. **State the window plainly (S)** in the limits register and in the showcase's removal dialog.

Done when: the tests in the design page pass (writes during the run are reported, a crash at any step resumes,
the old golden files still open). No format change.

## 76. Key and recovery UX (lost passkey plus lost recovery key means lost data)

*What stays:* there is no escrow, so a person who loses both loses the data.

*What can change:* how likely a person is to be in that state. This is the item with the largest effect
on real people, and all of it is fixable; the roadmap entry has the five steps. Order and size:

1. **Guard rails first (M).** The nudge while one key is enrolled; wording that states the limit plainly
   ("if you lose this and the recovery key, nobody can open your documents, including us").
2. **A test-my-recovery-key step (M).** Paste or scan the key; the page checks it against the store's
   recipient without exposing it or writing anything. A timestamp of the last successful check is kept.
3. **A printable recovery sheet (S).** Plain, high-contrast, with the key as text and as a QR code, and the
   fingerprint to compare. No network, no external fonts.
4. **A periodic reminder (S).** "You last checked your recovery key 90 days ago", dismissible, stored only
   in this browser, keyboard and screen-reader accessible (item 59, 69).
5. **A device list with last use and a revoke action (M)**, then **a guided re-key after a revoke (M)**
   built on `RepairAll`, with progress and a safe stop.

Done when: a person who enrolled one key is told, can test the recovery key without risk, can print it, is
reminded, and can revoke a device and re-key with a visible progress bar; each step has a browser test and
passes the accessibility gate (`make a11y-test`, now in the OpenDocs repository).

## 45. Unenrolled-address entry numbering by a holder of the directory key

*What stays:* on an address with no owner key there is nothing to compare an entry's number with.

*What can change:* fewer addresses stay unenrolled, and a client can refuse to rely on one.

1. **Prompt to enroll (S).** The showcase already offers it; make it a standing notice on the People tab
   for the signed-in address until it is enrolled, with the reason in one sentence.
2. **A client policy (S).** `directoryclient` option `RequireOwner`: a lookup on an unenrolled address
   returns a result marked weak, and a host that sets the option refuses to encrypt to it without the
   person's confirmation of the fingerprint.
3. **A server policy (M).** `GOJIBERRY_DIRECTORY_REQUIRE_ENROLLMENT=true`: the directory accepts a
   publish for an address only after the address enrolled an owner key (the first enrollment is allowed
   on the provider session, as today). Documented as a hardening option; off by default since it raises the
   bar for a first use.
4. **Watch (S).** `WatchLog` reports "an entry was made for an unenrolled address you use" so a number
   that changes under a relying person is noticed.

Done when: tests cover each option, and the limits register says what an enrolled address and a
`RequireOwner` client get.

## 44. An SSH-only store with no signed document file yet is undetected

*What stays:* the first owner list of a store has nothing older to be compared with.

*What can change:* the first use becomes a decision the person sees and can check, as for an SSH host key.

1. **Show the fingerprint at first unlock (S).** "This store's owner list is new to this browser. Its
   fingerprint is X. Check it against the other device." The person confirms; the answer is kept and any
   later change to the list is refused until confirmed again (a known-hosts file for stores).
2. **Prefer a passkey first (S).** The showcase and the docs say: unlock a new store with a passkey on the
   first use, then add SSH keys; the passkey path signs a document file at once.
3. **A second place for the first list (M).** Optionally write the list's digest into the key directory
   log (owner-signed) or into a second store, so a replaced list is noticed by comparison.

Done when: an SSH-only first unlock shows the fingerprint, a changed list is refused after the
confirmation, and a test replaces the list and sees the refusal.

## 41. Signed-out same-e-mail passkey registration makes two accounts

*What stays:* until an e-mail is verified it is a label, so two registrations with the same label are
two accounts.

*What can change:* the design of verification so that nothing is ever merged on the bare label.

1. **Write the rule down (S).** In the verification design: an account is never merged or taken over
   because a label matches; a verified e-mail can *link* accounts only through a ceremony that both
   accounts take part in.
2. **Show the situation (S).** A signed-in person with a verified e-mail sees "another account uses this
   address (not verified)", with no detail about it.
3. **A link ceremony (L)**, when verification exists: the person proves both accounts (a passkey of each),
   and chooses which to keep; the other's documents are shared to it, not moved silently.
4. **A test that fails on a merge by label (S)**, so a later change cannot add one by accident.

Done when: step 1 and 4 are in; steps 2 and 3 follow the e-mail verification work.

## Summary

| Item | Steps | Total | Effect |
| --- | --- | --- | --- |
| 53 | 4 | about 3 days | cost of the commonest request; no change to logout semantics by default |
| 42 | 3 | about 4 days | a revoked signer's later writes are rejected |
| 76 | 5 | about 7 days | far fewer people reach the unrecoverable state |
| 45 | 4 | about 3 days | unenrolled addresses are visible and refusable |
| 44 | 3 | about 2 days | first use of an SSH-only store is a visible decision |
| 41 | 4 | about 4 days, in part waiting | accounts never merged on a label |
