package speechcoach

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
)

var sealMagic = []byte("GJSC1")

var ErrSealed = errors.New("speechcoach: the sealed data cannot be opened")

func SealKey(secret []byte, purpose string) ([]byte, error) {
	if len(secret) == 0 {
		return nil, errors.New("speechcoach: no secret to seal with")
	}
	h := sha256.New()
	h.Write([]byte("gojiberry/speechcoach/seal/v1\x00" + purpose + "\x00"))
	h.Write(secret)
	return h.Sum(nil), nil
}

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
