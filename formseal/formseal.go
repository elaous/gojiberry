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

type Props struct {
	Kind, Name, Label, Placeholder, Pattern string
	Required                                bool
	Options                                 []string
}

type Seal struct {
	Key, Sig string
}

var ErrNoKey = errors.New("formseal: the key cannot sign (Ed25519 is needed)")

const domain = "gojiberry-formlock-v1\n"

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
