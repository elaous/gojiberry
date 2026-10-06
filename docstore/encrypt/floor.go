package encrypt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// The floors below keep the highest record seq seen per document somewhere the store's writers
// cannot reach, so that a record older than one this client has seen, or a deleted one, is
// refused (ErrStaleRecord) even where the file frames cannot tell (a document whose files are
// still v1, or whose files were all replaced). They are the optional belt and braces of
// docs/encryption-and-passkeys.md C.5: the frames inside the encryption need no state kept on the client; this floor is that state, and the only one.

// KV is the little string store a StorageSeqFloor keeps its values in (the browser's
// localStorage, see LocalStorageSeqFloor in a js/wasm build).
type KV interface {
	// Get returns the value of key, and false when there is none.
	Get(key string) (string, bool, error)
	// Set stores value under key.
	Set(key, value string) error
}

// ErrFloorDegraded is wrapped by the error a StorageSeqFloor returns from Get and Raise when its
// KV failed (a private window, blocked or full storage, a damaged value): the floor kept
// working from memory, and the value returned with it is the memory's (or what the KV did
// give), so a caller may go on but the floor no longer outlives this process, and the
// protection it adds ends at the next reload. The readers in this package (the keyring, Sharing)
// go on as well, and report it through SetFloorDegradedHandler (logged by default); a caller
// that uses the floor directly sees the error. StorageSeqFloor.Err says the same later.
var ErrFloorDegraded = errors.New("encrypt: the sequence floor's storage failed: it works from memory only and will not survive a reload")

var (
	floorHandler atomic.Pointer[func(error)]
	floorLogged  atomic.Pointer[string]
)

// SetFloorDegradedHandler sets the function called, with an error that wraps ErrFloorDegraded,
// each time a read or write of a floor made by this package's keyring or Sharing finds its
// storage failing, so that an application can show it (a banner: "this browser is not
// remembering what it saw"). Nil restores the default, which logs a message when it changes. It
// may be called from any goroutine.
func SetFloorDegradedHandler(fn func(error)) {
	if fn == nil {
		floorHandler.Store(nil)
		return
	}
	floorHandler.Store(&fn)
}

func noteFloorDegraded(err error) {
	if h := floorHandler.Load(); h != nil {
		(*h)(err)
		return
	}
	msg := err.Error()
	if last := floorLogged.Load(); last == nil || *last != msg {
		floorLogged.Store(&msg)
		log.Print(msg)
	}
}

// floorGet is floor.Get for the readers here: a degraded floor is noted and its value used.
func floorGet(ctx context.Context, f SeqFloor, id string) (uint64, error) {
	n, err := f.Get(ctx, id)
	if errors.Is(err, ErrFloorDegraded) {
		noteFloorDegraded(err)
		return n, nil
	}
	return n, err
}

// floorRaise is floor.Raise for the writers here, with the same rule.
func floorRaise(ctx context.Context, f SeqFloor, id string, seq uint64) error {
	err := f.Raise(ctx, id, seq)
	if errors.Is(err, ErrFloorDegraded) {
		noteFloorDegraded(err)
		return nil
	}
	return err
}

// BatchSeqFloor is a SeqFloor that can raise many documents at once, with one write: what
// VerifyIndex uses (a SeqFloor without it is raised one by one).
type BatchSeqFloor interface {
	SeqFloor
	// RaiseMany raises each document to its value, as Raise does for one.
	RaiseMany(ctx context.Context, seqs map[string]uint64) error
}

func floorRaiseMany(ctx context.Context, f SeqFloor, seqs map[string]uint64) error {
	if len(seqs) == 0 {
		return nil
	}
	if b, ok := f.(BatchSeqFloor); ok {
		err := b.RaiseMany(ctx, seqs)
		if errors.Is(err, ErrFloorDegraded) {
			noteFloorDegraded(err)
			return nil
		}
		return err
	}
	ids := make([]string, 0, len(seqs))
	for id := range seqs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := floorRaise(ctx, f, id, seqs[id]); err != nil {
			return err
		}
	}
	return nil
}

// StorageSeqFloor is a SeqFloor over a KV: one entry per document, so concurrent tabs or
// processes only ever write their own document's value. The highest value is kept (a lower one
// changes nothing). If the KV fails (a private window, blocked or full storage) the floor
// keeps working from memory for the life of the process, as MemorySeqFloor does: availability
// is not traded for a belt-and-braces layer. It does not do so silently: Get and Raise return
// the value together with an error that wraps ErrFloorDegraded (the keyring and Sharing here
// note it and go on, see SetFloorDegradedHandler), and Err keeps the last failure for an
// application that polls (a page showing that this browser is not remembering what it saw).
type StorageSeqFloor struct {
	// Prefix is put in front of the document ID to make the key.
	Prefix string
	Store  KV

	mem MemorySeqFloor
	mu  sync.Mutex
	err error
}

func (f *StorageSeqFloor) fail(err error) error {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
	return fmt.Errorf("%w: %w", ErrFloorDegraded, err)
}

// Err returns the last error of the KV, nil if it has not failed.
func (f *StorageSeqFloor) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

// stored reads the KV's value: 0 when there is none, an error when the KV failed or holds
// something that is not a number.
func (f *StorageSeqFloor) stored(docID string) (uint64, error) {
	v, ok, err := f.Store.Get(f.Prefix + docID)
	if err != nil || !ok {
		return 0, err
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("encrypt: the stored floor of %s is damaged: %w", docID, err)
	}
	return n, nil
}

// Get implements SeqFloor: the higher of what the KV holds and what this process raised. When
// the KV failed the value is the memory's and the error wraps ErrFloorDegraded.
func (f *StorageSeqFloor) Get(ctx context.Context, docID string) (uint64, error) {
	m, _ := f.mem.Get(ctx, docID)
	n, err := f.stored(docID)
	if err != nil {
		return max(n, m), f.fail(err)
	}
	return max(n, m), nil
}

// Raise implements SeqFloor. When the KV failed the memory was still raised and the error wraps
// ErrFloorDegraded.
func (f *StorageSeqFloor) Raise(ctx context.Context, docID string, seq uint64) error {
	_ = f.mem.Raise(ctx, docID, seq)
	// What is written is the highest this process knows (its memory holds every value it raised
	// or read, and at least seq), so a damaged stored value, which cannot be compared, is never
	// replaced by something lower than what this process has already seen.
	target, _ := f.mem.Get(ctx, docID)
	n, err := f.stored(docID)
	if err != nil {
		// Writing over a damaged value is the one way to repair it, so it is tried.
		if serr := f.Store.Set(f.Prefix+docID, strconv.FormatUint(target, 10)); serr != nil {
			return f.fail(errors.Join(err, serr))
		}
		return f.fail(err)
	} else if n >= target {
		return nil
	}
	if err := f.Store.Set(f.Prefix+docID, strconv.FormatUint(target, 10)); err != nil {
		return f.fail(err)
	}
	return nil
}

// RaiseMany implements BatchSeqFloor (one KV write per document: the KV has no batch).
func (f *StorageSeqFloor) RaiseMany(ctx context.Context, seqs map[string]uint64) error {
	var errs []error
	for id, seq := range seqs {
		if err := f.Raise(ctx, id, seq); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// FileSeqFloor is a SeqFloor kept in a JSON file ({"<doc id>": seq}), for a server or a
// command-line client: keep the file where the store's writers cannot write (not in the store).
// It is written atomically (a temporary file renamed over it, mode 0600, the file and then its
// directory synced) and, unlike the browser's floor, a failure to read or write it is returned:
// a server that cannot keep its floor says so.
//
// The file is read once and kept in memory (written through on a raise, and read again only if
// the file changed on disk: its size or time), so a Get costs a stat and a Raise of a value
// that does not exceed the held one costs nothing; RaiseMany raises any number of documents
// with one write. Two processes sharing the file merge by taking the higher value on every
// write, but a write racing another's may lose a value: give each process its own file, or one
// writer.
//
// Runbook, a damaged floor file: Get and Raise return an error naming the file and nothing is
// overwritten. Move the file away (or delete it) and run Sharing.VerifyIndex: the floor starts
// empty and the index raises it for every end-to-end document (the documents the index does not
// list have no floor until their records are next read). Whatever was only in the lost file,
// such as a floor for a document that is no longer listed, is gone: that protection resumes from
// what the store says now, as for a fresh client.
type FileSeqFloor struct {
	// Path is the file. It is created on the first Raise; a missing file is an empty floor.
	Path string

	mu     sync.Mutex
	seqs   map[string]uint64 // what the file held when last read or written
	stamp  fileStamp
	loaded bool

	reads, writes int // for tests
}

type fileStamp struct {
	exists  bool
	size    int64
	modTime time.Time
}

func (f *FileSeqFloor) stat() (fileStamp, error) {
	st, err := os.Stat(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return fileStamp{}, nil
	}
	if err != nil {
		return fileStamp{}, fmt.Errorf("encrypt: reading the floor: %w", err)
	}
	return fileStamp{true, st.Size(), st.ModTime()}, nil
}

// load returns the floor's values, reading the file only if it is not the one last seen.
func (f *FileSeqFloor) load() (map[string]uint64, error) {
	stamp, err := f.stat()
	if err != nil {
		return nil, err
	}
	if f.loaded && stamp == f.stamp {
		return f.seqs, nil
	}
	m := map[string]uint64{}
	if stamp.exists {
		f.reads++
		b, err := os.ReadFile(f.Path)
		if errors.Is(err, os.ErrNotExist) {
			stamp = fileStamp{}
		} else if err != nil {
			return nil, fmt.Errorf("encrypt: reading the floor: %w", err)
		} else if err := json.Unmarshal(b, &m); err != nil {
			f.loaded = false
			return nil, fmt.Errorf("encrypt: the floor file %s is damaged (move it away and run Sharing.VerifyIndex): %w", f.Path, err)
		}
	}
	f.seqs, f.stamp, f.loaded = m, stamp, true
	return m, nil
}

// Get implements SeqFloor.
func (f *FileSeqFloor) Get(_ context.Context, docID string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return 0, err
	}
	return m[docID], nil
}

// Raise implements SeqFloor.
func (f *FileSeqFloor) Raise(ctx context.Context, docID string, seq uint64) error {
	return f.RaiseMany(ctx, map[string]uint64{docID: seq})
}

// RaiseMany implements BatchSeqFloor: every value that is higher than the one held is raised
// with one write.
func (f *FileSeqFloor) RaiseMany(_ context.Context, seqs map[string]uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	next := make(map[string]uint64, len(m)+len(seqs))
	for k, v := range m {
		next[k] = v
	}
	changed := false
	for id, seq := range seqs {
		if seq > next[id] {
			next[id] = seq
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := f.write(next); err != nil {
		return err
	}
	stamp, err := f.stat()
	if err != nil {
		f.loaded = false // written; the next read finds out what is there
		return nil
	}
	f.seqs, f.stamp, f.loaded = next, stamp, true
	return nil
}

// syncDir makes a rename in dir durable. Not every platform can sync a directory (Windows, the
// js/wasm file system): there it is skipped.
var syncDir = func(dir string) error {
	if runtime.GOOS == "windows" || runtime.GOOS == "js" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

func (f *FileSeqFloor) write(m map[string]uint64) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	dir := filepath.Dir(f.Path)
	tmp, err := os.CreateTemp(dir, filepath.Base(f.Path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("encrypt: writing the floor: %w", err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(b)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, 0o600)
	}
	if werr == nil {
		werr = os.Rename(name, f.Path)
	}
	if werr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("encrypt: writing the floor: %w", werr)
	}
	f.writes++
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("encrypt: syncing the floor's directory: %w", err)
	}
	return nil
}
