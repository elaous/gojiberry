// Package encstore is the showcase's key management for its encrypted store.
package encstore

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"filippo.io/age"
	"fmt"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"slices"
	"sync"
)

// The kinds of owner key.
const (
	KindPasskey  = "passkey"
	KindRecovery = "recovery"
	KindSSH      = "ssh"
)

var ErrLocked = fmt.Errorf("the store is locked: unlock it with one of its keys: %w", docstore.ErrLocked)

var ErrNotThisStore = errors.New("confidential documents need the encrypted browser store: turn on Encrypt this store and save there")

var ErrNotAKey = errors.New("that is not one of this store's keys")

// ErrDisabled is returned for what needs encryption to be on.
var ErrDisabled = errors.New("encryption is not on for this store")

var ErrAlreadyOn = errors.New("encryption is already on")

// ErrNoKeys is returned by Enable when no key was chosen.
var ErrNoKeys = errors.New("choose at least one key")

var ErrKeyTwice = errors.New("that key is already one of the store's, or given twice")

var ErrConfidentialLeft = errors.New("make the confidential documents and folders not confidential first")

var ErrInterrupted = errors.New("an earlier change to the store's keys did not finish: finish it or undo it first")

var ErrConfigChanged = errors.New("the key configuration on this device was changed since this key last opened the store: not unlocking")

var ErrStaleConfig = fmt.Errorf("the key configuration was changed in another tab or window since this page loaded: this tab is locked, reload the page: %w", ErrConfigChanged)

var ErrConfigMissing = errors.New("configuration missing: encrypted files found")

var ErrConfigCorrupt = errors.New("the key configuration could not be read")

var ErrSecretNotSaved = errors.New("the recovery key's secret has not been saved: it must be shown and confirmed before the key is added")

var ErrPasskeyOnly = errors.New("only passkeys: losing them loses every document in this store")

// ErrLastOwner is returned by RemoveOwner for the only key.
var ErrLastOwner = errors.New("that is the store's only key")

var ErrWouldLoseRecovery = errors.New("that would leave only passkeys: keep a recovery key or an SSH key")

var ErrUseAnotherKey = errors.New("unlock the store with a different key to remove this one")

var ErrNoSuchOwner = errors.New("no such owner key")

var ErrRollbackFailed = errors.New("the change failed and could not be undone completely: reload and finish it or undo it")

var ErrStopped = errors.New("stopped: nothing was changed")

var ErrNothingPending = errors.New("there is no unfinished change")

type NoRoomError struct{ Need, Free int64 }

func (e *NoRoomError) Error() string {
	return fmt.Sprintf("not enough room in the browser's storage: about %d more bytes are needed and about %d are free", e.Need, e.Free)
}

// Is makes a NoRoomError a docstore.ErrStorageFull.
func (e *NoRoomError) Is(t error) bool { return t == docstore.ErrStorageFull }

type UnreadableError struct{ IDs []string }

func (e *UnreadableError) Error() string {
	return fmt.Sprintf("%d stored files cannot be opened with the keys unlocked here: nothing was changed", len(e.IDs))
}

type Owner struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Type   string `json:"type"`   // the recipient's type: "passkey-prf", "age-x25519", "ssh-ed25519"
	Public string `json:"public"` // the recipient's public key text
	Signer string `json:"signer"`
}

func (o Owner) Fingerprint() string {
	r, err := parseOwner(o)
	if err != nil {
		return ""
	}
	return r.Fingerprint
}

type Config struct {
	Version int      `json:"v"`
	Owners  []Owner  `json:"owners"`
	Pending *Pending `json:"pending,omitempty"`
	Seals   []Seal   `json:"seals,omitempty"`
	Links   []Link   `json:"links,omitempty"`
}

// The operations a Pending journal names.
const (
	OpEnable  = "enable"
	OpAdd     = "add"
	OpRemove  = "remove"
	OpReplace = "replace"
	OpDisable = "disable"
	OpRestore = "restore"
)

type Pending struct {
	Op    string  `json:"op"`
	To    []Owner `json:"to"`
	From  string  `json:"from,omitempty"`
	Seals []Seal  `json:"seals,omitempty"`
	Link  *Link   `json:"link,omitempty"`
}

// NewKey is an owner key being added.
type NewKey struct {
	Kind        string
	Label       string
	Recipient   encrypt.Recipient
	SignerPub   ed25519.PublicKey
	Unlock      *Unlocked
	Forget      bool
	SecretSaved bool
}

type Unlocked struct {
	Identity age.Identity
	Signer   *encrypt.Signer
}

// Options are the choices of Enable.
type Options struct {
	AcceptPasskeyOnly bool
}

// Store is the encrypted store of one page.
type Store struct {
	Raw      docstore.Adapter
	Target   string
	Persist  func(Config) error
	Capacity int64
	// Reload reads the stored Config again (nil: not checked).
	Reload          func() (Config, error)
	ConfirmFirstUse bool
	OnTrust         func(digest string)
	OnUnlock        func(Owner)
	Progress        func(done, total int)

	mu      sync.RWMutex
	cfg     Config
	owners  []encrypt.Recipient
	signers []ed25519.PublicKey
	ids     []age.Identity
	holders []string // the owner key (Owner.Public) each of ids belongs to
	sigs    []*encrypt.Signer
	floor   *encrypt.MemorySeqFloor
	ad      *encrypt.Adapter // strict: refuses a stored file that is not encrypted
	tol     *encrypt.Adapter
	gate    sync.RWMutex
	stale   bool
	trusted map[string]bool
	stop    context.CancelFunc // cancels the change that is running, nil when none is
	stopped bool               // Stop was called for the change that is running
}

func New(raw docstore.Adapter, target string, cfg Config) (*Store, error) {
	s := &Store{Raw: raw, Target: target, floor: &encrypt.MemorySeqFloor{}}
	if err := s.install(cfg); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.rebuildAdapters()
	s.mu.Unlock()
	return s, nil
}

func (s *Store) rebuildAdapters() {
	s.ad = encrypt.New(s.Raw, ring{s})
	s.tol = encrypt.New(s.Raw, ring{s})
	s.tol.AllowPlaintext = true
	s.ad.OwnerSigners, s.tol.OwnerSigners = slices.Clone(s.signers), slices.Clone(s.signers)
	if sg := s.signer(); sg != nil {
		s.ad.Signer, s.tol.Signer = sg, sg
	}
}

func (s *Store) strict() *encrypt.Adapter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ad
}

func (s *Store) tolerant() *encrypt.Adapter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tol
}

func (s *Store) install(cfg Config) error {
	owners, signers, err := parseOwners(cfg.Owners)
	if err != nil {
		return err
	}
	if cfg.Pending != nil {
		if _, _, err := parseOwners(cfg.Pending.To); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cfg, s.owners, s.signers = cloneConfig(cfg), owners, signers
	s.rebuildAdapters()
	s.mu.Unlock()
	return nil
}

func cloneConfig(c Config) Config {
	out := Config{Version: c.Version, Owners: slices.Clone(c.Owners), Seals: slices.Clone(c.Seals), Links: slices.Clone(c.Links)}
	if c.Pending != nil {
		p := *c.Pending
		p.To, p.Seals = slices.Clone(p.To), slices.Clone(p.Seals)
		if p.Link != nil {
			l := *p.Link
			l.To = slices.Clone(l.To)
			p.Link = &l
		}
		out.Pending = &p
	}
	return out
}

func parseOwners(list []Owner) ([]encrypt.Recipient, []ed25519.PublicKey, error) {
	var owners []encrypt.Recipient
	var signers []ed25519.PublicKey
	for _, o := range list {
		r, err := parseOwner(o)
		if err != nil {
			return nil, nil, err
		}
		sg, err := hex.DecodeString(o.Signer)
		if err != nil || len(sg) != ed25519.PublicKeySize {
			return nil, nil, fmt.Errorf("encstore: the signing key of %q is not valid", o.Label)
		}
		owners, signers = append(owners, r), append(signers, ed25519.PublicKey(sg))
	}
	return owners, signers, nil
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

func (s *Store) Adapter() docstore.Adapter { return front{s} }

func (s *Store) Enabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.owners) > 0 || s.cfg.Pending != nil
}

func (s *Store) Interrupted() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Pending != nil
}

func (s *Store) PendingChange() *Pending {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.Pending == nil {
		return nil
	}
	c := cloneConfig(s.cfg)
	return c.Pending
}

func (s *Store) Unlocked() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return (len(s.owners) > 0 || s.cfg.Pending != nil) && len(s.ids) > 0
}

// Owners are the owner keys, public information.
func (s *Store) Owners() []Owner {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.cfg.Owners)
}

func (s *Store) IdentityFor(public string) (age.Identity, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i, h := range s.holders {
		if h == public && i < len(s.ids) {
			return s.ids[i], true
		}
	}
	return nil, false
}

// Config is the configuration as it stands.
func (s *Store) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneConfig(s.cfg)
}

func (s *Store) Recipients() []encrypt.Recipient {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.owners)
}

func (s *Store) fresh() error {
	s.mu.RLock()
	stale, cur := s.stale, s.cfg
	s.mu.RUnlock()
	if stale {
		return ErrStaleConfig
	}
	if s.Reload == nil {
		return nil
	}
	now, err := s.Reload()
	if err == nil && sameConfig(now, cur) {
		return nil
	}
	s.markStale()
	return ErrStaleConfig
}

func (s *Store) markStale() {
	s.mu.Lock()
	s.stale = true
	s.ids, s.holders, s.sigs = nil, nil, nil
	s.rebuildAdapters()
	s.mu.Unlock()
}

func (s *Store) Stale() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stale
}

func (s *Store) ConfigChangedElsewhere() {
	if s.Reload != nil {
		s.mu.RLock()
		cur := s.cfg
		s.mu.RUnlock()
		if now, err := s.Reload(); err == nil && sameConfig(now, cur) {
			return
		}
	}
	s.markStale()
}

func sameConfig(a, b Config) bool {
	norm := func(c Config) string {
		c = cloneConfig(c)
		if len(c.Owners) == 0 {
			c.Owners = nil
		}
		if len(c.Seals) == 0 {
			c.Seals = nil
		}
		if len(c.Links) == 0 {
			c.Links = nil
		}
		out, _ := json.Marshal(c)
		return string(out)
	}
	return norm(a) == norm(b)
}

// Lock forgets the private keys.
func (s *Store) Lock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids, s.holders, s.sigs = nil, nil, nil
	s.rebuildAdapters()
}

type held struct {
	Unlocked
	owner string // Owner.Public
}

func (s *Store) unlock(u ...held) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids, s.holders, s.sigs = nil, nil, nil
	for _, k := range u {
		s.ids = append(s.ids, k.Identity)
		s.holders = append(s.holders, k.owner)
		s.sigs = append(s.sigs, k.Signer)
	}
	s.rebuildAdapters()
}

func (s *Store) signer() *encrypt.Signer {
	if len(s.sigs) == 0 {
		return nil
	}
	return s.sigs[0]
}

func (s *Store) Sharing() (*encrypt.Sharing, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.Pending != nil {
		return nil, ErrInterrupted
	}
	if len(s.owners) == 0 {
		return nil, ErrDisabled
	}
	if s.signer() == nil {
		return nil, ErrLocked
	}
	return &encrypt.Sharing{Store: s.Raw, Adapter: s.ad, Signer: s.signer(), OwnerSigners: slices.Clone(s.signers), SeqFloor: s.floor}, nil
}

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

func (s *Store) PackKeys() (seal func(context.Context, string, []byte) ([]byte, error), open func(context.Context, string, []byte) ([]byte, error)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.owners) == 0 || len(s.ids) == 0 || s.cfg.Pending != nil {
		return nil, nil
	}
	to := make([]age.Recipient, len(s.owners))
	for i, o := range s.owners {
		to[i] = o.Key
	}
	return encrypt.PackSealer(to, slices.Clone(s.ids))
}

func ownerOf(k NewKey) Owner {
	return Owner{Kind: k.Kind, Label: k.Label, Type: k.Recipient.Type, Public: k.Recipient.Text, Signer: hex.EncodeToString(k.SignerPub)}
}

func (s *Store) persist(cfg Config) error {
	if s.Persist == nil {
		return nil
	}
	return s.Persist(cloneConfig(cfg))
}

func (s *Store) ownersFor() []Owner {
	out := slices.Clone(s.cfg.Owners)
	if s.cfg.Pending != nil {
		out = append(out, s.cfg.Pending.To...)
	}
	return out
}

func (s *Store) findOwner(kind, public string) (Owner, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, o := range s.ownersFor() {
		if o.Kind == kind && o.Public == public {
			return o, true
		}
	}
	return Owner{}, false
}
