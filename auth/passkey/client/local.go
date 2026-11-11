//go:build js && wasm

package client

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"syscall/js"
	"time"
)

const localPrefix = "gojiberry:passkey-local:"

func LocalCredential(slot string) (id string) {
	defer func() {
		if recover() != nil {
			id = ""
		}
	}()
	v := js.Global().Get("localStorage").Call("getItem", localPrefix+slot)
	if v.IsNull() || v.IsUndefined() {
		return ""
	}
	return v.String()
}

func SetLocalCredential(slot, id string) {
	if id == "" {
		ForgetLocal(slot)
		return
	}
	defer func() { _ = recover() }()
	js.Global().Get("localStorage").Call("setItem", localPrefix+slot, id)
}

func ForgetLocal(slot string) {
	defer func() { _ = recover() }()
	js.Global().Get("localStorage").Call("removeItem", localPrefix+slot)
}

func CreateLocal(ctx context.Context, slot, rpName, userName string) (string, error) {
	if !Supported() {
		return "", errors.New("passkeys are not supported here (they need a secure context: https or localhost)")
	}
	challenge, handle := make([]byte, 32), make([]byte, 16)
	if _, err := rand.Read(challenge); err != nil {
		return "", err
	}
	if _, err := rand.Read(handle); err != nil {
		return "", err
	}
	obj := func() js.Value { return js.Global().Get("Object").New() }
	pub := obj()
	pub.Set("challenge", bytesToJS(challenge))
	rp := obj()
	rp.Set("name", rpName)
	rp.Set("id", js.Global().Get("location").Get("hostname").String())
	pub.Set("rp", rp)
	user := obj()
	user.Set("id", bytesToJS(handle))
	user.Set("name", userName)
	user.Set("displayName", userName)
	pub.Set("user", user)
	var params []any
	for _, alg := range []int{-7, -257} { // ES256, RS256
		p := obj()
		p.Set("type", "public-key")
		p.Set("alg", alg)
		params = append(params, p)
	}
	pub.Set("pubKeyCredParams", js.Global().Get("Array").Call("of", params...))
	sel := obj()
	sel.Set("residentKey", "required")
	sel.Set("userVerification", "required")
	pub.Set("authenticatorSelection", sel)
	prf := obj()
	ext := obj()
	ext.Set("prf", prf)
	pub.Set("extensions", ext)
	opts := obj()
	opts.Set("publicKey", pub)
	pub.Set("timeout", promptTimeout.Milliseconds())
	since := time.Now()
	cred, err := await(ctx, js.Global().Get("navigator").Get("credentials").Call("create", opts))
	if err != nil {
		return "", rejection(err, since)
	}
	id := base64.RawURLEncoding.EncodeToString(jsToBytes(cred.Get("rawId")))
	if res := cred.Call("getClientExtensionResults").Get("prf"); !res.Truthy() || !res.Get("enabled").Truthy() {
		return id, ErrNoPRF
	}
	func() {
		defer func() { _ = recover() }()
		js.Global().Get("localStorage").Call("setItem", localPrefix+slot, id)
	}()
	return id, nil
}

// DeriveLocal is DeriveKey for the passkey CreateLocal stored under slot.
func DeriveLocal(ctx context.Context, slot string, salt []byte) (usedID string, secret []byte, err error) {
	return DeriveKey(ctx, LocalCredential(slot), salt)
}
