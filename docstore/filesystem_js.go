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

// FileSystemAdapter keeps documents as files in a folder of the person's own computer, through
// the browser's File System Access API (Chrome, Edge and Opera; see FileSystemSupported). The
// person chooses the folder once (PickFolder); the handle is remembered in IndexedDB
// (Remember, RestoreFolder) so the next visit finds the same folder again. Permission is the
// browser's to give: after a restart it has to be asked for again with a click
// (FileSystemAdapter.Request), and Permission says where it stands. The page can be served
// from any static host over HTTPS (or localhost): no server of ours is involved.
//
// Files are named as LocalFSAdapter names them (the ID, base64url-encoded), so the same folder
// can be opened by the Go tools (docsh -store files:DIR) and by the server. A file is written
// through a writable stream that replaces the old file when closed, so a write is whole or not
// there. The same adapter works on the browser's origin-private file system (OriginPrivate),
// which needs no picker, in every current browser, but is not a folder the person can see.
type FileSystemAdapter struct {
	dir     js.Value
	name    string
	private bool // the origin-private file system
}

// FileSystemSupported says whether the browser can let the person choose a folder.
func FileSystemSupported() bool { return js.Global().Get("showDirectoryPicker").Truthy() }

// PickFolder asks the person to choose a folder to save in (the browser shows its own picker).
// It must be called from a click or key press. A cancelled picker is an error satisfying
// errors.Is(err, ErrPickCancelled).
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

// ErrPickCancelled is returned by PickFolder when the person closed the picker.
var ErrPickCancelled = errors.New("docstore: no folder was chosen")

// OriginPrivate is the browser's private file system for this site: files in a real file
// system the person cannot browse, with nothing to pick or allow.
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

// Permission says where the browser's permission to write the folder stands: "granted",
// "prompt" (ask with Request, from a click) or "denied".
func (a *FileSystemAdapter) Permission(ctx context.Context) string {
	v, err := jsAwait(ctx, a.dir, "queryPermission", fsMode())
	if err != nil || !v.Truthy() {
		return "denied"
	}
	return v.String()
}

// Request asks for the permission back (the browser may show a prompt). Call it from a click.
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

// ---- remembering the folder ----

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

// Remember keeps the folder under key, so RestoreFolder finds it on a later visit.
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

// RestoreFolder finds the folder remembered under key; ok is false if there is none. The
// permission may have lapsed: check Permission and, from a click, Request.
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

// Forget drops the folder remembered under key (the files stay where they are).
func ForgetFolder(ctx context.Context, key string) error {
	db, err := openFSDB(ctx)
	if err != nil {
		return err
	}
	defer db.Call("close")
	_, err = idbRequest(ctx, db.Call("transaction", fsStore, "readwrite").Call("objectStore", fsStore).Call("delete", key))
	return err
}

// ---- Adapter ----

func fsName(id string) string { return base64.RawURLEncoding.EncodeToString([]byte(id)) }

func fsWrap(err error) error {
	var je *jsError
	if errors.As(err, &je) {
		switch je.Name {
		case "NotFoundError":
			return ErrNotFound
		case "QuotaExceededError":
			return fmt.Errorf("%w: %v", ErrStorageFull, err)
		case "NotAllowedError", "SecurityError":
			return fmt.Errorf("docstore: the browser has not allowed writing this folder (reconnect it with a click): %w", err)
		}
	}
	return err
}

func (a *FileSystemAdapter) write(ctx context.Context, id string, data []byte) error {
	opts := js.Global().Get("Object").New()
	opts.Set("create", true)
	fh, err := jsAwait(ctx, a.dir, "getFileHandle", fsName(id), opts)
	if err != nil {
		return fsWrap(err)
	}
	w, err := jsAwait(ctx, fh, "createWritable")
	if err != nil {
		return fsWrap(err)
	}
	if _, err := jsAwait(ctx, w, "write", jsUint8(data)); err != nil {
		_, _ = jsAwait(ctx, w, "abort")
		return fsWrap(err)
	}
	_, err = jsAwait(ctx, w, "close") // the old file is replaced here, whole
	return fsWrap(err)
}

// Save implements Adapter.
func (a *FileSystemAdapter) Save(ctx context.Context, id string, data []byte) error {
	return a.write(ctx, id, data)
}

func (a *FileSystemAdapter) read(ctx context.Context, id string) ([]byte, time.Time, error) {
	fh, err := jsAwait(ctx, a.dir, "getFileHandle", fsName(id))
	if err != nil {
		return nil, time.Time{}, fsWrap(err)
	}
	f, err := jsAwait(ctx, fh, "getFile")
	if err != nil {
		return nil, time.Time{}, fsWrap(err)
	}
	buf, err := jsAwait(ctx, f, "arrayBuffer")
	if err != nil {
		return nil, time.Time{}, fsWrap(err)
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
		return nil, err
	}
	var metas []Meta
	for {
		r, err := jsAwait(ctx, it, "next")
		if err != nil {
			return nil, fsWrap(err)
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
			continue // not one of ours
		}
		f, err := jsAwait(ctx, h, "getFile")
		if err != nil {
			continue // gone since the listing
		}
		metas = append(metas, Meta{ID: string(raw), Size: f.Get("size").Int(), UpdatedAt: time.UnixMilli(int64(f.Get("lastModified").Float()))})
	}
}

// Delete implements Adapter.
func (a *FileSystemAdapter) Delete(ctx context.Context, id string) error {
	if _, err := jsAwait(ctx, a.dir, "getFileHandle", fsName(id)); err != nil {
		return fsWrap(err)
	}
	_, err := jsAwait(ctx, a.dir, "removeEntry", fsName(id))
	return fsWrap(err)
}

// SaveIf implements ConditionalAdapter: the check and the write are made under a Web Lock shared
// by every tab of the origin that has this folder open (by its name), and the write is read
// back, as BrowserAdapter does. Another program writing the same folder at that moment is not
// excluded by anything the browser offers; the read-back catches most of it.
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
	if back, _, err := a.read(ctx, id); err != nil || Digest(back) != Digest(data) {
		return ErrConflict
	}
	return nil
}

// Usage implements QuotaReporter for the origin-private file system, whose room is the
// browser's storage quota of the site. For a folder the person chose the browser does not
// say how much room the disk has, so there is no answer.
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
