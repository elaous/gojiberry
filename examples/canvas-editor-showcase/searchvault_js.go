//go:build js && wasm

package main

import (
	"errors"
	"fmt"
	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/domrt/browser"
)

// The browser side of the sealed search index (searchkeep.go): the sealed bytes in IndexedDB (not in
// localStorage, which the browser target's documents share with a small quota), and the per-device
// secret as a non-extractable Ed25519 key in the same database. The secret is the key's signature of a
// fixed statement: a script in the page can ask for it while the page is open, but nobody can take the
// key away, and a copy of the stored index alone cannot be opened.

const (
	searchDBName     = "gojiberry-showcase-search"
	searchSealedName = "sealed"
	searchDeviceName = "device"
	deviceKeyID      = "key"
	deviceStatement  = "gojiberry/showcase/search-device-secret/v1"
)

func openSearchDB() (*browser.Database, error) {
	if js.Global().Get("indexedDB").IsUndefined() {
		return nil, errors.New("no IndexedDB")
	}
	return browser.OpenDatabase(searchDBName, 1, func(db *browser.Database) {
		db.CreateObjectStore(searchSealedName, "id")
		db.CreateObjectStore(searchDeviceName, "id")
	})
}

// idbVault is the sealedVault over IndexedDB.
type idbVault struct{}

func (idbVault) Get(name string) (data []byte, err error) {
	defer recoverTo(&err)
	db, err := openSearchDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rec, err := db.Transaction([]string{searchSealedName}, "readonly").ObjectStore(searchSealedName).Get(name)
	if err != nil || rec.IsUndefined() || rec.IsNull() {
		return nil, err
	}
	arr := rec.Get("data")
	if arr.IsUndefined() || arr.IsNull() {
		return nil, nil
	}
	data = make([]byte, arr.Get("length").Int())
	js.CopyBytesToGo(data, arr)
	return data, nil
}

func (idbVault) Put(name string, data []byte) (err error) {
	defer recoverTo(&err)
	db, err := openSearchDB()
	if err != nil {
		return err
	}
	defer db.Close()
	arr := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(arr, data)
	rec := js.Global().Get("Object").New()
	rec.Set("id", name)
	rec.Set("data", arr)
	return db.Transaction([]string{searchSealedName}, "readwrite").ObjectStore(searchSealedName).Put(rec)
}

func (idbVault) Delete(name string) (err error) {
	defer recoverTo(&err)
	db, err := openSearchDB()
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Transaction([]string{searchSealedName}, "readwrite").ObjectStore(searchSealedName).Delete(name)
}

func recoverTo(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("the browser's storage is not available: %v", r)
	}
}

// deviceSecret is the per-device secret: made on the first call, from a non-extractable key kept in
// IndexedDB. It fails where the browser has no Ed25519 in WebCrypto or no IndexedDB; the index is then not
// kept.
func deviceSecret() (secret []byte, err error) {
	defer recoverTo(&err)
	db, err := openSearchDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	store := func(mode string) *browser.ObjectStore {
		return db.Transaction([]string{searchDeviceName}, mode).ObjectStore(searchDeviceName)
	}
	rec, err := store("readonly").Get(deviceKeyID)
	if err != nil {
		return nil, err
	}
	var key js.Value
	if rec.IsUndefined() || rec.IsNull() {
		subtle := js.Global().Get("crypto").Get("subtle")
		pair, err := domrt.Await(subtle.Call("generateKey", map[string]any{"name": "Ed25519"}, false, []any{"sign", "verify"}))
		if err != nil {
			return nil, err
		}
		key = pair.Get("privateKey")
		rec = js.Global().Get("Object").New()
		rec.Set("id", deviceKeyID)
		rec.Set("key", key)
		if err := store("readwrite").Put(rec); err != nil {
			return nil, err
		}
	} else {
		key = rec.Get("key")
	}
	msg := []byte(deviceStatement)
	data := js.Global().Get("Uint8Array").New(len(msg))
	js.CopyBytesToJS(data, msg)
	res, err := domrt.Await(js.Global().Get("crypto").Get("subtle").Call("sign", map[string]any{"name": "Ed25519"}, key, data))
	if err != nil {
		return nil, err
	}
	sig := js.Global().Get("Uint8Array").New(res)
	secret = make([]byte, sig.Get("length").Int())
	js.CopyBytesToGo(secret, sig)
	return secret, nil
}
