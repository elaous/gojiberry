//go:build js && wasm

package docstore

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"syscall/js"
	"time"
)

// FileSystemAdapter keeps documents as files in a folder.
type FileSystemAdapter struct {
	dir     js.Value
	name    string
	private bool // the origin-private file system
}

// FileSystemSupported says whether the browser can pick folders.
func FileSystemSupported() bool { return js.Global().Get("showDirectoryPicker").Truthy() }

// PickFolder asks the person to choose a folder.
func PickFolder(ctx context.Context) (*FileSystemAdapter, error) {
	if !FileSystemSupported() {
		return nil, errors.New("docstore: this browser cannot save to a folder you choose (try Chrome or Edge), or use the browser's own storage")
	}
	opts := js.Global().Get("Object").New()
	opts.Set("mode", "readwrite")
	opts.Set("id", "gojiberry")
	h, err := jsAwait(ctx, js.Global(), "showDirectoryPicker", opts)
	if err != nil {
		var je *jsError
		if errors.As(err, &je) && je.Name == "AbortError" {
			return nil, ErrPickCancelled
		}
		return nil, err
	}
	return &FileSystemAdapter{dir: h, name: h.Get("name").String()}, nil
}

// ErrPickCancelled is returned by PickFolder when cancelled.
var ErrPickCancelled = errors.New("docstore: no folder was chosen")

// OriginPrivate is the browser's private file storage.
func OriginPrivate(ctx context.Context) (*FileSystemAdapter, error) {
	storage := js.Global().Get("navigator").Get("storage")
	if !storage.Truthy() || !storage.Get("getDirectory").Truthy() {
		return nil, errors.New("docstore: this browser has no private file system")
	}
	h, err := jsAwait(ctx, storage, "getDirectory")
	if err != nil {
		return nil, err
	}
	return &FileSystemAdapter{dir: h, name: "(this browser)", private: true}, nil
}

// Name is the folder's name, to show.
func (a *FileSystemAdapter) Name() string { return a.name }

// Permission says whether the folder can be written.
func (a *FileSystemAdapter) Permission(ctx context.Context) string {
	v, err := jsAwait(ctx, a.dir, "queryPermission", fsMode())
	if err != nil || !v.Truthy() {
		return "denied"
	}
	return v.String()
}

// Request asks for the permission back.
func (a *FileSystemAdapter) Request(ctx context.Context) (granted bool, err error) {
	v, err := jsAwait(ctx, a.dir, "requestPermission", fsMode())
	if err != nil {
		return false, err
	}
	return v.String() == "granted", nil
}

func fsMode() js.Value {
	o := js.Global().Get("Object").New()
	o.Set("mode", "readwrite")
	return o
}

const (
	fsDB    = "gojiberry-folders"
	fsStore = "handles"
)

func openFSDB(ctx context.Context) (js.Value, error) {
	idb := js.Global().Get("indexedDB")
	if !idb.Truthy() {
		return js.Undefined(), errors.New("docstore: this browser has no IndexedDB")
	}
	req := idb.Call("open", fsDB, 1)
	done := make(chan error, 1)
	var up, ok, bad js.Func
	release := func() { up.Release(); ok.Release(); bad.Release() }
	up = js.FuncOf(func(_ js.Value, args []js.Value) any {
		args[0].Get("target").Get("result").Call("createObjectStore", fsStore)
		return nil
	})
	ok = js.FuncOf(func(js.Value, []js.Value) any { release(); done <- nil; return nil })
	bad = js.FuncOf(func(js.Value, []js.Value) any { release(); done <- toJSError(req.Get("error")); return nil })
	req.Set("onupgradeneeded", up)
	req.Set("onsuccess", ok)
	req.Set("onerror", bad)
	select {
	case err := <-done:
		if err != nil {
			return js.Undefined(), err
		}
		return req.Get("result"), nil
	case <-ctx.Done():
		return js.Undefined(), ctx.Err()
	}
}

func idbRequest(ctx context.Context, req js.Value) (js.Value, error) {
	done := make(chan error, 1)
	var ok, bad js.Func
	release := func() { ok.Release(); bad.Release() }
	ok = js.FuncOf(func(js.Value, []js.Value) any { release(); done <- nil; return nil })
	bad = js.FuncOf(func(js.Value, []js.Value) any { release(); done <- toJSError(req.Get("error")); return nil })
	req.Set("onsuccess", ok)
	req.Set("onerror", bad)
	select {
	case err := <-done:
		return req.Get("result"), err
	case <-ctx.Done():
		return js.Undefined(), ctx.Err()
	}
}

// Remember keeps the folder under key.
func (a *FileSystemAdapter) Remember(ctx context.Context, key string) error {
	db, err := openFSDB(ctx)
	if err != nil {
		return err
	}
	defer db.Call("close")
	tx := db.Call("transaction", fsStore, "readwrite")
	if _, err := idbRequest(ctx, tx.Call("objectStore", fsStore).Call("put", a.dir, key)); err != nil {
		return err
	}
	return nil
}

// RestoreFolder finds the folder remembered under key.
func RestoreFolder(ctx context.Context, key string) (a *FileSystemAdapter, ok bool, err error) {
	db, err := openFSDB(ctx)
	if err != nil {
		return nil, false, err
	}
	defer db.Call("close")
	v, err := idbRequest(ctx, db.Call("transaction", fsStore, "readonly").Call("objectStore", fsStore).Call("get", key))
	if err != nil || v.IsUndefined() || v.IsNull() {
		return nil, false, err
	}
	return &FileSystemAdapter{dir: v, name: v.Get("name").String()}, true, nil
}

// Forget drops the folder remembered under key.
func ForgetFolder(ctx context.Context, key string) error {
	db, err := openFSDB(ctx)
	if err != nil {
		return err
	}
	defer db.Call("close")
	_, err = idbRequest(ctx, db.Call("transaction", fsStore, "readwrite").Call("objectStore", fsStore).Call("delete", key))
	return err
}

func fsName(id string) string { return base64.RawURLEncoding.EncodeToString([]byte(id)) }

func fsWrap(err error, writing bool) error {
	var je *jsError
	if errors.As(err, &je) {
		switch je.Name {
		case "NotFoundError":
			return ErrNotFound
		case "QuotaExceededError":
			return fmt.Errorf("%w: %v", ErrStorageFull, err)
		case "NotAllowedError", "SecurityError":
			if writing {
				return fmt.Errorf("%w: the browser has not allowed writing this folder (reconnect it with a click): %v", ErrPermission, err)
			}
			return fmt.Errorf("%w: the browser has not allowed reading this folder (reconnect it with a click): %v", ErrPermission, err)
		}
	}
	return err
}

func abortWritable(w js.Value) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = jsAwait(ctx, w, "abort")
}

func (a *FileSystemAdapter) write(ctx context.Context, id string, data []byte) error {
	opts := js.Global().Get("Object").New()
	opts.Set("create", true)
	fh, err := jsAwait(ctx, a.dir, "getFileHandle", fsName(id), opts)
	if err != nil {
		return fsWrap(err, true)
	}
	w, err := jsAwait(ctx, fh, "createWritable")
	if err != nil {
		return fsWrap(err, true)
	}
	if _, err := jsAwait(ctx, w, "write", jsUint8(data)); err != nil {
		abortWritable(w)
		return fsWrap(err, true)
	}
	if _, err := jsAwait(ctx, w, "close"); err != nil {
		abortWritable(w)
		return fsWrap(err, true)
	}
	return nil
}

// Save implements Adapter.
func (a *FileSystemAdapter) Save(ctx context.Context, id string, data []byte) error {
	return a.write(ctx, id, data)
}

func (a *FileSystemAdapter) read(ctx context.Context, id string) ([]byte, time.Time, error) {
	fh, err := jsAwait(ctx, a.dir, "getFileHandle", fsName(id))
	if err != nil {
		return nil, time.Time{}, fsWrap(err, false)
	}
	f, err := jsAwait(ctx, fh, "getFile")
	if err != nil {
		return nil, time.Time{}, fsWrap(err, false)
	}
	buf, err := jsAwait(ctx, f, "arrayBuffer")
	if err != nil {
		return nil, time.Time{}, fsWrap(err, false)
	}
	return jsBytes(buf), time.UnixMilli(int64(f.Get("lastModified").Float())), nil
}

// Load implements Adapter.
func (a *FileSystemAdapter) Load(ctx context.Context, id string) (Document, error) {
	data, mod, err := a.read(ctx, id)
	if err != nil {
		return Document{}, err
	}
	return Document{ID: id, Data: data, UpdatedAt: mod}, nil
}

// List implements Adapter.
func (a *FileSystemAdapter) List(ctx context.Context) ([]Meta, error) {
	it, err := jsCall(a.dir, "values")
	if err != nil {
		return nil, fsWrap(err, false)
	}
	var metas []Meta
	for {
		r, err := jsAwait(ctx, it, "next")
		if err != nil {
			return nil, fsWrap(err, false)
		}
		if r.Get("done").Bool() {
			return metas, nil
		}
		h := r.Get("value")
		if h.Get("kind").String() != "file" {
			continue
		}
		raw, derr := base64.RawURLEncoding.DecodeString(h.Get("name").String())
		if derr != nil {
			continue
		}
		f, err := jsAwait(ctx, h, "getFile")
		if err != nil {
			if werr := fsWrap(err, false); errors.Is(werr, ErrPermission) {
				return nil, werr
			}
			continue // gone since the listing
		}
		metas = append(metas, Meta{ID: string(raw), Size: f.Get("size").Int(), UpdatedAt: time.UnixMilli(int64(f.Get("lastModified").Float()))})
	}
}

// Delete implements Adapter.
func (a *FileSystemAdapter) Delete(ctx context.Context, id string) error {
	if _, err := jsAwait(ctx, a.dir, "getFileHandle", fsName(id)); err != nil {
		return fsWrap(err, true)
	}
	_, err := jsAwait(ctx, a.dir, "removeEntry", fsName(id))
	return fsWrap(err, true)
}

// SaveIf implements ConditionalAdapter.
func (a *FileSystemAdapter) SaveIf(ctx context.Context, id string, data []byte, prevDigest string) error {
	release, err := webLock(ctx, "gojiberry-fs:"+a.name+"#cas")
	if err != nil {
		return err
	}
	defer release()
	cur, _, err := a.read(ctx, id)
	switch {
	case errors.Is(err, ErrNotFound):
		if prevDigest != "" {
			return ErrConflict
		}
	case err != nil:
		return err
	default:
		if prevDigest == "" || Digest(cur) != prevDigest {
			return ErrConflict
		}
	}
	if err := a.write(ctx, id, data); err != nil {
		return err
	}
	back, _, err := a.read(ctx, id)
	switch {
	case errors.Is(err, ErrNotFound):
		return ErrConflict
	case err != nil:
		return err
	case Digest(back) != Digest(data):
		return ErrConflict
	}
	return nil
}

// Usage implements QuotaReporter for the folder.
func (a *FileSystemAdapter) Usage(ctx context.Context) (Quota, error) {
	if !a.private {
		return Quota{}, errors.New("docstore: the browser does not report the free space of a chosen folder")
	}
	storage := js.Global().Get("navigator").Get("storage")
	if !storage.Truthy() || !storage.Get("estimate").Truthy() {
		return Quota{}, errors.New("docstore: no storage estimate")
	}
	v, err := jsAwait(ctx, storage, "estimate")
	if err != nil {
		return Quota{}, err
	}
	return Quota{Used: int64(v.Get("usage").Float()), Total: int64(v.Get("quota").Float())}, nil
}
