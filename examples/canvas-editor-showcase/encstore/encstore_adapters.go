package encstore

import (
	"context"
	"filippo.io/age"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"slices"
)

type ring struct{ s *Store }

func (r ring) keyring() (anchored, error) {
	s := r.s
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.owners) == 0 {
		return anchored{}, ErrDisabled
	}
	if len(s.ids) == 0 {
		return anchored{}, ErrLocked
	}
	to := make([]age.Recipient, len(s.owners))
	for i, o := range s.owners {
		to[i] = o.Key
	}
	return anchored{ConfidentialKeyring: encrypt.ConfidentialKeyring{
		People: encrypt.StoreKeyring{Store: s.Raw, Owners: to, Keys: slices.Clone(s.ids), OwnerSigners: slices.Clone(s.signers), SeqFloor: s.floor},
		AtRest: encrypt.Static{To: to, Keys: slices.Clone(s.ids)},
	}, anchor: ownersHash(s.cfg.Owners)}, nil
}

// Recipients implements encrypt.Keyring.
func (r ring) Recipients(ctx context.Context, id string) ([]age.Recipient, error) {
	k, err := r.keyring()
	if err != nil {
		return nil, err
	}
	return k.Recipients(ctx, id)
}

func (r ring) Bind(ctx context.Context, id string) ([]age.Recipient, encrypt.Binding, error) {
	k, err := r.keyring()
	if err != nil {
		return nil, encrypt.Binding{}, err
	}
	return k.Bind(ctx, id)
}

// Binding implements encrypt.Binder.
func (r ring) Binding(ctx context.Context, id string) (encrypt.Binding, error) {
	k, err := r.keyring()
	if err != nil {
		return encrypt.Binding{}, err
	}
	return k.Binding(ctx, id)
}

// Identities implements encrypt.Keyring.
func (r ring) Identities(ctx context.Context, id string) ([]age.Identity, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	return slices.Clone(r.s.ids), nil
}

type front struct{ s *Store }

func (f front) writable() error {
	if err := f.s.fresh(); err != nil {
		return err
	}
	if f.s.Interrupted() {
		return ErrInterrupted
	}
	return nil
}

func (f front) reader() *encrypt.Adapter {
	if f.s.Interrupted() {
		return f.s.tolerant()
	}
	return f.s.strict()
}

// Save implements docstore.Adapter.
func (f front) Save(ctx context.Context, id string, data []byte) error {
	f.s.gate.RLock()
	defer f.s.gate.RUnlock()
	if err := f.writable(); err != nil {
		return err
	}
	return f.s.strict().Save(ctx, id, data)
}

// SaveIf implements docstore.ConditionalAdapter.
func (f front) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	f.s.gate.RLock()
	defer f.s.gate.RUnlock()
	if err := f.writable(); err != nil {
		return err
	}
	return f.s.strict().SaveIf(ctx, id, data, prev)
}

// Load implements docstore.Adapter.
func (f front) Load(ctx context.Context, id string) (docstore.Document, error) {
	f.s.gate.RLock()
	defer f.s.gate.RUnlock()
	return f.reader().Load(ctx, id)
}

// List implements docstore.Adapter.
func (f front) List(ctx context.Context) ([]docstore.Meta, error) {
	f.s.gate.RLock()
	defer f.s.gate.RUnlock()
	return f.s.strict().List(ctx)
}

// ListPrefix implements docstore.PrefixLister.
func (f front) ListPrefix(ctx context.Context, prefix string) ([]docstore.Meta, error) {
	f.s.gate.RLock()
	defer f.s.gate.RUnlock()
	return f.s.strict().ListPrefix(ctx, prefix)
}

// Delete implements docstore.Adapter.
func (f front) Delete(ctx context.Context, id string) error {
	f.s.gate.RLock()
	defer f.s.gate.RUnlock()
	if err := f.writable(); err != nil {
		return err
	}
	return f.s.strict().Delete(ctx, id)
}

// Lock implements docstore.Locker by the inner store's.
func (f front) Lock(ctx context.Context) (func(), error) { return f.s.strict().Lock(ctx) }
