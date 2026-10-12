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
// Every change to the set of keys, or to whether encryption is on at all, is a transaction over
// the stored files (see change in transaction.go): a journal record in Config first, each file
// re-encrypted and read back, the originals kept until all are done, and only then the new key
// list is made current; a failure puts everything back, and a crash leaves the journal for
// Recover to finish or undo. What it cannot do is in the "Limits" below.
//
// Limits (stated plainly; docs/encryption-and-passkeys.md section 5.7 has them in full):
//
//   - Config, with the owner signing keys, is kept next to the store. Anyone who can write this
//     origin's storage can replace both. What catches that: the seals in Config (bind.go: a key
//     that has a seal refuses a list its chain of links does not reach; a passkey or recovery key
//     with no seal is refused, whatever Version says) and the anchor (anchor.go: the hash of the
//     owner list is inside the signed frame of every document file, so a list that is not the one
//     the files were sealed under is refused, which also catches an older valid config put back
//     and a seal-less one for an SSH key). Both are ErrConfigChanged. What nothing here catches:
//     a replacement of the files together with the config by an older consistent snapshot of
//     both (a whole-store rollback), a store that holds no signed document file yet (an SSH-only
//     store before its first save: its SSH key has nothing to check against), and an attacker who
//     replaces every file as well as the config (the data is gone, and only what is saved
//     afterwards is exposed). A deleted or unreadable config over encrypted files is not
//     "encryption off": see Inspect (ErrConfigMissing, ErrConfigCorrupt).
//   - Another tab of the same page can change the keys: the store's own lock covers this page and
//     the browser's web lock (where it has one) serializes the tabs, but the lock does not detect
//     a tab that is out of date. Before every write the stored config is read again and compared
//     with the one this tab loaded (ErrStaleConfig); a tab that finds it changed locks itself.
//   - Taking a key out stops it opening what is saved from now on; whoever held it keeps what
//     they decrypted and any copy of old ciphertext.
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

// ErrAlreadyOn is returned by Enable for a store that is already encrypted.
var ErrAlreadyOn = errors.New("encryption is already on")

// ErrNoKeys is returned by Enable when no key was chosen.
var ErrNoKeys = errors.New("choose at least one key")

// ErrKeyTwice is returned when the same key is given twice, or is already an owner.
var ErrKeyTwice = errors.New("that key is already one of the store's, or given twice")

// ErrConfidentialLeft is returned by Disable while a document or folder is still confidential:
// turning encryption off would leave names that need a key and files nothing could read.
var ErrConfidentialLeft = errors.New("make the confidential documents and folders not confidential first")

// ErrInterrupted is returned for what cannot run while a change to the keys is unfinished (a
// journal is in Config): Recover finishes it or puts it back.
var ErrInterrupted = errors.New("an earlier change to the store's keys did not finish: finish it or undo it first")

// ErrConfigChanged is returned by an unlock when the key list on this device is not the one this
// key last saw (see the seals in bind.go): it was replaced or edited by something else.
var ErrConfigChanged = errors.New("the key configuration on this device was changed since this key last opened the store: not unlocking")

// ErrStaleConfig is returned when the key configuration kept on the device is no longer the one this
// tab loaded: another tab (or something else) changed it. The tab locks itself and refuses every
// change until the page is reloaded. It is ErrConfigChanged for errors.Is.
var ErrStaleConfig = fmt.Errorf("the key configuration was changed in another tab or window since this page loaded: this tab is locked, reload the page: %w", ErrConfigChanged)

// ErrConfigMissing is returned by Inspect for a store whose key configuration is gone while
// encrypted files are still there: not "encryption off".
var ErrConfigMissing = errors.New("configuration missing: encrypted files found")

// ErrConfigCorrupt is returned by Inspect for a key configuration that could not be read.
var ErrConfigCorrupt = errors.New("the key configuration could not be read")

// ErrSecretNotSaved is returned when a recovery key is to become an owner key before the person
// has confirmed they saved its secret: a key whose secret was never seen would count as a way
// back that does not exist.
var ErrSecretNotSaved = errors.New("the recovery key's secret has not been saved: it must be shown and confirmed before the key is added")

// ErrPasskeyOnly is returned by Enable for passkeys alone unless Options.AcceptPasskeyOnly: with
// no key that is not a passkey, losing the passkey loses everything the store holds.
var ErrPasskeyOnly = errors.New("only passkeys: losing them loses every document in this store")

// ErrLastOwner is returned by RemoveOwner for the only key.
var ErrLastOwner = errors.New("that is the store's only key")

// ErrWouldLoseRecovery is returned when removing or replacing a key would leave only passkeys
// where there was a way back that is not one.
var ErrWouldLoseRecovery = errors.New("that would leave only passkeys: keep a recovery key or an SSH key")

// ErrUseAnotherKey is returned by RemoveOwner when the store is unlocked with the key being
// removed alone: another key's signing key is needed to carry the lists over.
var ErrUseAnotherKey = errors.New("unlock the store with a different key to remove this one")

// ErrNoSuchOwner is returned for a fingerprint that is not one of the owners.
var ErrNoSuchOwner = errors.New("no such owner key")

// ErrRollbackFailed is returned when a change failed and what it wrote could not be put back
// either: the journal stays, and Recover (finish or undo) is how to get out.
var ErrRollbackFailed = errors.New("the change failed and could not be undone completely: reload and finish it or undo it")

// ErrStopped is returned by a change the person stopped with Stop: everything it had written was put
// back, the keys are as they were before.
var ErrStopped = errors.New("stopped: nothing was changed")

// ErrNothingPending is returned by Recover when there is nothing to finish.
var ErrNothingPending = errors.New("there is no unfinished change")

// NoRoomError is returned before a change starts when the storage would not hold the files as
// they will be (age adds a header to every file, and a key adds a stanza to each).
type NoRoomError struct{ Need, Free int64 }

func (e *NoRoomError) Error() string {
	return fmt.Sprintf("not enough room in the browser's storage: about %d more bytes are needed and about %d are free", e.Need, e.Free)
}

// Is makes a NoRoomError a docstore.ErrStorageFull.
func (e *NoRoomError) Is(t error) bool { return t == docstore.ErrStorageFull }

// UnreadableError is returned before anything is written when stored files cannot be opened with
// the keys held: they would be lost by the change, so it does not start.
type UnreadableError struct{ IDs []string }

func (e *UnreadableError) Error() string {
	return fmt.Sprintf("%d stored files cannot be opened with the keys unlocked here: nothing was changed", len(e.IDs))
}

// Owner is an owner key as kept on the device: public information only.
type Owner struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Type   string `json:"type"`   // the recipient's type: "passkey-prf", "age-x25519", "ssh-ed25519"
	Public string `json:"public"` // the recipient's public key text
	// Signer is the Ed25519 public key (hex) that verifies the recipient lists this key signs.
	Signer string `json:"signer"`
}

// Fingerprint is the key's fingerprint as the People tab shows it ("" for an owner that cannot be
// parsed).
func (o Owner) Fingerprint() string {
	r, err := parseOwner(o)
	if err != nil {
		return ""
	}
	return r.Fingerprint
}

// Config is what is kept on the device about the store. It holds no secret: public keys, the
// signatures that bind them (bind.go) and the journal of a change in progress.
type Config struct {
	// Version is advisory (what wrote it): nothing is accepted or refused because of it, since
	// whoever writes the storage writes it too.
	Version int     `json:"v"`
	Owners  []Owner `json:"owners"`
	// Pending is the journal of a change that was started and not finished.
	Pending *Pending `json:"pending,omitempty"`
	// Seals and Links are the signatures that let a key tell, at unlock, that this list is the one
	// it saw or a descendant of it signed by owners it trusted.
	Seals []Seal `json:"seals,omitempty"`
	Links []Link `json:"links,omitempty"`
}

// The operations a Pending journal names.
const (
	OpEnable  = "enable"
	OpAdd     = "add"
	OpRemove  = "remove"
	OpReplace = "replace"
	OpDisable = "disable"
	// OpRestore rebuilds the key configuration from a key that opens the files (Restore*).
	OpRestore = "restore"
)

// Pending is the journal record of a change that is in progress or was cut short: what it is
// and the owner list it is making current (empty for turning encryption off). Nothing in it is
// trusted by itself: Recover authenticates it against the unlocking key's own seal chain (see
// authJournal), never against the owners the journal or the config name.
//
//   - From is the digest of the committed owner list the change started from ("" when there was
//     none: turning encryption on).
//   - Seals are signatures of To by the keys held when the change started.
//   - Link is the signature of From -> To by a key of the old list, made at journal time: what
//     keeps the old keys able to open the store once the new list is current (it becomes a link
//     of the finished Config), even if only a new key is unlocked when the change is finished.
type Pending struct {
	Op    string  `json:"op"`
	To    []Owner `json:"to"`
	From  string  `json:"from,omitempty"`
	Seals []Seal  `json:"seals,omitempty"`
	Link  *Link   `json:"link,omitempty"`
}

// NewKey is an owner key being added. Unlock, when set, is the private side, available now (the
// passkey just derived, a recovery key just made): it signs the new key list and verifies what was
// written, and the store is left unlocked with it unless Forget says it must not stay in memory
// (a recovery key shown once and saved elsewhere), which needs another key of the set to carry
// its own.
type NewKey struct {
	Kind      string
	Label     string
	Recipient encrypt.Recipient
	SignerPub ed25519.PublicKey
	Unlock    *Unlocked
	// Forget drops Unlock once it has been used for the change.
	Forget bool
	// SecretSaved is set by the caller once the person has confirmed they saved a recovery key's
	// secret (NewRecoveryKey returns the key with it unset); a recovery key without it is
	// refused (ErrSecretNotSaved).
	SecretSaved bool
}

// Unlocked is the private side of an owner key: what opens the store, and the signing key that
// goes with it.
type Unlocked struct {
	Identity age.Identity
	Signer   *encrypt.Signer
}

// Options are the choices of Enable.
type Options struct {
	// AcceptPasskeyOnly is the person's explicit acknowledgement that the store has only
	// passkeys and that losing them loses everything in it.
	AcceptPasskeyOnly bool
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
	// Capacity is how much the storage may hold, in the storage's own units: the characters of
	// the strings localStorage keeps (a record is base64, so 4 characters for 3 bytes of file),
	// which some browsers count as 2 bytes each (0: not known, nothing is checked before a
	// change; a write that fails is still rolled back).
	Capacity int64
	// Reload reads the stored Config again (nil: not checked). Before every write the store
	// compares it with the Config this tab loaded; a difference is ErrStaleConfig.
	Reload func() (Config, error)
	// ConfirmFirstUse makes the first unlock with an SSH key of a list that no key of this device has
	// sealed (an SSH-only store has nothing to anchor its first owner list to) stop with a
	// *FirstUseError, until the host passes the list's Digest to TrustList: a known-hosts file for
	// stores. Off, that first use is trusted silently, as before.
	ConfirmFirstUse bool
	// OnTrust, if set, is told each digest added to the trusted lists, so the host can keep them.
	OnTrust func(digest string)
	// OnUnlock, if set, is called after a key unlocked the store, with that key (to record that it
	// was used here).
	OnUnlock func(Owner)
	// Progress, if set, is called while a change re-encrypts the store, after each document, with how
	// many are done and how many there are.
	Progress func(done, total int)

	mu      sync.RWMutex
	cfg     Config
	owners  []encrypt.Recipient
	signers []ed25519.PublicKey
	ids     []age.Identity
	holders []string // the owner key (Owner.Public) each of ids belongs to
	sigs    []*encrypt.Signer
	floor   *encrypt.MemorySeqFloor
	ad      *encrypt.Adapter   // strict: refuses a stored file that is not encrypted
	tol     *encrypt.Adapter   // reads a file that is still in the clear: for an unfinished change
	gate    sync.RWMutex       // writes through Adapter() hold it shared; a change holds it exclusively
	stale   bool               // the stored Config was found changed by something else: refuse everything
	trusted map[string]bool    // digests of owner lists the person confirmed at a first use
	stop    context.CancelFunc // cancels the change that is running, nil when none is
	stopped bool               // Stop was called for the change that is running
}

// New makes the store over raw with the owner keys in cfg (none: encryption is off).
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

// rebuildAdapters makes the two encrypting adapters over Raw with the signing key of the first
// unlocked owner key (Adapter.Signer: frame v3 is signed by an owner, and a confidential save
// needs it). The caller holds s.mu. Called whenever the held keys change.
func (s *Store) rebuildAdapters() {
	s.ad = encrypt.New(s.Raw, ring{s})
	s.tol = encrypt.New(s.Raw, ring{s})
	s.tol.AllowPlaintext = true
	s.ad.OwnerSigners, s.tol.OwnerSigners = slices.Clone(s.signers), slices.Clone(s.signers)
	if sg := s.signer(); sg != nil {
		s.ad.Signer, s.tol.Signer = sg, sg
	}
}

// strict is the adapter that refuses a stored file that is not encrypted.
func (s *Store) strict() *encrypt.Adapter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ad
}

// tolerant is the adapter that also reads a file still in the clear (an unfinished change).
func (s *Store) tolerant() *encrypt.Adapter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tol
}

// install parses cfg and makes it the current one.
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

// Adapter is the store as the saver uses it: every document encrypted, the catalog readable. It
// is the same adapter whether the store is locked or not (it reads the state at each call). While
// a change to the keys is unfinished it reads (a file still in the clear is read as it is) and
// refuses every write (ErrInterrupted); while a change runs, calls wait for it to finish.
func (s *Store) Adapter() docstore.Adapter { return front{s} }

// Enabled says whether encryption is on (there are owner keys), or is being turned on and the
// change was cut short.
func (s *Store) Enabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.owners) > 0 || s.cfg.Pending != nil
}

// Interrupted says whether a change to the keys was started and not finished (Config.Pending).
func (s *Store) Interrupted() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Pending != nil
}

// PendingChange is the journal of the unfinished change, nil when there is none.
func (s *Store) PendingChange() *Pending {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.Pending == nil {
		return nil
	}
	c := cloneConfig(s.cfg)
	return c.Pending
}

// Unlocked says whether a private key is held, so documents can be saved and opened.
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

// IdentityFor is the unlocked private side of the owner key whose public text is public, to open a
// challenge with (proof that its holder publishes it). False while that key is locked.
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

// Recipients are the owner keys as the People tab lists them.
func (s *Store) Recipients() []encrypt.Recipient {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.owners)
}

// fresh compares the Config kept on the device now with the one this tab loaded. Another tab may
// have changed the keys since (the lock serializes the tabs, it does not tell this one it is out of
// date): a difference locks this tab for good (ErrStaleConfig). Nothing to compare with when Reload
// is not set.
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

// markStale locks the tab: the keys are forgotten and every later write is refused.
func (s *Store) markStale() {
	s.mu.Lock()
	s.stale = true
	s.ids, s.holders, s.sigs = nil, nil, nil
	s.rebuildAdapters()
	s.mu.Unlock()
}

// Stale says whether the stored Config was found changed by something else.
func (s *Store) Stale() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stale
}

// ConfigChangedElsewhere is for the page's storage event: the Config changed under this tab. The
// store locks and refuses changes, unless the new stored Config is the one this tab has (a
// write of its own that was echoed).
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

// sameConfig compares two Configs as they are stored (an empty list and none are the same).
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

// Lock forgets the private keys. What was saved stays saved; nothing can be opened or saved
// until a key unlocks it again.
func (s *Store) Lock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids, s.holders, s.sigs = nil, nil, nil
	s.rebuildAdapters()
}

// held is one private key of an unlock, with the owner key it opens.
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

// signer is the first unlocked key's signing key.
func (s *Store) signer() *encrypt.Signer {
	if len(s.sigs) == 0 {
		return nil
	}
	return s.sigs[0]
}

// Sharing is the control of who a document is shared with, for the unlocked store.
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

// Check is saver.Saver.ConfidentialCheck: a document is made confidential only on this store,
// while it is unlocked, and only when one of the owner's keys is not a passkey
// (encrypt.CheckRecoverableOwner), so losing the passkey does not lose the document. Only keys
// that are committed count: a key whose change did not finish is not an owner yet.
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

// ownersFor is every owner key an unlock may match: the current ones and, while a change is
// unfinished, the ones it is making current.
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
