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

// ErrStaleRecord is returned for a record older than seen.
var ErrStaleRecord = errors.New("encrypt: the recipient list is older than one already seen (a replayed record)")

// SeqFloor remembers, per document, the highest record seq seen.
type SeqFloor interface {
	// Get returns the highest sequence number recorded.
	Get(ctx context.Context, docID string) (uint64, error)
	// Raise records seq as seen; a lower value is ignored.
	Raise(ctx context.Context, docID string, seq uint64) error
}

// MemorySeqFloor is a SeqFloor in memory.
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

// ErrFingerprint is returned by Share when the fingerprint is wrong.
var ErrFingerprint = errors.New("encrypt: the fingerprint confirmed is not the one of this key")

// ErrUnsignedRecord is returned when a recipient lists unsigned records.
var ErrUnsignedRecord = errors.New("encrypt: the recipient list is not signed by an owner key")

// ErrBadSignature is returned when a recipient's signature is invalid.
var ErrBadSignature = errors.New("encrypt: the recipient list's signature is not valid")

// ErrNoSigner is returned by Sharing operations that need a Signer.
var ErrNoSigner = errors.New("encrypt: sharing needs the owner's signing key")

const recordContext = "gojiberry/keys-record/v1"

// Signer is the owner's key for signing recipient records.
type Signer struct{ key ed25519.PrivateKey }

// NewSigner makes a Signer from a 32-byte secret seed.
func NewSigner(seed []byte) (*Signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("encrypt: a signing seed is 32 bytes")
	}
	return &Signer{ed25519.NewKeyFromSeed(seed)}, nil
}

// SignerFromAge derives the signing key of an age identity.
func SignerFromAge(id *age.X25519Identity) *Signer {
	m := hmac.New(sha256.New, []byte(id.String()))
	m.Write([]byte("gojiberry/keys-record-signer/v1"))
	s, _ := NewSigner(m.Sum(nil))
	return s
}

// SignerFromSSH uses an ed25519 SSH private key to sign.
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

// Public is the key that verifies this Signer's signatures.
func (s *Signer) Public() ed25519.PublicKey { return s.key.Public().(ed25519.PublicKey) }

const messageContext = "gojiberry/signed-message/v1"

// Sign signs msg with the Signer's key.
func (s *Signer) Sign(msg []byte) []byte {
	return ed25519.Sign(s.key, append([]byte(messageContext+"\x00"), msg...))
}

// VerifySigned checks a signature made by a Signer.
func VerifySigned(pub ed25519.PublicKey, msg, sig []byte) bool {
	return len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, append([]byte(messageContext+"\x00"), msg...), sig)
}

func recordMessage(docID string, payload []byte) []byte {
	return append([]byte(recordContext+"\x00"+docID+"\x00"), payload...)
}
