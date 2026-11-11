//go:build js && wasm

package browser

import (
	"errors"
	"syscall/js"
	"time"
)

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

// OpenDatabase opens, creating if needed, the named database.
func OpenDatabase(name string, version int, upgrade func(db *Database)) (*Database, error) {
	req := js.Global().Get("indexedDB").Call("open", name, version)

	result := make(chan js.Value, 1)
	fail := make(chan error, 1)

	var onUpgrade, onSuccess, onError js.Func
	abandoned := false
	releaseAll := func() {
		onUpgrade.Release()
		onSuccess.Release()
		onError.Release()
	}
	onUpgrade = js.FuncOf(func(this js.Value, args []js.Value) any {
		if upgrade != nil {
			upgrade(&Database{Value: req.Get("result")})
		}
		return nil
	})
	onSuccess = js.FuncOf(func(this js.Value, args []js.Value) any {
		if abandoned {
			req.Get("result").Call("close")
			releaseAll()
			return nil
		}
		result <- req.Get("result")
		releaseAll()
		return nil
	})
	onError = js.FuncOf(func(this js.Value, args []js.Value) any {
		if !abandoned {
			fail <- idbError(req, "indexeddb open failed")
		}
		releaseAll()
		return nil
	})

	req.Call("addEventListener", "upgradeneeded", onUpgrade)
	req.Call("addEventListener", "success", onSuccess)
	req.Call("addEventListener", "error", onError)

	select {
	case v := <-result:
		return &Database{Value: v}, nil
	case err := <-fail:
		return nil, err
	case <-time.After(openTimeout):
		// neither success nor error: a "blocked" upgrade by an older tab
		abandoned = true
		return nil, errors.New("indexeddb: open timed out (another tab may be holding an older version open)")
	}
}

const openTimeout = 30 * time.Second

// CreateObjectStore creates a new object store keyed by key.
func (d *Database) CreateObjectStore(name, keyPath string) js.Value {
	return d.Value.Call("createObjectStore", name, map[string]any{"keyPath": keyPath})
}

// Close closes the database connection.
func (d *Database) Close() { d.Value.Call("close") }

// Transaction starts a transaction over storeNames.
func (d *Database) Transaction(storeNames []string, mode string) *Transaction {
	names := make([]any, len(storeNames))
	for i, n := range storeNames {
		names[i] = n
	}
	return &Transaction{Value: d.Value.Call("transaction", names, mode)}
}

// Transaction is an in-progress IndexedDB transaction.
type Transaction struct {
	Value js.Value
}

// ObjectStore returns the named object store within this transaction.
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

// Get retrieves the value stored under key.
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

// GetAll returns every record currently in the store.
func (s *ObjectStore) GetAll() (js.Value, error) {
	return awaitIDBRequest(s.Value.Call("getAll"))
}
