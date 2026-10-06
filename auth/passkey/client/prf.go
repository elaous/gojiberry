//go:build js && wasm

package client

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"syscall/js"
)

// ErrNoPRF is returned when the passkey (or the browser) does not do the PRF extension, so it
// cannot be used as an encryption key. Passkeys made before the page asked for PRF at
// registration, and some security keys and password managers, do not.
var ErrNoPRF = errors.New("this passkey cannot be used to encrypt (no PRF support); use another passkey, or an SSH or recovery key")

func bytesToJS(b []byte) js.Value {
	a := js.Global().Get("Uint8Array").New(len(b))
	js.CopyBytesToJS(a, b)
	return a
}

func jsToBytes(v js.Value) []byte {
	a := js.Global().Get("Uint8Array").New(v)
	b := make([]byte, a.Get("length").Int())
	js.CopyBytesToGo(b, a)
	return b
}

// DeriveKey asks a passkey for the 32 secret bytes it answers salt with (the WebAuthn PRF
// extension): the same passkey and salt always give the same bytes, and they never leave the
// browser. credentialID (base64url) picks the passkey; "" lets the person choose one of this
// site's. It returns the ID of the passkey that answered. The caller derives an encryption key
// from the bytes (encrypt.PRFIdentity) and should wipe them after.
func DeriveKey(ctx context.Context, credentialID string, salt []byte) (usedID string, secret []byte, err error) {
	challenge := make([]byte, 32)
	if _, err := rand.Read(challenge); err != nil {
		return "", nil, err
	}
	pub := js.Global().Get("Object").New()
	pub.Set("challenge", bytesToJS(challenge))
	pub.Set("userVerification", "required")
	if credentialID != "" {
		raw, err := base64.RawURLEncoding.DecodeString(credentialID)
		if err != nil {
			return "", nil, fmt.Errorf("passkey: bad credential ID: %w", err)
		}
		allow := js.Global().Get("Object").New()
		allow.Set("type", "public-key")
		allow.Set("id", bytesToJS(raw))
		pub.Set("allowCredentials", js.Global().Get("Array").Call("of", allow))
	}
	eval := js.Global().Get("Object").New()
	eval.Set("first", bytesToJS(salt))
	prf := js.Global().Get("Object").New()
	prf.Set("eval", eval)
	ext := js.Global().Get("Object").New()
	ext.Set("prf", prf)
	pub.Set("extensions", ext)
	opts := js.Global().Get("Object").New()
	opts.Set("publicKey", pub)
	cred, err := await(ctx, js.Global().Get("navigator").Get("credentials").Call("get", opts))
	if err != nil {
		var je *jsErr
		if errors.As(err, &je) && (je.name == "NotAllowedError" || je.name == "AbortError") {
			return "", nil, fmt.Errorf("%w (%s)", ErrCancelled, je)
		}
		return "", nil, err
	}
	res := cred.Call("getClientExtensionResults").Get("prf")
	if !res.Truthy() || !res.Get("results").Truthy() || !res.Get("results").Get("first").Truthy() {
		return "", nil, ErrNoPRF
	}
	secret = jsToBytes(res.Get("results").Get("first"))
	if len(secret) != 32 {
		return "", nil, ErrNoPRF
	}
	return cred.Get("id").String(), secret, nil
}
