// Package webauthntest is a software authenticator: it answers a WebAuthn registration and
// sign-in the way a browser's navigator.credentials.create and .get would, so the server side
// can be tested end to end without a browser (the browser side is tested with Chrome's virtual
// authenticator). It makes "none" attestation and ES256 keys, which is what the server verifies
// most often, and can be told to skip user verification or to misbehave.
package webauthntest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"

	"github.com/fxamacker/cbor/v2"
)

// Authenticator holds one credential.
type Authenticator struct {
	RPID string
	// UserVerified says whether the authenticator checked the person (a PIN, a fingerprint).
	UserVerified bool
	// Counter is the signature counter; each assertion raises it, unless Frozen.
	Counter uint32
	Frozen  bool

	key    *ecdsa.PrivateKey
	credID []byte
	handle []byte // the user handle the server gave at registration
}

// New makes an authenticator for a relying party ID.
func New(rpID string) *Authenticator {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id := make([]byte, 32)
	_, _ = rand.Read(id)
	return &Authenticator{RPID: rpID, UserVerified: true, key: k, credID: id}
}

// CredentialID is the credential's ID, base64url.
func (a *Authenticator) CredentialID() string { return b64(a.credID) }

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func unb64(s string) []byte {
	b, _ := base64.RawURLEncoding.DecodeString(s)
	return b
}

type creationOptions struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		RP        struct {
			ID string `json:"id"`
		} `json:"rp"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		AuthenticatorSelection struct {
			UserVerification string `json:"userVerification"`
			ResidentKey      string `json:"residentKey"`
		} `json:"authenticatorSelection"`
	} `json:"publicKey"`
}

type requestOptions struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		RPID      string `json:"rpId"`
	} `json:"publicKey"`
}

func (a *Authenticator) flags(attested bool) byte {
	f := byte(0x01) // user present
	if a.UserVerified {
		f |= 0x04
	}
	if attested {
		f |= 0x40
	}
	return f
}

func (a *Authenticator) authData(attested bool) []byte {
	h := sha256.Sum256([]byte(a.RPID))
	d := append([]byte(nil), h[:]...)
	d = append(d, a.flags(attested))
	d = binary.BigEndian.AppendUint32(d, a.Counter)
	if !attested {
		return d
	}
	d = append(d, make([]byte, 16)...) // AAGUID: none
	d = binary.BigEndian.AppendUint16(d, uint16(len(a.credID)))
	d = append(d, a.credID...)
	x, y := make([]byte, 32), make([]byte, 32)
	a.key.PublicKey.X.FillBytes(x)
	a.key.PublicKey.Y.FillBytes(y)
	cose, _ := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	return append(d, cose...)
}

func clientData(typ, challenge, origin string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return b
}

// Create answers a registration (the options JSON the server sent, as {"publicKey": ...}) as
// coming from origin, and returns the JSON the browser would post back.
func (a *Authenticator) Create(options []byte, origin string) ([]byte, error) {
	var o creationOptions
	if err := json.Unmarshal(options, &o); err != nil {
		return nil, err
	}
	if o.PublicKey.AuthenticatorSelection.UserVerification == "required" && !a.UserVerified {
		return nil, errors.New("webauthntest: the server requires user verification and this authenticator has none")
	}
	a.handle = unb64(o.PublicKey.User.ID)
	cd := clientData("webauthn.create", o.PublicKey.Challenge, origin)
	att, _ := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": a.authData(true)})
	return json.Marshal(map[string]any{
		"id": b64(a.credID), "rawId": b64(a.credID), "type": "public-key",
		"response":               map[string]any{"clientDataJSON": b64(cd), "attestationObject": b64(att), "transports": []string{"internal"}},
		"clientExtensionResults": map[string]any{},
	})
}

// Get answers a sign-in (the options JSON, as {"publicKey": ...}) as coming from origin, and
// returns the JSON the browser would post back.
func (a *Authenticator) Get(options []byte, origin string) ([]byte, error) {
	var o requestOptions
	if err := json.Unmarshal(options, &o); err != nil {
		return nil, err
	}
	if !a.Frozen {
		a.Counter++
	}
	cd := clientData("webauthn.get", o.PublicKey.Challenge, origin)
	ad := a.authData(false)
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte(nil), ad...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id": b64(a.credID), "rawId": b64(a.credID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON": b64(cd), "authenticatorData": b64(ad), "signature": b64(sig), "userHandle": b64(a.handle),
		},
		"clientExtensionResults": map[string]any{},
	})
}
