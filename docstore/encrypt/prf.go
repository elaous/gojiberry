package encrypt

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"filippo.io/age"
	"golang.org/x/crypto/hkdf"
)

// TypePasskey is the Recipient.Type of a passkey-derived key.
const TypePasskey = "passkey-prf"

// A passkey cannot hand out a private key.

// PRFSalt is the salt every passkey is asked with.
var PRFSalt = func() []byte { s := sha256.Sum256([]byte("gojiberry/encrypt/passkey/v1")); return s[:] }()

var prfInfo = []byte("gojiberry/encrypt/passkey/age-x25519/v1")

// ErrNeedSecondRecipient is returned when a document would be unrecoverable.
var ErrNeedSecondRecipient = errors.New("encrypt: a passkey cannot be the only key: add a recovery key or an SSH key too")

// PRFIdentity derives the age identity from a passkey's PRF output.
func PRFIdentity(secret []byte) (*age.X25519Identity, error) {
	if len(secret) != 32 {
		return nil, fmt.Errorf("encrypt: a passkey PRF output is 32 bytes, got %d", len(secret))
	}
	seed := make([]byte, 32)
	defer clear(seed)
	if _, err := hkdf.New(sha256.New, secret, nil, prfInfo).Read(seed); err != nil {
		return nil, err
	}
	text := ageSecretKey(seed)
	return age.ParseX25519Identity(text)
}

// PRFRecipient derives a passkey's recipient from its PRF output.
func PRFRecipient(secret []byte, label, credentialID string) (Recipient, error) {
	id, err := PRFIdentity(secret)
	if err != nil {
		return Recipient{}, err
	}
	r, err := ParseAgeRecipient(id.Recipient().String())
	if err != nil {
		return Recipient{}, err
	}
	r.Label, r.Type, r.Hint = label, TypePasskey, credentialID
	return r, nil
}

// IsPasskey says whether a recipient is passkey-derived.
func (r Recipient) IsPasskey() bool { return r.Type == TypePasskey }

// CheckRecoverable returns ErrNeedSecondRecipient unless recoverable.
func CheckRecoverable(keys ...Recipient) error {
	for _, k := range keys {
		if !k.IsPasskey() {
			return nil
		}
	}
	return ErrNeedSecondRecipient
}

// CheckRecoverableOwner is CheckRecoverable for a document with owners.
func CheckRecoverableOwner(owner, shared []Recipient) error {
	_ = shared
	return CheckRecoverable(owner...)
}

// ageSecretKey encodes 32 bytes as age's secret key text.
func ageSecretKey(b []byte) string { return strings.ToUpper(bech32Encode("age-secret-key-", b)) }

// bech32Encode is Bech32 of data under a human-readable prefix.
func bech32Encode(hrp string, b []byte) string {
	const charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
	var data []byte
	acc, bits := 0, 0
	for _, v := range b {
		acc, bits = acc<<8|int(v), bits+8
		for bits >= 5 {
			bits -= 5
			data = append(data, byte(acc>>bits)&31)
		}
	}
	if bits > 0 {
		data = append(data, byte(acc<<(5-bits))&31)
	}
	polymod := func(vals []byte) uint32 {
		gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
		chk := uint32(1)
		for _, v := range vals {
			top := chk >> 25
			chk = chk&0x1ffffff<<5 ^ uint32(v)
			for i := range gen {
				if top>>i&1 == 1 {
					chk ^= gen[i]
				}
			}
		}
		return chk
	}
	var exp []byte
	for _, c := range []byte(hrp) {
		exp = append(exp, c>>5)
	}
	exp = append(exp, 0)
	for _, c := range []byte(hrp) {
		exp = append(exp, c&31)
	}
	sum := polymod(append(append(exp, data...), 0, 0, 0, 0, 0, 0)) ^ 1
	var sb strings.Builder
	sb.WriteString(hrp + "1")
	for _, v := range data {
		sb.WriteByte(charset[v])
	}
	for i := 0; i < 6; i++ {
		sb.WriteByte(charset[sum>>(5*(5-i))&31])
	}
	return sb.String()
}
