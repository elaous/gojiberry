// Package encstore is the key management behind the showcase's "Encrypt this store": a document
// store whose files are encrypted to the owner's keys (a passkey, an SSH key, a recovery key),
// with the owner keys as public information kept on the device and the private side in memory
// only, while the store is unlocked.
//
// It is the host code that docs/encryption-and-passkeys.md leaves to an application: which keys
// the owner has, where their public halves are kept (Config), how a key unlocks the store
// (UnlockPasskey, UnlockRecovery, UnlockSSH), what is forgotten when it is locked, and the hooks
// the saver asks (Check before a document is made confidential, Rekey when its level changes).
// It has no browser code, so everything here is tested natively; the page supplies the passkey
// ceremony, the file chooser and the persistence of Config.
//
// What it does not do (and a real application must): the owner signers in Config are trusted
// because they are on this device, next to the store, so someone who can write this origin's
// storage can replace both. A deployment keeps them somewhere the store's writers cannot reach.
package encstore

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

// The kinds of owner key.
const (
	KindPasskey  = "passkey"
	KindRecovery = "recovery"
	KindSSH      = "ssh"
)

// ErrLocked is returned by what needs a private key while the store is locked. It is
// docstore.ErrLocked for errors.Is.
var ErrLocked = fmt.Errorf("the store is locked: unlock it with one of its keys: %w", docstore.ErrLocked)

// ErrNotThisStore is returned for a store whose protection was asked of another target (the
// store is the encrypted one of this page; a memory or folder target cannot keep a secret).
var ErrNotThisStore = errors.New("confidential documents need the encrypted browser store: turn on Encrypt this store and save there")

// ErrNotAKey is returned when the key given to unlock is not one of the store's.
var ErrNotAKey = errors.New("that is not one of this store's keys")

// ErrDisabled is returned for what needs encryption to be on.
var ErrDisabled = errors.New("encryption is not on for this store")

// ErrConfidentialLeft is returned by Disable while a document or folder is still confidential:
// turning encryption off would leave names that need a key and files nothing could read.
var ErrConfidentialLeft = errors.New("make the confidential documents and folders not confidential first")

// Owner is an owner key as kept on the device: public information only.
type Owner struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Type   string `json:"type"`   // the recipient's type: "passkey-prf", "age-x25519", "ssh-ed25519"
	Public string `json:"public"` // the recipient's public key text
	// Signer is the Ed25519 public key (hex) that verifies the recipient lists this key signs.
	Signer string `json:"signer"`
}

// Config is what is kept on the device about the store. It holds no secret.
type Config struct {
	Version int     `json:"v"`
	Owners  []Owner `json:"owners"`
}

// NewKey is an owner key being added. Unlock, when set, is the private side, available now (the
// passkey just derived, a recovery key just made): the store is unlocked with it, so a caller
// that wants a key not to stay in memory (a recovery key shown once and saved elsewhere) clears
// it, as long as another key of the set carries its own.
type NewKey struct {
	Kind      string
	Label     string
	Recipient encrypt.Recipient
	SignerPub ed25519.PublicKey
	Unlock    *Unlocked
}

// Unlocked is the private side of an owner key: what opens the store, and the signing key that
// goes with it.
type Unlocked struct {
	Identity age.Identity
	Signer   *encrypt.Signer
}

// Store is the encrypted store of one page. Raw is the storage underneath (the browser's
// localStorage); Adapter() is what the saver uses. The zero value is not usable: use New.
type Store struct {
	Raw docstore.Adapter
	// Target is the saver target this store is (the page's "browser"): protection is refused
	// for any other.
	Target string
	// Persist saves the Config after a change; an error stops the change. Nil keeps nothing.
	Persist func(Config) error

	mu      sync.RWMutex
	cfg     Config
	owners  []encrypt.Recipient
	signers []ed25519.PublicKey
	ids     []age.Identity
	signer  *encrypt.Signer
	floor   encrypt.MemorySeqFloor
	ad      *encrypt.Adapter
}

// New makes the store over raw with the owner keys in cfg (none: encryption is off).
func New(raw docstore.Adapter, target string, cfg Config) (*Store, error) {
	s := &Store{Raw: raw, Target: target}
	if err := s.install(cfg); err != nil {
		return nil, err
	}
	s.ad = encrypt.New(raw, ring{s})
	return s, nil
}

// install parses cfg and makes it the current one.
func (s *Store) install(cfg Config) error {
	var owners []encrypt.Recipient
	var signers []ed25519.PublicKey
	for _, o := range cfg.Owners {
		r, err := parseOwner(o)
		if err != nil {
			return err
		}
		sg, err := hex.DecodeString(o.Signer)
		if err != nil || len(sg) != ed25519.PublicKeySize {
			return fmt.Errorf("encstore: the signing key of %q is not valid", o.Label)
		}
		owners, signers = append(owners, r), append(signers, ed25519.PublicKey(sg))
	}
	s.mu.Lock()
	s.cfg, s.owners, s.signers = cfg, owners, signers
	s.mu.Unlock()
	return nil
}

func parseOwner(o Owner) (encrypt.Recipient, error) {
	var r encrypt.Recipient
	var err error
	if o.Kind == KindSSH {
		r, err = encrypt.ParseSSHRecipient(o.Public)
	} else {
		r, err = encrypt.ParseAgeRecipient(o.Public)
		r.Type = o.Type
	}
	if err != nil {
		return r, err
	}
	r.Label = o.Label
	return r, nil
}

// Adapter is the store as the saver uses it: every document encrypted, the catalog readable.
// It is the same adapter whether the store is locked or not (it reads the state at each call).
func (s *Store) Adapter() *encrypt.Adapter { return s.ad }

// Enabled says whether encryption is on (there are owner keys).
func (s *Store) Enabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.owners) > 0
}

// Unlocked says whether a private key is held, so documents can be saved and opened.
func (s *Store) Unlocked() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.owners) > 0 && len(s.ids) > 0
}

// Owners are the owner keys, public information.
func (s *Store) Owners() []Owner {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.cfg.Owners)
}

// Config is the configuration as it stands.
func (s *Store) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Config{Version: s.cfg.Version, Owners: slices.Clone(s.cfg.Owners)}
}

// Recipients are the owner keys as the People tab lists them.
func (s *Store) Recipients() []encrypt.Recipient {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.owners)
}

// Lock forgets the private keys. What was saved stays saved; nothing can be opened or saved
// until a key unlocks it again.
func (s *Store) Lock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids, s.signer = nil, nil
}

func (s *Store) unlock(u ...Unlocked) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids, s.signer = nil, nil
	for _, k := range u {
		s.ids = append(s.ids, k.Identity)
		if s.signer == nil {
			s.signer = k.Signer
		}
	}
}

// ring is the Keyring of the store: a ConfidentialKeyring over the owners' keys, rebuilt from the
// current state for each call, and closed (ErrLocked) while no private key is held. For a store
// with no owner keys it is closed too: it is never used then.
type ring struct{ s *Store }

func (r ring) keyring() (encrypt.ConfidentialKeyring, error) {
	s := r.s
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.owners) == 0 {
		return encrypt.ConfidentialKeyring{}, ErrDisabled
	}
	if len(s.ids) == 0 {
		return encrypt.ConfidentialKeyring{}, ErrLocked
	}
	to := make([]age.Recipient, len(s.owners))
	for i, o := range s.owners {
		to[i] = o.Key
	}
	return encrypt.ConfidentialKeyring{
		People: encrypt.StoreKeyring{Store: s.Raw, Owners: to, Keys: slices.Clone(s.ids), OwnerSigners: slices.Clone(s.signers), SeqFloor: &s.floor},
		// There is no server here: what is not confidential is still encrypted at rest, to the
		// owner's keys, so a stolen copy of the browser's storage holds no text.
		AtRest: encrypt.Static{To: to, Keys: slices.Clone(s.ids)},
	}, nil
}

// Recipients implements encrypt.Keyring.
func (r ring) Recipients(ctx context.Context, id string) ([]age.Recipient, error) {
	k, err := r.keyring()
	if err != nil {
		return nil, err
	}
	return k.Recipients(ctx, id)
}

// Identities implements encrypt.Keyring. A locked store has none: opening says ErrNoKey.
func (r ring) Identities(ctx context.Context, id string) ([]age.Identity, error) {
	k, err := r.keyring()
	if errors.Is(err, ErrLocked) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return k.Identities(ctx, id)
}

// Sharing is the control of who a document is shared with, for the unlocked store.
func (s *Store) Sharing() (*encrypt.Sharing, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.owners) == 0 {
		return nil, ErrDisabled
	}
	if s.signer == nil {
		return nil, ErrLocked
	}
	return &encrypt.Sharing{Store: s.Raw, Adapter: s.ad, Signer: s.signer, OwnerSigners: slices.Clone(s.signers), SeqFloor: &s.floor}, nil
}

// Check is saver.Saver.ConfidentialCheck: a document is made confidential only on this store,
// while it is unlocked, and only when one of the owner's keys is not a passkey
// (encrypt.CheckRecoverableOwner), so losing the passkey does not lose the document.
func (s *Store) Check(ctx context.Context, target, id string) error {
	if target != s.Target || !s.Enabled() {
		return ErrNotThisStore
	}
	sh, err := s.Sharing()
	if err != nil {
		return err
	}
	return encrypt.ConfidentialCheck(sh, s.Recipients()...)(ctx, target, id)
}

// Rekey is saver.Saver.Rekey: the files of a document follow its protection level (encrypt.RekeyHook).
// Another target has no keys to follow.
func (s *Store) Rekey(ctx context.Context, target, id, protection string) error {
	if target != s.Target {
		return nil
	}
	if !s.Enabled() {
		if protection == "e2ee" {
			return ErrNotThisStore
		}
		return nil // an unencrypted store has nothing to re-key
	}
	sh, err := s.Sharing()
	if err != nil {
		return err
	}
	return encrypt.RekeyHook(sh)(ctx, target, id, protection)
}

// PackKeys is saver.Session.PackKeys: the functions that seal confidential documents into a
// pack for the owner's keys and open them again. Both are nil while the store is locked.
func (s *Store) PackKeys() (seal func(context.Context, string, []byte) ([]byte, error), open func(context.Context, string, []byte) ([]byte, error)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.owners) == 0 || len(s.ids) == 0 {
		return nil, nil
	}
	to := make([]age.Recipient, len(s.owners))
	for i, o := range s.owners {
		to[i] = o.Key
	}
	return encrypt.PackSealer(to, slices.Clone(s.ids))
}

// Enable turns encryption on with the given owner keys; what was saved in the clear before is
// encrypted now. Keys that carry their private side (Unlock) leave the store unlocked; with none
// (an SSH public key only) it starts locked and the private key file unlocks it. Keys are public
// information and go to Persist; a failure there changes nothing.
func (s *Store) Enable(ctx context.Context, keys ...NewKey) error {
	if s.Enabled() {
		return errors.New("encstore: encryption is already on")
	}
	if len(keys) == 0 {
		return errors.New("encstore: choose at least one key")
	}
	cfg := Config{Version: 1}
	var unlocked []Unlocked
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k.Recipient.Fingerprint] {
			return errors.New("encstore: the same key twice")
		}
		seen[k.Recipient.Fingerprint] = true
		cfg.Owners = append(cfg.Owners, ownerOf(k))
		if k.Unlock != nil {
			unlocked = append(unlocked, *k.Unlock)
		}
	}
	if err := s.persist(cfg); err != nil {
		return err
	}
	if err := s.install(cfg); err != nil {
		return err
	}
	s.unlock(unlocked...)
	// What is already saved in the clear (documents from before) is encrypted now: one pass over an
	// adapter that accepts plaintext, which the store's own adapter never does. There is no
	// confidential document yet, so the owner keys are all the recipients, and no private key is
	// needed to write.
	to := make([]age.Recipient, len(cfg.Owners))
	for i, r := range s.Recipients() {
		to[i] = r.Key
	}
	mig := encrypt.New(s.Raw, encrypt.Static{To: to})
	mig.AllowPlaintext = true
	if _, err := mig.RekeyAll(ctx); err != nil {
		return fmt.Errorf("encstore: encrypting what was already saved: %w", err)
	}
	return nil
}

func ownerOf(k NewKey) Owner {
	return Owner{Kind: k.Kind, Label: k.Label, Type: k.Recipient.Type, Public: k.Recipient.Text, Signer: hex.EncodeToString(k.SignerPub)}
}

func (s *Store) persist(cfg Config) error {
	if s.Persist == nil {
		return nil
	}
	return s.Persist(cfg)
}

// AddOwner adds an owner key (a recovery key made later, a passkey) to an unlocked store: every
// stored file is encrypted again so the new key opens it. A failure after the key is added is
// the re-key's (Repair finishes it); the key stays added.
func (s *Store) AddOwner(ctx context.Context, k NewKey) error {
	if !s.Unlocked() {
		if !s.Enabled() {
			return ErrDisabled
		}
		return ErrLocked
	}
	cfg := s.Config()
	for _, o := range cfg.Owners {
		if o.Public == k.Recipient.Text {
			return errors.New("encstore: that key is already one of the store's")
		}
	}
	cfg.Owners = append(cfg.Owners, ownerOf(k))
	if err := s.persist(cfg); err != nil {
		return err
	}
	if err := s.install(cfg); err != nil {
		return err
	}
	return s.Repair(ctx)
}

// Repair encrypts every stored file again to the current owner keys (and each document's own
// list): the way to finish a re-key that was cut short.
func (s *Store) Repair(ctx context.Context) error {
	if !s.Unlocked() {
		return ErrLocked
	}
	if _, err := s.ad.RekeyAll(ctx); err != nil {
		return fmt.Errorf("encstore: encrypting the files again for the new key: %w", err)
	}
	return nil
}

// UnlockPasskey unlocks with the 32 bytes a passkey answered (client.DeriveLocal). The caller
// clears secret.
func (s *Store) UnlockPasskey(secret []byte) error {
	id, err := encrypt.PRFIdentity(secret)
	if err != nil {
		return err
	}
	if !s.hasOwner(KindPasskey, id.Recipient().String()) {
		return ErrNotAKey
	}
	s.unlock(Unlocked{Identity: id, Signer: encrypt.SignerFromAge(id)})
	return nil
}

// UnlockRecovery unlocks with a recovery key's secret ("AGE-SECRET-KEY-1...").
func (s *Store) UnlockRecovery(secret string) error {
	ident, err := encrypt.ParseAgeIdentity(secret)
	if err != nil {
		return err
	}
	id, ok := ident.(*age.X25519Identity)
	if !ok || !s.hasOwner(KindRecovery, id.Recipient().String()) {
		return ErrNotAKey
	}
	s.unlock(Unlocked{Identity: id, Signer: encrypt.SignerFromAge(id)})
	return nil
}

// UnlockSSH unlocks with the private key file (PEM, not passphrase protected) of an ed25519
// SSH key that is one of the owners.
func (s *Store) UnlockSSH(pem []byte) error {
	id, err := encrypt.ParseSSHIdentity(pem)
	if err != nil {
		return err
	}
	signer, err := encrypt.SignerFromSSH(pem)
	if err != nil {
		return err
	}
	pub := signer.Public()
	s.mu.RLock()
	match := false
	for _, o := range s.cfg.Owners {
		if o.Kind == KindSSH && o.Signer == hex.EncodeToString(pub) {
			match = true
		}
	}
	s.mu.RUnlock()
	if !match {
		return ErrNotAKey
	}
	s.unlock(Unlocked{Identity: id, Signer: signer})
	return nil
}

func (s *Store) hasOwner(kind, public string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, o := range s.cfg.Owners {
		if o.Kind == kind && o.Public == public {
			return true
		}
	}
	return false
}

// HasKind says whether the store has an owner key of that kind (which unlock method to offer).
func (s *Store) HasKind(kind string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, o := range s.cfg.Owners {
		if o.Kind == kind {
			return true
		}
	}
	return false
}

// Disable turns encryption off: every file is decrypted back into the store as it was before,
// the lists of who a document is shared with are removed, and the keys are forgotten. The store
// must be unlocked, and no document or folder may still be confidential (confidential is the
// caller's to check: it knows the catalog) because turning it off would leave their names and
// files unreadable; on a failure part way the store is left on and Disable can be run again.
func (s *Store) Disable(ctx context.Context) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	if !s.Unlocked() {
		return ErrLocked
	}
	metas, err := s.Raw.List(ctx)
	if err != nil {
		return err
	}
	for _, m := range metas {
		if encrypt.DefaultSkip(m.ID) {
			continue
		}
		raw, err := s.Raw.Load(ctx, m.ID)
		if err != nil {
			return err
		}
		if !encrypt.IsEncrypted(raw.Data) {
			continue
		}
		plain, err := s.ad.Load(ctx, m.ID)
		if err != nil {
			return fmt.Errorf("encstore: %s: %w", m.ID, err)
		}
		if err := s.Raw.Save(ctx, m.ID, plain.Data); err != nil {
			return err
		}
	}
	for _, m := range metas {
		if strings.HasPrefix(m.ID, encrypt.KeysPrefix) {
			if err := s.Raw.Delete(ctx, m.ID); err != nil && !errors.Is(err, docstore.ErrNotFound) {
				return err
			}
		}
	}
	if err := s.persist(Config{}); err != nil {
		return err
	}
	if err := s.install(Config{}); err != nil {
		return err
	}
	s.Lock()
	return nil
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
// secret to be shown once and kept offline. The private side is not kept here.
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
