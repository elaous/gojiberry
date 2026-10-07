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

// The anchor: what a writer of the browser's storage cannot forge.
//
// Config is kept next to the store, so it can be replaced, stripped of its seals, put back to an
// older valid state or deleted. The files cannot be forged: every document file is encrypted, and
// its frame (inside the encryption) is signed by an owner's signing key (frame v3, docs/
// encryption-and-passkeys.md C.5). The store writes into the frame of every document that has no
// signed record of its own the hash of the owner list the file was sealed under (anchored below,
// as Binding.Hash). At unlock, every such frame that verifies must carry the hash of the list in
// Config: a list that is not the one the files were sealed under is ErrConfigChanged. That makes
// "strip the seals", "a first-generation config", "an older valid config" and "a replaced list"
// visible even to an SSH key that has no seal yet, as long as one document file exists that was
// written by an unlocked store.
//
// What it cannot do is in the package comment: it needs a signed document file (an SSH-only store
// has none until its first save), and it cannot tell a rollback of the whole store (files and
// config together) from the truth.

// ownersHash is the digest of an owner list as the 32 bytes a frame carries.
func ownersHash(list []Owner) [32]byte {
	var h [32]byte
	b, err := hex.DecodeString(ownersDigest(list))
	if err == nil {
		copy(h[:], b)
	}
	return h
}

// anchored is a ConfidentialKeyring that also binds the owner list: for a document with no signed
// record (Exists false) the Binding it gives carries the hash of the owner list in Hash, which the
// Adapter writes into the signed frame. A document with a record keeps the record's own binding.
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

// maxAnchorFiles bounds the files looked at: the newest ones.
const maxAnchorFiles = 300

// anchorHashes is the set of owner-list hashes carried by the verifiable signed frames of the
// stored documents that the identity opens. A frame is counted when it is v3, its signature is by
// one of the verifiers, and it is a document with no record (level server, seq 0, a hash).
// unverified says that a v3 frame was found whose signer is none of the verifiers: no owner the
// list knows signed it, so either the list dropped the key that did or someone else wrote it.
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

// signersOf is the signing keys of a list of owners.
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

// checkAnchor is the anchor check of an unlock: every verifiable anchored frame must carry the
// hash of the Config's owner list. lists are the lists the key's own seal vouches for (their
// signing keys verify the frames; for an SSH key with no seal yet, only its own and the ones in
// the Config, which an attacker could name: frames signed by the real owners still disagree).
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

// anchorMatches is ErrConfigChanged when a verifiable anchored frame carries a hash that is not
// one of allowed, or a signed frame has a signer none of the verifiers is.
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

// HasEncryptedFiles says whether any stored item that is not a catalog or a record is an age file.
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

// Inspect is what a page asks at load, before it trusts that encryption is off: readErr is the
// error of reading the stored Config (nil when it was read or absent). A Config that could not be
// read is ErrConfigCorrupt; none (no owners, no unfinished change) over files that are encrypted
// is ErrConfigMissing. In both the store must stay read-only: saving plain text beside ciphertext
// would be wrong, and "encryption off" a lie. The ways out are Restore* (a key that opens the
// files) and Erase.
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

// readOnly is the adapter of a store that must not be written: reads pass (the files are what
// they are), every write is why.
type readOnly struct {
	docstore.Adapter
	why error
}

// ReadOnly wraps raw so every write is refused with why (ErrConfigMissing or ErrConfigCorrupt).
func ReadOnly(raw docstore.Adapter, why error) docstore.Adapter { return readOnly{raw, why} }

func (r readOnly) Save(context.Context, string, []byte) error           { return r.why }
func (r readOnly) Delete(context.Context, string) error                 { return r.why }
func (r readOnly) SaveIf(context.Context, string, []byte, string) error { return r.why }
