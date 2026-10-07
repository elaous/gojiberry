package docstore

import (
	"context"
	"errors"
	"os"
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
	return a.writeFile(id, data)
}

// DeleteIf implements ConditionalDeleter under the same directory lock as SaveIf.
func (a *LocalFSAdapter) DeleteIf(_ context.Context, id string, prev string) error {
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
		return ErrNotFound
	case err != nil:
		return err
	case prev == "" || Digest(cur) != prev:
		return ErrConflict
	}
	err = os.Remove(a.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}
