# Encryption with SSH keys and passkeys; signing in with a passkey (design, draft 1)

Two features that share a vocabulary (public keys, who holds which private key) but
not code: **encrypting documents to keys** and **signing in with a passkey**.

**This page is the index.** The design was one file of 147 KB and long lines, which GitHub cut off when it showed it, so it
is split into the parts below. Nothing was removed; every section keeps its heading here (so links to
`encryption-and-passkeys.md#c5-who-may-change-a-recipient-list` and the others still land) with a pointer to the part
that holds it. Cross-references in the text ("C.4", "step 4b", "5.7") use the same numbers in every part.

| Part | What is in it |
| --- | --- |
| [A. Encrypting documents](encryption-a-model.md) | Model, where it plugs in, passkeys as a key, what encryption costs |
| [B. Signing in with a passkey](encryption-b-passkey-signin.md) | The ceremonies, the extension points (limiter, challenge store, sessions, account store) |
| [C. Decisions](encryption-c-decisions.md) | Protection levels, the readable catalog, verified e-mail, who may change a recipient list (C.5), the key directory and its log |
| [D. Plan, steps 1 to 4](encryption-d-plan-1.md) | What was built, in order: the encrypting adapter, sharing, the passkey sign-in, the passkey as a key, and the key directory (step 4b) |
| [D. Plan, step 5](encryption-d-plan-2.md) | Packs, confidential items, the showcase's encrypted store, the limits that remain |

Where a number points: **A.1 to A.4** are in part A; **B** and **B.1** in part B; **C.1 to C.6** and the key directory
decisions in part C (**C.5** is the long one: who may change a recipient list, the signed `keys:` record, the log);
**D, steps 1 to 4** (including **4a** and **4b**, the key directory and its transparency log) in the first plan part;
**D, step 5** (packs, confidential items, **5.1 to 5.8**, the showcase's store, the limits that remain) in the second.

## A. Encrypting documents

In [part A](encryption-a-model.md).

### A.1 Model

In [part A](encryption-a-model.md): envelope encryption, recipients are public keys, the age format, where the key lives, revoking.

### A.2 Where it plugs in

In [part A](encryption-a-model.md).

### A.3 Passkeys as a key

In [part A](encryption-a-model.md): the WebAuthn PRF extension, and why losing the passkey loses the documents unless another recipient exists.

### A.4 What encryption costs

In [part A](encryption-a-model.md).

## B. Signing in with a passkey

In [part B](encryption-b-passkey-signin.md).

### B.1 Extension points

In [part B](encryption-b-passkey-signin.md): the limiter, the challenge store, the sessions and the account store, each an interface with a single-process default.

## C. Decisions

In [part C](encryption-c-decisions.md): protection levels, what stays readable, a passkey is never the only way to unlock, verified e-mail, no separate second factor, removing a key.

### C.5 Who may change a recipient list

In [part C](encryption-c-decisions.md): the owner-signed `keys:` record, signers, frames, rollback, the transparency log and what each layer proves and does not.

## D. Plan

In [plan part 1](encryption-d-plan-1.md) (steps 1 to 4: the adapter, sharing, the passkey sign-in, the passkey as a key, the key directory) and [plan part 2](encryption-d-plan-2.md) (step 5: packs, confidential items, the showcase's store, the limits that remain).
