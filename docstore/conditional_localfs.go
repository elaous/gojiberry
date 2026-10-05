package docstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// SaveIf implements ConditionalAdapter. A lock file next to the document makes
// the check and the write one step across processes as well as goroutines; a lock
// left behind by a crash is taken over after thirty seconds.
func (a *LocalFSAdapter) SaveIf(_ context.Context, id string, data []byte, prev string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	lock := a.path(id) + ".lock"
	for start := time.Now(); ; {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > 30*time.Second {
			_ = os.Remove(lock)
			continue
		}
		if time.Since(start) > 10*time.Second {
			return errors.New("docstore: the document is locked by another process")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() { _ = os.Remove(lock) }()
	cur, err := os.ReadFile(a.path(id))
	switch {
	case errors.Is(err, os.ErrNotExist):
		if prev != "" {
			return ErrConflict
		}
	case err != nil:
		return err
	case prev == "" || Digest(cur) != prev:
		return ErrConflict
	}
	tmp := filepath.Clean(a.path(id) + ".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.path(id))
}
