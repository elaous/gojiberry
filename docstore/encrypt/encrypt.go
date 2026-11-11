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

// ErrNoKey is returned when no key can open a document.
var ErrNoKey error = noKeyError{}

type noKeyError struct{}

func (noKeyError) Error() string { return "encrypt: none of your keys opens this document" }

func (noKeyError) Is(target error) bool { return target == docstore.ErrLocked }

// ErrNotEncrypted is returned by Load for a plaintext document.
var ErrNotEncrypted = errors.New("encrypt: the stored document is not encrypted")

// ErrWrongDocument is returned when a ciphertext opens but belongs elsewhere.
var ErrWrongDocument = errors.New("encrypt: the stored file belongs to another document")

// ErrFormat is returned when decrypted data has no valid format.
var ErrFormat = errors.New("encrypt: the decrypted data has an unknown format")

// ErrReservedID is returned by Save for a reserved id.
var ErrReservedID = errors.New("encrypt: this id is in a reserved keyspace")

// ErrNoRecipients is returned by Save with no recipients.
var ErrNoRecipients = errors.New("encrypt: no recipients")

// Keyring says who can read a document.
type Keyring interface {
	// Recipients are the keys a document is encrypted to.
	Recipients(ctx context.Context, id string) ([]age.Recipient, error)
	// Identities are the private keys that may open it.
	Identities(ctx context.Context, id string) ([]age.Identity, error)
}

type Static struct {
	To   []age.Recipient
	Keys []age.Identity
}

// Recipients implements Keyring.
func (s Static) Recipients(context.Context, string) ([]age.Recipient, error) { return s.To, nil }

// Identities implements Keyring.
func (s Static) Identities(context.Context, string) ([]age.Identity, error) { return s.Keys, nil }

// DefaultSkip lists items stored unencrypted.
func DefaultSkip(id string) bool {
	lower := strings.ToLower(id)
	for _, p := range defaultSkipPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

var defaultSkipPrefixes = []string{"catalog:", "acl:", "fonts/", strings.ToLower(KeysPrefix), strings.ToLower(directoryPrefix),
	strings.ToLower(docstore.DirectoryLogPrefix), strings.ToLower(docstore.DirectoryEnrollPrefix), strings.ToLower(docstore.RateLimitPrefix), strings.ToLower(RevokedSignerPrefix)}

// Adapter is a docstore.Adapter that encrypts what it saves.
type Adapter struct {
	Inner docstore.Adapter
	Ring  Keyring
	// RekeyProgress, if set, is called by RekeyAll after each document.
	RekeyProgress func(done, total int)
	// Skip names ids stored unencrypted; nil uses DefaultSkip.
	Skip func(id string) bool
	// AllowPlaintext lets Load return a document stored in the clear.
	AllowPlaintext bool
	// Signer is the owner's signing key for frames.
	Signer *Signer
	// OwnerSigners are further public signing keys trusted for frames.
	OwnerSigners []ed25519.PublicKey
	// RequireSigned refuses unsigned frames.
	RequireSigned bool
	// TrustFrames makes the Adapter accept every frame as it is.
	TrustFrames bool

	warnOnce      sync.Once
	afterBind     func()
	beforeBinding func()

	// serialises Save with re-keys, which hold it from read to write
	mu sync.Mutex
}

// New wraps inner; it is secure by default.
func New(inner docstore.Adapter, ring Keyring) *Adapter { return &Adapter{Inner: inner, Ring: ring} }

func (a *Adapter) skip(id string) bool {
	if a.Skip != nil {
		return a.Skip(id)
	}
	return DefaultSkip(id)
}

// Binding ties a stored file to the signed key record.
type Binding struct {
	Exists bool
	// Level is "" (server) or ProtectionE2EE.
	Level string
	// Seq is the record's sequence number; 0 for unrecorded documents.
	Seq uint64
	// Pending flags a level switch or recipient change in progress.
	Pending bool
	Hash    [sha256.Size]byte
	// Signed flags that every file of the document is signed.
	Signed bool
}

type Binder interface {
	Keyring
	// Bind is Recipients and the record's Binding, read once.
	Bind(ctx context.Context, id string) ([]age.Recipient, Binding, error)
	// Binding is the record's Binding alone.
	Binding(ctx context.Context, id string) (Binding, error)
}

// ErrUnreadable is returned when a stored file cannot be read.
var ErrUnreadable = errors.New("encrypt: a stored file of this document cannot be opened")

var reservedPrefixes = []string{"catalog:", "acl:", "fonts/"}

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

func (a *Adapter) bind(ctx context.Context, id string) ([]age.Recipient, Binding, error) {
	if b, ok := a.Ring.(Binder); ok && DocID(id) != "" {
		rs, bd, err := b.Bind(ctx, id)
		return rs, bd, keyringNotFound(err)
	}
	rs, err := a.Ring.Recipients(ctx, id)
	return rs, Binding{}, keyringNotFound(err)
}

type keyringErr struct{ msg string }

func (e keyringErr) Error() string { return e.msg }

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

// a confidential document is never sealed unsigned
func (a *Adapter) sealTo(id string, rs []age.Recipient, b Binding, data []byte) ([]byte, error) {
	if len(rs) == 0 {
		return nil, ErrNoRecipients
	}
	if a.Signer == nil && (b.Level == ProtectionE2EE || b.Signed) {
		return nil, fmt.Errorf("%w: %s", ErrNoFrameSigner, id)
	}
	if a.Signer == nil && a.RequireSigned && a.binds(id) {
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

type signerKeys interface{ ownerSignerKeys() []ed25519.PublicKey }

func (k StoreKeyring) ownerSignerKeys() []ed25519.PublicKey { return k.OwnerSigners }

func (k ConfidentialKeyring) ownerSignerKeys() []ed25519.PublicKey { return k.People.OwnerSigners }

// DocID finds the document a stored item belongs to.
func DocID(id string) string {
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
