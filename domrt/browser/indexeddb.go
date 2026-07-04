//go:build js && wasm

package browser

import (
	"errors"
	"syscall/js"
)

// awaitIDBRequest blocks until an IDBRequest fires "success" or
// "error" -- the same channel-bridge idea as domrt.Await, but for
// IndexedDB's event-based requests instead of a real Promise.
func awaitIDBRequest(req js.Value) (js.Value, error) {
	result := make(chan js.Value, 1)
	fail := make(chan error, 1)

	var onSuccess, onError js.Func
	onSuccess = js.FuncOf(func(this js.Value, args []js.Value) any {
		result <- req.Get("result")
		return nil
	})
	onError = js.FuncOf(func(this js.Value, args []js.Value) any {
		fail <- idbError(req, "indexeddb request failed")
		return nil
	})
	defer onSuccess.Release()
	defer onError.Release()

	req.Call("addEventListener", "success", onSuccess)
	req.Call("addEventListener", "error", onError)

	select {
	case v := <-result:
		return v, nil
	case err := <-fail:
		return js.Value{}, err
	}
}

func idbError(req js.Value, fallback string) error {
	if e := req.Get("error"); !e.IsUndefined() && !e.IsNull() {
		return errors.New(e.Call("toString").String())
	}
	return errors.New(fallback)
}

// Database is an open IndexedDB connection.
type Database struct {
	Value js.Value
}

// OpenDatabase opens (creating if necessary) database name at the
// given version, blocking until open completes. upgrade, if non-nil,
// runs synchronously during the browser's "upgradeneeded" event --
// the only time object stores can be created or removed -- which
// fires on a brand new database or after bumping version.
func OpenDatabase(name string, version int, upgrade func(db *Database)) (*Database, error) {
	req := js.Global().Get("indexedDB").Call("open", name, version)

	result := make(chan js.Value, 1)
	fail := make(chan error, 1)

	var onUpgrade, onSuccess, onError js.Func
	onUpgrade = js.FuncOf(func(this js.Value, args []js.Value) any {
		if upgrade != nil {
			upgrade(&Database{Value: req.Get("result")})
		}
		return nil
	})
	onSuccess = js.FuncOf(func(this js.Value, args []js.Value) any {
		result <- req.Get("result")
		return nil
	})
	onError = js.FuncOf(func(this js.Value, args []js.Value) any {
		fail <- idbError(req, "indexeddb open failed")
		return nil
	})
	defer onUpgrade.Release()
	defer onSuccess.Release()
	defer onError.Release()

	req.Call("addEventListener", "upgradeneeded", onUpgrade)
	req.Call("addEventListener", "success", onSuccess)
	req.Call("addEventListener", "error", onError)

	select {
	case v := <-result:
		return &Database{Value: v}, nil
	case err := <-fail:
		return nil, err
	}
}

// CreateObjectStore creates a new object store keyed by keyPath. Only
// valid inside OpenDatabase's upgrade callback.
func (d *Database) CreateObjectStore(name, keyPath string) js.Value {
	return d.Value.Call("createObjectStore", name, map[string]any{"keyPath": keyPath})
}

// Close closes the database connection.
func (d *Database) Close() { d.Value.Call("close") }

// Transaction starts a transaction over storeNames in the given mode
// ("readonly" or "readwrite").
func (d *Database) Transaction(storeNames []string, mode string) *Transaction {
	names := make([]any, len(storeNames))
	for i, n := range storeNames {
		names[i] = n
	}
	return &Transaction{Value: d.Value.Call("transaction", names, mode)}
}

// Transaction is an in-progress IndexedDB transaction over one or more object stores.
type Transaction struct {
	Value js.Value
}

// ObjectStore returns the named object store within this transaction's scope.
func (t *Transaction) ObjectStore(name string) *ObjectStore {
	return &ObjectStore{Value: t.Value.Call("objectStore", name)}
}

// ObjectStore is a key/value store within a Transaction.
type ObjectStore struct {
	Value js.Value
}

// Put inserts or updates value, blocking until the request completes.
func (s *ObjectStore) Put(value any) error {
	_, err := awaitIDBRequest(s.Value.Call("put", value))
	return err
}

// Get retrieves the value stored under key, blocking until the
// request completes. The result is JS `undefined` if key doesn't
// exist -- check with .IsUndefined().
func (s *ObjectStore) Get(key any) (js.Value, error) {
	return awaitIDBRequest(s.Value.Call("get", key))
}

// Delete removes the record stored under key.
func (s *ObjectStore) Delete(key any) error {
	_, err := awaitIDBRequest(s.Value.Call("delete", key))
	return err
}

// Clear removes every record in the store.
func (s *ObjectStore) Clear() error {
	_, err := awaitIDBRequest(s.Value.Call("clear"))
	return err
}
