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

// DirectoryKeyEnv names the environment variable with the directory key.
const DirectoryKeyEnv = "GOJIBERRY_DOCSTORE_DIRECTORY_KEY"

// DirectoryKeyPrevEnv names the environment variable with the previous key.
const DirectoryKeyPrevEnv = "GOJIBERRY_DOCSTORE_DIRECTORY_KEY_PREV"

// DirectorySigner is the server secret behind the directory.
type DirectorySigner interface {
	// KeyID names the current signing key.
	KeyID() string
	// Sign returns a signature by the current key.
	Sign(msg []byte) []byte
	// Verify says sig is a signature over msg.
	Verify(kid string, msg, sig []byte) bool
	// Pseudonym is a keyed hash of data.
	Pseudonym(data string) string
	// Pseudonyms is Pseudonym under every key.
	Pseudonyms(data string) []string
}

// PublicKeyer is implemented by a DirectorySigner.
type PublicKeyer interface {
	PublicKeys() map[string]ed25519.PublicKey
}

// ErrDirectoryOff is returned by AdapterDirectory when the directory is off.
var ErrDirectoryOff = errors.New("encrypt: the key directory is off: no directory key is configured")

type seedKey struct {
	priv ed25519.PrivateKey
	mac  []byte
	kid  string
}

func newSeedKey(seed []byte) (seedKey, error) {
	if len(seed) != ed25519.SeedSize {
		return seedKey{}, fmt.Errorf("encrypt: the directory key must be %d bytes (%d hex characters)", ed25519.SeedSize, 2*ed25519.SeedSize)
	}
	mac := sha256.Sum256(append([]byte("gojiberry/directory/email-hmac/v1\x00"), seed...))
	priv := ed25519.NewKeyFromSeed(seed)
	sum := sha256.Sum256(priv.Public().(ed25519.PublicKey))
	return seedKey{priv: priv, mac: mac[:], kid: hex.EncodeToString(sum[:8])}, nil
}

func (k seedKey) pseudonym(data string) string {
	h := hmac.New(sha256.New, k.mac)
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

type seedSigner struct {
	cur  seedKey
	prev []seedKey
}

// DirectorySignerFromSeed makes a DirectorySigner from a seed.
func DirectorySignerFromSeed(seed []byte, prev ...[]byte) (DirectorySigner, error) {
	cur, err := newSeedKey(seed)
	if err != nil {
		return nil, err
	}
	s := &seedSigner{cur: cur}
	seen := map[string]bool{cur.kid: true}
	for _, p := range prev {
		k, err := newSeedKey(p)
		if err != nil {
			return nil, fmt.Errorf("a retired key: %w", err)
		}
		if !seen[k.kid] {
			seen[k.kid] = true
			s.prev = append(s.prev, k)
		}
	}
	return s, nil
}

func decodeSeed(s string) ([]byte, error) {
	seed, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, errors.New("encrypt: the directory key is not hex")
	}
	return seed, nil
}

// DirectorySignerFromHex is DirectorySignerFromSeed for hex text.
func DirectorySignerFromHex(s string, prev ...string) (DirectorySigner, error) {
	seed, err := decodeSeed(s)
	if err != nil {
		return nil, err
	}
	var olds [][]byte
	for _, list := range prev {
		for _, p := range strings.Split(list, ",") {
			if strings.TrimSpace(p) == "" {
				continue
			}
			o, err := decodeSeed(p)
			if err != nil {
				return nil, fmt.Errorf("a retired key: %w", err)
			}
			olds = append(olds, o)
		}
	}
	return DirectorySignerFromSeed(seed, olds...)
}

// DirectorySignerFromEnv reads the directory key from the environment.
func DirectorySignerFromEnv() (DirectorySigner, error) {
	v := os.Getenv(DirectoryKeyEnv)
	if strings.TrimSpace(v) == "" {
		return nil, nil
	}
	return DirectorySignerFromHex(v, os.Getenv(DirectoryKeyPrevEnv))
}

func (s *seedSigner) KeyID() string { return s.cur.kid }

func (s *seedSigner) Sign(msg []byte) []byte { return ed25519.Sign(s.cur.priv, msg) }

func (s *seedSigner) Verify(kid string, msg, sig []byte) bool {
	if len(sig) != ed25519.SignatureSize {
		return false
	}
	for _, k := range append([]seedKey{s.cur}, s.prev...) {
		if (kid == "" || kid == k.kid) && ed25519.Verify(k.priv.Public().(ed25519.PublicKey), msg, sig) {
			return true
		}
	}
	return false
}

func (s *seedSigner) Pseudonym(data string) string { return s.cur.pseudonym(data) }

func (s *seedSigner) Pseudonyms(data string) []string {
	out := []string{s.cur.pseudonym(data)}
	for _, k := range s.prev {
		out = append(out, k.pseudonym(data))
	}
	return out
}

// PublicKeys implements PublicKeyer.
func (s *seedSigner) PublicKeys() map[string]ed25519.PublicKey {
	m := map[string]ed25519.PublicKey{s.cur.kid: s.cur.priv.Public().(ed25519.PublicKey)}
	for _, k := range s.prev {
		m[k.kid] = k.priv.Public().(ed25519.PublicKey)
	}
	return m
}
