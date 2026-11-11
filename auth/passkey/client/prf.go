//go:build js && wasm

package client

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"syscall/js"
	"time"
)

var ErrNoPRF = errors.New("this passkey cannot be used to encrypt (no PRF support); use another passkey, or an SSH or recovery key")

func bytesToJS(b []byte) js.Value {
	a := js.Global().Get("Uint8Array").New(len(b))
	js.CopyBytesToJS(a, b)
	return a
}

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

func takeBytes(v js.Value) []byte {
	b := jsToBytes(v)
	bytesView(v).Call("fill", 0)
	return b
}

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
	pub.Set("timeout", promptTimeout.Milliseconds())
	since := time.Now()
	cred, err := await(ctx, js.Global().Get("navigator").Get("credentials").Call("get", opts))
	if err != nil {
		return "", nil, rejection(err, since)
	}
	secret, err = prfOutput(cred)
	if err != nil {
		return "", nil, err
	}
	return cred.Get("id").String(), secret, nil
}
