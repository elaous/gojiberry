//go:build js && wasm

package encrypt

import (
	"errors"
	"fmt"
	"sync"
	"syscall/js"
)

// storageFn is the JavaScript function every localStorage access uses.
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

// LocalStorageSeqFloor is a StorageSeqFloor over localStorage.
func LocalStorageSeqFloor(prefix string) *StorageSeqFloor {
	return &StorageSeqFloor{Prefix: prefix, Store: localStorageKV{}}
}
