package docstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// LocalFSConfig is the localfs backend's own config, decoded from
// Config.BackendConfig when Config.Backend == "localfs".
type LocalFSConfig struct {
	// Dir is where documents are stored, created (with any missing
	// parents) on first use if it doesn't already exist. Defaults to
	// "./docstore-data" if empty.
	Dir string `json:"dir"`
}

func init() {
	RegisterAdapter("localfs", func(_ context.Context, raw json.RawMessage) (Adapter, error) {
		cfg := LocalFSConfig{Dir: "./docstore-data"}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return nil, err
			}
		}
		return NewLocalFSAdapter(cfg.Dir)
	})
}

// LocalFSAdapter stores each document as one file (base64url-encoded
// ID as the filename, to keep arbitrary document IDs safe on disk) in
// a single flat directory. It has no external dependencies, so it is
// the adapter of choice for a self-hosted single-machine deployment
// that wants real persistence without running a database.
type LocalFSAdapter struct {
	dir string
	mu  sync.Mutex
}

// NewLocalFSAdapter stores documents under dir, creating it (and any
// missing parents) if it does not already exist.
func NewLocalFSAdapter(dir string) (*LocalFSAdapter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &LocalFSAdapter{dir: dir}, nil
}

func (a *LocalFSAdapter) path(id string) string {
	return filepath.Join(a.dir, base64.RawURLEncoding.EncodeToString([]byte(id)))
}

func (a *LocalFSAdapter) Save(_ context.Context, id string, data []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	tmp := a.path(id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.path(id))
}

func (a *LocalFSAdapter) Load(_ context.Context, id string) (Document, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	data, err := os.ReadFile(a.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return Document{}, ErrNotFound
	}
	if err != nil {
		return Document{}, err
	}
	info, err := os.Stat(a.path(id))
	if err != nil {
		return Document{}, err
	}
	return Document{ID: id, Data: data, UpdatedAt: info.ModTime()}, nil
}

func (a *LocalFSAdapter) List(_ context.Context) ([]Meta, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		return nil, err
	}
	metas := make([]Meta, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) == ".tmp" {
			continue
		}
		idBytes, err := base64.RawURLEncoding.DecodeString(e.Name())
		if err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		metas = append(metas, Meta{ID: string(idBytes), UpdatedAt: info.ModTime(), Size: int(info.Size())})
	}
	return metas, nil
}

func (a *LocalFSAdapter) Delete(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	err := os.Remove(a.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}
