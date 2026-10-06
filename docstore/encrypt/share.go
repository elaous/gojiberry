package encrypt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// KeysPrefix is the prefix of the records that say who a document is shared with: "keys:<document
// ID>". They hold public keys only, so they are stored readable (DefaultSkip) and a person
// with only the store can see who has access, but not open anything.
const KeysPrefix = "keys:"

// ErrFingerprint is returned by Share when the fingerprint the owner confirmed is not the key's:
// adding a key is only done on the owner's word about whose it is.
var ErrFingerprint = errors.New("encrypt: the fingerprint confirmed is not the one of this key")

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

// Shared is a key a document is shared with, as the list shows it.
type Shared struct {
	Label       string
	Type        string
	Fingerprint string
	Key         string // the public key's text
	Added       time.Time
}

type sharedRecord struct {
	Recipients []sharedEntry `json:"recipients"`
}

type sharedEntry struct {
	Label       string `json:"label,omitempty"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	Key         string `json:"key"`
	Added       int64  `json:"added,omitempty"`
}

func parseShared(e sharedEntry) (Recipient, error) {
	if e.Type == "age-x25519" {
		return ParseAgeRecipient(e.Key)
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

func loadRecord(ctx context.Context, store docstore.Adapter, docID string) (sharedRecord, error) {
	var rec sharedRecord
	d, err := store.Load(ctx, KeysPrefix+docID)
	if errors.Is(err, docstore.ErrNotFound) {
		return rec, nil
	}
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(d.Data, &rec); err != nil {
		return rec, fmt.Errorf("encrypt: the recipient list of %s is damaged: %w", docID, err)
	}
	return rec, nil
}

// StoreKeyring is a Keyring whose recipients are the owner's keys and, for each document, the
// keys it is shared with, kept in "keys:<ID>" records in Store. Store is the store itself (not
// the encrypting adapter: the records are readable).
type StoreKeyring struct {
	Store docstore.Adapter
	// Owners are always recipients of everything; Keys are the private keys that open it.
	Owners []age.Recipient
	Keys   []age.Identity
}

// Recipients implements Keyring.
func (k StoreKeyring) Recipients(ctx context.Context, id string) ([]age.Recipient, error) {
	out := append([]age.Recipient(nil), k.Owners...)
	doc := DocID(id)
	if doc == "" {
		return out, nil
	}
	rec, err := loadRecord(ctx, k.Store, doc)
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
// whose keyring is a StoreKeyring over the same Store.
type Sharing struct {
	Store   docstore.Adapter
	Adapter *Adapter
}

// List says who a document is shared with, besides the owner's own keys.
func (s *Sharing) List(ctx context.Context, docID string) ([]Shared, error) {
	rec, err := loadRecord(ctx, s.Store, docID)
	if err != nil {
		return nil, err
	}
	out := make([]Shared, 0, len(rec.Recipients))
	for _, e := range rec.Recipients {
		out = append(out, Shared{Label: e.Label, Type: e.Type, Fingerprint: e.Fingerprint, Key: e.Key, Added: time.Unix(e.Added, 0)})
	}
	return out, nil
}

// Add shares a document with a key. confirmed is the fingerprint the owner says they checked
// with the person it belongs to; it must be the key's own (ErrFingerprint), so a key pasted
// for the wrong person is not added by a click alone. Every file of the document (its current
// and previous copies and its versions) is then encrypted again to the longer list, so the new
// person can open all of it. Adding the same key twice changes nothing.
func (s *Sharing) Add(ctx context.Context, docID string, r Recipient, confirmed string) error {
	if !docfile.ValidID(docID) {
		return errors.New("encrypt: not a document ID")
	}
	if strings.TrimSpace(confirmed) != r.Fingerprint || r.Fingerprint == "" {
		return ErrFingerprint
	}
	rec, err := loadRecord(ctx, s.Store, docID)
	if err != nil {
		return err
	}
	for _, e := range rec.Recipients {
		if e.Fingerprint == r.Fingerprint {
			return nil
		}
	}
	rec.Recipients = append(rec.Recipients, sharedEntry{Label: r.Label, Type: r.Type, Fingerprint: r.Fingerprint, Key: r.Text, Added: time.Now().Unix()})
	if err := saveRecord(ctx, s.Store, docID, rec); err != nil {
		return err
	}
	_, err = s.Adapter.RekeyDocument(ctx, docID)
	return err
}

// Remove takes a key out and encrypts the document again, with a new data key, to who is left.
// That stops them reading what is saved from now on; nothing takes back what they already
// opened or copied, and a caller should say so. It returns how many files were encrypted again.
func (s *Sharing) Remove(ctx context.Context, docID, fingerprint string) (int, error) {
	rec, err := loadRecord(ctx, s.Store, docID)
	if err != nil {
		return 0, err
	}
	kept := rec.Recipients[:0]
	found := false
	for _, e := range rec.Recipients {
		if e.Fingerprint == fingerprint {
			found = true
			continue
		}
		kept = append(kept, e)
	}
	if !found {
		return 0, docstore.ErrNotFound
	}
	rec.Recipients = kept
	if err := saveRecord(ctx, s.Store, docID, rec); err != nil {
		return 0, err
	}
	return s.Adapter.RekeyDocument(ctx, docID)
}

func saveRecord(ctx context.Context, store docstore.Adapter, docID string, rec sharedRecord) error {
	if len(rec.Recipients) == 0 {
		err := store.Delete(ctx, KeysPrefix+docID)
		if errors.Is(err, docstore.ErrNotFound) {
			return nil
		}
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return store.Save(ctx, KeysPrefix+docID, data)
}

// RekeyDocument encrypts every stored file of one document again, to the keyring's current
// recipients for it (a new data key each): its current and previous copies and its versions.
func (a *Adapter) RekeyDocument(ctx context.Context, docID string) (int, error) {
	metas, err := a.Inner.List(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range metas {
		if a.skip(m.ID) || DocID(m.ID) != docID {
			continue
		}
		if err := a.Rekey(ctx, m.ID); err != nil {
			return n, fmt.Errorf("%s: %w", m.ID, err)
		}
		n++
	}
	return n, nil
}

// Usage lists the documents a key is a recipient of (their IDs, sorted), so a caller can say
// what removing it will change before doing it.
func (s *Sharing) Usage(ctx context.Context, fingerprint string) ([]string, error) {
	metas, err := s.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	var docs []string
	for _, m := range metas {
		if !strings.HasPrefix(m.ID, KeysPrefix) {
			continue
		}
		doc := strings.TrimPrefix(m.ID, KeysPrefix)
		rec, err := loadRecord(ctx, s.Store, doc)
		if err != nil {
			return nil, err
		}
		for _, e := range rec.Recipients {
			if e.Fingerprint == fingerprint {
				docs = append(docs, doc)
				break
			}
		}
	}
	sort.Strings(docs)
	return docs, nil
}

// RemoveEverywhere takes a key out of every document it is shared with: each is decrypted and
// encrypted again, with a new data key, to who is left. It returns the documents changed and
// how many files were encrypted again. A document that fails stops it with an error naming it;
// the ones before it stay done and running it again finishes the rest. As with Remove, nothing
// takes back what the key's owner already opened or copied.
func (s *Sharing) RemoveEverywhere(ctx context.Context, fingerprint string) (docs []string, files int, err error) {
	all, err := s.Usage(ctx, fingerprint)
	if err != nil {
		return nil, 0, err
	}
	for _, doc := range all {
		n, err := s.Remove(ctx, doc, fingerprint)
		if err != nil {
			return docs, files, fmt.Errorf("%s: %w", doc, err)
		}
		docs = append(docs, doc)
		files += n
	}
	return docs, files, nil
}
