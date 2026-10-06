package encrypt

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"filippo.io/age"
	"golang.org/x/crypto/hkdf"
)

// TypePasskey is the Recipient.Type of a key derived from a passkey.
const TypePasskey = "passkey-prf"

// A passkey cannot hand out a private key (WebAuthn only signs), but with the PRF extension
// (hmac-secret) an authenticator answers a salt with 32 secret bytes that only it can compute.
// We turn those bytes into an age key: the same passkey and salt always give the same key, the
// secret never leaves the browser, and the public half is a normal age recipient.

// PRFSalt is the salt every passkey is asked with (the browser mixes it with a per-credential
// secret, so two passkeys never give the same bytes).
var PRFSalt = func() []byte { s := sha256.Sum256([]byte("gojiberry/encrypt/passkey/v1")); return s[:] }()

var prfInfo = []byte("gojiberry/encrypt/passkey/age-x25519/v1")

// ErrNeedSecondRecipient is returned when a document would be readable only through passkeys:
// a lost or reset passkey would lose the document.
var ErrNeedSecondRecipient = errors.New("encrypt: a passkey cannot be the only key: add a recovery key or an SSH key too")

// PRFIdentity derives the age identity from a passkey's PRF output (32 bytes). The derived key
// is the secret that opens the documents: the caller owns secret and must clear(secret) once
// it has the identity (the intermediate seed is cleared here). The identity itself lives as
// long as the caller keeps it; drop it when the unlocked state ends.
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

// PRFRecipient derives the recipient of a passkey from its PRF output. label says whose passkey
// it is; credentialID (base64url) is kept as the hint of which credential to ask when opening.
//
// Privacy: the hint is stored in the document's recipient list, which is readable by whoever
// can read the store (it is signed, not encrypted). The credential ID is not a secret and
// cannot sign anything without the authenticator, but it tells an observer that a passkey is
// in use, which credential, and links the documents that share it: leave credentialID empty to
// keep it out, at the cost of the browser asking the person to pick the passkey. As with
// PRFIdentity, the caller must clear(secret) afterwards.
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

// IsPasskey says whether a recipient is a passkey-derived key.
func (r Recipient) IsPasskey() bool { return r.Type == TypePasskey }

// CheckRecoverable returns ErrNeedSecondRecipient unless the keys given include at least one
// that is not a passkey, so that losing every passkey does not lose the document. It counts
// whatever it is given: if the list includes keys of people a document is shared with, one of
// theirs satisfies it, though the owner cannot recover with someone else's key (or count on it
// staying listed). For the owner's own recovery use CheckRecoverableOwner.
func CheckRecoverable(keys ...Recipient) error {
	for _, k := range keys {
		if !k.IsPasskey() {
			return nil
		}
	}
	return ErrNeedSecondRecipient
}

// CheckRecoverableOwner is CheckRecoverable for a document with an owner and people it is shared
// with: only the owner's keys count, because a recovery key must be one the owner controls (a
// recovery key kept offline, an SSH key), not the key of someone who may be removed or may
// lose theirs. The shared keys are accepted so a caller can pass both lists, and are not
// counted. Prefer it to CheckRecoverable.
func CheckRecoverableOwner(owner, shared []Recipient) error {
	_ = shared
	return CheckRecoverable(owner...)
}

// ageSecretKey encodes 32 bytes as age's secret key text: Bech32, upper case, HRP AGE-SECRET-KEY-.
func ageSecretKey(b []byte) string { return strings.ToUpper(bech32Encode("age-secret-key-", b)) }

// bech32Encode is Bech32 (BIP 173) of data under the human-readable part hrp, lower case.
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
