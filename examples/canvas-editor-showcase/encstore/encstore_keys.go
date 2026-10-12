package encstore

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"filippo.io/age"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"golang.org/x/crypto/ssh"
	"strings"
)

// UnlockPasskey unlocks with the 32 bytes a passkey answered (client.DeriveLocal). The secret is
// cleared here on every path: the caller need not.
func (s *Store) UnlockPasskey(secret []byte) error {
	defer clear(secret)
	id, err := encrypt.PRFIdentity(secret)
	if err != nil {
		return err
	}
	o, ok := s.findOwner(KindPasskey, id.Recipient().String())
	if !ok {
		return ErrNotAKey
	}
	return s.finishUnlock(o, Unlocked{Identity: id, Signer: encrypt.SignerFromAge(id)})
}

// UnlockRecovery unlocks with a recovery key's secret ("AGE-SECRET-KEY-1...").
func (s *Store) UnlockRecovery(secret string) error {
	ident, err := encrypt.ParseAgeIdentity(secret)
	if err != nil {
		return err
	}
	id, ok := ident.(*age.X25519Identity)
	if !ok {
		return ErrNotAKey
	}
	o, ok := s.findOwner(KindRecovery, id.Recipient().String())
	if !ok {
		return ErrNotAKey
	}
	return s.finishUnlock(o, Unlocked{Identity: id, Signer: encrypt.SignerFromAge(id)})
}

// UnlockSSH unlocks with the private key file (PEM, not passphrase protected) of an ed25519
// SSH key that is one of the owners. pem is cleared here on every path (success, a file that is
// not a key, a key that is not an owner): the caller need not.
func (s *Store) UnlockSSH(pem []byte) error {
	defer clear(pem)
	id, err := encrypt.ParseSSHIdentity(pem)
	if err != nil {
		return err
	}
	signer, err := encrypt.SignerFromSSH(pem)
	if err != nil {
		return err
	}
	pub := hex.EncodeToString(signer.Public())
	s.mu.RLock()
	var match Owner
	found := false
	for _, o := range s.ownersFor() {
		if o.Kind == KindSSH && o.Signer == pub {
			match, found = o, true
		}
	}
	s.mu.RUnlock()
	if !found {
		return ErrNotAKey
	}
	return s.finishUnlock(match, Unlocked{Identity: id, Signer: signer})
}

// finishUnlock checks the key list against the key's seal and against the files' anchor, then
// holds the key. A tab whose Config is out of date does not unlock.
func (s *Store) finishUnlock(o Owner, u Unlocked) error {
	if err := s.fresh(); err != nil {
		return err
	}
	lists, err := s.checkSeal(o)
	if err != nil {
		return err
	}
	if err := s.checkFirstUse(o); err != nil {
		return err
	}
	if err := s.checkAnchor(context.Background(), o, u, lists); err != nil {
		return err
	}
	s.unlock(held{Unlocked: u, owner: o.Public})
	s.sealOnUnlock(o, u.Signer) // first use of an SSH key, or the list moved on legitimately: bind it now
	if o.Kind != KindSSH && s.ConfirmFirstUse {
		// a passkey or recovery key opened the list through the chain of seals: it is not an
		// attacker's list, so an SSH key of it needs no confirmation here
		s.mu.RLock()
		d := ownersDigest(s.cfg.Owners)
		s.mu.RUnlock()
		s.TrustList(d)
	}
	if s.OnUnlock != nil {
		s.OnUnlock(o)
	}
	return nil
}

// HasKind says whether the store has an owner key of that kind (which unlock method to offer).
func (s *Store) HasKind(kind string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, o := range s.ownersFor() {
		if o.Kind == kind {
			return true
		}
	}
	return false
}

// PublicOf is the SSH public key text's Ed25519 key, the signing key an ed25519 SSH owner has
// (an RSA key cannot sign, so it cannot be the one that unlocks a store's lists).
func PublicOf(sshPublicKey string) (ed25519.PublicKey, error) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(sshPublicKey))
	if err != nil {
		return nil, err
	}
	cp, ok := pub.(ssh.CryptoPublicKey)
	if !ok {
		return nil, errors.New("encstore: not an ed25519 SSH key")
	}
	ed, ok := cp.CryptoPublicKey().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("encstore: only an ed25519 SSH key can sign: RSA keys cannot be used here")
	}
	return ed, nil
}

// NewPasskeyKey makes the owner key of a passkey from the 32 secret bytes it answered. The
// caller clears secret.
func NewPasskeyKey(secret []byte, label string) (NewKey, error) {
	r, err := encrypt.PRFRecipient(secret, label, "")
	if err != nil {
		return NewKey{}, err
	}
	id, err := encrypt.PRFIdentity(secret)
	if err != nil {
		return NewKey{}, err
	}
	sg := encrypt.SignerFromAge(id)
	return NewKey{Kind: KindPasskey, Label: label, Recipient: r, SignerPub: sg.Public(), Unlock: &Unlocked{Identity: id, Signer: sg}}, nil
}

// NewRecoveryKey makes a recovery key: the owner key (public half, with its signing key) and the
// secret to be shown once and kept offline. The private side is not kept here. The key is not
// accepted as an owner until the caller sets SecretSaved, after the person confirmed they saved
// the secret (ErrSecretNotSaved otherwise).
func NewRecoveryKey(label string) (key NewKey, secret string, err error) {
	pub, secret, err := encrypt.GenerateRecoveryKey()
	if err != nil {
		return NewKey{}, "", err
	}
	r, err := encrypt.ParseAgeRecipient(pub)
	if err != nil {
		return NewKey{}, "", err
	}
	r.Label = label
	ident, err := encrypt.ParseAgeIdentity(secret)
	if err != nil {
		return NewKey{}, "", err
	}
	xid, ok := ident.(*age.X25519Identity)
	if !ok {
		return NewKey{}, "", errors.New("encstore: unexpected identity type")
	}
	return NewKey{Kind: KindRecovery, Label: label, Recipient: r, SignerPub: encrypt.SignerFromAge(xid).Public(),
		Unlock: &Unlocked{Identity: xid, Signer: encrypt.SignerFromAge(xid)}}, secret, nil
}

// NewSSHKey makes the owner key of an SSH public key the person pasted (ed25519 only: the key
// also signs the lists of who a document is shared with). Unlocking later takes its private key file.
func NewSSHKey(publicKey string) (NewKey, error) {
	r, err := encrypt.ParseSSHRecipient(publicKey)
	if err != nil {
		return NewKey{}, err
	}
	pub, err := PublicOf(publicKey)
	if err != nil {
		return NewKey{}, err
	}
	label := r.Label
	if label == "" {
		label = "SSH key"
	}
	r.Label = label
	return NewKey{Kind: KindSSH, Label: label, Recipient: r, SignerPub: pub}, nil
}

// CheckRecoveryKey says whether secret ("AGE-SECRET-KEY-1...") is the secret of one of this store's recovery
// keys, without unlocking anything, writing anything or keeping the secret: it parses it, derives the public
// key and compares that with the store's list. ErrNotAKey if it is a key but none of this store's.
func (s *Store) CheckRecoveryKey(secret string) error {
	ident, err := encrypt.ParseAgeIdentity(strings.TrimSpace(secret))
	if err != nil {
		return err
	}
	xid, ok := ident.(*age.X25519Identity)
	if !ok {
		return ErrNotAKey
	}
	pub := xid.Recipient().String()
	for _, o := range s.Owners() {
		if o.Kind == KindRecovery && o.Public == pub {
			return nil
		}
	}
	return ErrNotAKey
}

// ErrFirstUse is wrapped by *FirstUseError.
var ErrFirstUse = errors.New("this store's key list is new to this device: check its fingerprint before the first unlock")

// FirstUseError is returned by an unlock that would trust the key list on first use while
// ConfirmFirstUse is set. Digest is what TrustList takes; Fingerprint is the list's short form to show
// (and to compare with what another device or the person who made the store shows).
type FirstUseError struct {
	Digest, Fingerprint string
}

func (e *FirstUseError) Error() string { return ErrFirstUse.Error() + " (" + e.Fingerprint + ")" }

func (e *FirstUseError) Unwrap() error { return ErrFirstUse }

// ListFingerprint is the fingerprint of the current key list: the first 128 bits of its digest in groups
// of four hexadecimal digits.
func ListFingerprint(digest string) string {
	if len(digest) > 32 {
		digest = digest[:32]
	}
	var parts []string
	for i := 0; i < len(digest); i += 4 {
		parts = append(parts, digest[i:min(i+4, len(digest))])
	}
	return strings.Join(parts, " ")
}

// ListFingerprint is the fingerprint of the store's current key list, to show where the store is made
// and to compare at a first unlock on another device ("" while encryption is off).
func (s *Store) ListFingerprint() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.cfg.Owners) == 0 {
		return ""
	}
	return ListFingerprint(ownersDigest(s.cfg.Owners))
}

// TrustList records that the person confirmed the key list with this digest, so the first unlock with an
// SSH key of it goes through. The host keeps the digests it passes here between visits.
func (s *Store) TrustList(digest string) {
	s.mu.Lock()
	if s.trusted == nil {
		s.trusted = map[string]bool{}
	}
	fresh := !s.trusted[digest]
	s.trusted[digest] = true
	s.mu.Unlock()
	if fresh && s.OnTrust != nil {
		s.OnTrust(digest)
	}
}

// checkFirstUse stops the first use of an unsealed SSH key of a list nobody confirmed (ConfirmFirstUse).
func (s *Store) checkFirstUse(o Owner) error {
	if !s.ConfirmFirstUse || o.Kind != KindSSH {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.cfg
	if cfg.Pending != nil || ownSeal(cfg, o) != nil {
		return nil
	}
	d := ownersDigest(cfg.Owners)
	if s.trusted[d] {
		return nil
	}
	return &FirstUseError{Digest: d, Fingerprint: ListFingerprint(d)}
}
