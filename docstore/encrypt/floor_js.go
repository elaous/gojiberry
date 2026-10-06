//go:build js && wasm

package encrypt

import (
	"errors"
	"fmt"
	"sync"
	"syscall/js"
)

// storageFn is the one JavaScript function every localStorage access goes through. It is
// written in JavaScript on purpose: reading the localStorage global itself throws in a private
// window or with site data blocked (a SecurityError), and syscall/js does not turn an exception
// thrown by a property read into a Go error (it ends the program); only Invoke does. So the
// whole access, getter included, runs inside a try block and reports {err} instead of
// throwing; setItem's quota error is caught the same way.
var (
	storageOnce sync.Once
	storageFn   js.Value
)

func storage() js.Value {
	storageOnce.Do(func() {
		storageFn = js.Global().Get("Function").New("op", "k", "v", `
			try {
				var s = globalThis.localStorage;
				if (s === undefined || s === null) return { err: "localStorage is not available" };
				if (op === "get") {
					var x = s.getItem(k);
					return x === null || x === undefined ? { ok: true } : { ok: true, has: true, v: String(x) };
				}
				s.setItem(k, v);
				return { ok: true };
			} catch (e) {
				return { err: String(e) };
			}`)
	})
	return storageFn
}

// localStorageKV is a KV over the browser's localStorage. A failure is returned, never raised.
type localStorageKV struct{}

func call(op, key, value string) (res js.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("encrypt: localStorage: %v", r)
		}
	}()
	res = storage().Invoke(op, key, value)
	if e := res.Get("err"); !e.IsUndefined() {
		return res, errors.New("encrypt: localStorage: " + e.String())
	}
	return res, nil
}

func (localStorageKV) Get(key string) (string, bool, error) {
	res, err := call("get", key, "")
	if err != nil || !res.Get("has").Truthy() {
		return "", false, err
	}
	return res.Get("v").String(), true, nil
}

func (localStorageKV) Set(key, value string) error {
	_, err := call("set", key, value)
	return err
}

// LocalStorageSeqFloor is a StorageSeqFloor over the browser's localStorage, one entry per
// document under prefix (use one per signed-in account, for example "gojiberry-floor:<user>:").
// It survives reloads and new tabs of the same origin, which is where the page's own code runs
// and a store's writers do not; clearing site data resets it, like a fresh client. When
// localStorage is unavailable or throws (a private window, blocked or full storage) it works
// from memory instead, and says so: Get and Raise return the value with an error that wraps
// ErrFloorDegraded, the keyring and Sharing report it through SetFloorDegradedHandler, and
// StorageSeqFloor.Err keeps the last failure for a page that polls. A page that offers this layer
// should show it: this browser is not remembering what it saw.
func LocalStorageSeqFloor(prefix string) *StorageSeqFloor {
	return &StorageSeqFloor{Prefix: prefix, Store: localStorageKV{}}
}
