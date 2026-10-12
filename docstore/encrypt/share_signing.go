package encrypt

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"filippo.io/age"
	"fmt"
	"golang.org/x/crypto/ssh"
	"sync"
)

// ErrStaleRecord is returned when a document's record is older than the highest sequence
// number the SeqFloor has seen for it (an older signed record was put back), or is missing
// where one was seen. It is not used, because it decides who a document is encrypted to and
// at what level. ErrDowngrade is the same finding made from the files themselves, with no
// client state.
var ErrStaleRecord = errors.New("encrypt: the recipient list is older than one already seen (a replayed record)")

// SeqFloor remembers, per document, the highest record sequence number seen, so that an older
// validly signed record that a store writer put back is refused (ErrStaleRecord). It is
// optional (nil: none, a replay is then not detected on read) and must live somewhere the
// store's writers cannot reach, or it defends nothing: memory of the client, or the owner's
// own device storage, not the store itself.
type SeqFloor interface {
	// Get returns the highest sequence number raised for the document, 0 if none.
	Get(ctx context.Context, docID string) (uint64, error)
	// Raise records seq as seen; a lower value than the one held changes nothing.
	Raise(ctx context.Context, docID string, seq uint64) error
}

// MemorySeqFloor is a SeqFloor in memory: it lasts as long as the process (or the page), so
// it protects a long-lived client, and a fresh one starts with no floor.
type MemorySeqFloor struct {
	mu sync.Mutex
	m  map[string]uint64
}

// Get implements SeqFloor.
func (f *MemorySeqFloor) Get(_ context.Context, docID string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m[docID], nil
}

// Raise implements SeqFloor.
func (f *MemorySeqFloor) Raise(_ context.Context, docID string, seq uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]uint64{}
	}
	if seq > f.m[docID] {
		f.m[docID] = seq
	}
	return nil
}

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

// messageContext separates what Sign signs from the lists the owner signs for documents.
const messageContext = "gojiberry/signed-message/v1"

// Sign signs msg (a statement of the host's own, for instance "this is the owner list of my
// store") under a context of its own, so a signature made here is never a signed recipient list.
func (s *Signer) Sign(msg []byte) []byte {
	return ed25519.Sign(s.key, append([]byte(messageContext+"\x00"), msg...))
}

// VerifySigned checks a signature made by Signer.Sign against its public key.
func VerifySigned(pub ed25519.PublicKey, msg, sig []byte) bool {
	return len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, append([]byte(messageContext+"\x00"), msg...), sig)
}

func recordMessage(docID string, payload []byte) []byte {
	return append([]byte(recordContext+"\x00"+docID+"\x00"), payload...)
}
