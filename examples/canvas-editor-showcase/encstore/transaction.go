package encstore

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"filippo.io/age"
	"fmt"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"slices"
	"strings"
	"sync"
)

// lock order: storage lock, then the adapter gate
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
	if err := s.fresh(); err != nil {
		s.gate.Unlock()
		release()
		return nil, err
	}
	return func() { s.gate.Unlock(); release() }, nil
}

type bare struct{ docstore.Adapter }

type origFile struct {
	data    []byte
	existed bool
}

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

const (
	ageFixed    = 160
	ageStanza   = 150
	frameBytes  = 200
	configSlack = 8 << 10
	roomMargin  = 2
	// the key and JSON around the base64 body, in characters
	recordSlack = 100
)

func storedUnits(n int64) int64 { return (n+2)/3*4 + recordSlack }

func (s *Store) checkRoom(ctx context.Context, from, to []Owner) error {
	if s.Capacity <= 0 || len(to) == 0 {
		return nil
	}
	metas, err := s.Raw.List(ctx)
	if err != nil {
		return err
	}
	var used, need int64
	grow := func(size int, extra int64) {
		need += roomMargin * (storedUnits(int64(size)+extra) - storedUnits(int64(size)))
	}
	for _, m := range metas {
		used += storedUnits(int64(m.Size))
		if encrypt.DefaultSkip(m.ID) {
			continue
		}
		d, err := s.Raw.Load(ctx, m.ID)
		if err != nil {
			return err
		}
		switch {
		case !encrypt.IsEncrypted(d.Data):
			grow(m.Size, ageFixed+int64(len(to))*ageStanza+frameBytes)
		case len(to) > len(from):
			grow(m.Size, int64(len(to)-len(from))*ageStanza+frameBytes)
		default:
			grow(m.Size, frameBytes) // the frame is rewritten with its signature and the anchor
		}
	}
	need += configSlack
	if free := s.Capacity - used; need > free {
		return &NoRoomError{Need: need, Free: free}
	}
	return nil
}

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

func (s *Store) sealPlain(ctx context.Context, view docstore.Adapter, to []Owner, frames *encrypt.Signer) error {
	recips, _, err := parseOwners(to)
	if err != nil {
		return err
	}
	rs := make([]age.Recipient, len(recips))
	for i, r := range recips {
		rs[i] = r.Key
	}
	mig := encrypt.New(view, anchored{ConfidentialKeyring: encrypt.ConfidentialKeyring{
		People: encrypt.StoreKeyring{Store: view, Owners: rs},
		AtRest: encrypt.Static{To: rs},
	}, anchor: ownersHash(to)})
	mig.AllowPlaintext = true
	mig.Signer = frames
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

func (s *Store) rekeyAll(ctx context.Context, view docstore.Adapter, to []Owner, ids []age.Identity, verifiers []ed25519.PublicKey, resign, frames *encrypt.Signer, trust bool) error {
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
	mig := encrypt.New(view, anchored{ConfidentialKeyring: encrypt.ConfidentialKeyring{
		People: encrypt.StoreKeyring{Store: view, Owners: rs, Keys: ids, OwnerSigners: verifiers},
		AtRest: encrypt.Static{To: rs, Keys: ids},
	}, anchor: ownersHash(to)})
	mig.AllowPlaintext = true
	mig.RekeyProgress = s.Progress
	mig.Signer = frames
	mig.OwnerSigners = verifiers
	mig.TrustFrames = trust
	if resign != nil {
		metas, err := view.List(ctx)
		if err != nil {
			return err
		}
		sh := &encrypt.Sharing{Store: view, Adapter: mig, Signer: resign, OwnerSigners: verifiers}
		for _, m := range metas {
			if doc, ok := strings.CutPrefix(m.ID, encrypt.KeysPrefix); ok && m.ID != encrypt.IndexKey {
				if err := sh.Resign(ctx, doc); err != nil {
					return fmt.Errorf("%s: %w", m.ID, err)
				}
			}
		}
		if err := sh.ResignIndex(ctx); err != nil {
			return fmt.Errorf("%s: %w", encrypt.IndexKey, err)
		}
	}
	_, err = mig.RekeyAll(ctx)
	return err
}

func (s *Store) convert(ctx context.Context, view docstore.Adapter, current, to []Owner, ids []age.Identity, verifiers []ed25519.PublicKey, resign, frames *encrypt.Signer, rekey bool) error {
	switch {
	case len(to) == 0:
		return s.toPlain(ctx, view, ids)
	case len(current) == 0 && !rekey:
		return s.sealPlain(ctx, view, to, frames)
	}
	return s.rekeyAll(ctx, view, to, ids, verifiers, resign, frames, rekey)
}

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
			continue
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

type change struct {
	op   string
	to   []Owner
	keys []NewKey
	// owner signing keys left the list: lists are signed again by a staying key
	resign bool
	// files are already encrypted to keys the change does not know (a restore)
	rekey bool
}

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

func keeper(to []Owner, sigs []*encrypt.Signer) *encrypt.Signer {
	for _, sg := range sigs {
		if hasSigner(to, hex.EncodeToString(sg.Public())) {
			return sg
		}
	}
	return nil
}

func frameSigner(to []Owner, sigs []*encrypt.Signer) *encrypt.Signer {
	if k := keeper(to, sigs); k != nil {
		return k
	}
	if len(sigs) > 0 {
		return sigs[0]
	}
	return nil
}

func (s *Store) run(ctx context.Context, c change) (err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	unlock, err := s.lockAll(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	s.mu.Lock()
	s.stop, s.stopped = cancel, false
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		stopped := s.stopped
		s.stop, s.stopped = nil, false
		s.mu.Unlock()
		if stopped && err != nil && !errors.Is(err, ErrRollbackFailed) {
			err = ErrStopped
		}
	}()
	old := s.Config()
	ids, sigs, holders := s.unlockedNow()
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
	convIDs := ids
	if c.rekey {
		convIDs = verifyIDs // a restore re-keys with the key that was given
	}
	before, err := s.snapshot(ctx, verifyIDs)
	if err != nil {
		return err
	}
	// the journal, before anything is written to a file
	jr := cloneConfig(old)
	jr.Pending = &Pending{Op: c.op, To: slices.Clone(c.to), Seals: pendingSeals(c.to, allSigs), Link: journalLink(old.Owners, c.to, allSigs)}
	if len(old.Owners) > 0 {
		jr.Pending.From = ownersDigest(old.Owners)
	}
	if err := s.persist(jr); err != nil {
		return err
	}
	view := newRecorder(bare{s.Raw})
	fail := func(cause error) error {
		ctx := context.WithoutCancel(ctx)
		// restoring ignores the stop that asked for it
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
	if err := s.convert(ctx, view, old.Owners, c.to, convIDs, verifiers, resign, frameSigner(c.to, allSigs), c.rekey); err != nil {
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
		final.Seals, final.Links = bindingFor(old, c.to, allSigs, jr.Pending.Link)
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
	digest := ownersDigest(s.cfg.Owners)
	s.mu.Unlock()
	if s.ConfirmFirstUse {
		s.TrustList(digest)
	}
}

func (s *Store) Stop() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop == nil {
		return false
	}
	s.stopped = true
	s.stop()
	return true
}
