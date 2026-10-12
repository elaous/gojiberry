package encstore

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"filippo.io/age"
	"fmt"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"golang.org/x/crypto/ssh"
	"slices"
	"strings"
)

func recoverable(list []Owner) bool {
	for _, o := range list {
		if o.Kind != KindPasskey {
			return true
		}
	}
	return false
}

func checkNew(keys []NewKey, existing []Owner) error {
	seen := map[string]bool{}
	for _, o := range existing {
		seen[o.Public] = true
	}
	for _, k := range keys {
		if seen[k.Recipient.Text] {
			return ErrKeyTwice
		}
		seen[k.Recipient.Text] = true
		if k.Kind == KindRecovery && !k.SecretSaved {
			return ErrSecretNotSaved
		}
	}
	return nil
}

// Enable turns encryption on with the given owner keys; what was saved in the clear before is
// encrypted now, or, if that cannot be done completely, nothing is changed. Keys that carry their
// private side (Unlock) leave the store unlocked; with none (an SSH public key only) it starts
// locked and the private key file unlocks it. The keys are public information and go to Persist.
//
// Passkeys alone are refused (ErrPasskeyOnly) unless opts.AcceptPasskeyOnly: everything in this
// store is encrypted to the owner keys, so losing them loses all of it. A recovery key must have
// its secret confirmed saved (NewKey.SecretSaved) first.
func (s *Store) Enable(ctx context.Context, opts Options, keys ...NewKey) error {
	if s.Interrupted() {
		return ErrInterrupted
	}
	if s.Enabled() {
		return ErrAlreadyOn
	}
	if len(keys) == 0 {
		return ErrNoKeys
	}
	var to []Owner
	for _, k := range keys {
		to = append(to, ownerOf(k))
	}
	if err := checkNew(keys, nil); err != nil {
		return err
	}
	if !recoverable(to) && !opts.AcceptPasskeyOnly {
		return ErrPasskeyOnly
	}
	return s.run(ctx, change{op: OpEnable, to: to, keys: keys})
}

// AddOwner adds an owner key (a recovery key made later, a passkey, an SSH key) to an unlocked
// store: every stored file is encrypted again so the new key opens it, as one transaction. If it
// was cut short, Recover finishes it. The key's recovery secret must have been confirmed saved.
func (s *Store) AddOwner(ctx context.Context, k NewKey) error {
	if err := s.canChangeOwners(); err != nil {
		return err
	}
	cur := s.Owners()
	if err := checkNew([]NewKey{k}, cur); err != nil {
		return err
	}
	return s.run(ctx, change{op: OpAdd, to: append(cur, ownerOf(k)), keys: []NewKey{k}})
}

func (s *Store) canChangeOwners() error {
	switch {
	case s.Interrupted():
		return ErrInterrupted
	case !s.Enabled():
		return ErrDisabled
	case !s.Unlocked():
		return ErrLocked
	}
	return nil
}

func (s *Store) ownerByFingerprint(fp string) (Owner, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i, r := range s.owners {
		if r.Fingerprint == fp {
			return s.cfg.Owners[i], i, nil
		}
	}
	return Owner{}, -1, ErrNoSuchOwner
}

// RemoveOwner takes an owner key out: every stored file is encrypted again to the keys that
// remain, and the lists of who documents are shared with are signed again by one of them, as one
// transaction. It is refused for the last key, for a change that would leave only passkeys where
// there was a way back that is not one, and while the store is unlocked with the key being
// removed alone. Taking a key out does not take back what its holder already read or copied.
func (s *Store) RemoveOwner(ctx context.Context, fingerprint string) error {
	if err := s.CanRemoveOwner(fingerprint); err != nil {
		return err
	}
	_, i, _ := s.ownerByFingerprint(fingerprint)
	to := slices.Delete(s.Owners(), i, i+1)
	return s.run(ctx, change{op: OpRemove, to: to, resign: true})
}

// CanRemoveOwner says whether RemoveOwner would go ahead for this key: nil, or why not
// (ErrNoSuchOwner, ErrLastOwner, ErrWouldLoseRecovery, ErrUseAnotherKey, or the store being
// locked or interrupted), so a page can show the button off with the reason.
func (s *Store) CanRemoveOwner(fingerprint string) error {
	if err := s.canChangeOwners(); err != nil {
		return err
	}
	gone, i, err := s.ownerByFingerprint(fingerprint)
	if err != nil {
		return err
	}
	cur := s.Owners()
	if len(cur) == 1 {
		return ErrLastOwner
	}
	to := slices.Delete(slices.Clone(cur), i, i+1)
	if recoverable(cur) && !recoverable(to) {
		return ErrWouldLoseRecovery
	}
	_, _, holders := s.unlockedNow()
	if !slices.ContainsFunc(holders, func(h string) bool { return h != gone.Public }) {
		return ErrUseAnotherKey
	}
	return nil
}

// ReplaceOwner swaps one owner key for another (a new passkey for the old one) as one transaction:
// the new key is added and the old one removed together, so there is no moment with neither or
// with both half done. The new key carries its private side (it signs the lists from then on).
func (s *Store) ReplaceOwner(ctx context.Context, fingerprint string, k NewKey) error {
	if err := s.canChangeOwners(); err != nil {
		return err
	}
	_, i, err := s.ownerByFingerprint(fingerprint)
	if err != nil {
		return err
	}
	cur := s.Owners()
	if err := checkNew([]NewKey{k}, cur); err != nil {
		return err
	}
	if k.Unlock == nil {
		return ErrUseAnotherKey
	}
	to := slices.Clone(cur)
	to[i] = ownerOf(k)
	if recoverable(cur) && !recoverable(to) {
		return ErrWouldLoseRecovery
	}
	return s.run(ctx, change{op: OpReplace, to: to, keys: []NewKey{k}, resign: true})
}

// confidentialLeft says whether a document or folder is still confidential (its signed list says
// end to end).
func (s *Store) confidentialLeft(ctx context.Context) error {
	k, err := ring{s}.keyring()
	if err != nil {
		return err
	}
	metas, err := s.Raw.List(ctx)
	if err != nil {
		return err
	}
	for _, m := range metas {
		doc, ok := strings.CutPrefix(m.ID, encrypt.KeysPrefix)
		if !ok || m.ID == encrypt.IndexKey {
			continue
		}
		conf, err := k.IsConfidential(ctx, doc)
		if err != nil {
			return err
		}
		if conf {
			return ErrConfidentialLeft
		}
	}
	return nil
}

// Disable turns encryption off: every file is decrypted back into the store as it was before, the
// lists of who a document is shared with are removed, and the keys are forgotten, as one
// transaction that puts everything back if it fails. The store must be unlocked and nothing may
// still be confidential (ErrConfidentialLeft: turning it off would leave their names and files
// unreadable). The change holds the store's lock, so a save made meanwhile waits for it and then
// finds encryption off, instead of writing ciphertext into a store that no longer reads it.
func (s *Store) Disable(ctx context.Context) error {
	if s.Interrupted() {
		return ErrInterrupted
	}
	if !s.Enabled() {
		return ErrDisabled
	}
	if !s.Unlocked() {
		return ErrLocked
	}
	if err := s.confidentialLeft(ctx); err != nil {
		return err
	}
	if err := s.run(ctx, change{op: OpDisable}); err != nil {
		return err
	}
	s.Lock()
	return nil
}

// Erase deletes everything the store holds and forgets its keys: the way out when every key is
// lost and the documents cannot be opened. It does not need a key, and it cannot be undone. It
// touches only this store's storage (Raw), never another target. It can be run again after a
// failure.
func (s *Store) Erase(ctx context.Context) error {
	unlock, err := s.lockAll(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	metas, err := s.Raw.List(ctx)
	if err != nil {
		return err
	}
	for _, m := range metas {
		if err := s.Raw.Delete(ctx, m.ID); err != nil && !errors.Is(err, docstore.ErrNotFound) {
			return fmt.Errorf("%s: %w", m.ID, err)
		}
	}
	if err := s.persist(Config{}); err != nil {
		return err
	}
	if err := s.install(Config{}); err != nil {
		return err
	}
	s.Lock()
	s.mu.Lock()
	s.floor = &encrypt.MemorySeqFloor{}
	s.mu.Unlock()
	return nil
}

// Recover finishes (finish true) or undoes (false) a change that was cut short, from its journal.
// Finishing converts what is left to the journal's owner list and makes it current; undoing
// converts it back to the list that is current. Both are safe to run again if they fail.
//
// Nothing in the journal or in the Config is believed by itself, since whoever writes the storage
// writes both: authJournal asks that the key unlocked here vouch for it (see there), otherwise
// ErrConfigChanged. Re-keying needs a key unlocked; sealing files that are still in the clear does
// not.
func (s *Store) Recover(ctx context.Context, finish bool) error {
	unlock, err := s.lockAll(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	p := s.PendingChange()
	if p == nil {
		return ErrNothingPending
	}
	cfg := s.Config()
	ids, sigs, holders := s.unlockedNow()
	if err := s.authJournal(ctx, cfg, p, finish, ids, sigs, holders); err != nil {
		return err
	}
	target := cfg.Owners
	if finish {
		target = p.To
	}
	var resign *encrypt.Signer
	if p.Op == OpRemove || p.Op == OpReplace {
		if resign = keeper(target, sigs); resign == nil && len(ids) > 0 {
			return ErrUseAnotherKey
		}
	}
	verifiers := append(signerPubs(cfg.Owners), signerPubs(p.To)...)
	view := newRecorder(bare{s.Raw})
	if err := s.convert(ctx, view, cfg.Owners, target, ids, verifiers, resign, frameSigner(target, sigs), p.Op == OpRestore); err != nil {
		return err
	}
	if len(target) == 0 {
		if err := dropLists(ctx, view); err != nil {
			return err
		}
	}
	if err := s.verifyAll(ctx, view, target, ids, nil); err != nil {
		return err
	}
	final := cloneConfig(cfg)
	final.Pending = nil
	if finish {
		final = Config{Owners: slices.Clone(p.To)}
		if len(p.To) > 0 {
			final.Seals, final.Links = bindingFor(cfg, p.To, sigs, p.Link)
			final.Version = versionFor(final)
		}
	}
	if err := s.persist(final); err != nil {
		return err
	}
	if err := s.install(final); err != nil {
		return err
	}
	s.afterCommit(nil, final.Owners, ids, sigs, holders)
	return nil
}

// validJournalSeal says whether sl is a seal of the journal's new list by one of keys.
func validJournalSeal(sl Seal, to []Owner, allowed func(signer string) bool) bool {
	return sl.valid() && ownersDigest(sl.Owners) == ownersDigest(to) && allowed(sl.Signer)
}

// authJournal decides whether the key unlocked here vouches for the journal. A journal is whatever
// is in the storage, so its owner list, its seals and the Config's owners are all data an attacker
// can write; what is not forgeable is a signature made with a key's secret, and the signed frames
// of the files. So:
//
//   - A journal over a committed list (add, remove, replace, disable) is accepted when a held key
//     has its own seal chain leading to the Config's owners (reach: the old list, vouched for by that
//     key's own signature and by owners' links), the Config's owners are the list the journal says it
//     started from (From), and, to finish, the journal carries the Link From -> To signed by a key
//     of that old list (the signature covers both lists, so it cannot be replayed for another
//     direction). A forged journal has nobody of the old list to sign it. Undoing needs no
//     signature: it goes back to the Config's list.
//   - Or, for finishing only, a key held that is in the new list vouches for it with its own seal
//     over that list (made when it was in hand at the start: it is the person's new key), and the
//     journal carries the Link from the old list (signed by a key of the Config's owners), which is
//     kept so the old keys still open the store afterwards. An attacker has neither that key's
//     signature nor a link from the real old list.
//   - Undoing needs the first kind: the list it goes back to must be vouched for by a held key.
//   - A journal of turning encryption on (nothing before it) is accepted when a held key of the
//     new list signed it; when no key could sign (an SSH-only list) only if no stored file is
//     encrypted, or the held key opens every one that is (then they were sealed to it). The
//     explicit confirmation in the interface is the check left for that case.
//
// And in each case the signed frames of the files must carry the hash of the old or the new list,
// nothing else (anchor.go).
//
// Replay: a Link is genuine for ever, so a copy of an old journal could be presented once the owner
// list is content-identical to its From again (AddOwner X, then RemoveOwner X). A journal's Link
// carries a signed random nonce, and one that Config.Links already records is refused for
// finishing (ErrConfigChanged). What is left (links made before the nonce existed, and links older
// than the last maxLinks) is the second residual limit in docs/encryption-and-passkeys.md.
func (s *Store) authJournal(ctx context.Context, cfg Config, p *Pending, finish bool, ids []age.Identity, sigs []*encrypt.Signer, holders []string) error {
	heldOwner := func(h string) (Owner, bool) {
		for _, o := range append(slices.Clone(cfg.Owners), p.To...) {
			if o.Public == h {
				return o, true
			}
		}
		return Owner{}, false
	}
	heldSigner := func(signer string) bool {
		return slices.ContainsFunc(sigs, func(sg *encrypt.Signer) bool { return hex.EncodeToString(sg.Public()) == signer })
	}
	verifiers := signersOf(cfg.Owners, p.To)
	for _, sg := range sigs {
		verifiers = append(verifiers, sg.Public())
	}
	toHash := ownersHash(p.To)
	allowed := [][32]byte{toHash}
	check := func(id age.Identity, vs []ed25519.PublicKey) error {
		if id == nil {
			return nil
		}
		if p.From != "" {
			allowed = append(allowed, ownersHash(cfg.Owners))
		}
		return s.anchorMatches(ctx, id, vs, allowed...)
	}
	firstID := func() age.Identity {
		if len(ids) > 0 {
			return ids[0]
		}
		return nil
	}

	if len(cfg.Owners) > 0 && p.From == "" {
		return ErrConfigChanged // a journal of turning encryption on over a store that has owners
	}
	if p.From == "" { // turning encryption on (or a restore): there is no committed list to vouch
		consent := slices.ContainsFunc(p.Seals, func(sl Seal) bool { return validJournalSeal(sl, p.To, heldSigner) && hasSigner(p.To, sl.Signer) })
		switch {
		case consent:
			return check(firstID(), verifiers)
		case len(p.Seals) > 0:
			return ErrConfigChanged // sealed by keys, none of which is held here: unlock one first
		}
		// No seal at all: nobody could sign (SSH keys only).
		found, err := HasEncryptedFiles(ctx, s.Raw)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if len(ids) == 0 || !slices.ContainsFunc(holders, func(h string) bool { return hasOwnerKey(p.To, h) }) {
			return ErrConfigChanged
		}
		if _, err := s.snapshot(ctx, ids); err != nil { // the held key opens every file: they were sealed to it
			return ErrConfigChanged
		}
		return check(firstID(), verifiers)
	}

	// Over a committed list. A link the Config already records was finished once: presented as a
	// journal now it is a replay (the list may have come back to the same content since), not an
	// interrupted change. Undoing needs no link, so it is not affected.
	if finish && p.Link != nil && linkRecorded(cfg, *p.Link) {
		return ErrConfigChanged
	}
	if ownersDigest(cfg.Owners) == p.From {
		for i, h := range holders {
			o, ok := heldOwner(h)
			if !ok || !hasOwnerKey(cfg.Owners, h) {
				continue
			}
			lists, reached := reach(cfg, o, p.From)
			if !reached {
				continue
			}
			from := lists[len(lists)-1] // the old list as the key's own seal vouches for it
			// Finishing needs the Link From -> To, signed by a key of the old list: its signature
			// covers both digests, so it cannot be replayed for another From or To. A seal over To
			// alone is not enough: it says nothing of the list the change started from, so a seal
			// over an old list would pass as the journal of going back to it. Undoing needs no
			// signature: it goes back to the list in the Config.
			signed := p.Link != nil && p.Link.valid() && p.Link.From == p.From && ownersDigest(p.Link.To) == ownersDigest(p.To) && hasSigner(from, p.Link.Signer)
			if !signed && finish {
				continue
			}
			return check(ids[i], append(signersOf(lists...), verifiers...))
		}
	}
	if finish && p.Link != nil && p.Link.valid() && p.Link.From == p.From && ownersDigest(cfg.Owners) == p.From &&
		ownersDigest(p.Link.To) == ownersDigest(p.To) && hasSigner(cfg.Owners, p.Link.Signer) {
		for i, h := range holders {
			if !hasOwnerKey(p.To, h) {
				continue
			}
			o, _ := heldOwner(h)
			if slices.ContainsFunc(p.Seals, func(sl Seal) bool {
				return validJournalSeal(sl, p.To, func(sg string) bool { return sg == o.Signer && heldSigner(sg) })
			}) {
				return check(ids[i], verifiers)
			}
		}
	}
	return ErrConfigChanged
}

// Restore rebuilds the key configuration of a store whose configuration is gone (Inspect said
// ErrConfigMissing) from a key that opens its files: the owner list becomes that key alone, every
// file is encrypted again to it (so the anchor of the files and the new configuration agree), and
// the store is left unlocked with it. It is the person's explicit act with a key they hold: it adds
// nobody, and the keys that were owners before are not owners now (add them again afterwards).
// Every stored file must open with the key (UnreadableError otherwise: nothing is changed); a
// store whose files that key cannot open is Erase's. k carries its private side (NewPasskeyKey,
// RecoveryKeyFromSecret, SSHKeyFromPrivate).
func (s *Store) Restore(ctx context.Context, k NewKey) error {
	if s.Enabled() {
		return ErrAlreadyOn
	}
	if k.Unlock == nil {
		return ErrNotAKey
	}
	k.SecretSaved = true // it is a key the person already holds
	return s.run(ctx, change{op: OpRestore, to: []Owner{ownerOf(k)}, keys: []NewKey{k}, rekey: true})
}

// RecoveryKeyFromSecret makes the owner key of an existing recovery key from its secret
// ("AGE-SECRET-KEY-1..."), for Restore.
func RecoveryKeyFromSecret(label, secret string) (NewKey, error) {
	ident, err := encrypt.ParseAgeIdentity(secret)
	if err != nil {
		return NewKey{}, err
	}
	xid, ok := ident.(*age.X25519Identity)
	if !ok {
		return NewKey{}, ErrNotAKey
	}
	r, err := encrypt.ParseAgeRecipient(xid.Recipient().String())
	if err != nil {
		return NewKey{}, err
	}
	r.Label = label
	sg := encrypt.SignerFromAge(xid)
	return NewKey{Kind: KindRecovery, Label: label, Recipient: r, SignerPub: sg.Public(), SecretSaved: true,
		Unlock: &Unlocked{Identity: xid, Signer: sg}}, nil
}

// SSHKeyFromPrivate makes the owner key of an ed25519 SSH private key file (PEM, not passphrase
// protected), for Restore. pem is not kept.
func SSHKeyFromPrivate(pem []byte) (NewKey, error) {
	raw, err := ssh.ParseRawPrivateKey(pem)
	if err != nil {
		return NewKey{}, err
	}
	var pub ssh.PublicKey
	switch k := raw.(type) {
	case *ed25519.PrivateKey:
		pub, err = ssh.NewPublicKey(k.Public())
	case ed25519.PrivateKey:
		pub, err = ssh.NewPublicKey(k.Public())
	default:
		return NewKey{}, errors.New("encstore: only an ed25519 SSH key can be an owner")
	}
	if err != nil {
		return NewKey{}, err
	}
	nk, err := NewSSHKey(string(ssh.MarshalAuthorizedKey(pub)))
	if err != nil {
		return NewKey{}, err
	}
	id, err := encrypt.ParseSSHIdentity(pem)
	if err != nil {
		return NewKey{}, err
	}
	sg, err := encrypt.SignerFromSSH(pem)
	if err != nil {
		return NewKey{}, err
	}
	nk.Unlock = &Unlocked{Identity: id, Signer: sg}
	return nk, nil
}
