package encrypt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// The floors below keep the highest record seq seen per document somewhere the store's writers
// cannot reach, so that a record older than one this client has seen, or a deleted one, is
// refused (ErrStaleRecord) even where the file frames cannot tell (a document whose files are
// still v1, or whose files were all replaced). They are the optional belt and braces of
// docs/encryption-and-passkeys.md C.5: the frames inside the encryption need no client state.

// KV is the little string store a StorageSeqFloor keeps its values in (the browser's
// localStorage, see LocalStorageSeqFloor in a js/wasm build).
type KV interface {
	// Get returns the value of key, and false when there is none.
	Get(key string) (string, bool, error)
	// Set stores value under key.
	Set(key, value string) error
}

// StorageSeqFloor is a SeqFloor over a KV: one entry per document, so concurrent tabs or
// processes only ever write their own document's value. The highest value is kept (a lower one
// changes nothing). If the KV fails (a private window, blocked or full storage) the floor
// keeps working from memory for the life of the process, as MemorySeqFloor does: availability
// is not traded for a belt-and-braces layer. Err says whether that has happened.
type StorageSeqFloor struct {
	// Prefix is put in front of the document ID to make the key.
	Prefix string
	Store  KV

	mem MemorySeqFloor
	mu  sync.Mutex
	err error
}

func (f *StorageSeqFloor) fail(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
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

// Get implements SeqFloor: the higher of what the KV holds and what this process raised.
func (f *StorageSeqFloor) Get(ctx context.Context, docID string) (uint64, error) {
	m, _ := f.mem.Get(ctx, docID)
	n, err := f.stored(docID)
	if err != nil {
		f.fail(err)
	}
	return max(n, m), nil
}

// Raise implements SeqFloor.
func (f *StorageSeqFloor) Raise(ctx context.Context, docID string, seq uint64) error {
	_ = f.mem.Raise(ctx, docID, seq)
	n, err := f.stored(docID)
	if err != nil {
		f.fail(err)
	} else if n >= seq {
		return nil
	}
	if err := f.Store.Set(f.Prefix+docID, strconv.FormatUint(seq, 10)); err != nil {
		f.fail(err)
	}
	return nil
}

// FileSeqFloor is a SeqFloor kept in a JSON file ({"<doc id>": seq}), for a server or a
// command-line client: keep the file where the store's writers cannot write (not in the store).
// It is written atomically (a temporary file renamed over it, mode 0600) and, unlike the
// browser's floor, a failure to read or write it is returned: a server that cannot keep its
// floor says so. Two processes sharing the file merge by taking the higher value on every
// write, but a write racing another's may lose a value: give each process its own file, or one
// writer.
type FileSeqFloor struct {
	// Path is the file. It is created on the first Raise; a missing file is an empty floor.
	Path string

	mu sync.Mutex
}

func (f *FileSeqFloor) read() (map[string]uint64, error) {
	b, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]uint64{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("encrypt: reading the floor: %w", err)
	}
	m := map[string]uint64{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("encrypt: the floor file %s is damaged: %w", f.Path, err)
	}
	return m, nil
}

// Get implements SeqFloor.
func (f *FileSeqFloor) Get(_ context.Context, docID string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.read()
	if err != nil {
		return 0, err
	}
	return m[docID], nil
}

// Raise implements SeqFloor.
func (f *FileSeqFloor) Raise(_ context.Context, docID string, seq uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.read()
	if err != nil {
		return err
	}
	if seq <= m[docID] {
		return nil
	}
	m[docID] = seq
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
	return nil
}
