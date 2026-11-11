# D. Plan, step 5

This is part of the design [Encryption with SSH keys and passkeys](encryption-and-passkeys.md), which is split into parts so that each can be read on GitHub (one file of 147 KB was cut off). The index says where everything is.

## D. Plan (continued)

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

   7. **The showcase's encrypted store (done): in the OpenDocs repository.**
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
        showcase's `dirclient.Directory`, a small interface over `directoryclient`. **The address is a trust decision:** a
        link only *offers* an address, nothing is sent to it until the person confirms it in a dialog that names the
        host; it must be https (http only for a host on this machine), it is confirmed again whenever it changes, and
        a session token belongs to one address and is dropped when the address changes (the token field is a password
        field). A server without the routes gets a clear message (`dirclient.ErrNoDirectory`). Tested natively
        against `examples/people-keys-demo/demoserver` (`dirclient_test.go`) and in a browser
        (`TestPeopleTabUsesTheKeyDirectory`). The page has no sign-in: a server that needs a provider session needs its
        token pasted (the demo server hands one out at `/demo/provider-session`, for demo servers only); the page must
        be allowed to call the server (same origin, or CORS on the server).
        **Proof of possession** is wired (the client opens the challenge only if it is exactly a nonce, so a directory cannot use it to have a ciphertext made for the key decrypted): "Publish my keys" opens the server's challenge with the private side of each
        unlocked key (`encstore.Store.IdentityFor`), so the entry says the key was held; a locked key, or a server that
        wants an owner-signed entry, gets a message that says so. The **owner key** of the person's address can be made
        and enrolled from the directory settings (`encowner.go`): it is kept per directory as a **non-extractable WebCrypto
        Ed25519 key in IndexedDB** (a script in the page can ask it to sign but cannot copy it away; where the browser has
        no Ed25519 it falls back to text in `localStorage`, and the panel says so), its backup is shown once when it is
        made (as text in `localStorage` it can be shown at any time), restorable from the backup (checked against the
        enrolled keys; an older text copy is moved out of reach on the next visit) and forgettable, and "Publish my keys" signs
        with it. **Watching:** the person pins the log keys the server states, after seeing their fingerprints and,
        if they hold one from another source, typing it (a pin that does not match is refused; without it this is trust
        on first use: it shows the server stays consistent with itself, not that it is the one meant), and "Watch my address" runs `WatchLog`, keeps the
        position across reloads and lists what somebody else did to the address until dismissed. **Other people's rows** carry
        the marks the client proved itself; one signed by an owner key nobody confirmed is listed under "Owner keys to
        confirm" with the key's fingerprint, and the person can pin it after comparing it another way (kept per
        directory). **Replacing the owner key** is on the page: the holder rotates it (the old key signs the new one in), and a person who lost it asks for a reset, which the directory completes after 24 hours and the old key's holder can cancel; the new key is made at the request and kept apart until then (roadmap item 87). The demo `examples/people-keys-demo` has the whole flow.
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
