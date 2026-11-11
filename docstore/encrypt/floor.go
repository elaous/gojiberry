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

// KV is a small string store for floor values.
type KV interface {
	Get(key string) (string, bool, error)
	// Set stores value under key.
	Set(key, value string) error
}

// ErrFloorDegraded is wrapped by errors from a degraded floor.
var ErrFloorDegraded = errors.New("encrypt: the sequence floor's storage failed: it works from memory only and will not survive a reload")

var (
	floorHandler atomic.Pointer[func(error)]
	floorLogged  atomic.Pointer[string]
)

// SetFloorDegradedHandler sets the function called on degradation.
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

func floorGet(ctx context.Context, f SeqFloor, id string) (uint64, error) {
	n, err := f.Get(ctx, id)
	if errors.Is(err, ErrFloorDegraded) {
		noteFloorDegraded(err)
		return n, nil
	}
	return n, err
}

func floorRaise(ctx context.Context, f SeqFloor, id string, seq uint64) error {
	err := f.Raise(ctx, id, seq)
	if errors.Is(err, ErrFloorDegraded) {
		noteFloorDegraded(err)
		return nil
	}
	return err
}

// BatchSeqFloor is a SeqFloor that raises many documents at once.
type BatchSeqFloor interface {
	SeqFloor
	// RaiseMany raises each document to its value.
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

// StorageSeqFloor is a SeqFloor over a KV store.
type StorageSeqFloor struct {
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

func (f *StorageSeqFloor) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

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

// Get implements SeqFloor: the higher of KV and memory values.
func (f *StorageSeqFloor) Get(ctx context.Context, docID string) (uint64, error) {
	m, _ := f.mem.Get(ctx, docID)
	n, err := f.stored(docID)
	if err != nil {
		return max(n, m), f.fail(err)
	}
	return max(n, m), nil
}

// Raise implements SeqFloor; after a KV failure memory is used.
func (f *StorageSeqFloor) Raise(ctx context.Context, docID string, seq uint64) error {
	_ = f.mem.Raise(ctx, docID, seq)
	target, _ := f.mem.Get(ctx, docID)
	n, err := f.stored(docID)
	if err != nil {
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

// RaiseMany implements BatchSeqFloor with one KV write per document.
func (f *StorageSeqFloor) RaiseMany(ctx context.Context, seqs map[string]uint64) error {
	var errs []error
	for id, seq := range seqs {
		if err := f.Raise(ctx, id, seq); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// FileSeqFloor is a SeqFloor kept in a JSON file.
type FileSeqFloor struct {
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

// RaiseMany implements BatchSeqFloor: each value is raised.
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
		f.loaded = false
		return nil
	}
	f.seqs, f.stamp, f.loaded = next, stamp, true
	return nil
}

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
