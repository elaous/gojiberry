// Package encrypt wraps any docstore.Adapter so that what it stores is encrypted to keys the
// person holds: envelope encryption in the shape of age (https://age-encryption.org). Each
// document is encrypted with a fresh random data key, and that key is wrapped once for each
// recipient (an SSH key, an age key); sharing is another wrapped copy, revoking is a re-key.
// The private key never reaches the store: encryption and decryption happen where the
// Adapter runs, and the store holds ciphertext only. See docs/encryption-and-passkeys.md.
//
// What stays readable is what lists a store: the catalogs, folder records and ACL records
// (the Skip function). Everything else, documents, versions and their histories, is an age
// file. The catalog is therefore plaintext metadata: document names, tags, folder names and
// shape, sizes and times are visible to whoever can read the store.
//
// Each file's plaintext is framed: a small header with a format version and the stored id is
// inside the encryption, and Load checks it, so a ciphertext copied over another id is
// refused (ErrWrongDocument). Rolling an id back to an older ciphertext of the same id is
// not detectable at this level (the store can always return an older version of what it
// held); the saver's catalog checksums are what notice that.
//
// Reading is strict by default: a stored file without the age header is refused
// (ErrNotEncrypted), so a store writer cannot replace a ciphertext with forged plaintext.
// AllowPlaintext is the explicit opt-out for migrating a store that was clear.
package encrypt

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// ErrNoKey is returned when a document is encrypted and none of the keyring's identities
// can open it.
var ErrNoKey = errors.New("encrypt: none of your keys opens this document")

// ErrNotEncrypted is returned by Load for a stored document that is not encrypted, unless
// the Adapter has AllowPlaintext.
var ErrNotEncrypted = errors.New("encrypt: the stored document is not encrypted")

// ErrWrongDocument is returned by Load when a ciphertext opens but was written for another
// id: it was copied over this one.
var ErrWrongDocument = errors.New("encrypt: the stored file belongs to another document")

// ErrFormat is returned by Load when the decrypted data has no valid frame (an unknown
// version, or not written by this package).
var ErrFormat = errors.New("encrypt: the decrypted data has an unknown format")

// ErrReservedID is returned by Save when an id in the catalog, ACL or font keyspace would be
// encrypted (it must stay readable, so something else owns those ids).
var ErrReservedID = errors.New("encrypt: this id is in a reserved keyspace")

// ErrNoRecipients is returned by Save when there is no one to encrypt to: a document no one
// can open is not saved.
var ErrNoRecipients = errors.New("encrypt: no recipients")

// Keyring says who a document is encrypted to and which keys can open it.
type Keyring interface {
	// Recipients are the keys the document id is encrypted to when saved (the owner, and
	// everyone it is shared with).
	Recipients(ctx context.Context, id string) ([]age.Recipient, error)
	// Identities are the private keys that may open it.
	Identities(ctx context.Context, id string) ([]age.Identity, error)
}

// Static is a Keyring with one set of keys for every document.
type Static struct {
	To   []age.Recipient
	Keys []age.Identity
}

// Recipients implements Keyring.
func (s Static) Recipients(context.Context, string) ([]age.Recipient, error) { return s.To, nil }

// Identities implements Keyring.
func (s Static) Identities(context.Context, string) ([]age.Identity, error) { return s.Keys, nil }

// DefaultSkip says which stored items stay readable: the catalogs, the folder records, the
// ACL records and the fonts, which list or serve a store and hold no document text.
func DefaultSkip(id string) bool {
	return strings.HasPrefix(id, "catalog:") || strings.HasPrefix(id, "acl:") || strings.HasPrefix(id, "fonts/") || strings.HasPrefix(id, KeysPrefix)
}

// Adapter is a docstore.Adapter that encrypts what it saves in Inner.
type Adapter struct {
	Inner docstore.Adapter
	Ring  Keyring
	// Skip says which ids are stored as they are; nil is DefaultSkip.
	Skip func(id string) bool
	// AllowPlaintext lets Load return a document that is stored in the clear, for migrating
	// a store that was not encrypted before (Rekey or RekeyAll then encrypts it). Leave it
	// false otherwise: with it, anyone who can write the store can replace a ciphertext
	// with plaintext of their own and have it read as the document.
	AllowPlaintext bool

	// mu serializes Save of an encrypted id with the re-keys, which hold it from their read
	// to their write: on a store without SaveIf a Save between the two would otherwise be
	// overwritten by the old content. (Taking the store's own Lock in Save is not an option:
	// the saver holds it around its catalog updates, which save documents.) It is taken after
	// the store's Lock, never before.
	mu sync.Mutex
}

// New wraps inner. It is secure by default: every stored file that Skip does not leave
// readable must be an age file for its own id, or Load refuses it.
func New(inner docstore.Adapter, ring Keyring) *Adapter { return &Adapter{Inner: inner, Ring: ring} }

func (a *Adapter) skip(id string) bool {
	if a.Skip != nil {
		return a.Skip(id)
	}
	return DefaultSkip(id)
}

var header = []byte("age-encryption.org/v1\n")

// frameMagic starts the plaintext inside the encryption: magic, a version byte, the id as a
// uvarint length and bytes, then the data.
var frameMagic = []byte("GJE\x01")

func frame(id string, data []byte) []byte {
	out := make([]byte, 0, len(frameMagic)+binary.MaxVarintLen64+len(id)+len(data))
	out = append(out, frameMagic...)
	out = binary.AppendUvarint(out, uint64(len(id)))
	out = append(out, id...)
	return append(out, data...)
}

func unframe(id string, b []byte) ([]byte, error) {
	if !bytes.HasPrefix(b, frameMagic) {
		return nil, ErrFormat
	}
	b = b[len(frameMagic):]
	n, k := binary.Uvarint(b)
	if k <= 0 || n > uint64(len(b)-k) {
		return nil, ErrFormat
	}
	b = b[k:]
	if string(b[:n]) != id {
		return nil, ErrWrongDocument
	}
	return b[n:], nil
}

var reservedPrefixes = []string{"catalog:", "acl:", "fonts/"}

// checkID refuses to encrypt an id in a keyspace that is meant to stay readable.
func (a *Adapter) checkID(id string) error {
	if a.skip(id) {
		return nil
	}
	for _, p := range reservedPrefixes {
		if strings.HasPrefix(id, p) {
			return ErrReservedID
		}
	}
	return nil
}

// IsEncrypted says whether stored bytes are an age file.
func IsEncrypted(b []byte) bool { return bytes.HasPrefix(b, header) }

func (a *Adapter) seal(ctx context.Context, id string, data []byte) ([]byte, error) {
	rs, err := a.Ring.Recipients(ctx, id)
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
	if _, err := w.Write(frame(id, data)); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// open returns the plaintext of a stored file and whether it was encrypted.
func (a *Adapter) open(ctx context.Context, id string, stored []byte) (plain []byte, encrypted bool, err error) {
	if !IsEncrypted(stored) {
		if !a.AllowPlaintext {
			return nil, false, ErrNotEncrypted
		}
		return stored, false, nil
	}
	ids, err := a.Ring.Identities(ctx, id)
	if err != nil {
		return nil, true, err
	}
	r, err := age.Decrypt(bytes.NewReader(stored), ids...)
	if err != nil {
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			return nil, true, ErrNoKey
		}
		return nil, true, fmt.Errorf("encrypt: %w", err)
	}
	framed, err := io.ReadAll(r)
	if err != nil {
		return nil, true, fmt.Errorf("encrypt: %w", err)
	}
	plain, err = unframe(id, framed)
	return plain, true, err
}

// Save implements docstore.Adapter: the data is encrypted to the keyring's recipients.
func (a *Adapter) Save(ctx context.Context, id string, data []byte) error {
	if a.skip(id) {
		return a.Inner.Save(ctx, id, data)
	}
	if err := a.checkID(id); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	ct, err := a.seal(ctx, id, data)
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
	plain, _, err := a.open(ctx, id, d.Data)
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

// SaveIf implements docstore.ConditionalAdapter. prevDigest is the Digest of what the caller
// read through this adapter: for an encrypted id, the plaintext (what Load returned), and ""
// for "there is no such document". It is compared with the decrypted stored file, and the
// write is then conditional on the stored bytes being the ones that were just read, so a
// concurrent change between the check and the write is an ErrConflict (read again and
// retry). A store that cannot write conditionally says so (ErrConditionalUnsupported).
func (a *Adapter) SaveIf(ctx context.Context, id string, data []byte, prevDigest string) error {
	c, ok := a.Inner.(docstore.ConditionalAdapter)
	if !ok {
		return docstore.ErrConditionalUnsupported
	}
	if a.skip(id) {
		return c.SaveIf(ctx, id, data, prevDigest)
	}
	if err := a.checkID(id); err != nil {
		return err
	}
	rawPrev := ""
	d, err := a.Inner.Load(ctx, id)
	switch {
	case errors.Is(err, docstore.ErrNotFound):
		if prevDigest != "" {
			return docstore.ErrConflict
		}
	case err != nil:
		return err
	default:
		plain, _, err := a.open(ctx, id, d.Data)
		if err != nil {
			return err
		}
		if prevDigest == "" || docstore.Digest(plain) != prevDigest {
			return docstore.ErrConflict
		}
		rawPrev = docstore.Digest(d.Data)
	}
	ct, err := a.seal(ctx, id, data)
	if err != nil {
		return err
	}
	return c.SaveIf(ctx, id, ct, rawPrev)
}

// Lock implements docstore.Locker by the inner store's, if it has one.
func (a *Adapter) Lock(ctx context.Context) (func(), error) {
	if l, ok := a.Inner.(docstore.Locker); ok {
		return l.Lock(ctx)
	}
	return func() {}, nil
}

// rekeyAttempts is how many times a re-key re-reads a file that was saved under it.
const rekeyAttempts = 5

// plan opens one stored file for re-keying and checks it can be sealed again, without
// writing anything. It returns the stored digest the write is conditional on.
func (a *Adapter) plan(ctx context.Context, id string) (plain []byte, rawDigest string, err error) {
	d, err := a.Inner.Load(ctx, id)
	if err != nil {
		return nil, "", err
	}
	plain, _, err = a.open(ctx, id, d.Data)
	if err != nil {
		return nil, "", err
	}
	rs, err := a.Ring.Recipients(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if len(rs) == 0 {
		return nil, "", ErrNoRecipients
	}
	return plain, docstore.Digest(d.Data), nil
}

// rekeyOne re-keys one file: read, open, seal to the current recipients, and write only if
// the file is still the one that was read (a Save in between is not lost: the file is read
// again). Without a conditional store the caller's lock is what serializes it.
func (a *Adapter) rekeyOne(ctx context.Context, id string) error {
	for range rekeyAttempts {
		plain, rawDigest, err := a.plan(ctx, id)
		if err != nil {
			return err
		}
		ct, err := a.seal(ctx, id, plain)
		if err != nil {
			return err
		}
		c, ok := a.Inner.(docstore.ConditionalAdapter)
		if !ok {
			return a.Inner.Save(ctx, id, ct)
		}
		if err := c.SaveIf(ctx, id, ct, rawDigest); !errors.Is(err, docstore.ErrConflict) {
			return err
		}
	}
	return docstore.ErrConflict
}

// rekeyGroup re-keys the files of one document: all of them are opened and checked first,
// and if any cannot be, none is written (a document is not left half re-keyed because one of
// its files cannot be opened). Write failures after that are collected, not stopping the
// others. The caller holds the lock.
func (a *Adapter) rekeyGroup(ctx context.Context, ids []string) (int, error) {
	var errs []error
	var present []string
	for _, id := range ids {
		_, _, err := a.plan(ctx, id)
		switch {
		case errors.Is(err, docstore.ErrNotFound):
			// Deleted since it was listed: nothing to re-key.
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		default:
			present = append(present, id)
		}
	}
	if len(errs) > 0 {
		return 0, errors.Join(errs...)
	}
	n := 0
	for _, id := range present {
		if err := a.rekeyOne(ctx, id); err != nil {
			if errors.Is(err, docstore.ErrNotFound) {
				continue
			}
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

// exclusive takes the store's Lock and then the adapter's own, which a Save of an encrypted
// id takes: what a re-key holds from its read to its write.
func (a *Adapter) exclusive(ctx context.Context) (func(), error) {
	unlock, err := a.Lock(ctx)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	return func() { a.mu.Unlock(); unlock() }, nil
}

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

// groupOf names the group of files that are re-keyed as one unit: the document that DocID
// finds, or, for a stored item that is not a document's, the item alone. Rekey, RekeyAll and
// RekeyDocument all group this way.
func groupOf(id string) string {
	if d := DocID(id); d != "" {
		return d
	}
	return id
}

// filesOf lists the stored files, not skipped, whose group is g. The caller holds the lock,
// so what is listed is what is then read.
func (a *Adapter) filesOf(ctx context.Context, g string) ([]string, error) {
	metas, err := a.Inner.List(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, m := range metas {
		if !a.skip(m.ID) && groupOf(m.ID) == g {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// Rekey saves a document again to the keyring's current recipients, with a new data key:
// after a recipient is taken out of the keyring, this is what stops them reading what is
// saved from now on (they keep whatever they decrypted before, and a copy of the old
// ciphertext they may hold). It re-keys every file of the document id belongs to (as
// RekeyDocument does), all or none. It holds the store lock (so a caller must not already
// hold it) and the adapter's, so a Save through this adapter is not overwritten by it, on a
// store that writes conditionally or not. A file that is stored in the clear
// (AllowPlaintext) is encrypted by it.
func (a *Adapter) Rekey(ctx context.Context, id string) error {
	if a.skip(id) {
		return nil
	}
	unlock, err := a.exclusive(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	ids, err := a.filesOf(ctx, groupOf(id))
	if err != nil {
		return err
	}
	found := false
	for _, x := range ids {
		found = found || x == id
	}
	if !found {
		return docstore.ErrNotFound
	}
	_, err = a.rekeyGroup(ctx, ids)
	return err
}

// RekeyDocument encrypts every stored file of one document again, to the keyring's current
// recipients for it (a new data key each): its current and previous copies and its versions.
// The files are listed under the lock and all opened first, and none is written if one
// cannot be (see RekeyIDs).
func (a *Adapter) RekeyDocument(ctx context.Context, docID string) (int, error) {
	unlock, err := a.exclusive(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock()
	ids, err := a.filesOf(ctx, docID)
	if err != nil {
		return 0, err
	}
	return a.rekeyGroup(ctx, ids)
}

// RekeyIDs re-keys the given files as one unit (the files of one document): all are opened
// first and none is written if any cannot be opened. It returns how many were written and
// all the errors.
func (a *Adapter) RekeyIDs(ctx context.Context, ids []string) (int, error) {
	var todo []string
	for _, id := range ids {
		if !a.skip(id) {
			todo = append(todo, id)
		}
	}
	unlock, err := a.exclusive(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock()
	return a.rekeyGroup(ctx, todo)
}

// RekeyAll re-keys every document that is encrypted (not the ones Skip leaves readable),
// and returns how many files it wrote. A document with a file it cannot open is left as it
// was (none of its files written) and the others go on; every error is returned, joined.
// The files are listed under the lock; one that is gone by the time it is read is skipped.
func (a *Adapter) RekeyAll(ctx context.Context) (int, error) {
	unlock, err := a.exclusive(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock()
	metas, err := a.Inner.List(ctx)
	if err != nil {
		return 0, err
	}
	groups := map[string][]string{}
	var order []string
	for _, m := range metas {
		if a.skip(m.ID) {
			continue
		}
		g := groupOf(m.ID)
		if _, ok := groups[g]; !ok {
			order = append(order, g)
		}
		groups[g] = append(groups[g], m.ID)
	}
	n := 0
	var errs []error
	for _, g := range order {
		k, err := a.rekeyGroup(ctx, groups[g])
		n += k
		if err != nil {
			errs = append(errs, err)
		}
	}
	return n, errors.Join(errs...)
}
