package encrypt

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// KeysPrefix is the prefix of the records that say who a document is shared with: "keys:<document
// ID>". They hold public keys only, so they are stored readable (DefaultSkip) and a person
// with only the store can see who has access, but not open anything.
//
// A record is signed by an owner key (Ed25519, see Signer), and it is what decides who
// the document is encrypted to, so it is never trusted unsigned: StoreKeyring.Recipients
// verifies it against the owners' signing keys and fails closed. A person who can only
// write the store cannot add themselves as a recipient, or change the list, without
// being refused. What the signature does not stop is in docs/encryption-and-passkeys.md
// (a store writer can still delete a record, or put back an older signed one).
const KeysPrefix = "keys:"

// ErrFingerprint is returned by Share when the fingerprint the owner confirmed is not the key's:
// adding a key is only done on the owner's word about whose it is.
var ErrFingerprint = errors.New("encrypt: the fingerprint confirmed is not the one of this key")

// ErrUnsignedRecord is returned when a recipient list is not signed (or not in the signed
// format): it is not used, because it decides who a document is encrypted to.
var ErrUnsignedRecord = errors.New("encrypt: the recipient list is not signed by an owner key")

// ErrBadSignature is returned when a recipient list's signature is not by any of the owners'
// signing keys, or does not match what it signs (the list was changed or came from another
// document).
var ErrBadSignature = errors.New("encrypt: the recipient list's signature is not valid")

// ErrNoSigner is returned by Sharing operations that change a list when it has no Signer.
var ErrNoSigner = errors.New("encrypt: sharing needs the owner's signing key")

// DocID finds the document a stored item belongs to: the document file (the ID, a generation
// suffix, an extension) and its versions ("versions/<ID>/..."); "" for anything else.
func DocID(id string) string {
	if rest, ok := strings.CutPrefix(id, "versions/"); ok {
		if i := strings.IndexByte(rest, '/'); i > 0 {
			rest = rest[:i]
		}
		if docfile.ValidID(rest) {
			return rest
		}
		return ""
	}
	if len(id) >= 36 && docfile.ValidID(id[:36]) {
		return id[:36]
	}
	return ""
}

// recordContext separates what the owner signs here from any other use of the key.
const recordContext = "gojiberry/keys-record/v1"

// Signer is the owner's key for signing recipient lists: Ed25519, from an owner's own key,
// so whoever can change who a document is shared with holds the same secret that opens it.
// Each owner key has its own signing key; every one that may change lists must be in
// StoreKeyring.OwnerSigners (and Sharing.OwnerSigners) of everyone who reads them.
type Signer struct{ key ed25519.PrivateKey }

// NewSigner makes a Signer from a 32-byte secret seed.
func NewSigner(seed []byte) (*Signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("encrypt: a signing seed is 32 bytes")
	}
	return &Signer{ed25519.NewKeyFromSeed(seed)}, nil
}

// SignerFromAge derives the signing key of an age identity (an owner's or a PRF-derived
// one): the seed is the HMAC-SHA256 of a fixed context under the identity's secret key, so
// the same identity always gives the same signing key and nothing else uses it.
func SignerFromAge(id *age.X25519Identity) *Signer {
	m := hmac.New(sha256.New, []byte(id.String()))
	m.Write([]byte("gojiberry/keys-record-signer/v1"))
	s, _ := NewSigner(m.Sum(nil))
	return s
}

// SignerFromSSH uses an ed25519 SSH private key (unencrypted PEM) as the signing key. An RSA
// key cannot: use an age identity or another owner key for signing.
func SignerFromSSH(pem []byte) (*Signer, error) {
	k, err := ssh.ParseRawPrivateKey(pem)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	switch k := k.(type) {
	case *ed25519.PrivateKey:
		return &Signer{*k}, nil
	case ed25519.PrivateKey:
		return &Signer{k}, nil
	}
	return nil, errors.New("encrypt: only an ed25519 SSH key can sign recipient lists")
}

// Public is the key that verifies this Signer's signatures: what goes in OwnerSigners.
func (s *Signer) Public() ed25519.PublicKey { return s.key.Public().(ed25519.PublicKey) }

func recordMessage(docID string, payload []byte) []byte {
	return append([]byte(recordContext+"\x00"+docID+"\x00"), payload...)
}

// Shared is a key a document is shared with, as the list shows it.
type Shared struct {
	Label       string
	Type        string
	Fingerprint string // computed from Key
	Key         string // the public key's text
	Added       time.Time
}

// sharedRecord is the signed payload.
type sharedRecord struct {
	V   int    `json:"v"`
	Doc string `json:"doc"`
	// Seq grows with each change, so a reader that remembers the last one it saw can notice a
	// signed record being put back.
	Seq uint64 `json:"seq"`
	// Pending means the files of the document may not all be encrypted to Recipients yet (a
	// change was saved and its re-key did not finish): Remove, Add and Repair finish it.
	Pending    bool          `json:"pending,omitempty"`
	Recipients []sharedEntry `json:"recipients"`
}

// signedRecord is what is stored under keys:<ID>.
type signedRecord struct {
	Payload []byte `json:"payload"`
	Sig     []byte `json:"sig"`
	Signer  []byte `json:"signer,omitempty"` // a hint; only OwnerSigners are trusted
}

type sharedEntry struct {
	Label string `json:"label,omitempty"`
	Type  string `json:"type"`
	// Fingerprint is for people reading the record; it is recomputed from Key, never trusted.
	Fingerprint string `json:"fingerprint,omitempty"`
	Key         string `json:"key"`
	Added       int64  `json:"added,omitempty"`
}

// parseShared reads an entry's key and returns the Recipient, with the fingerprint and type
// computed from the key itself.
func parseShared(e sharedEntry) (Recipient, error) {
	if e.Type == "age-x25519" {
		r, err := ParseAgeRecipient(e.Key)
		if err != nil {
			return Recipient{}, err
		}
		r.Label = e.Label
		return r, nil
	}
	r, err := ParseSSHRecipient(e.Key)
	if err != nil {
		return Recipient{}, err
	}
	if r.Label == "" {
		r.Label = e.Label
	}
	return r, nil
}

// verifyRecord checks a stored record's signature against the owners' signing keys and
// returns its payload.
func verifyRecord(raw []byte, docID string, owners []ed25519.PublicKey) (sharedRecord, error) {
	var rec sharedRecord
	var sr signedRecord
	if err := json.Unmarshal(raw, &sr); err != nil {
		return rec, fmt.Errorf("encrypt: the recipient list of %s is damaged: %w", docID, err)
	}
	if len(sr.Sig) == 0 || len(sr.Payload) == 0 {
		return rec, fmt.Errorf("%s: %w", docID, ErrUnsignedRecord)
	}
	msg := recordMessage(docID, sr.Payload)
	ok := false
	for _, k := range owners {
		if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, msg, sr.Sig) {
			ok = true
			break
		}
	}
	if !ok {
		return rec, fmt.Errorf("%s: %w", docID, ErrBadSignature)
	}
	if err := json.Unmarshal(sr.Payload, &rec); err != nil || rec.V != 1 || rec.Doc != docID {
		return sharedRecord{}, fmt.Errorf("%s: %w", docID, ErrBadSignature)
	}
	return rec, nil
}

// loadRecord reads and verifies a document's record. A document with no record has no
// shared keys; one that has a record that does not verify is an error (fail closed).
func loadRecord(ctx context.Context, store docstore.Adapter, docID string, owners []ed25519.PublicKey) (rec sharedRecord, digest string, err error) {
	d, err := store.Load(ctx, KeysPrefix+docID)
	if errors.Is(err, docstore.ErrNotFound) {
		return sharedRecord{}, "", nil
	}
	if err != nil {
		return sharedRecord{}, "", err
	}
	rec, err = verifyRecord(d.Data, docID, owners)
	return rec, docstore.Digest(d.Data), err
}

// StoreKeyring is a Keyring whose recipients are the owner's keys and, for each document, the
// keys it is shared with, kept in signed "keys:<ID>" records in Store. Store is the store
// itself (not the encrypting adapter: the records are readable).
type StoreKeyring struct {
	Store docstore.Adapter
	// Owners are always recipients of everything; Keys are the private keys that open it.
	Owners []age.Recipient
	Keys   []age.Identity
	// OwnerSigners are the public signing keys (Signer.Public) of the owner's keys: a
	// record is used only if one of them signed it. With none, a document that has a record
	// cannot be saved (fail closed).
	OwnerSigners []ed25519.PublicKey
}

// Recipients implements Keyring. A record that is missing its signature, or has a wrong
// one, is an error, never a shorter or longer list.
func (k StoreKeyring) Recipients(ctx context.Context, id string) ([]age.Recipient, error) {
	out := append([]age.Recipient(nil), k.Owners...)
	doc := DocID(id)
	if doc == "" {
		return out, nil
	}
	rec, _, err := loadRecord(ctx, k.Store, doc, k.OwnerSigners)
	if err != nil {
		return nil, err
	}
	for _, e := range rec.Recipients {
		r, err := parseShared(e)
		if err != nil {
			return nil, fmt.Errorf("encrypt: a recipient of %s cannot be read: %w", doc, err)
		}
		out = append(out, r.Key)
	}
	return out, nil
}

// Identities implements Keyring.
func (k StoreKeyring) Identities(context.Context, string) ([]age.Identity, error) { return k.Keys, nil }

// Sharing changes who a document is shared with. Adapter is the encrypting adapter over Store,
// whose keyring is a StoreKeyring over the same Store. Signer is the owner's signing key, which
// every change to a list needs; OwnerSigners are further owner keys whose lists are accepted
// (Signer's own key always is).
type Sharing struct {
	Store        docstore.Adapter
	Adapter      *Adapter
	Signer       *Signer
	OwnerSigners []ed25519.PublicKey
}

func (s *Sharing) verifiers() []ed25519.PublicKey {
	out := append([]ed25519.PublicKey(nil), s.OwnerSigners...)
	if s.Signer != nil {
		out = append(out, s.Signer.Public())
	}
	return out
}

// mutateAttempts is how many times a change to a list is redone when it was changed meanwhile.
const mutateAttempts = 5

// mutate reads and verifies the list, lets fn change it, and writes it back signed, only if it
// is still what was read (on a store that writes conditionally). It returns the list as it is
// after fn, whether or not fn changed it.
func (s *Sharing) mutate(ctx context.Context, docID string, fn func(*sharedRecord) (changed bool, err error)) (sharedRecord, error) {
	if s.Signer == nil {
		return sharedRecord{}, ErrNoSigner
	}
	for range mutateAttempts {
		rec, digest, err := loadRecord(ctx, s.Store, docID, s.verifiers())
		if err != nil {
			return rec, err
		}
		changed, err := fn(&rec)
		if err != nil || !changed {
			return rec, err
		}
		rec.V, rec.Doc = 1, docID
		rec.Seq++
		payload, err := json.Marshal(rec)
		if err != nil {
			return rec, err
		}
		data, err := json.Marshal(signedRecord{Payload: payload, Sig: ed25519.Sign(s.Signer.key, recordMessage(docID, payload)), Signer: s.Signer.Public()})
		if err != nil {
			return rec, err
		}
		if c, ok := s.Store.(docstore.ConditionalAdapter); ok {
			err = c.SaveIf(ctx, KeysPrefix+docID, data, digest)
			if errors.Is(err, docstore.ErrConflict) {
				continue
			}
			return rec, err
		}
		return rec, s.Store.Save(ctx, KeysPrefix+docID, data)
	}
	return sharedRecord{}, docstore.ErrConflict
}

// List says who a document is shared with, besides the owner's own keys. A list whose
// signature does not verify is an error.
func (s *Sharing) List(ctx context.Context, docID string) ([]Shared, error) {
	rec, _, err := loadRecord(ctx, s.Store, docID, s.verifiers())
	if err != nil {
		return nil, err
	}
	out := make([]Shared, 0, len(rec.Recipients))
	for _, e := range rec.Recipients {
		r, err := parseShared(e)
		if err != nil {
			return nil, fmt.Errorf("encrypt: a recipient of %s cannot be read: %w", docID, err)
		}
		out = append(out, Shared{Label: r.Label, Type: r.Type, Fingerprint: r.Fingerprint, Key: r.Text, Added: time.Unix(e.Added, 0)})
	}
	return out, nil
}

// Pending says whether a change to the list was saved whose re-key did not finish, so some
// files of the document may still be encrypted to the old list. Repair (or Remove or Add
// again) finishes it.
func (s *Sharing) Pending(ctx context.Context, docID string) (bool, error) {
	rec, _, err := loadRecord(ctx, s.Store, docID, s.verifiers())
	return rec.Pending, err
}

// finish encrypts every file of the document again to the current list (always: it is
// idempotent), and once that worked clears the pending mark. It returns how many files were
// written.
func (s *Sharing) finish(ctx context.Context, docID string) (int, error) {
	n, err := s.Adapter.RekeyDocument(ctx, docID)
	if err != nil {
		return n, err
	}
	_, err = s.mutate(ctx, docID, func(rec *sharedRecord) (bool, error) {
		if !rec.Pending {
			return false, nil
		}
		rec.Pending = false
		return true, nil
	})
	return n, err
}

// Add shares a document with a key. confirmed is the fingerprint the owner says they checked
// with the person it belongs to; it must be the key's own (ErrFingerprint), computed here from
// the key's text, so a key pasted for the wrong person is not added by a click alone. The
// list is saved (signed), then every file of the document (its current and previous copies
// and its versions) is encrypted again to it, so the new person can open all of it. Adding a
// key that is already listed still encrypts the files again (it finishes an earlier add that
// was cut short); it changes nothing else.
func (s *Sharing) Add(ctx context.Context, docID string, r Recipient, confirmed string) error {
	if !docfile.ValidID(docID) {
		return errors.New("encrypt: not a document ID")
	}
	if r.Text == "" {
		return errors.New("encrypt: the recipient has no key text")
	}
	parsed, err := parseShared(sharedEntry{Type: r.Type, Key: r.Text, Label: r.Label})
	if err != nil {
		return err
	}
	if parsed.Fingerprint == "" || strings.TrimSpace(confirmed) != parsed.Fingerprint {
		return ErrFingerprint
	}
	if _, err := s.mutate(ctx, docID, func(rec *sharedRecord) (bool, error) {
		for _, e := range rec.Recipients {
			if p, err := parseShared(e); err == nil && p.Fingerprint == parsed.Fingerprint {
				return false, nil
			}
		}
		label := r.Label
		if label == "" {
			label = parsed.Label
		}
		rec.Recipients = append(rec.Recipients, sharedEntry{Label: label, Type: parsed.Type, Fingerprint: parsed.Fingerprint, Key: parsed.Text, Added: time.Now().Unix()})
		rec.Pending = true
		return true, nil
	}); err != nil {
		return err
	}
	_, err = s.finish(ctx, docID)
	return err
}

// Remove takes a key out and encrypts the document again, with a new data key, to who is left.
// That stops them reading what is saved from now on; nothing takes back what they already
// opened or copied, and a caller should say so. It returns how many files were encrypted again.
//
// It can be run again after a failure: the list is saved first, marked pending, and the
// mark is cleared only when every file is re-keyed. Remove of a key that is no longer listed
// finishes a pending re-key, and is docstore.ErrNotFound only when nothing is pending.
func (s *Sharing) Remove(ctx context.Context, docID, fingerprint string) (int, error) {
	found := false
	rec, err := s.mutate(ctx, docID, func(rec *sharedRecord) (bool, error) {
		kept := make([]sharedEntry, 0, len(rec.Recipients))
		for _, e := range rec.Recipients {
			if p, err := parseShared(e); err == nil && p.Fingerprint == fingerprint {
				found = true
				continue
			}
			kept = append(kept, e)
		}
		if !found {
			return false, nil
		}
		rec.Recipients, rec.Pending = kept, true
		return true, nil
	})
	if err != nil {
		return 0, err
	}
	if !found && !rec.Pending {
		return 0, docstore.ErrNotFound
	}
	return s.finish(ctx, docID)
}

// Repair encrypts every file of a document again to its current list and clears a pending
// mark: the way to finish a share or removal that was cut short, whichever key it was for.
func (s *Sharing) Repair(ctx context.Context, docID string) (int, error) {
	if !docfile.ValidID(docID) {
		return 0, errors.New("encrypt: not a document ID")
	}
	if _, _, err := loadRecord(ctx, s.Store, docID, s.verifiers()); err != nil {
		return 0, err
	}
	return s.finish(ctx, docID)
}

// RekeyDocument encrypts every stored file of one document again, to the keyring's current
// recipients for it (a new data key each): its current and previous copies and its versions.
// The files are all opened first, and none is written if one cannot be (see RekeyIDs).
func (a *Adapter) RekeyDocument(ctx context.Context, docID string) (int, error) {
	metas, err := a.Inner.List(ctx)
	if err != nil {
		return 0, err
	}
	var ids []string
	for _, m := range metas {
		if !a.skip(m.ID) && DocID(m.ID) == docID {
			ids = append(ids, m.ID)
		}
	}
	return a.RekeyIDs(ctx, ids)
}

// Usage lists the documents (their IDs, sorted) that removing a key changes: the ones it is a
// recipient of, and the ones with a pending re-key (a change that was cut short, whose files
// may still be encrypted to an older list), so a caller can say what removing it will do and a
// re-run finds what an earlier run left. A record that does not verify is skipped and
// reported in the error, joined, beside the documents found.
func (s *Sharing) Usage(ctx context.Context, fingerprint string) ([]string, error) {
	metas, err := s.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	var docs []string
	var errs []error
	for _, m := range metas {
		doc, ok := strings.CutPrefix(m.ID, KeysPrefix)
		if !ok {
			continue
		}
		rec, _, err := loadRecord(ctx, s.Store, doc, s.verifiers())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		hit := rec.Pending
		for _, e := range rec.Recipients {
			if p, err := parseShared(e); err == nil && p.Fingerprint == fingerprint {
				hit = true
				break
			}
		}
		if hit {
			docs = append(docs, doc)
		}
	}
	sort.Strings(docs)
	return docs, errors.Join(errs...)
}

// RemoveEverywhere takes a key out of every document it is shared with: each is decrypted and
// encrypted again, with a new data key, to who is left. It returns the documents done and how
// many files were encrypted again. A document that fails does not stop the others: every
// failure is returned, joined and each naming its document, and running it again finishes
// those (Usage lists a document whose re-key is pending even when the key is already out of
// its list, and Remove finishes it). As with Remove, nothing takes back what the key's owner
// already opened or copied.
func (s *Sharing) RemoveEverywhere(ctx context.Context, fingerprint string) (docs []string, files int, err error) {
	all, uerr := s.Usage(ctx, fingerprint)
	var errs []error
	if uerr != nil {
		errs = append(errs, uerr)
	}
	for _, doc := range all {
		n, err := s.Remove(ctx, doc, fingerprint)
		files += n
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", doc, err))
			continue
		}
		docs = append(docs, doc)
	}
	return docs, files, errors.Join(errs...)
}
