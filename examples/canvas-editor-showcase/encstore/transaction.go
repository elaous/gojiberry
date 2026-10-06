package encstore

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

// A change to the keys is a transaction over the stored files. Making the store half encrypted
// (the key list saved, the files not all re-keyed) is what this exists to prevent, so the order is:
//
//  1. check the room (age adds a header to every file) and that every file can be opened now;
//  2. write the journal into Config (Pending: what is being done, to which owner list), signed
//     by the keys that make the change;
//  3. convert each file, reading each back, remembering the original of every file written;
//  4. verify: every file is in the new state and opens with the new keys, with the content it
//     had;
//  5. commit: make the new owner list the current one in Config, clearing the journal.
//
// Any failure in 3 to 5 puts the originals back and the old Config back. The originals live in
// memory: if the page dies in the middle, the journal is what is left, and Recover finishes the
// change (every step is safe to repeat: a file already in its new state is left as it is, one
// not yet converted is converted) or undoes it.

// lockAll takes the storage's own lock and then the gate that Adapter() writes hold shared, in the
// order the saver does (storage lock, then adapter), so a change and a save cannot deadlock.
func (s *Store) lockAll(ctx context.Context) (func(), error) {
	release := func() {}
	if l, ok := s.Raw.(docstore.Locker); ok {
		r, err := l.Lock(ctx)
		if err != nil {
			return nil, err
		}
		release = r
	}
	s.gate.Lock()
	return func() { s.gate.Unlock(); release() }, nil
}

// bare is Raw without its optional interfaces: what a change uses, so the adapters it builds do not
// take the storage's lock again (the change holds it already).
type bare struct{ docstore.Adapter }

type origFile struct {
	data    []byte
	existed bool
}

// recorder is the adapter a change writes through: before the first write to an id it keeps the
// original, and after each write it reads the id back and refuses a store that did not keep what
// it was given.
type recorder struct {
	inner docstore.Adapter
	mu    sync.Mutex
	orig  map[string]origFile
	order []string
}

func newRecorder(inner docstore.Adapter) *recorder {
	return &recorder{inner: inner, orig: map[string]origFile{}}
}

func (r *recorder) note(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.orig[id]; ok {
		return nil
	}
	d, err := r.inner.Load(ctx, id)
	switch {
	case err == nil:
		r.orig[id] = origFile{data: bytes.Clone(d.Data), existed: true}
	case errors.Is(err, docstore.ErrNotFound):
		r.orig[id] = origFile{}
	default:
		return err
	}
	r.order = append(r.order, id)
	return nil
}

func (r *recorder) Save(ctx context.Context, id string, data []byte) error {
	if err := r.note(ctx, id); err != nil {
		return err
	}
	if err := r.inner.Save(ctx, id, data); err != nil {
		return err
	}
	back, err := r.inner.Load(ctx, id)
	if err != nil {
		return fmt.Errorf("%s: written but not readable back: %w", id, err)
	}
	if !bytes.Equal(back.Data, data) {
		return fmt.Errorf("%s: the store did not keep what was written", id)
	}
	return nil
}

func (r *recorder) Load(ctx context.Context, id string) (docstore.Document, error) {
	return r.inner.Load(ctx, id)
}
func (r *recorder) List(ctx context.Context) ([]docstore.Meta, error) { return r.inner.List(ctx) }
func (r *recorder) Delete(ctx context.Context, id string) error {
	if err := r.note(ctx, id); err != nil {
		return err
	}
	return r.inner.Delete(ctx, id)
}

// restore puts every file written (or deleted) back as it was, newest first, and checks each.
func (r *recorder) restore(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for i := len(r.order) - 1; i >= 0; i-- {
		id, o := r.order[i], r.orig[r.order[i]]
		if !o.existed {
			if err := r.inner.Delete(ctx, id); err != nil && !errors.Is(err, docstore.ErrNotFound) {
				errs = append(errs, fmt.Errorf("%s: %w", id, err))
			}
			continue
		}
		if err := r.inner.Save(ctx, id, o.data); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			continue
		}
		if back, err := r.inner.Load(ctx, id); err != nil || !bytes.Equal(back.Data, o.data) {
			errs = append(errs, fmt.Errorf("%s: not restored", id))
		}
	}
	return errors.Join(errs...)
}

// Estimated sizes, in bytes, of what age adds: the header and footer of a file, a stanza for each
// recipient, and the frame that binds the file to its id. Generous on purpose: a store that is
// tight is better told so before anything is written.
const (
	ageFixed    = 160
	ageStanza   = 150
	frameBytes  = 100
	configSlack = 8 << 10
)

// checkRoom refuses a change the storage cannot hold, before it writes anything.
func (s *Store) checkRoom(ctx context.Context, from, to []Owner) error {
	if s.Capacity <= 0 || len(to) == 0 {
		return nil
	}
	metas, err := s.Raw.List(ctx)
	if err != nil {
		return err
	}
	var used, need int64
	for _, m := range metas {
		used += int64(m.Size)
		if encrypt.DefaultSkip(m.ID) {
			continue
		}
		d, err := s.Raw.Load(ctx, m.ID)
		if err != nil {
			return err
		}
		switch {
		case !encrypt.IsEncrypted(d.Data):
			need += ageFixed + int64(len(to))*ageStanza + frameBytes
		case len(to) > len(from):
			need += int64(len(to)-len(from)) * ageStanza
		}
	}
	need += configSlack
	if free := s.Capacity - used; need > free {
		return &NoRoomError{Need: need, Free: free}
	}
	return nil
}

// snapshot reads every stored file that is not left readable (through the keys held, in the clear
// where it is in the clear) and returns each one's content digest: what verify compares against.
// A file that cannot be opened is reported before anything is written.
func (s *Store) snapshot(ctx context.Context, ids []age.Identity) (map[string]string, error) {
	rd := encrypt.New(bare{s.Raw}, encrypt.Static{Keys: ids})
	rd.AllowPlaintext = true
	metas, err := s.Raw.List(ctx)
	if err != nil {
		return nil, err
	}
	before := map[string]string{}
	var bad []string
	for _, m := range metas {
		if encrypt.DefaultSkip(m.ID) {
			continue
		}
		d, err := rd.Load(ctx, m.ID)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			bad = append(bad, m.ID)
			continue
		}
		before[m.ID] = docstore.Digest(d.Data)
	}
	if len(bad) > 0 {
		return nil, &UnreadableError{IDs: bad}
	}
	return before, nil
}

// sealPlain encrypts every file that is still in the clear to the owner list. It needs no private
// key; a file that is already encrypted is left as it is.
func (s *Store) sealPlain(ctx context.Context, view docstore.Adapter, to []Owner) error {
	recips, _, err := parseOwners(to)
	if err != nil {
		return err
	}
	rs := make([]age.Recipient, len(recips))
	for i, r := range recips {
		rs[i] = r.Key
	}
	mig := encrypt.New(view, encrypt.Static{To: rs})
	metas, err := view.List(ctx)
	if err != nil {
		return err
	}
	for _, m := range metas {
		if encrypt.DefaultSkip(m.ID) {
			continue
		}
		d, err := view.Load(ctx, m.ID)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if encrypt.IsEncrypted(d.Data) {
			continue
		}
		if err := mig.Save(ctx, m.ID, d.Data); err != nil {
			return fmt.Errorf("%s: %w", m.ID, err)
		}
	}
	return nil
}

// toPlain decrypts every encrypted file back into the store and removes the lists of who
// documents are shared with (they have no meaning without encryption).
func (s *Store) toPlain(ctx context.Context, view docstore.Adapter, ids []age.Identity) error {
	if len(ids) == 0 {
		return ErrLocked
	}
	rd := encrypt.New(view, encrypt.Static{Keys: ids})
	rd.AllowPlaintext = true
	metas, err := view.List(ctx)
	if err != nil {
		return err
	}
	for _, m := range metas {
		if encrypt.DefaultSkip(m.ID) {
			continue
		}
		d, err := view.Load(ctx, m.ID)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if !encrypt.IsEncrypted(d.Data) {
			continue
		}
		plain, err := rd.Load(ctx, m.ID)
		if err != nil {
			return fmt.Errorf("%s: %w", m.ID, err)
		}
		if err := view.Save(ctx, m.ID, plain.Data); err != nil {
			return err
		}
	}
	return nil
}

// dropLists removes the "keys:" records.
func dropLists(ctx context.Context, view docstore.Adapter) error {
	metas, err := view.List(ctx)
	if err != nil {
		return err
	}
	for _, m := range metas {
		if strings.HasPrefix(m.ID, encrypt.KeysPrefix) {
			if err := view.Delete(ctx, m.ID); err != nil && !errors.Is(err, docstore.ErrNotFound) {
				return err
			}
		}
	}
	return nil
}

// rekeyAll encrypts every file again to the owner list, whatever recipients it has now (or none:
// a file still in the clear is sealed), re-signing the lists first when resign is set.
func (s *Store) rekeyAll(ctx context.Context, view docstore.Adapter, to []Owner, ids []age.Identity, verifiers []ed25519.PublicKey, resign *encrypt.Signer) error {
	if len(ids) == 0 {
		return ErrLocked
	}
	recips, _, err := parseOwners(to)
	if err != nil {
		return err
	}
	rs := make([]age.Recipient, len(recips))
	for i, r := range recips {
		rs[i] = r.Key
	}
	mig := encrypt.New(view, encrypt.ConfidentialKeyring{
		People: encrypt.StoreKeyring{Store: view, Owners: rs, Keys: ids, OwnerSigners: verifiers},
		AtRest: encrypt.Static{To: rs, Keys: ids},
	})
	mig.AllowPlaintext = true
	if resign != nil {
		metas, err := view.List(ctx)
		if err != nil {
			return err
		}
		sh := &encrypt.Sharing{Store: view, Adapter: mig, Signer: resign, OwnerSigners: verifiers}
		for _, m := range metas {
			if doc, ok := strings.CutPrefix(m.ID, encrypt.KeysPrefix); ok {
				if err := sh.Resign(ctx, doc); err != nil {
					return fmt.Errorf("%s: %w", m.ID, err)
				}
			}
		}
	}
	_, err = mig.RekeyAll(ctx)
	return err
}

// convert brings every stored file to the state of the owner list "to" (none: in the clear).
// current says whether the store has owner keys now (what tells sealing the clear files, which
// needs no private key, from re-keying).
func (s *Store) convert(ctx context.Context, view docstore.Adapter, current, to []Owner, ids []age.Identity, verifiers []ed25519.PublicKey, resign *encrypt.Signer) error {
	switch {
	case len(to) == 0:
		return s.toPlain(ctx, view, ids)
	case len(current) == 0:
		return s.sealPlain(ctx, view, to)
	}
	return s.rekeyAll(ctx, view, to, ids, verifiers, resign)
}

// verifyAll checks every stored file is in the state "to" says and, where private keys are held,
// that it opens with them to the content it had (before, when known).
func (s *Store) verifyAll(ctx context.Context, view docstore.Adapter, to []Owner, ids []age.Identity, before map[string]string) error {
	rd := encrypt.New(view, encrypt.Static{Keys: ids})
	metas, err := view.List(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, m := range metas {
		if encrypt.DefaultSkip(m.ID) {
			continue
		}
		seen[m.ID] = true
		d, err := view.Load(ctx, m.ID)
		if err != nil {
			return fmt.Errorf("verify %s: %w", m.ID, err)
		}
		enc := encrypt.IsEncrypted(d.Data)
		if len(to) == 0 {
			if enc {
				return fmt.Errorf("verify %s: still encrypted", m.ID)
			}
			if want, ok := before[m.ID]; ok && docstore.Digest(d.Data) != want {
				return fmt.Errorf("verify %s: the content changed", m.ID)
			}
			continue
		}
		if !enc {
			return fmt.Errorf("verify %s: still in the clear", m.ID)
		}
		if len(ids) == 0 {
			continue // no private key here (an SSH key that is not unlocked): the read-back is what could be checked
		}
		p, err := rd.Load(ctx, m.ID)
		if err != nil {
			return fmt.Errorf("verify %s: does not open with the new keys: %w", m.ID, err)
		}
		if want, ok := before[m.ID]; ok && docstore.Digest(p.Data) != want {
			return fmt.Errorf("verify %s: the content changed", m.ID)
		}
	}
	for id := range before {
		if !seen[id] {
			return fmt.Errorf("verify %s: the file is gone", id)
		}
	}
	return nil
}

// change is one transaction.
type change struct {
	op string
	to []Owner
	// keys are the keys being added (their private side signs and verifies, when they carry it).
	keys []NewKey
	// resign says owner signing keys left the list: the signed lists of documents are signed
	// again by a key that stays.
	resign bool
}

// unlockedNow copies what is held: identities, signers and the owner each belongs to.
func (s *Store) unlockedNow() (ids []age.Identity, sigs []*encrypt.Signer, holders []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.ids), slices.Clone(s.sigs), slices.Clone(s.holders)
}

func signerPubs(owners []Owner) []ed25519.PublicKey {
	var out []ed25519.PublicKey
	for _, o := range owners {
		if b, err := hex.DecodeString(o.Signer); err == nil && len(b) == ed25519.PublicKeySize {
			out = append(out, ed25519.PublicKey(b))
		}
	}
	return out
}

// keeper picks the signing key that carries the lists over: one held that stays an owner.
func keeper(to []Owner, sigs []*encrypt.Signer) *encrypt.Signer {
	for _, sg := range sigs {
		if hasSigner(to, hex.EncodeToString(sg.Public())) {
			return sg
		}
	}
	return nil
}

// run does the transaction c. The caller has checked what can be checked without the lock.
func (s *Store) run(ctx context.Context, c change) error {
	unlock, err := s.lockAll(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	old := s.Config()
	ids, sigs, holders := s.unlockedNow()
	// The keys being added sign and verify too, while they are in hand.
	verifyIDs, allSigs := slices.Clone(ids), slices.Clone(sigs)
	for _, k := range c.keys {
		if k.Unlock != nil {
			verifyIDs, allSigs = append(verifyIDs, k.Unlock.Identity), append(allSigs, k.Unlock.Signer)
		}
	}
	var resign *encrypt.Signer
	if c.resign {
		if resign = keeper(c.to, allSigs); resign == nil {
			return ErrUseAnotherKey
		}
	}
	if err := s.checkRoom(ctx, old.Owners, c.to); err != nil {
		return err
	}
	before, err := s.snapshot(ctx, verifyIDs)
	if err != nil {
		return err
	}
	// The journal, before anything is written to a file.
	jr := cloneConfig(old)
	jr.Pending = &Pending{Op: c.op, To: slices.Clone(c.to), Seals: pendingSeals(c.to, allSigs)}
	if err := s.persist(jr); err != nil {
		return err
	}
	view := newRecorder(bare{s.Raw})
	fail := func(cause error) error {
		if rerr := view.restore(ctx); rerr != nil {
			_ = s.install(jr)
			return errors.Join(ErrRollbackFailed, cause, rerr)
		}
		if perr := s.persist(old); perr != nil {
			_ = s.install(jr)
			return errors.Join(ErrRollbackFailed, cause, perr)
		}
		return cause
	}
	verifiers := append(signerPubs(old.Owners), signerPubs(c.to)...)
	if err := s.convert(ctx, view, old.Owners, c.to, ids, verifiers, resign); err != nil {
		return fail(err)
	}
	if len(c.to) == 0 {
		if err := dropLists(ctx, view); err != nil {
			return fail(err)
		}
	}
	if err := s.verifyAll(ctx, view, c.to, verifyIDs, before); err != nil {
		return fail(err)
	}
	final := Config{Owners: slices.Clone(c.to)}
	if len(c.to) > 0 {
		final.Seals, final.Links = bindingFor(old, c.to, allSigs)
		final.Version = versionFor(final)
	}
	if err := s.persist(final); err != nil {
		return fail(err)
	}
	if err := s.install(final); err != nil {
		return fail(err)
	}
	s.afterCommit(c.keys, c.to, ids, sigs, holders)
	return nil
}

// afterCommit sets what is held once a change is made current: the keys that are still owners, and
// the new ones that carry a private side and are not to be forgotten. The sequence floors restart
// (they only protect against going back within a session, and the lists were just re-signed).
func (s *Store) afterCommit(added []NewKey, to []Owner, ids []age.Identity, sigs []*encrypt.Signer, holders []string) {
	var hs []held
	for i := range ids {
		if hasOwnerKey(to, holders[i]) {
			hs = append(hs, held{Unlocked: Unlocked{Identity: ids[i], Signer: sigs[i]}, owner: holders[i]})
		}
	}
	for _, k := range added {
		if k.Unlock != nil && !k.Forget {
			hs = append(hs, held{Unlocked: *k.Unlock, owner: k.Recipient.Text})
		}
	}
	s.unlock(hs...)
	s.mu.Lock()
	s.floor = &encrypt.MemorySeqFloor{}
	s.mu.Unlock()
}

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
		if !ok {
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
// converts it back to the list that is current. Both are safe to run again if they fail. Finishing
// checks the journal's seals first (ErrConfigChanged when it was not made by this store's keys).
// Re-keying needs a key unlocked; sealing files that are still in the clear does not.
func (s *Store) Recover(ctx context.Context, finish bool) error {
	p := s.PendingChange()
	if p == nil {
		return ErrNothingPending
	}
	cfg := s.Config()
	if finish {
		if err := checkJournal(cfg, p); err != nil {
			return err
		}
	}
	unlock, err := s.lockAll(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	target := cfg.Owners
	if finish {
		target = p.To
	}
	ids, sigs, holders := s.unlockedNow()
	var resign *encrypt.Signer
	if p.Op == OpRemove || p.Op == OpReplace {
		if resign = keeper(target, sigs); resign == nil && len(ids) > 0 {
			return ErrUseAnotherKey
		}
	}
	verifiers := append(signerPubs(cfg.Owners), signerPubs(p.To)...)
	view := newRecorder(bare{s.Raw})
	if err := s.convert(ctx, view, cfg.Owners, target, ids, verifiers, resign); err != nil {
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
			final.Seals, final.Links = bindingFor(cfg, p.To, sigs)
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

// checkJournal checks the journal's seals before it is finished: a change of the key list that
// was already there (add, remove, replace, turning off) must be signed by one of the keys that
// were owners; a first enable by one of the keys it adds, when any could sign. A journal that is
// not signed that way was not written by this store.
func checkJournal(cfg Config, p *Pending) error {
	valid := func(allowed []Owner) bool {
		for _, sl := range p.Seals {
			if sl.valid() && ownersDigest(sl.Owners) == ownersDigest(p.To) && hasSigner(allowed, sl.Signer) {
				return true
			}
		}
		return false
	}
	switch {
	case len(cfg.Owners) > 0:
		if !valid(cfg.Owners) {
			return ErrConfigChanged
		}
	case len(p.To) > 0 && len(p.Seals) > 0:
		if !valid(p.To) {
			return ErrConfigChanged
		}
	}
	return nil
}
