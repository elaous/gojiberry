package encrypt

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// DirectoryKeyEnv names the environment variable that holds the directory's key material: a
// 32-byte seed in hex (64 characters). Without it the directory is off.
const DirectoryKeyEnv = "GOJIBERRY_DOCSTORE_DIRECTORY_KEY"

// DirectorySigner is the server secret behind the key directory: it signs the records the
// publish route writes, checks them when they are read, and makes the keyed hash of an e-mail
// address that names a record (so the names do not let anyone list or guess addresses). It is
// injected so a deployment can keep the key in a KMS; DirectorySignerFromSeed is the in-process
// one. The same key material must be used for as long as the records are to be believed:
// changing it makes every record unverified and unfindable.
type DirectorySigner interface {
	// Sign returns a signature over msg that Verify accepts.
	Sign(msg []byte) []byte
	// Verify says sig is Sign(msg) of this signer's key.
	Verify(msg, sig []byte) bool
	// Pseudonym is a keyed hash (hex) of data, stable for this key and not computable without it.
	Pseudonym(data string) string
}

// ErrDirectoryOff is returned by AdapterDirectory.Publish when it has no signer: the directory
// is off until the server is given a key.
var ErrDirectoryOff = errors.New("encrypt: the key directory is off: no directory key is configured")

type seedSigner struct {
	priv ed25519.PrivateKey
	mac  []byte
}

// DirectorySignerFromSeed makes a DirectorySigner from a 32-byte seed: an Ed25519 key signs,
// and an HMAC-SHA256 key derived from the same seed (a different, domain-separated key) names
// the records.
func DirectorySignerFromSeed(seed []byte) (DirectorySigner, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("encrypt: the directory key must be %d bytes (%d hex characters)", ed25519.SeedSize, 2*ed25519.SeedSize)
	}
	mac := sha256.Sum256(append([]byte("gojiberry/directory/email-hmac/v1\x00"), seed...))
	return &seedSigner{priv: ed25519.NewKeyFromSeed(seed), mac: mac[:]}, nil
}

// DirectorySignerFromHex is DirectorySignerFromSeed for a hex seed, as the environment has it.
func DirectorySignerFromHex(s string) (DirectorySigner, error) {
	seed, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, errors.New("encrypt: the directory key is not hex")
	}
	return DirectorySignerFromSeed(seed)
}

// DirectorySignerFromEnv reads GOJIBERRY_DOCSTORE_DIRECTORY_KEY. It returns (nil, nil) when the
// variable is unset: the directory feature is off. A key per process is deliberately not
// generated: records must verify after a restart.
func DirectorySignerFromEnv() (DirectorySigner, error) {
	v := os.Getenv(DirectoryKeyEnv)
	if strings.TrimSpace(v) == "" {
		return nil, nil
	}
	return DirectorySignerFromHex(v)
}

func (s *seedSigner) Sign(msg []byte) []byte { return ed25519.Sign(s.priv, msg) }

func (s *seedSigner) Verify(msg, sig []byte) bool {
	return len(sig) == ed25519.SignatureSize && ed25519.Verify(s.priv.Public().(ed25519.PublicKey), msg, sig)
}

func (s *seedSigner) Pseudonym(data string) string {
	h := hmac.New(sha256.New, s.mac)
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}
