package docstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// SaveIf implements ConditionalAdapter. The directory lock (see lockDir) makes the
// check and the write one step across processes as well as goroutines.
func (a *LocalFSAdapter) SaveIf(_ context.Context, id string, data []byte, prev string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	unlock, err := a.lockDir()
	if err != nil {
		return err
	}
	defer unlock()
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
