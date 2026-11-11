package encstore

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"slices"
	"sort"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

func ownersHash(list []Owner) [32]byte {
	var h [32]byte
	b, err := hex.DecodeString(ownersDigest(list))
	if err == nil {
		copy(h[:], b)
	}
	return h
}

type anchored struct {
	encrypt.ConfidentialKeyring
	anchor [32]byte
}

func (a anchored) mark(b encrypt.Binding, id string) encrypt.Binding {
	if !b.Exists && encrypt.DocID(id) != "" {
		b.Hash = a.anchor
	}
	return b
}

// Bind implements encrypt.Binder.
func (a anchored) Bind(ctx context.Context, id string) ([]age.Recipient, encrypt.Binding, error) {
	rs, b, err := a.ConfidentialKeyring.Bind(ctx, id)
	return rs, a.mark(b, id), err
}

// Binding implements encrypt.Binder.
func (a anchored) Binding(ctx context.Context, id string) (encrypt.Binding, error) {
	b, err := a.ConfidentialKeyring.Binding(ctx, id)
	return a.mark(b, id), err
}

// Recipients implements encrypt.Keyring.
func (a anchored) Recipients(ctx context.Context, id string) ([]age.Recipient, error) {
	rs, _, err := a.Bind(ctx, id)
	return rs, err
}

const maxAnchorFiles = 300

func (s *Store) anchorHashes(ctx context.Context, id age.Identity, verifiers []ed25519.PublicKey) (hashes map[[32]byte]bool, unverified bool, err error) {
	ad := encrypt.New(bare{s.Raw}, encrypt.Static{Keys: []age.Identity{id}})
	ad.OwnerSigners = verifiers
	metas, err := s.Raw.List(ctx)
	if err != nil {
		return nil, false, err
	}
	sort.SliceStable(metas, func(i, j int) bool { return metas[i].UpdatedAt.After(metas[j].UpdatedAt) })
	out := map[[32]byte]bool{}
	looked := 0
	for _, m := range metas {
		if encrypt.DefaultSkip(m.ID) || encrypt.DocID(m.ID) == "" {
			continue
		}
		if looked++; looked > maxAnchorFiles {
			break
		}
		d, err := s.Raw.Load(ctx, m.ID)
		if err != nil || !encrypt.IsEncrypted(d.Data) {
			continue
		}
		h, err := ad.PeekFrame(ctx, m.ID, d.Data)
		if err != nil {
			continue
		}
		if h.Version == 3 && !h.Signed {
			unverified = true
			continue
		}
		if !h.Signed || h.Level != "" || h.Seq != 0 || h.Hash == ([32]byte{}) {
			continue
		}
		out[h.Hash] = true
	}
	return out, unverified, nil
}

func signersOf(lists ...[]Owner) []ed25519.PublicKey {
	var out []ed25519.PublicKey
	for _, l := range lists {
		for _, k := range signerPubs(l) {
			if !slices.ContainsFunc(out, func(x ed25519.PublicKey) bool { return x.Equal(k) }) {
				out = append(out, k)
			}
		}
	}
	return out
}

func (s *Store) checkAnchor(ctx context.Context, o Owner, u Unlocked, lists [][]Owner) error {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if cfg.Pending != nil || len(cfg.Owners) == 0 {
		return nil
	}
	verifiers := signersOf(lists...)
	if lists == nil {
		verifiers = signersOf(cfg.Owners)
	}
	verifiers = append(verifiers, u.Signer.Public())
	return s.anchorMatches(ctx, u.Identity, verifiers, ownersHash(cfg.Owners))
}

func (s *Store) anchorMatches(ctx context.Context, id age.Identity, verifiers []ed25519.PublicKey, allowed ...[32]byte) error {
	seen, unverified, err := s.anchorHashes(ctx, id, verifiers)
	if err != nil {
		return err
	}
	if unverified {
		return ErrConfigChanged
	}
	for h := range seen {
		if !slices.Contains(allowed, h) {
			return ErrConfigChanged
		}
	}
	return nil
}

func HasEncryptedFiles(ctx context.Context, raw docstore.Adapter) (bool, error) {
	metas, err := raw.List(ctx)
	if err != nil {
		return false, err
	}
	for _, m := range metas {
		if encrypt.DefaultSkip(m.ID) {
			continue
		}
		d, err := raw.Load(ctx, m.ID)
		if err != nil {
			if errors.Is(err, docstore.ErrNotFound) {
				continue
			}
			return false, err
		}
		if encrypt.IsEncrypted(d.Data) {
			return true, nil
		}
	}
	return false, nil
}

func Inspect(ctx context.Context, raw docstore.Adapter, cfg Config, readErr error) error {
	if readErr != nil {
		return errors.Join(ErrConfigCorrupt, readErr)
	}
	if len(cfg.Owners) == 0 && cfg.Pending == nil {
		found, err := HasEncryptedFiles(ctx, raw)
		if err != nil {
			return err
		}
		if found {
			return ErrConfigMissing
		}
	}
	return nil
}

type readOnly struct {
	docstore.Adapter
	why error
}

func ReadOnly(raw docstore.Adapter, why error) docstore.Adapter { return readOnly{raw, why} }

func (r readOnly) Save(context.Context, string, []byte) error           { return r.why }
func (r readOnly) Delete(context.Context, string) error                 { return r.why }
func (r readOnly) SaveIf(context.Context, string, []byte, string) error { return r.why }
