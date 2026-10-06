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
// recipient list. From frame version 3 the header also carries an Ed25519 signature by an
// owner Signer over the version, id, level, seq, recipient-list hash and a hash of the content
// that follows (Adapter.Signer signs, the owners' public keys, StoreKeyring.OwnerSigners,
// verify). Load checks the id, so a ciphertext copied over another id is refused
// (ErrWrongDocument), and checks the rest against the record (see Binding and ErrDowngrade).
//
// What the frame proves, precisely. age has no sender authentication: anyone who knows a
// recipient's PUBLIC key (an SSH key, the server's key) can mint a file that opens for that
// recipient, with any content and any frame. So an age file proves nothing about who wrote it;
// the signature does. A v3 frame whose signature is missing, is by no owner signer, or does not
// match the content is ErrForgedFrame (an ErrDowngrade-class failure) and returns no data, so
// a writer who knows only public keys can no longer forge a frame (a level, a seq, content) for
// a document the owner's signers wrote. What is NOT defended: a store writer can still roll an
// id, or the whole store, back to a genuine older consistent snapshot (files, records and
// index together): every signature in it is genuine, and only a client-side floor
// (SeqFloor) that outlives the rollback notices. Frames in the older formats (v1: no level;
// v2: level and seq, unsigned) are read as before, with a warning logged once, and can be
// forged by a holder of a public key until the first save or re-key of the file writes v3
// (Adapter.RequireSigned refuses them once everything was upgraded).
//
// Reading is strict by default: a stored file without the age header is refused
// (ErrNotEncrypted), so a store writer cannot replace a ciphertext with forged plaintext.
// AllowPlaintext is the explicit opt-out for migrating a store that was clear.
package encrypt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
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
	return strings.HasPrefix(id, "catalog:") || strings.HasPrefix(id, "acl:") || strings.HasPrefix(id, "fonts/") || strings.HasPrefix(id, KeysPrefix) || strings.HasPrefix(id, directoryPrefix) || strings.HasPrefix(id, docstore.DirectoryLogPrefix) || strings.HasPrefix(id, docstore.DirectoryEnrollPrefix) || strings.HasPrefix(id, docstore.RateLimitPrefix)
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
	// Signer is an owner's signing key (the same Signer that signs the keys:<ID> records): every
	// file the Adapter writes for a document with a record carries a frame signed with it (v3),
	// which is what lets a reader tell an owner's file from one minted by anyone who knows a
	// recipient's public key (ErrForgedFrame). Without it only documents below the end-to-end
	// level are written, with the older unsigned frame; a confidential document is not saved
	// (ErrNoFrameSigner), nor re-keyed.
	Signer *Signer
	// OwnerSigners are further public signing keys whose frames are accepted, besides the ones
	// the keyring lists (StoreKeyring.OwnerSigners) and Signer's own.
	OwnerSigners []ed25519.PublicKey
	// RequireSigned refuses a frame that is not signed (v1, v2) of any document id the keyring
	// binds (a Binder keyring: every id DocID finds, whether or not a keys:<ID> record exists
	// for it; the ids the keyring does not bind are not checked), as ErrForgedFrame, instead
	// of reading it with a warning. Turn it on once every file was
	// written or re-keyed by a signing client (Sharing.Repair of each document upgrades them): until
	// then an unsigned frame can be minted by anyone who knows a recipient's public key.
	RequireSigned bool

	warnOnce sync.Once
	// afterBind, when set, is called by Save and SaveIf between reading the record and checking
	// the files against it: the window in which another client can complete a switch (tests).
	afterBind func()
	// beforeBinding, when set, is called by Load between reading the file and the record (tests).
	beforeBinding func()

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
// signed record as a uvarint, the 32-byte hash of its recipient list, the data (unsigned).
// Version 3: magic "GJE\x03", the same header as v2, then the 32-byte SHA-256 of the data and a
// 64-byte Ed25519 signature over everything before it (frameSigMessage), then the data. The
// signature covers the content hash, so a signed header cannot be put on other content, and a
// reader that only needs the header (the checks that scan a document's files) can verify it
// without reading the rest. A v1 file is read as before and carries no level to cross-check;
// the first Save or re-key of it writes v3 (v2 when the adapter has no Signer and the
// document is not end to end), which pins the level from then on.
var (
	frameMagicV1 = []byte("GJE\x01")
	frameMagicV2 = []byte("GJE\x02")
	frameMagic   = []byte("GJE\x03")
)

const frameSigContext = "gojiberry/frame/v3"

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
	// Signed is the record's flag that every file of the document carries a signed frame; only
	// the record has it.
	Signed bool
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

// Hints appended to the findings, so the message says how the owner goes on: Recover when the
// record was lost or replaced (and the owner knows what it should say), Repair when the record is
// right and some file was left wrapped below its level by a switch that was cut short.
const (
	recoverHint  = " (if the record was restored on purpose, the owner can run Sharing.Recover to write a new one)"
	unsignedHint = " (if the owner knows this file is theirs, left by a client without the signing key or by an older build, Sharing.Repair with AcceptUnsigned seals it again, signed; a file that is not theirs must not be accepted: restore or delete it. Sharing.Recover keeps the record's claim and takes the same option)"
	repairHint   = " (if a switch of level was cut short, the owner can run Sharing.Repair to re-key the files; if the record was replaced on purpose, Sharing.Recover)"
)

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

// frameInfo says which frame version a file had and, from v2, the binding in it; for v3 also the
// content hash and signature it carries.
type frameInfo struct {
	version int
	b       Binding
	content [sha256.Size]byte
	sig     []byte
}

// frameSigMessage is what a v3 frame's signature covers.
func frameSigMessage(id string, level byte, seq uint64, recipients, content [sha256.Size]byte) []byte {
	out := append([]byte(frameSigContext), 0)
	out = binary.AppendUvarint(out, uint64(len(id)))
	out = append(out, id...)
	out = append(out, level)
	out = binary.AppendUvarint(out, seq)
	out = append(out, recipients[:]...)
	return append(out, content[:]...)
}

// frame writes the frame: v3, signed, when there is a signer, else v2.
func frame(id string, b Binding, data []byte, signer *Signer) []byte {
	out := make([]byte, 0, len(frameMagic)+binary.MaxVarintLen64*2+len(id)+1+2*sha256.Size+ed25519.SignatureSize+len(data))
	if signer == nil {
		out = append(out, frameMagicV2...)
	} else {
		out = append(out, frameMagic...)
	}
	out = binary.AppendUvarint(out, uint64(len(id)))
	out = append(out, id...)
	out = append(out, levelByte(b.Level))
	out = binary.AppendUvarint(out, b.Seq)
	out = append(out, b.Hash[:]...)
	if signer != nil {
		content := sha256.Sum256(data)
		out = append(out, content[:]...)
		out = append(out, ed25519.Sign(signer.key, frameSigMessage(id, levelByte(b.Level), b.Seq, b.Hash, content))...)
	}
	return append(out, data...)
}

// unframe checks the frame of the decrypted b against id and returns the data and what the
// frame said. It does not verify a v3 signature (Adapter.verifyFrame does). A prefix of a frame
// (the header alone) parses too: the data is then short.
func unframe(id string, b []byte) ([]byte, frameInfo, error) {
	var fi frameInfo
	switch {
	case bytes.HasPrefix(b, frameMagic):
		fi.version = 3
		b = b[len(frameMagic):]
	case bytes.HasPrefix(b, frameMagicV2):
		fi.version = 2
		b = b[len(frameMagicV2):]
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
	b = b[sha256.Size:]
	if fi.version == 2 {
		return b, fi, nil
	}
	if len(b) < sha256.Size+ed25519.SignatureSize {
		return nil, fi, ErrFormat
	}
	copy(fi.content[:], b[:sha256.Size])
	fi.sig = append([]byte(nil), b[sha256.Size:sha256.Size+ed25519.SignatureSize]...)
	return b[sha256.Size+ed25519.SignatureSize:], fi, nil
}

// ErrForgedFrame is returned when a file's frame is not what an owner signer wrote: a v3 frame
// whose signature is missing, is by none of the owners' signing keys, or does not match the
// content it came with (the header put on other content), or, with Adapter.RequireSigned, an
// unsigned frame. age gives no sender authentication, so a holder of a recipient's public key
// can mint a file that opens: this is what tells it from the owner's. It is an ErrDowngrade-class
// failure (errors.Is(err, docstore.ErrDowngrade)): nothing is read from the file or written
// over it.
var ErrForgedFrame error = forgedError{}

type forgedError struct{}

func (forgedError) Error() string {
	return "encrypt: the file's frame was not written by an owner of this document"
}
func (forgedError) Is(target error) bool { return target == docstore.ErrDowngrade }

func forged(id, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrForgedFrame, id, fmt.Sprintf(format, args...))
}

// ErrNoFrameSigner is returned by Save and the re-keys when a document would be sealed at the
// end-to-end level and the Adapter has no Signer: the frame of a confidential document must be
// signed (see ErrForgedFrame), so it is not saved unsigned.
var ErrNoFrameSigner = errors.New("encrypt: a confidential document is saved with the owner's signature: the Adapter has no Signer")

// ErrUnreadable is returned (wrapped, naming the file) when a stored file of a document exists
// and cannot be opened here for a reason other than being provably someone else's: it has the age
// header but is malformed, damaged, or opens with a key of ours and says something else. The
// checks that compare a document's files with its record cannot tell such a file from one that
// hides what the record should say, so the operation stops instead of skipping it: restore the
// file, or delete it (a version snapshot: saver.Saver.DropVersions; a generation "<ID>~..." is
// deleted in the store, there is no saver call for it).
//
// Which operations such a file blocks depends on where it is. A generation ("<ID>~<tag>.<ext>")
// or a confidential folder's "folder-secret/<ID>" is scanned by Save, SaveIf and every change to
// the list (Add, Remove, SetProtection: the check before the record is written), and by the
// re-keys. A version snapshot ("versions/<ID>/...") is NOT scanned by Save, SaveIf, Load or the
// check before a change to the list, but it IS opened by every re-key (all or none: Add, Remove,
// SetProtection and Repair finish by re-keying, so they write the record and then stop, leaving
// it pending) and by the scan for the highest seq (Recover). Load opens only the file it
// returns. A file that is provably someone else's (ErrNoKey) is skipped by the scans, but a re-key
// cannot seal it again and stops on it the same way.
var ErrUnreadable = errors.New("encrypt: a stored file of this document cannot be opened")

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
// A v1 frame has nothing to compare and passes, except in a document whose record says every
// file is signed (Binding.Signed), where any unsigned frame is ErrForgedFrame (the owner's way out is
// Sharing.Repair with AcceptUnsigned).
func crossCheck(id string, f frameInfo, r Binding, strict bool) error {
	if r.Exists && r.Signed && f.version < 3 {
		return forged(id, "its frame is not signed (v%d), but the document's record says every file of it is: it was not written by an owner"+unsignedHint, f.version)
	}
	if f.version < 2 {
		return nil
	}
	fb := f.b
	switch {
	case !r.Exists:
		if fb.Seq > 0 || fb.Level == ProtectionE2EE {
			return downgrade(id, "the file was sealed under recipient record %d (%s) and that record is missing: it was deleted"+recoverHint, fb.Seq, levelName(fb.Level))
		}
	case fb.Seq > r.Seq:
		return downgrade(id, "the file was sealed under recipient record %d (%s) but the record is %d (%s): an older signed record was put back"+recoverHint, fb.Seq, levelName(fb.Level), r.Seq, levelName(r.Level))
	case fb.Seq == r.Seq:
		if fb.Level != r.Level || fb.Hash != r.Hash {
			return downgrade(id, "the file and the record disagree about level or recipients at record %d"+recoverHint, r.Seq)
		}
	case strict && rank(fb.Level) < rank(r.Level) && !r.Pending:
		return downgrade(id, "the file is wrapped at the %s level but the document is %s and no switch is in progress"+repairHint, levelName(fb.Level), levelName(r.Level))
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
	return a.sealTo(id, rs, b, data)
}

// sealTo encrypts data to rs under the frame for binding b: signed when the adapter has a Signer.
// A confidential document is never sealed without one.
func (a *Adapter) sealTo(id string, rs []age.Recipient, b Binding, data []byte) ([]byte, error) {
	if len(rs) == 0 {
		return nil, ErrNoRecipients
	}
	if a.Signer == nil && (b.Level == ProtectionE2EE || b.Signed) {
		return nil, fmt.Errorf("%w: %s", ErrNoFrameSigner, id)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, rs...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(frame(id, b, data, a.Signer)); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// signerKeys is implemented by the keyrings that know the owners' signing keys.
type signerKeys interface{ ownerSignerKeys() []ed25519.PublicKey }

func (k StoreKeyring) ownerSignerKeys() []ed25519.PublicKey        { return k.OwnerSigners }
func (k ConfidentialKeyring) ownerSignerKeys() []ed25519.PublicKey { return k.People.OwnerSigners }

// frameVerifiers are the public keys a v3 frame may be signed by.
func (a *Adapter) frameVerifiers() []ed25519.PublicKey {
	out := append([]ed25519.PublicKey(nil), a.OwnerSigners...)
	if sk, ok := a.Ring.(signerKeys); ok {
		out = append(out, sk.ownerSignerKeys()...)
	}
	if a.Signer != nil {
		out = append(out, a.Signer.Public())
	}
	return out
}

// verifyFrame checks the signature of a frame of a document that has a record (a Binder keyring):
// a v3 frame must verify against the owners' signing keys, and, when full is set (data is the
// whole content, not just what followed the header), carry the hash of that content; an
// unsigned frame is read with a warning logged once, unless RequireSigned. Anything else is
// ErrForgedFrame.
//
// acceptUnsigned (the owner's explicit Repair option, see AcceptUnsigned) lets an unsigned frame
// pass without the warning and whatever RequireSigned says, so it can be read and sealed again;
// it never excuses a v3 frame whose signature is bad.
func (a *Adapter) verifyFrame(id string, fi frameInfo, data []byte, full, acceptUnsigned bool) error {
	if !a.binds(id) {
		return nil
	}
	if fi.version < 3 {
		if acceptUnsigned {
			return nil
		}
		if a.RequireSigned {
			return forged(id, "the frame is not signed (v%d) and this adapter requires signed frames", fi.version)
		}
		a.warnOnce.Do(func() {
			log.Printf("encrypt: %s has a frame without the owner's signature (v%d): anyone who knows a recipient's public key could have written it; saving or re-keying the document (Sharing.Repair) writes a signed one", id, fi.version)
		})
		return nil
	}
	msg := frameSigMessage(id, levelByte(fi.b.Level), fi.b.Seq, fi.b.Hash, fi.content)
	ok := false
	for _, k := range a.frameVerifiers() {
		if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, msg, fi.sig) {
			ok = true
			break
		}
	}
	if !ok {
		return forged(id, "its signature is not by any owner signing key")
	}
	if full && sha256.Sum256(data) != fi.content {
		return forged(id, "its content is not the content the owner signed")
	}
	return nil
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
	return a.openWith(ctx, id, stored, false)
}

// openWith is open, with acceptUnsigned as verifyFrame has it.
func (a *Adapter) openWith(ctx context.Context, id string, stored []byte, acceptUnsigned bool) (plain []byte, fi frameInfo, encrypted bool, err error) {
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
	if err == nil {
		err = a.verifyFrame(id, fi, plain, true, acceptUnsigned)
	}
	if err != nil {
		return nil, frameInfo{}, true, err
	}
	return plain, fi, true, nil
}

// peekFrame reads only the frame header of a stored file: age authenticates the first chunk
// of the stream before releasing it, which is all that is read here. The header's signature is
// verified (it covers the content hash), the content is not: what the header says is what an
// owner signed even if the file around it has been given other content, and the readers of the
// content (open) check the hash.
func (a *Adapter) peekFrame(ctx context.Context, id string, stored []byte) (frameInfo, error) {
	return a.peekFrameWith(ctx, id, stored, false)
}

func (a *Adapter) peekFrameWith(ctx context.Context, id string, stored []byte, acceptUnsigned bool) (frameInfo, error) {
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
	if err == nil {
		err = a.verifyFrame(id, fi, nil, false, acceptUnsigned)
	}
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

// checkDocument cross-checks the record binding r of the document id belongs to with what the
// document's stored files say (the rules of crossCheck without the strict one: this is the write
// side). It reads nothing for an id that is not a document's or for a keyring that is not a
// Binder. See checkFiles for which files count as evidence and which stop the operation.
func (a *Adapter) checkDocument(ctx context.Context, id string, r Binding) error {
	return a.checkFiles(ctx, DocID(id), []string{mainPrefix(id)}, r)
}

// checkDocumentByID is checkDocument for a caller that has the document's (or folder's) ID
// and not the id of one of its files: it looks at the document's generations and at a
// confidential folder's record.
func (a *Adapter) checkDocumentByID(ctx context.Context, docID string, r Binding) error {
	return a.checkFiles(ctx, docID, []string{docID, docstore.FolderSecretPrefix + docID}, r)
}

// checkFiles cross-checks r with EVERY stored file of the document, whatever order the store
// lists them in or times it reports (both are the store's to choose, so neither is evidence):
//
//   - a file that opens here and verifies (a v3 frame signed by an owner signer, or an older
//     unsigned one, v1 carrying no level) is evidence, and each is checked with crossCheck;
//   - a file that is provably someone else's (a well-formed age file none of our keys matches:
//     ErrNoKey) is skipped, and so is one that is gone (ErrNotFound);
//   - a file that is forged (ErrForgedFrame) fails the check with that error;
//   - any other file that cannot be opened (the age header but malformed or damaged, opening with
//     a key of ours but saying something else, not an age file at all unless AllowPlaintext) is
//     ErrUnreadable: it could be hiding what the record should say, so the operation stops. It
//     is never counted as "no evidence".
//
// A document none of whose files opens (all provably foreign, or none stored) has no evidence
// and passes: what the record says is then all there is.
func (a *Adapter) checkFiles(ctx context.Context, docID string, prefixes []string, r Binding) error {
	if docID == "" || !a.binds(docID) {
		return nil
	}
	seen := map[string]bool{}
	var files []string
	for _, prefix := range prefixes {
		metas, err := docstore.ListPrefix(ctx, a.Inner, prefix)
		if err != nil {
			return err
		}
		for _, m := range metas {
			if !seen[m.ID] && !a.skip(m.ID) && DocID(m.ID) == docID {
				seen[m.ID] = true
				files = append(files, m.ID)
			}
		}
	}
	sort.Strings(files)
	type evidence struct {
		id string
		fi frameInfo
	}
	var found []evidence
	for _, id := range files {
		d, err := a.Inner.Load(ctx, id)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		fi, err := a.peekFrame(ctx, id, d.Data)
		switch {
		case err == nil:
			found = append(found, evidence{id, fi})
		case errors.Is(err, ErrNoKey):
			// Provably someone else's: no evidence from this one.
		case errors.Is(err, ErrNotEncrypted) && a.AllowPlaintext:
			// A file the migration has not encrypted yet.
		case errors.Is(err, ErrForgedFrame):
			return err
		default:
			return fmt.Errorf("%w: %s: %w", ErrUnreadable, id, err)
		}
	}
	// The highest seq first: it is the one that names the record most precisely.
	sort.SliceStable(found, func(i, j int) bool { return found[i].fi.b.Seq > found[j].fi.b.Seq })
	for _, e := range found {
		if err := crossCheck(e.id, e.fi, r, false); err != nil {
			return err
		}
	}
	return nil
}

// bindChecked reads the record (the recipients and their Binding) and checks the document's
// files against it. A downgrade finding is made again once with the record read again: a client
// that finished a switch between the two reads leaves files sealed under a record newer than
// the one this read, which is not a downgrade; a record that is really missing or replaced is
// the same the second time.
func (a *Adapter) bindChecked(ctx context.Context, id string) ([]age.Recipient, Binding, error) {
	for attempt := 0; ; attempt++ {
		rs, b, err := a.bind(ctx, id)
		if err != nil {
			return nil, Binding{}, err
		}
		if a.afterBind != nil {
			a.afterBind()
		}
		err = a.checkDocument(ctx, id, b)
		if err == nil {
			return rs, b, nil
		}
		if attempt == 0 && errors.Is(err, ErrDowngrade) && !errors.Is(err, ErrForgedFrame) {
			continue
		}
		return nil, Binding{}, err
	}
}

// Save implements docstore.Adapter: the data is encrypted to the keyring's recipients.
//
// For a document with a record (a Binder keyring) the file written carries the record's level,
// seq and recipient hash, signed by Signer, and before it is written the document's stored
// files are cross-checked with the record the recipients came from: a record that is missing or
// older than a file says it was sealed under is ErrDowngrade, a file that cannot be opened and
// is not provably someone else's is ErrUnreadable, and nothing is written. A confidential
// document without a Signer is ErrNoFrameSigner.
func (a *Adapter) Save(ctx context.Context, id string, data []byte) error {
	if a.skip(id) {
		return a.Inner.Save(ctx, id, data)
	}
	if err := a.checkID(id); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rs, b, err := a.bindChecked(ctx, id)
	if err != nil {
		return err
	}
	ct, err := a.sealTo(id, rs, b, data)
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
	for attempt := 0; ; attempt++ {
		d, err := a.Inner.Load(ctx, id)
		if err != nil || a.skip(id) {
			return d, err
		}
		plain, fi, _, err := a.open(ctx, id, d.Data)
		if err != nil {
			return docstore.Document{}, err
		}
		if a.binds(id) {
			if a.beforeBinding != nil {
				a.beforeBinding()
			}
			r, err := a.Ring.(Binder).Binding(ctx, id)
			if err != nil {
				return docstore.Document{}, err
			}
			if err := crossCheck(id, fi, r, true); err != nil {
				// A switch completed between the read of the file and the record leaves the
				// file as it was and the record newer: read both again once. A real finding is
				// the same the second time.
				if attempt == 0 && errors.Is(err, ErrDowngrade) && !errors.Is(err, ErrForgedFrame) {
					continue
				}
				return docstore.Document{}, err
			}
		}
		d.Data = plain
		return d, nil
	}
}

// List implements docstore.Adapter. Sizes are of what is stored (the ciphertext).
func (a *Adapter) List(ctx context.Context) ([]docstore.Meta, error) { return a.Inner.List(ctx) }

// ListPrefix implements docstore.PrefixLister over Inner (ids are stored as they are).
func (a *Adapter) ListPrefix(ctx context.Context, prefix string) ([]docstore.Meta, error) {
	return docstore.ListPrefix(ctx, a.Inner, prefix)
}

// Delete implements docstore.Adapter.
func (a *Adapter) Delete(ctx context.Context, id string) error { return a.Inner.Delete(ctx, id) }

// DeleteIf implements docstore.ConditionalDeleter for the ids this adapter leaves in the clear
// (see DefaultSkip); an encrypted id is docstore.ErrConditionalUnsupported.
func (a *Adapter) DeleteIf(ctx context.Context, id string, prevDigest string) error {
	c, ok := a.Inner.(docstore.ConditionalDeleter)
	if !ok || !a.skip(id) {
		return docstore.ErrConditionalUnsupported
	}
	return c.DeleteIf(ctx, id, prevDigest)
}

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
	rs, b, err := a.bindChecked(ctx, id)
	if err != nil {
		return err
	}
	ct, err := a.sealTo(id, rs, b, data)
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
func (a *Adapter) plan(ctx context.Context, id string, rk rekeyOpts) (planned, error) {
	d, err := a.Inner.Load(ctx, id)
	if err != nil {
		return planned{}, err
	}
	plain, fi, _, err := a.openWith(ctx, id, d.Data, rk.acceptUnsigned)
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
		cb := b
		if rk.acceptUnsigned && fi.version < 3 {
			// The owner said to seal unsigned files again: the "every file is signed" claim of
			// the record is what such a file contradicts, and sealing it again is what makes it
			// true. Level, seq and recipients are still checked; a v3 frame is never excused.
			cb.Signed = false
		}
		if err := crossCheck(id, fi, cb, false); err != nil {
			return planned{}, err
		}
	}
	return planned{plain, docstore.Digest(d.Data), rs, b}, nil
}

// rekeyOne re-keys one file: read, open, seal to the current recipients, and write only if
// the file is still the one that was read (a Save in between is not lost: the file is read
// again). Without a conditional store the caller's lock is what serializes it.
func (a *Adapter) rekeyOne(ctx context.Context, id string, rk rekeyOpts) error {
	for range rekeyAttempts {
		p, err := a.plan(ctx, id, rk)
		if err != nil {
			return err
		}
		ct, err := a.sealTo(id, p.rs, p.b, p.plain)
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
	done, _, err := a.rekeyGroupFiles(ctx, ids, rekeyOpts{})
	return len(done), err
}

// rekeyGroupFiles is rekeyGroup that says which files it wrote and, when it wrote none because
// some could not be opened, which those were (unreadable).
func (a *Adapter) rekeyGroupFiles(ctx context.Context, ids []string, rk rekeyOpts) (written, unreadable []string, err error) {
	var errs []error
	var present []string
	for _, id := range ids {
		_, err := a.plan(ctx, id, rk)
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
		if err := a.rekeyOne(ctx, id, rk); err != nil {
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

// unsignedFiles lists the stored files of the document whose frame is not signed (v1, v2) or
// that are stored in the clear (AllowPlaintext): the ones a re-key by a signing adapter has still
// to seal. A file that cannot be opened (other than a foreign one, or one that is gone) is an
// error: nothing can be said about it. A v3 file with a bad signature is ErrForgedFrame.
func (a *Adapter) unsignedFiles(ctx context.Context, docID string) ([]string, error) {
	ids, err := a.filesOf(ctx, docID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, id := range ids {
		d, err := a.Inner.Load(ctx, id)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		fi, err := a.peekFrameWith(ctx, id, d.Data, true)
		switch {
		case err == nil:
			if fi.version < 3 {
				out = append(out, id)
			}
		case errors.Is(err, ErrNoKey):
		case errors.Is(err, ErrNotEncrypted) && a.AllowPlaintext:
			out = append(out, id)
		case errors.Is(err, ErrForgedFrame):
			return nil, err
		default:
			return nil, fmt.Errorf("%w: %s: %w", ErrUnreadable, id, err)
		}
	}
	return out, nil
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
	return a.rekeyDocumentReport(ctx, docID, rekeyOpts{})
}

// rekeyOpts changes what a re-key accepts.
type rekeyOpts struct {
	// acceptUnsigned: an unsigned (v1, v2) file is opened and sealed again with a signed frame
	// even where the record says every file is signed or the Adapter has RequireSigned (the
	// owner's AcceptUnsigned). A v3 file with a bad signature is still refused.
	acceptUnsigned bool
}

func (a *Adapter) rekeyDocumentReport(ctx context.Context, docID string, rk rekeyOpts) (done, stale, unreadable []string, err error) {
	unlock, err := a.exclusive(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	defer unlock()
	ids, err := a.filesOf(ctx, docID)
	if err != nil {
		return nil, ids, nil, err
	}
	done, unreadable, err = a.rekeyGroupFiles(ctx, ids, rk)
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
