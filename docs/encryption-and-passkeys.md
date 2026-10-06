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

## D. Plan

1. **Done:** `docstore/encrypt` (`encrypt.Adapter`, `Keyring`, `Static`) using `filippo.io/age`
   and `agessh`: SSH (ed25519, RSA), age and recovery-key recipients, `Rekey` and `RekeyAll`,
   `RequireEncrypted`, catalogs / folder records / ACL records / fonts left readable
   (`DefaultSkip`), conditional catalog writes and the store lock passed through. Tests: round
   trip, wrong key, tampered ciphertext, sharing and re-key on revoke, a recovery key, bad keys,
   and the whole saver (folders, versions, `Verify`) on an encrypted store. It is its own
   package, so a build that does not import it does not link `age` (about 3.8 MB of wasm when it
   does).
2. **Done (library and component):** `encrypt.Sharing` (`Add` needs the fingerprint the owner
   confirmed, `Remove`, `List`) over `encrypt.StoreKeyring`, which keeps each document's
   recipients in a readable `keys:<ID>` record (public keys only) beside the owner's keys; every
   file of the document (its copies and versions) is encrypted again to the new list
   (`Adapter.RekeyDocument`). `components.Recipients` is the People tab's control: the owner's keys,
   the shared keys with Remove (which says it cannot take back what was already opened), and a form
   that parses a pasted key, shows its type, label and fingerprint, and keeps Add off until the
   fingerprint is typed. *Left:* wiring it into an application (the showcase has no encrypted store
   yet: it needs the key management of steps 3 and 4, where the owner's key lives and how it is
   unlocked) and the "verified e-mail" mark of C.4.
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
   `CheckRecoverable` (`ErrNeedSecondRecipient` unless a key that is not a passkey is among the recipients;
   two passkeys are not enough, they can be lost together). Registration asks for PRF
   (`WithExtensionPRFSupport`), and `client.DeriveKey` asks a passkey for the bytes (`ErrNoPRF` when the
   authenticator does not do PRF: passkeys made before this, some security keys and password managers).
   The e2e derives the same key twice in Chrome. *Left:* the People-tab control that adds "my passkey" as a
   key, and calling `CheckRecoverable` when a document is made confidential (step 5).
5. `.opendoc` encrypted records, and the placeholder catalog entries for confidential items.
