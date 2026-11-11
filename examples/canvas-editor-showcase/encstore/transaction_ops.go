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

func (s *Store) RemoveOwner(ctx context.Context, fingerprint string) error {
	if err := s.CanRemoveOwner(fingerprint); err != nil {
		return err
	}
	_, i, _ := s.ownerByFingerprint(fingerprint)
	to := slices.Delete(s.Owners(), i, i+1)
	return s.run(ctx, change{op: OpRemove, to: to, resign: true})
}

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

func validJournalSeal(sl Seal, to []Owner, allowed func(signer string) bool) bool {
	return sl.valid() && ownersDigest(sl.Owners) == ownersDigest(to) && allowed(sl.Signer)
}

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
		return ErrConfigChanged
	}
	if p.From == "" {
		consent := slices.ContainsFunc(p.Seals, func(sl Seal) bool { return validJournalSeal(sl, p.To, heldSigner) && hasSigner(p.To, sl.Signer) })
		switch {
		case consent:
			return check(firstID(), verifiers)
		case len(p.Seals) > 0:
			return ErrConfigChanged
		}
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
		if _, err := s.snapshot(ctx, ids); err != nil {
			return ErrConfigChanged
		}
		return check(firstID(), verifiers)
	}

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
			from := lists[len(lists)-1]
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
