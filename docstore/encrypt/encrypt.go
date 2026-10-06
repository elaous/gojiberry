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
// Each file's plaintext is framed: a small header inside the encryption holds a format
// version, the stored id and, from frame version 2, the protection level, the sequence number
// of the signed keys:<ID> record the file was sealed under and a hash of that record's
// recipient list. Load checks the id, so a ciphertext copied over another id is refused
// (ErrWrongDocument), and checks the rest against the record (see Binding and ErrDowngrade):
// only a holder of a recipient key can write a frame, so the file itself proves what level and
// record it was written under, which a store writer who deletes or replays the record cannot
// undo. Rolling an id back to an older ciphertext of the same id is not detectable at this
// level (the store can always return an older version of what it held); the saver's catalog
// checksums are what notice that.
//
// Reading is strict by default: a stored file without the age header is refused
// (ErrNotEncrypted), so a store writer cannot replace a ciphertext with forged plaintext.
// AllowPlaintext is the explicit opt-out for migrating a store that was clear.
package encrypt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// ErrNoKey is returned when a document is encrypted and none of the keyring's identities
// can open it.
var ErrNoKey error = noKeyError{}

// noKeyError is ErrNoKey, which is also docstore.ErrLocked (errors.Is), so code that does not
// import this package (the saver's catalog rebuild) can tell a document it cannot open from a
// store that failed.
type noKeyError struct{}

func (noKeyError) Error() string        { return "encrypt: none of your keys opens this document" }
func (noKeyError) Is(target error) bool { return target == docstore.ErrLocked }

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
// ACL records and the fonts, which list or serve a store and hold no document text. The
// folder records ("catalog:folder/<ID>") hold only a folder's place in the tree and its
// name, or the placeholder for a confidential folder. That folder's real name is NOT
// skipped: it lives in "folder-secret/<ID>" (docstore.FolderSecretPrefix), which is
// encrypted like any document file and, once the folder's signed record says e2ee, wrapped
// for people's keys only.
func DefaultSkip(id string) bool {
	return strings.HasPrefix(id, "catalog:") || strings.HasPrefix(id, "acl:") || strings.HasPrefix(id, "fonts/") || strings.HasPrefix(id, KeysPrefix) || strings.HasPrefix(id, directoryPrefix)
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

// The frame is the plaintext inside the encryption. Version 1 (written before the protection
// level was bound in): magic "GJE\x01", the id as a uvarint length and bytes, the data.
// Version 2: magic "GJE\x02", the id, then a level byte (0 server, 1 end to end), the seq of the
// signed record as a uvarint, the 32-byte hash of its recipient list, the data. A v1 file is
// read as before and carries no level to cross-check; the first Save or re-key of it writes v2
// (which pins the level from then on).
var (
	frameMagicV1 = []byte("GJE\x01")
	frameMagic   = []byte("GJE\x02")
)

const (
	levelServer byte = 0
	levelE2EE   byte = 1
)

// Binding is what ties a stored file to the signed keys:<ID> record: the record's state as the
// keyring read it (Exists, Pending, and the three values below) and, in a v2 frame, what the
// file was sealed under.
type Binding struct {
	// Exists is set for a record that was found (and verified); only the keyring's reading
	// of the record sets it, a frame has none.
	Exists bool
	// Level is "" (server) or ProtectionE2EE.
	Level string
	// Seq is the record's sequence number; 0 for a document with no record.
	Seq uint64
	// Pending is the record's flag: a switch of level or a change of list is being re-keyed.
	// Only the record has it.
	Pending bool
	// Hash is a hash of the record's recipient list (zero when there is no record).
	Hash [sha256.Size]byte
}

// Binder is a Keyring that can also say the Binding of the record it chose recipients by. The
// adapter then writes v2 frames carrying it and cross-checks the frames it reads or replaces.
// StoreKeyring and ConfidentialKeyring are Binders; a Keyring that is not (Static) writes v2
// frames with a zero Binding and nothing is cross-checked.
type Binder interface {
	Keyring
	// Bind is Recipients and the record's Binding, read once so they agree.
	Bind(ctx context.Context, id string) ([]age.Recipient, Binding, error)
	// Binding is the record's Binding alone (the verified record, never more than one read).
	Binding(ctx context.Context, id string) (Binding, error)
}

// ErrDowngrade is returned (wrapped, with the reason) when a file proves a document's
// protection was lowered behind the owner's back: the file was sealed under record seq N or at
// the end-to-end level, and the keys:<ID> record is now missing, older than N (a replayed
// record), or (Load only) says a level the file is not wrapped for with no switch in progress.
// Save and re-key write nothing and Load returns no data. It is docstore.ErrDowngrade.
// Sharing.Recover is the owner's way forward when the record was restored on purpose.
var ErrDowngrade = docstore.ErrDowngrade

func downgrade(id, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrDowngrade, id, fmt.Sprintf(format, args...))
}

func levelByte(l string) byte {
	if l == ProtectionE2EE {
		return levelE2EE
	}
	return levelServer
}

func levelString(b byte) (string, bool) {
	switch b {
	case levelServer:
		return "", true
	case levelE2EE:
		return ProtectionE2EE, true
	}
	return "", false
}

func rank(level string) int {
	if level == ProtectionE2EE {
		return 1
	}
	return 0
}

// frameInfo says which frame version a file had and, for v2, the binding in it.
type frameInfo struct {
	version int
	b       Binding
}

func frame(id string, b Binding, data []byte) []byte {
	out := make([]byte, 0, len(frameMagic)+binary.MaxVarintLen64*2+len(id)+1+sha256.Size+len(data))
	out = append(out, frameMagic...)
	out = binary.AppendUvarint(out, uint64(len(id)))
	out = append(out, id...)
	out = append(out, levelByte(b.Level))
	out = binary.AppendUvarint(out, b.Seq)
	out = append(out, b.Hash[:]...)
	return append(out, data...)
}

// unframe checks the frame of the decrypted b against id and returns the data and what the
// frame said. A prefix of a frame (the header alone) parses too: the data is then short.
func unframe(id string, b []byte) ([]byte, frameInfo, error) {
	var fi frameInfo
	switch {
	case bytes.HasPrefix(b, frameMagic):
		fi.version = 2
		b = b[len(frameMagic):]
	case bytes.HasPrefix(b, frameMagicV1):
		fi.version = 1
		b = b[len(frameMagicV1):]
	default:
		return nil, fi, ErrFormat
	}
	n, k := binary.Uvarint(b)
	if k <= 0 || n > uint64(len(b)-k) {
		return nil, fi, ErrFormat
	}
	b = b[k:]
	if string(b[:n]) != id {
		return nil, fi, ErrWrongDocument
	}
	b = b[n:]
	if fi.version == 1 {
		return b, fi, nil
	}
	if len(b) < 1 {
		return nil, fi, ErrFormat
	}
	level, ok := levelString(b[0])
	if !ok {
		return nil, fi, ErrFormat
	}
	fi.b.Level = level
	b = b[1:]
	seq, k := binary.Uvarint(b)
	if k <= 0 || len(b)-k < sha256.Size {
		return nil, fi, ErrFormat
	}
	fi.b.Seq = seq
	b = b[k:]
	copy(fi.b.Hash[:], b[:sha256.Size])
	return b[sha256.Size:], fi, nil
}

// crossCheck compares what a file says (f) with the record as read now (r). The rules, with
// Fs and Rs the two seqs:
//
//   - no record, but the file was sealed under record Fs > 0 or at the end-to-end level: the
//     record was deleted.
//   - Fs > Rs: the record is older than one the file was sealed under: a replay.
//   - Fs == Rs: the level and recipient hash must agree (a signed record cannot differ at one
//     seq; this is a corrupt or crafted state).
//   - Fs < Rs is normal (every change to the record, including clearing pending, raises it
//     while files are re-keyed in between), and is accepted, except, with strict (Load only,
//     where data would be returned under the wrong level), a file wrapped below the record's
//     level when the record is not pending: a server-level ciphertext put back into an
//     end-to-end document. While a switch is pending the stale files are the ones the
//     re-key has not reached yet. A file wrapped above the record's level (people only, in a
//     document the record calls server) is the safe direction and accepted.
//
// A v1 frame has nothing to compare and always passes.
func crossCheck(id string, f frameInfo, r Binding, strict bool) error {
	if f.version < 2 {
		return nil
	}
	fb := f.b
	switch {
	case !r.Exists:
		if fb.Seq > 0 || fb.Level == ProtectionE2EE {
			return downgrade(id, "the file was sealed under recipient record %d (%s) and that record is missing: it was deleted", fb.Seq, levelName(fb.Level))
		}
	case fb.Seq > r.Seq:
		return downgrade(id, "the file was sealed under recipient record %d (%s) but the record is %d (%s): an older signed record was put back", fb.Seq, levelName(fb.Level), r.Seq, levelName(r.Level))
	case fb.Seq == r.Seq:
		if fb.Level != r.Level || fb.Hash != r.Hash {
			return downgrade(id, "the file and the record disagree about level or recipients at record %d", r.Seq)
		}
	case strict && rank(fb.Level) < rank(r.Level) && !r.Pending:
		return downgrade(id, "the file is wrapped at the %s level but the document is %s and no switch is in progress", levelName(fb.Level), levelName(r.Level))
	}
	return nil
}

func levelName(l string) string {
	if l == ProtectionE2EE {
		return "end-to-end"
	}
	return "server"
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

// bind returns the recipients of id and the Binding their record has. A keyring that is not a
// Binder, or an id that belongs to no document, has the zero Binding.
func (a *Adapter) bind(ctx context.Context, id string) ([]age.Recipient, Binding, error) {
	if b, ok := a.Ring.(Binder); ok && DocID(id) != "" {
		return b.Bind(ctx, id)
	}
	rs, err := a.Ring.Recipients(ctx, id)
	return rs, Binding{}, err
}

func (a *Adapter) binds(id string) bool {
	_, ok := a.Ring.(Binder)
	return ok && DocID(id) != ""
}

func (a *Adapter) seal(ctx context.Context, id string, data []byte) ([]byte, error) {
	rs, b, err := a.bind(ctx, id)
	if err != nil {
		return nil, err
	}
	return sealTo(id, rs, b, data)
}

func sealTo(id string, rs []age.Recipient, b Binding, data []byte) ([]byte, error) {
	if len(rs) == 0 {
		return nil, ErrNoRecipients
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, rs...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(frame(id, b, data)); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decrypt opens the age layer of a stored file; ok is false for a file that is not encrypted
// and AllowPlaintext lets it through (stored is then the plaintext).
func (a *Adapter) decrypt(ctx context.Context, id string, stored []byte) (r io.Reader, ok bool, err error) {
	if !IsEncrypted(stored) {
		if !a.AllowPlaintext {
			return nil, false, ErrNotEncrypted
		}
		return nil, false, nil
	}
	ids, err := a.Ring.Identities(ctx, id)
	if err != nil {
		return nil, true, err
	}
	if len(ids) == 0 {
		return nil, true, ErrNoKey // a keyring with no private key opens nothing
	}
	r, err = age.Decrypt(bytes.NewReader(stored), ids...)
	if err != nil {
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			return nil, true, ErrNoKey
		}
		return nil, true, fmt.Errorf("encrypt: %w", err)
	}
	return r, true, nil
}

// open returns the plaintext of a stored file, what its frame said, and whether it was
// encrypted. It does not cross-check the frame with the record: the callers do, each with
// the rule that suits it (crossCheck).
func (a *Adapter) open(ctx context.Context, id string, stored []byte) (plain []byte, fi frameInfo, encrypted bool, err error) {
	r, enc, err := a.decrypt(ctx, id, stored)
	if err != nil || !enc {
		if err == nil {
			return stored, frameInfo{}, false, nil
		}
		return nil, frameInfo{}, enc, err
	}
	framed, err := io.ReadAll(r)
	if err != nil {
		return nil, frameInfo{}, true, fmt.Errorf("encrypt: %w", err)
	}
	plain, fi, err = unframe(id, framed)
	return plain, fi, true, err
}

// peekFrame reads only the frame header of a stored file: age authenticates the first chunk
// of the stream before releasing it, which is all that is read here.
func (a *Adapter) peekFrame(ctx context.Context, id string, stored []byte) (frameInfo, error) {
	r, enc, err := a.decrypt(ctx, id, stored)
	if err != nil {
		return frameInfo{}, err
	}
	if !enc {
		return frameInfo{}, ErrNotEncrypted
	}
	buf := make([]byte, 1024)
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return frameInfo{}, fmt.Errorf("encrypt: %w", err)
	}
	_, fi, err := unframe(id, buf[:n])
	return fi, err
}

// mainPrefix is the prefix of the stored files that carry a document's current content: the
// generations "<ID>~<tag>.<ext>" the saver writes, or a confidential folder's one record.
func mainPrefix(id string) string {
	if strings.HasPrefix(id, docstore.FolderSecretPrefix) {
		return id
	}
	return DocID(id)
}

// peekAttempts is how many of a document's newest files checkDocument tries to read a frame
// from (one that cannot be opened here gives none).
const peekAttempts = 3

// checkDocument cross-checks the record binding r of the document id belongs to with what the
// newest stored file of that document says (the rules of crossCheck without the strict one:
// this is the write side). It reads nothing for an id that is not a document's or for a keyring
// that is not a Binder, and finds no evidence (so passes) when the document has no file that
// can be opened here or only v1 files, which have no level to compare.
func (a *Adapter) checkDocument(ctx context.Context, id string, r Binding) error {
	return a.checkFiles(ctx, DocID(id), []string{mainPrefix(id)}, r)
}

// checkDocumentByID is checkDocument for a caller that has the document's (or folder's) ID
// and not the id of one of its files: it looks at the document's generations and at a
// confidential folder's record.
func (a *Adapter) checkDocumentByID(ctx context.Context, docID string, r Binding) error {
	return a.checkFiles(ctx, docID, []string{docID, docstore.FolderSecretPrefix + docID}, r)
}

func (a *Adapter) checkFiles(ctx context.Context, docID string, prefixes []string, r Binding) error {
	if docID == "" || !a.binds(docID) {
		return nil
	}
	var files []docstore.Meta
	for _, prefix := range prefixes {
		metas, err := docstore.ListPrefix(ctx, a.Inner, prefix)
		if err != nil {
			return err
		}
		for _, m := range metas {
			if !a.skip(m.ID) && DocID(m.ID) == docID {
				files = append(files, m)
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].UpdatedAt.After(files[j].UpdatedAt) })
	tried := 0
	for _, m := range files {
		if tried == peekAttempts {
			break
		}
		d, err := a.Inner.Load(ctx, m.ID)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		tried++
		fi, err := a.peekFrame(ctx, m.ID, d.Data)
		if err != nil {
			continue // not openable here: no evidence from this one
		}
		return crossCheck(m.ID, fi, r, false)
	}
	return nil
}

// Save implements docstore.Adapter: the data is encrypted to the keyring's recipients.
//
// For a document with a record (a Binder keyring) the file written carries the record's level,
// seq and recipient hash, and before it is written the document's newest stored file is
// cross-checked with the record the recipients came from: a record that is missing or older
// than the file says it was sealed under is ErrDowngrade, and nothing is written.
func (a *Adapter) Save(ctx context.Context, id string, data []byte) error {
	if a.skip(id) {
		return a.Inner.Save(ctx, id, data)
	}
	if err := a.checkID(id); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rs, b, err := a.bind(ctx, id)
	if err != nil {
		return err
	}
	if err := a.checkDocument(ctx, id, b); err != nil {
		return err
	}
	ct, err := sealTo(id, rs, b, data)
	if err != nil {
		return err
	}
	return a.Inner.Save(ctx, id, ct)
}

// Load implements docstore.Adapter: the document is decrypted with the keyring's identities,
// and, for a Binder keyring, the file's frame is cross-checked with the keys record: a file
// that proves the record was deleted or replaced by an older one, or that is wrapped below
// the document's level with no switch in progress, is ErrDowngrade and returns no data. A v1
// file (written before the frame had a level) is read without that check.
func (a *Adapter) Load(ctx context.Context, id string) (docstore.Document, error) {
	d, err := a.Inner.Load(ctx, id)
	if err != nil || a.skip(id) {
		return d, err
	}
	plain, fi, _, err := a.open(ctx, id, d.Data)
	if err != nil {
		return docstore.Document{}, err
	}
	if fi.version >= 2 && a.binds(id) {
		r, err := a.Ring.(Binder).Binding(ctx, id)
		if err != nil {
			return docstore.Document{}, err
		}
		if err := crossCheck(id, fi, r, true); err != nil {
			return docstore.Document{}, err
		}
	}
	d.Data = plain
	return d, nil
}

// List implements docstore.Adapter. Sizes are of what is stored (the ciphertext).
func (a *Adapter) List(ctx context.Context) ([]docstore.Meta, error) { return a.Inner.List(ctx) }

// ListPrefix implements docstore.PrefixLister over Inner (ids are stored as they are).
func (a *Adapter) ListPrefix(ctx context.Context, prefix string) ([]docstore.Meta, error) {
	return docstore.ListPrefix(ctx, a.Inner, prefix)
}

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
		plain, _, _, err := a.open(ctx, id, d.Data)
		if err != nil {
			return err
		}
		if prevDigest == "" || docstore.Digest(plain) != prevDigest {
			return docstore.ErrConflict
		}
		rawPrev = docstore.Digest(d.Data)
	}
	rs, b, err := a.bind(ctx, id)
	if err != nil {
		return err
	}
	if err := a.checkDocument(ctx, id, b); err != nil {
		return err
	}
	ct, err := sealTo(id, rs, b, data)
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

// planned is a stored file opened for re-keying: its plaintext, the digest the write is
// conditional on, and the recipients and Binding it will be sealed with.
type planned struct {
	plain     []byte
	rawDigest string
	rs        []age.Recipient
	b         Binding
}

// plan opens one stored file for re-keying and checks it can be sealed again, without
// writing anything. The file's frame is cross-checked with the record the new recipients
// come from, so a re-key never seals a file under a record older than the one it was sealed
// under (ErrDowngrade). A file below the record's level is exactly what a re-key repairs, so
// that part of the rule is not applied.
func (a *Adapter) plan(ctx context.Context, id string) (planned, error) {
	d, err := a.Inner.Load(ctx, id)
	if err != nil {
		return planned{}, err
	}
	plain, fi, _, err := a.open(ctx, id, d.Data)
	if err != nil {
		return planned{}, err
	}
	rs, b, err := a.bind(ctx, id)
	if err != nil {
		return planned{}, err
	}
	if len(rs) == 0 {
		return planned{}, ErrNoRecipients
	}
	if a.binds(id) {
		if err := crossCheck(id, fi, b, false); err != nil {
			return planned{}, err
		}
	}
	return planned{plain, docstore.Digest(d.Data), rs, b}, nil
}

// rekeyOne re-keys one file: read, open, seal to the current recipients, and write only if
// the file is still the one that was read (a Save in between is not lost: the file is read
// again). Without a conditional store the caller's lock is what serializes it.
func (a *Adapter) rekeyOne(ctx context.Context, id string) error {
	for range rekeyAttempts {
		p, err := a.plan(ctx, id)
		if err != nil {
			return err
		}
		ct, err := sealTo(id, p.rs, p.b, p.plain)
		if err != nil {
			return err
		}
		c, ok := a.Inner.(docstore.ConditionalAdapter)
		if !ok {
			return a.Inner.Save(ctx, id, ct)
		}
		if err := c.SaveIf(ctx, id, ct, p.rawDigest); !errors.Is(err, docstore.ErrConflict) {
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
	done, _, err := a.rekeyGroupFiles(ctx, ids)
	return len(done), err
}

// rekeyGroupFiles is rekeyGroup that says which files it wrote and, when it wrote none because
// some could not be opened, which those were (unreadable).
func (a *Adapter) rekeyGroupFiles(ctx context.Context, ids []string) (written, unreadable []string, err error) {
	var errs []error
	var present []string
	for _, id := range ids {
		_, err := a.plan(ctx, id)
		switch {
		case errors.Is(err, docstore.ErrNotFound):
			// Deleted since it was listed: nothing to re-key.
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			if !errors.Is(err, ErrDowngrade) {
				unreadable = append(unreadable, id) // a downgrade is a finding, not a lost file
			}
		default:
			present = append(present, id)
		}
	}
	if len(errs) > 0 {
		return nil, unreadable, errors.Join(errs...)
	}
	var done []string
	for _, id := range present {
		if err := a.rekeyOne(ctx, id); err != nil {
			if errors.Is(err, docstore.ErrNotFound) {
				continue
			}
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			continue
		}
		done = append(done, id)
	}
	return done, nil, errors.Join(errs...)
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
// suffix, an extension), its versions ("versions/<ID>/...") and, for a confidential folder
// (whose ID is a UUID like a document's), the record of its real name
// ("folder-secret/<ID>"); "" for anything else.
func DocID(id string) string {
	// A confidential folder's real name ("folder-secret/<folder ID>") is governed by the
	// folder's own keys:<ID> record, the way a document's files are by the document's.
	if rest, ok := strings.CutPrefix(id, docstore.FolderSecretPrefix); ok {
		if docfile.ValidID(rest) {
			return rest
		}
		return ""
	}
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

// RekeyDocumentFiles is RekeyDocument that says which files it wrote (done) and which of the
// document's files it listed and did not write (stale: they are still encrypted to whatever
// recipients they had), so a caller can report what a failed re-key left behind.
func (a *Adapter) RekeyDocumentFiles(ctx context.Context, docID string) (done, stale []string, err error) {
	done, stale, _, err = a.RekeyDocumentReport(ctx, docID)
	return done, stale, err
}

// RekeyDocumentReport is RekeyDocumentFiles that also says which of the files could not be
// opened (unreadable, a subset of stale): the reason none was written. Nothing is dropped:
// what to do with a file that cannot be opened is the caller's decision.
func (a *Adapter) RekeyDocumentReport(ctx context.Context, docID string) (done, stale, unreadable []string, err error) {
	unlock, err := a.exclusive(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	defer unlock()
	ids, err := a.filesOf(ctx, docID)
	if err != nil {
		return nil, ids, nil, err
	}
	done, unreadable, err = a.rekeyGroupFiles(ctx, ids)
	if err != nil {
		in := map[string]bool{}
		for _, d := range done {
			in[d] = true
		}
		for _, id := range ids {
			if !in[id] {
				stale = append(stale, id)
			}
		}
	}
	return done, stale, unreadable, err
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
