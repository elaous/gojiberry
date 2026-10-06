// Package encrypt wraps any docstore.Adapter so that what it stores is encrypted to keys the
// person holds: envelope encryption in the shape of age (https://age-encryption.org). Each
// document is encrypted with a fresh random data key, and that key is wrapped once for each
// recipient (an SSH key, an age key); sharing is another wrapped copy, revoking is a re-key.
// The private key never reaches the store: encryption and decryption happen where the
// Adapter runs, and the store holds ciphertext only. See docs/encryption-and-passkeys.md.
//
// What stays readable is what lists a store: the catalogs, folder records and ACL records
// (the Skip function). Everything else, documents, versions and their histories, is an age
// file.
package encrypt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docstore"
)

// ErrNoKey is returned when a document is encrypted and none of the keyring's identities
// can open it.
var ErrNoKey = errors.New("encrypt: none of your keys opens this document")

// ErrNotEncrypted is returned by Load for a stored document that is not encrypted, when the
// Adapter was made to require encryption.
var ErrNotEncrypted = errors.New("encrypt: the stored document is not encrypted")

// ErrNoRecipients is returned by Save when there is no one to encrypt to: a document no one
// can open is not saved.
var ErrNoRecipients = errors.New("encrypt: no recipients")

// Keyring says who a document is encrypted to and which keys can open it.
type Keyring interface {
	// Recipients are the keys the document id is encrypted to when saved (the owner, and
	// everyone it is shared with).
	Recipients(id string) ([]age.Recipient, error)
	// Identities are the private keys that may open it.
	Identities(id string) ([]age.Identity, error)
}

// Static is a Keyring with one set of keys for every document.
type Static struct {
	To   []age.Recipient
	Keys []age.Identity
}

// Recipients implements Keyring.
func (s Static) Recipients(string) ([]age.Recipient, error) { return s.To, nil }

// Identities implements Keyring.
func (s Static) Identities(string) ([]age.Identity, error) { return s.Keys, nil }

// DefaultSkip says which stored items stay readable: the catalogs, the folder records, the
// ACL records and the fonts, which list or serve a store and hold no document text.
func DefaultSkip(id string) bool {
	return strings.HasPrefix(id, "catalog:") || strings.HasPrefix(id, "acl:") || strings.HasPrefix(id, "fonts/")
}

// Adapter is a docstore.Adapter that encrypts what it saves in Inner.
type Adapter struct {
	Inner docstore.Adapter
	Ring  Keyring
	// Skip says which ids are stored as they are; nil is DefaultSkip.
	Skip func(id string) bool
	// RequireEncrypted makes Load refuse a document that is stored in the clear (a store
	// that was not encrypted before, or was tampered with), instead of returning it.
	RequireEncrypted bool
}

// New wraps inner.
func New(inner docstore.Adapter, ring Keyring) *Adapter { return &Adapter{Inner: inner, Ring: ring} }

func (a *Adapter) skip(id string) bool {
	if a.Skip != nil {
		return a.Skip(id)
	}
	return DefaultSkip(id)
}

var header = []byte("age-encryption.org/v1\n")

// IsEncrypted says whether stored bytes are an age file.
func IsEncrypted(b []byte) bool { return bytes.HasPrefix(b, header) }

func (a *Adapter) seal(id string, data []byte) ([]byte, error) {
	rs, err := a.Ring.Recipients(id)
	if err != nil {
		return nil, err
	}
	if len(rs) == 0 {
		return nil, ErrNoRecipients
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, rs...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (a *Adapter) open(id string, stored []byte) ([]byte, error) {
	if !IsEncrypted(stored) {
		if a.RequireEncrypted {
			return nil, ErrNotEncrypted
		}
		return stored, nil
	}
	ids, err := a.Ring.Identities(id)
	if err != nil {
		return nil, err
	}
	r, err := age.Decrypt(bytes.NewReader(stored), ids...)
	if err != nil {
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			return nil, ErrNoKey
		}
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	return io.ReadAll(r)
}

// Save implements docstore.Adapter: the data is encrypted to the keyring's recipients.
func (a *Adapter) Save(ctx context.Context, id string, data []byte) error {
	if a.skip(id) {
		return a.Inner.Save(ctx, id, data)
	}
	ct, err := a.seal(id, data)
	if err != nil {
		return err
	}
	return a.Inner.Save(ctx, id, ct)
}

// Load implements docstore.Adapter: the document is decrypted with the keyring's identities.
func (a *Adapter) Load(ctx context.Context, id string) (docstore.Document, error) {
	d, err := a.Inner.Load(ctx, id)
	if err != nil || a.skip(id) {
		return d, err
	}
	plain, err := a.open(id, d.Data)
	if err != nil {
		return docstore.Document{}, err
	}
	d.Data = plain
	return d, nil
}

// List implements docstore.Adapter. Sizes are of what is stored (the ciphertext).
func (a *Adapter) List(ctx context.Context) ([]docstore.Meta, error) { return a.Inner.List(ctx) }

// Delete implements docstore.Adapter.
func (a *Adapter) Delete(ctx context.Context, id string) error { return a.Inner.Delete(ctx, id) }

// SaveIf implements docstore.ConditionalAdapter: the digest a document is compared with is
// of the bytes stored (for an encrypted document, the ciphertext), which is what a caller
// that read it through Inner sees; the catalog, which is stored as it is, is the case that
// matters. A store that cannot write conditionally says so.
func (a *Adapter) SaveIf(ctx context.Context, id string, data []byte, prevDigest string) error {
	c, ok := a.Inner.(docstore.ConditionalAdapter)
	if !ok {
		return docstore.ErrConditionalUnsupported
	}
	if !a.skip(id) {
		ct, err := a.seal(id, data)
		if err != nil {
			return err
		}
		data = ct
	}
	return c.SaveIf(ctx, id, data, prevDigest)
}

// Lock implements docstore.Locker by the inner store's, if it has one.
func (a *Adapter) Lock(ctx context.Context) (func(), error) {
	if l, ok := a.Inner.(docstore.Locker); ok {
		return l.Lock(ctx)
	}
	return func() {}, nil
}

// Rekey saves a document again to the keyring's current recipients, with a new data key:
// after a recipient is taken out of the keyring, this is what stops them reading what is
// saved from now on (they keep whatever they decrypted before, and a copy of the old
// ciphertext they may hold).
func (a *Adapter) Rekey(ctx context.Context, id string) error {
	d, err := a.Load(ctx, id)
	if err != nil {
		return err
	}
	return a.Save(ctx, id, d.Data)
}

// RekeyAll re-keys every document that is encrypted (not the ones Skip leaves readable),
// and returns how many it did. A document it cannot open stops it with an error naming it.
func (a *Adapter) RekeyAll(ctx context.Context) (int, error) {
	metas, err := a.Inner.List(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range metas {
		if a.skip(m.ID) {
			continue
		}
		if err := a.Rekey(ctx, m.ID); err != nil {
			return n, fmt.Errorf("%s: %w", m.ID, err)
		}
		n++
	}
	return n, nil
}
