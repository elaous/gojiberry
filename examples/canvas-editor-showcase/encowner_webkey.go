//go:build js && wasm

package main

import (
	"crypto"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/domrt/browser"
)

// The owner key as a key the page cannot copy out.
//
// Where the browser can sign with Ed25519 (WebCrypto), the owner key is imported as a
// *non-extractable* CryptoKey and kept in IndexedDB, which stores a CryptoKey itself. A script that
// runs in the page (an XSS bug, an extension) can still ask it to sign while the page is open, but
// cannot read the key's bytes and take them away: stealing the secret from localStorage was the
// weakness of keeping the seed there. Signing goes through webKey, a crypto.Signer, so the
// directory client needs nothing else. Where Ed25519 is not available the key falls back to
// localStorage (encowner.go says so to the person).

const (
	ownerDBName    = "gojiberry-showcase-owner"
	ownerStoreName = "keys"
)

// pkcs8Prefix is the DER header of an Ed25519 private key in PKCS #8; the 32-byte seed follows.
var pkcs8Prefix = []byte{0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20}

// webKey is a crypto.Signer over a non-extractable WebCrypto Ed25519 key.
type webKey struct {
	key js.Value
	pub ed25519.PublicKey
}

var _ crypto.Signer = (*webKey)(nil)

func (w *webKey) Public() crypto.PublicKey { return w.pub }

// Sign implements crypto.Signer: msg is signed whole (Ed25519 takes no pre-hash).
func (w *webKey) Sign(_ io.Reader, msg []byte, opts crypto.SignerOpts) ([]byte, error) {
	if opts != nil && opts.HashFunc() != 0 {
		return nil, errors.New("webKey: Ed25519 signs the message itself, not a hash")
	}
	data := js.Global().Get("Uint8Array").New(len(msg))
	js.CopyBytesToJS(data, msg)
	res, err := domrt.Await(js.Global().Get("crypto").Get("subtle").Call("sign", map[string]any{"name": "Ed25519"}, w.key, data))
	if err != nil {
		return nil, fmt.Errorf("the browser could not sign: %w", err)
	}
	sig := make([]byte, ed25519.SignatureSize)
	buf := js.Global().Get("Uint8Array").New(res)
	if buf.Get("length").Int() != ed25519.SignatureSize {
		return nil, errors.New("the browser returned a signature of the wrong size")
	}
	js.CopyBytesToGo(sig, buf)
	return sig, nil
}

// webOwnerImport makes a non-extractable WebCrypto key of k. It fails where the browser has no
// Ed25519 in WebCrypto (or no WebCrypto): the caller then keeps the key another way.
func webOwnerImport(k ed25519.PrivateKey) (key *webKey, err error) {
	defer func() {
		if r := recover(); r != nil {
			key, err = nil, fmt.Errorf("WebCrypto is not available: %v", r)
		}
	}()
	subtle := js.Global().Get("crypto").Get("subtle")
	der := append(append([]byte{}, pkcs8Prefix...), k.Seed()...)
	arr := js.Global().Get("Uint8Array").New(len(der))
	js.CopyBytesToJS(arr, der)
	ck, err := domrt.Await(subtle.Call("importKey", "pkcs8", arr, map[string]any{"name": "Ed25519"}, false, []any{"sign"}))
	for i := range der {
		der[i] = 0
	}
	if err != nil {
		return nil, err
	}
	pub, ok := k.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("not an Ed25519 key")
	}
	return &webKey{key: ck, pub: pub}, nil
}

func openOwnerDB() (*browser.Database, error) {
	if js.Global().Get("indexedDB").IsUndefined() {
		return nil, errors.New("no IndexedDB")
	}
	return browser.OpenDatabase(ownerDBName, 1, func(db *browser.Database) { db.CreateObjectStore(ownerStoreName, "id") })
}

// webOwnerSave keeps key for the directory at url, enrolled for email.
func webOwnerSave(url, email string, key *webKey) error {
	db, err := openOwnerDB()
	if err != nil {
		return err
	}
	defer db.Close()
	rec := js.Global().Get("Object").New()
	rec.Set("id", url)
	rec.Set("email", email)
	pub := js.Global().Get("Uint8Array").New(len(key.pub))
	js.CopyBytesToJS(pub, key.pub)
	rec.Set("pub", pub)
	rec.Set("key", key.key)
	return db.Transaction([]string{ownerStoreName}, "readwrite").ObjectStore(ownerStoreName).Put(rec)
}

// webOwnerLoad reads the key kept for the directory at url; ok is false when there is none.
func webOwnerLoad(url string) (email string, key *webKey, ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			email, key, ok, err = "", nil, false, fmt.Errorf("IndexedDB is not available: %v", r)
		}
	}()
	db, err := openOwnerDB()
	if err != nil {
		return "", nil, false, err
	}
	defer db.Close()
	rec, err := db.Transaction([]string{ownerStoreName}, "readonly").ObjectStore(ownerStoreName).Get(url)
	if err != nil {
		return "", nil, false, err
	}
	if rec.IsUndefined() || rec.IsNull() {
		return "", nil, false, nil
	}
	pub := make([]byte, ed25519.PublicKeySize)
	arr := rec.Get("pub")
	if arr.Get("length").Int() != ed25519.PublicKeySize {
		return "", nil, false, errors.New("the stored owner key is damaged")
	}
	js.CopyBytesToGo(pub, arr)
	return rec.Get("email").String(), &webKey{key: rec.Get("key"), pub: pub}, true, nil
}

// webOwnerDelete forgets the key kept for the directory at url.
func webOwnerDelete(url string) error {
	db, err := openOwnerDB()
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Transaction([]string{ownerStoreName}, "readwrite").ObjectStore(ownerStoreName).Delete(url)
}
