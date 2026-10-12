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
	"errors"
	"filippo.io/age"
	"fmt"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"strings"
	"sync"
)

// ErrNoKey is returned when a document is encrypted and none of the keyring's identities
// can open it.
var ErrNoKey error = noKeyError{}

// noKeyError is ErrNoKey, which is also docstore.ErrLocked (errors.Is), so code that does not
// import this package (the saver's catalog rebuild) can tell a document it cannot open from a
// store that failed.
type noKeyError struct{}

func (noKeyError) Error() string { return "encrypt: none of your keys opens this document" }

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
	// everyone it is shared with). A keyring with no record for id yet returns nil or empty
	// (a save then fails with ErrNoRecipients), not an error.
	//
	// docstore.ErrNotFound (or an error wrapping it) from any method of the keyring (Recipients,
	// Identities, and Bind and Binding of a Binder) is never read as "the stored file is gone":
	// the adapter turns it into an ordinary keyring error (the text kept, the chain cut), so a
	// re-key fails with it and RepairAll skips the item (SkipError) and counts it in Left. Only
	// a stored file that is really missing is "gone".
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
	// Compared without case, like docstore's reserved ids (isReservedDocID) and IsDocumentKey: on
	// a backend that folds case "Directory/x" and "directory/x" are one record, and the one the
	// directory wrote in the clear must stay readable under either spelling.
	lower := strings.ToLower(id)
	for _, p := range defaultSkipPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// defaultSkipPrefixes are DefaultSkip's keyspaces, lower-cased.
var defaultSkipPrefixes = []string{"catalog:", "acl:", "fonts/", strings.ToLower(KeysPrefix), strings.ToLower(directoryPrefix),
	strings.ToLower(docstore.DirectoryLogPrefix), strings.ToLower(docstore.DirectoryEnrollPrefix), strings.ToLower(docstore.RateLimitPrefix)}

// Adapter is a docstore.Adapter that encrypts what it saves in Inner.
type Adapter struct {
	Inner docstore.Adapter
	Ring  Keyring
	// RekeyProgress, if set, is called by RekeyAll after each document (its files together) with how
	// many documents are done and how many there are, so a caller can show progress. It runs in the
	// goroutine of RekeyAll, under its lock: it must not call back into the Adapter.
	RekeyProgress func(done, total int)
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
	// TrustFrames makes the Adapter accept every frame as it is: no signature is checked (nor
	// required, whatever RequireSigned says). It is for a host that rebuilds a store whose list of
	// owner signing keys is lost and whose owner decided to take the files as theirs (the showcase's
	// restore): every file is then written again with a signed frame of the owner's key. Never set
	// it on an Adapter that serves reads.
	TrustFrames bool

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
//
// An error from the keyring is never docstore.ErrNotFound to the callers (see keyringNotFound):
// the re-key and RepairAll read that as "the file is gone". The other keyring calls (Identities
// in decrypt, Binding in Load) apply the same conversion at their own call sites.
func (a *Adapter) bind(ctx context.Context, id string) ([]age.Recipient, Binding, error) {
	if b, ok := a.Ring.(Binder); ok && DocID(id) != "" {
		rs, bd, err := b.Bind(ctx, id)
		return rs, bd, keyringNotFound(err)
	}
	rs, err := a.Ring.Recipients(ctx, id)
	return rs, Binding{}, keyringNotFound(err)
}

// keyringErr is a keyring's docstore.ErrNotFound with the chain cut: it says what the keyring
// said, but errors.Is(err, docstore.ErrNotFound) is false, as that means "the stored file is
// gone" to a re-key and to RepairAll, and a missing keyring record is not that.
type keyringErr struct{ msg string }

func (e keyringErr) Error() string { return e.msg }

// keyringNotFound returns err, or, when it is (or wraps) docstore.ErrNotFound, a keyringErr.
func keyringNotFound(err error) error {
	if errors.Is(err, docstore.ErrNotFound) {
		return keyringErr{"encrypt: keyring: " + err.Error()}
	}
	return err
}

func (a *Adapter) binds(id string) bool {
	_, ok := a.Ring.(Binder)
	return ok && DocID(id) != ""
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
	if a.Signer == nil && a.RequireSigned && a.binds(id) {
		// This adapter refuses to read an unsigned frame of this document, so writing one would
		// only make a file it cannot open, and one that could enter the window before the record
		// says Signed. Fail closed: the host that set RequireSigned has the signer.
		return nil, fmt.Errorf("%w: %s: this Adapter has RequireSigned and cannot sign, so it does not write an unsigned frame", ErrNoFrameSigner, id)
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

func (k StoreKeyring) ownerSignerKeys() []ed25519.PublicKey { return k.OwnerSigners }

func (k ConfidentialKeyring) ownerSignerKeys() []ed25519.PublicKey { return k.People.OwnerSigners }

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
