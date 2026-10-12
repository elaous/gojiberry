package speechcoach

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
)

// The dataset is the person's voice, so where it is kept it is sealed: AES-256-GCM under a key the caller
// derives from a secret the person holds (the unlocked encrypted store's owner key, or a key the browser
// keeps non-extractable). Without that key the bytes are of no use, and a changed byte is refused.

var sealMagic = []byte("GJSC1")

// ErrSealed is returned when sealed bytes cannot be opened (wrong key, changed, or not sealed by Seal).
var ErrSealed = errors.New("speechcoach: the sealed data cannot be opened")

// SealKey turns any secret into the 32-byte key for a named purpose ("entry", "index").
func SealKey(secret []byte, purpose string) ([]byte, error) {
	if len(secret) == 0 {
		return nil, errors.New("speechcoach: no secret to seal with")
	}
	h := sha256.New()
	h.Write([]byte("gojiberry/speechcoach/seal/v1\x00" + purpose + "\x00"))
	h.Write(secret)
	return h.Sum(nil), nil
}

// Seal encrypts data under key (32 bytes) with a fresh nonce; name is bound in as additional data, so a
// sealed recording cannot be moved to another name unnoticed.
func Seal(key []byte, name string, data []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append(append([]byte{}, sealMagic...), nonce...)
	return g.Seal(out, nonce, data, []byte(name)), nil
}

// Open decrypts what Seal wrote under the same key and name.
func Open(key []byte, name string, sealed []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	n := len(sealMagic) + g.NonceSize()
	if len(sealed) < n+g.Overhead() || string(sealed[:len(sealMagic)]) != string(sealMagic) {
		return nil, ErrSealed
	}
	out, err := g.Open(nil, sealed[len(sealMagic):n], sealed[n:], []byte(name))
	if err != nil {
		return nil, ErrSealed
	}
	return out, nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("speechcoach: the key must be 32 bytes")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}
