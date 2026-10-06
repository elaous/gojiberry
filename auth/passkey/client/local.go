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

// A passkey with no server. Registration and sign-in against a server (Client.Register, SignIn)
// need one to make and check the challenge; a page that only wants the passkey's PRF secret
// as an encryption key (a static page keeping documents in the browser) needs neither: the
// browser's authenticator makes the credential on this device for this origin, and DeriveKey
// asks it for the secret. CreateLocal makes such a credential and remembers its ID in
// localStorage (so DeriveLocal can ask for exactly that passkey); nothing is sent anywhere
// and nothing is verified, because nobody else is being convinced of anything.

const localPrefix = "gojiberry:passkey-local:"

// LocalCredential returns the credential ID (base64url) CreateLocal stored under slot, or "".
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

// SetLocalCredential makes id (a credential ID CreateLocal returned, or LocalCredential read
// earlier) the one stored under slot: what a page does to put back the passkey it used before when
// a new one was made for a change that then failed, so the new one is not asked for at the next
// unlock. An empty id forgets the slot.
func SetLocalCredential(slot, id string) {
	if id == "" {
		ForgetLocal(slot)
		return
	}
	defer func() { _ = recover() }()
	js.Global().Get("localStorage").Call("setItem", localPrefix+slot, id)
}

// ForgetLocal drops the credential ID stored under slot (the passkey itself stays in the
// authenticator; it is the person's to delete there).
func ForgetLocal(slot string) {
	defer func() { _ = recover() }()
	js.Global().Get("localStorage").Call("removeItem", localPrefix+slot)
}

// CreateLocal makes a discoverable passkey for this origin that does the PRF extension and
// keeps its credential ID in localStorage under slot. rpName and userName are what the
// browser's prompt shows. It returns the credential ID (base64url). ErrNoPRF says the
// authenticator made a passkey that cannot be used to encrypt (no PRF), in which case nothing
// is stored (the passkey itself exists in the authenticator, though: the credential ID is still
// returned with ErrNoPRF so the page can name it, and the person has to delete it there);
// ErrCancelled (or its kinds ErrTimedOut, ErrNotVerified) that the prompt did not complete;
// ErrAlreadyRegistered that this authenticator already holds the passkey the page asked for.
//
// A fresh random user handle is used each time, so making a second passkey never replaces the
// first. Use DeriveLocal to get the secret.
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
		return id, ErrNoPRF // made, but of no use as a key: the page tells the person which passkey to delete
	}
	func() {
		defer func() { _ = recover() }() // storage blocked: the page can still use the ID it was given
		js.Global().Get("localStorage").Call("setItem", localPrefix+slot, id)
	}()
	return id, nil
}

// DeriveLocal is DeriveKey for the passkey CreateLocal stored under slot. With none stored it
// lets the person choose among this site's passkeys, like DeriveKey with an empty ID. The same
// rules apply to the secret it returns: derive the key (encrypt.PRFIdentity) and clear it.
func DeriveLocal(ctx context.Context, slot string, salt []byte) (usedID string, secret []byte, err error) {
	return DeriveKey(ctx, LocalCredential(slot), salt)
}
