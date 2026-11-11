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
	"time"
)

// UnlockPasskey unlocks with the 32 bytes a passkey answered (client.DeriveLocal).
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
	s.sealOnUnlock(o, u.Signer)
	// first SSH use, or a legitimate list move: bind now
	if o.Kind != KindSSH && s.ConfirmFirstUse {
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

type FirstUseError struct {
	Digest, Fingerprint string
}

func (e *FirstUseError) Error() string { return ErrFirstUse.Error() + " (" + e.Fingerprint + ")" }

func (e *FirstUseError) Unwrap() error { return ErrFirstUse }

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

func (s *Store) ListFingerprint() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.cfg.Owners) == 0 {
		return ""
	}
	return ListFingerprint(ownersDigest(s.cfg.Owners))
}

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

// an error leaves nothing changed; unlock need not stop
func (s *Store) RecordUse(ctx context.Context, o Owner, device string, now time.Time) error {
	fp := o.Fingerprint()
	if fp == "" {
		return nil
	}
	if !s.Unlocked() {
		return ErrLocked
	}
	return s.strict().RecordDeviceUse(ctx, fp, device, now)
}

func (s *Store) DeviceUses(ctx context.Context) ([]encrypt.DeviceUse, error) {
	if !s.Unlocked() {
		return nil, ErrLocked
	}
	all, err := s.strict().DeviceUses(ctx)
	if err != nil {
		return nil, err
	}
	var out []encrypt.DeviceUse
	for _, u := range all {
		for _, r := range s.Recipients() {
			if r.Fingerprint == u.Fingerprint {
				out = append(out, u)
				break
			}
		}
	}
	return out, nil
}
