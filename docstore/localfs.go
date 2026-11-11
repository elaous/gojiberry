package docstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// LocalFSConfig is the localfs backend's configuration.
type LocalFSConfig struct {
	// Dir is where documents are stored.
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

// LocalFSAdapter stores each document as one file.
type LocalFSAdapter struct {
	dir string
	mu  sync.Mutex
}

// NewLocalFSAdapter stores documents under dir.
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
	unlock, err := a.lockDir()
	if err != nil {
		return err
	}
	defer unlock()
	return a.writeFile(id, data)
}

// writeFile replaces the file atomically.
func (a *LocalFSAdapter) writeFile(id string, data []byte) error {
	err := a.writeFile1(id, data)
	if errors.Is(err, syscall.ENOSPC) {
		err = fmt.Errorf("%w: %w", ErrStorageFull, err)
	}
	return err
}

func (a *LocalFSAdapter) writeFile1(id string, data []byte) error {
	tmp := a.path(id) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, a.path(id)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncDir(a.dir)
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

// LoadHead implements HeadReader by reading the file start.
func (a *LocalFSAdapter) LoadHead(_ context.Context, id string, n int) ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := os.Open(a.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, int64(max(n, 0))))
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

// ListPrefix implements PrefixLister.
func (a *LocalFSAdapter) ListPrefix(ctx context.Context, prefix string) ([]Meta, error) {
	all, err := a.List(ctx)
	if err != nil {
		return nil, err
	}
	var metas []Meta
	for _, m := range all {
		if strings.HasPrefix(m.ID, prefix) {
			metas = append(metas, m)
		}
	}
	return metas, nil
}

func (a *LocalFSAdapter) Delete(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	unlock, err := a.lockDir()
	if err != nil {
		return err
	}
	defer unlock()
	err = os.Remove(a.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}
