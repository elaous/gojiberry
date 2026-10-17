// Package formseal makes the lock of a form field something a key stands behind. A field that is locked cannot have its
// properties changed in the editor; the seal adds a signature, made with a key only its owner holds, over those
// properties, so that a reader can tell who locked it and whether the field is still as it was locked. Unlocking, and
// locking again, needs that key.
//
// The seal covers what the field is (kind, name, label, placeholder, pattern, required, the choices) and not what is
// written in it: a locked field is still filled in. A seal does not stop somebody who can edit the file from taking the
// lock off; it makes the lock something a person cannot forge, and a change to a locked field something that shows.
package formseal

import (
	"crypto"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
)

// Props are the properties of a field that a seal covers.
type Props struct {
	Kind, Name, Label, Placeholder, Pattern string
	Required                                bool
	Options                                 []string
}

// Seal is a signature over the properties and the public key it was made with, both as base64.
type Seal struct {
	Key, Sig string
}

// ErrNoKey is returned for a signer that is not an Ed25519 key.
var ErrNoKey = errors.New("formseal: the key cannot sign (Ed25519 is needed)")

const domain = "gojiberry-formlock-v1\n"

// message is the bytes that are signed: a label of what they are, then each property with its length in front, so that
// no two different sets of properties give the same bytes.
func message(p Props) []byte {
	var b []byte
	add := func(s string) {
		b = binary.BigEndian.AppendUint32(b, uint32(len(s)))
		b = append(b, s...)
	}
	b = append(b, domain...)
	add(p.Kind)
	add(p.Name)
	add(p.Label)
	add(p.Placeholder)
	add(p.Pattern)
	if p.Required {
		add("1")
	} else {
		add("0")
	}
	b = binary.BigEndian.AppendUint32(b, uint32(len(p.Options)))
	for _, o := range p.Options {
		add(o)
	}
	return b
}

// Make seals the properties with the signer.
func Make(s crypto.Signer, p Props) (Seal, error) {
	pub, ok := s.Public().(ed25519.PublicKey)
	if !ok {
		return Seal{}, ErrNoKey
	}
	sig, err := s.Sign(nil, message(p), crypto.Hash(0))
	if err != nil {
		return Seal{}, err
	}
	return Seal{Key: base64.StdEncoding.EncodeToString(pub), Sig: base64.StdEncoding.EncodeToString(sig)}, nil
}

// Verify says the seal was made with its key over exactly these properties.
func Verify(sl Seal, p Props) bool {
	pub, err := base64.StdEncoding.DecodeString(sl.Key)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(sl.Sig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, message(p), sig)
}

// Holds says the signer is the key of the seal.
func Holds(s crypto.Signer, sl Seal) bool {
	if s == nil {
		return false
	}
	pub, ok := s.Public().(ed25519.PublicKey)
	return ok && base64.StdEncoding.EncodeToString(pub) == sl.Key
}

// Fingerprint is a short form of the key to show a person and to compare: the first 8 bytes of its SHA-256, in hex
// groups of four ("a1b2-c3d4-e5f6-0718"). "" for a key that is not a key.
func Fingerprint(key string) string {
	pub, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return ""
	}
	sum := sha256.Sum256(pub)
	const hex = "0123456789abcdef"
	var sb strings.Builder
	for i, c := range sum[:8] {
		if i > 0 && i%2 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(hex[c>>4])
		sb.WriteByte(hex[c&15])
	}
	return sb.String()
}
