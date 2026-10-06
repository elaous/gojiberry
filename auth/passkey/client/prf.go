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

// ErrPRFLength is returned when the passkey answered with something other than 32 bytes: it
// does PRF, but not in the shape an encryption key is made from, so it is not used.
var ErrPRFLength = errors.New("passkey: the PRF output is not 32 bytes; this passkey cannot be used to encrypt")

func bytesView(v js.Value) js.Value {
	u8 := js.Global().Get("Uint8Array")
	if js.Global().Get("ArrayBuffer").Call("isView", v).Bool() {
		return u8.New(v.Get("buffer"), v.Get("byteOffset"), v.Get("byteLength"))
	}
	return u8.New(v) // an ArrayBuffer: a view on the same memory
}

func jsToBytes(v js.Value) []byte {
	a := bytesView(v)
	b := make([]byte, a.Get("length").Int())
	js.CopyBytesToGo(b, a)
	return b
}

// takeBytes copies v into Go and then zeroes the JavaScript buffer it came from, so the secret
// is not left lying in the page's memory for the garbage collector to get to.
func takeBytes(v js.Value) []byte {
	b := jsToBytes(v)
	bytesView(v).Call("fill", 0)
	return b
}

// prfOutput reads the PRF result of a credential (what navigator.credentials.get answered): the
// 32 bytes, or ErrNoPRF when there is no PRF result at all (the passkey, or the browser, does
// not do the extension) and ErrPRFLength when there is one of the wrong size. The result's
// JavaScript buffer is zeroed.
func prfOutput(cred js.Value) ([]byte, error) {
	res := cred.Call("getClientExtensionResults").Get("prf")
	if !res.Truthy() || !res.Get("results").Truthy() || !res.Get("results").Get("first").Truthy() {
		return nil, ErrNoPRF
	}
	secret := takeBytes(res.Get("results").Get("first"))
	if len(secret) != 32 {
		clear(secret)
		return nil, ErrPRFLength
	}
	return secret, nil
}

// DeriveKey asks a passkey for the 32 secret bytes it answers salt with (the WebAuthn PRF
// extension): the same passkey and salt always give the same bytes, and they never leave the
// browser. credentialID (base64url) picks the passkey; "" lets the person choose one of this
// site's. It returns the ID of the passkey that answered.
//
// The returned secret is the root of an encryption key: derive the key from it
// (encrypt.PRFIdentity) and then clear(secret). The copy the browser handed over is zeroed here
// once it has been copied. ErrNoPRF means the passkey or browser does not do PRF (or sent
// nothing), ErrPRFLength that it answered with the wrong size; ErrCancelled that the person
// closed the prompt.
//
// credentialID is a hint stored in clear in a document's recipient list (see
// encrypt.PRFRecipient for what that reveals); passing "" avoids needing it, and the browser
// then lets the person choose.
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
	secret, err = prfOutput(cred)
	if err != nil {
		return "", nil, err
	}
	return cred.Get("id").String(), secret, nil
}
