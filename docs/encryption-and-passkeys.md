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
   searches a store. Option 1 (default): leave it readable by the server and encrypt
   only the documents. Option 2: encrypt the catalog too, for the store owner, so the
   server sees opaque blobs; listing is then done on the client after one catalog
   download. A store picks one.
3. **`.opendoc` packs.** A record's data may be an envelope (a record flag). The index
   hashes are of the stored bytes, so a pack's integrity and signature are checked
   **without** any key; a document is decrypted only when opened.
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
   id}` with the e-mail the person gave when registering; linking a passkey to an
   existing OIDC identity is done while signed in with that identity.
4. **Discoverable credentials** allow signing in without typing a username. A person can
   register several passkeys, list them and remove one (not the last, unless another way
   to sign in exists).
5. **UI.** `components.Passkey`: register, list, remove, "Sign in with a passkey"; the
   browser calls `navigator.credentials.create/get` through `syscall/js`.
6. **Tests.** The server side against recorded ceremonies; the browser side with
   Chrome's virtual authenticator (the CDP WebAuthn domain), which `internal/e2etest`
   can drive. Replay is covered by the signature counter and one-time challenges.

## C. Open decisions

1. End-to-end (this design: keys stay with the person) or encryption at rest on the
   server (simpler, the server holds the key, and search and collaboration keep
   working)? This draft assumes end-to-end; at-rest is a smaller second mode that
   reuses `EncryptedAdapter` with a server-held key.
2. Catalog readable by the server (A.2 option 1) or encrypted?
3. Whether a passkey may be the only way to unlock (this draft: no).
4. Whether the e-mail should be verified (the OIDC e-mail is; one typed when adding a
   recipient is a label only).

## D. Plan

1. `docstore.EncryptedAdapter` + `Keyring` using `age`/`agessh`, with SSH key recipients,
   tests (round trip, wrong key, tampered ciphertext, re-key on revoke).
2. People tab: add a recipient by key, fingerprint confirmation, re-key on remove.
3. `auth/passkey` and `components.Passkey`, with the virtual-authenticator e2e.
4. Passkey as an encryption recipient through PRF, with detection and the
   second-recipient rule.
5. `.opendoc` encrypted records, and encrypted catalogs if chosen.
