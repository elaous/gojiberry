//go:build !js

package docstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite"
)

// SQLiteConfig is the sqlite backend's configuration.
type SQLiteConfig struct {
	// Path is the database file.
	Path string `json:"path"`
}

func init() {
	RegisterAdapter("sqlite", func(ctx context.Context, raw json.RawMessage) (Adapter, error) {
		var cfg SQLiteConfig
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return nil, err
			}
		}
		if cfg.Path == "" {
			return nil, errors.New(`docstore: sqlite backend requires a "path"`)
		}
		return NewSQLiteAdapter(ctx, cfg.Path)
	})
}

// SQLiteAdapter stores documents in one table.
type SQLiteAdapter struct{ db *sql.DB }

// NewSQLiteAdapter opens the database, creating it if needed.
func NewSQLiteAdapter(ctx context.Context, path string) (*SQLiteAdapter, error) {
	dsn := path
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=busy_timeout(10000)&_txlock=immediate"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// one connection: :memory: databases are per connection
	db.SetMaxOpenConns(1)
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS documents (
		id TEXT PRIMARY KEY,
		data BLOB NOT NULL,
		updated_at INTEGER NOT NULL
	)`)
	if err != nil {
		_ = db.Close() // best-effort cleanup on the failure path
		return nil, err
	}
	return &SQLiteAdapter{db: db}, nil
}

// Close closes the database.
func (a *SQLiteAdapter) Close() error { return a.db.Close() }

// Save implements Adapter.
func (a *SQLiteAdapter) Save(ctx context.Context, id string, data []byte) error {
	if data == nil {
		data = []byte{}
	}
	_, err := a.db.ExecContext(ctx, `INSERT INTO documents (id, data, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
		id, data, time.Now().UnixNano())
	return err
}

// Load implements Adapter.
func (a *SQLiteAdapter) Load(ctx context.Context, id string) (Document, error) {
	doc := Document{ID: id}
	var ns int64
	err := a.db.QueryRowContext(ctx, `SELECT data, updated_at FROM documents WHERE id = ?`, id).Scan(&doc.Data, &ns)
	if errors.Is(err, sql.ErrNoRows) {
		return Document{}, ErrNotFound
	}
	if err != nil {
		return Document{}, err
	}
	doc.UpdatedAt = time.Unix(0, ns)
	return doc, nil
}

// LoadHead implements HeadReader using substr.
func (a *SQLiteAdapter) LoadHead(ctx context.Context, id string, n int) ([]byte, error) {
	var head []byte
	err := a.db.QueryRowContext(ctx, `SELECT substr(data, 1, ?) FROM documents WHERE id = ?`, max(n, 0), id).Scan(&head)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return head, nil
}

// List implements Adapter.
func (a *SQLiteAdapter) List(ctx context.Context) ([]Meta, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id, length(data), updated_at FROM documents`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var metas []Meta
	for rows.Next() {
		var m Meta
		var ns int64
		if err := rows.Scan(&m.ID, &m.Size, &ns); err != nil {
			return nil, err
		}
		m.UpdatedAt = time.Unix(0, ns)
		metas = append(metas, m)
	}
	return metas, rows.Err()
}

// ListPrefix implements PrefixLister.
func (a *SQLiteAdapter) ListPrefix(ctx context.Context, prefix string) ([]Meta, error) {
	if prefix == "" {
		return a.List(ctx)
	}
	upper := []byte(prefix)
	if upper[len(upper)-1] == 0xff {
		all, err := a.List(ctx)
		if err != nil {
			return nil, err
		}
		var out []Meta
		for _, m := range all {
			if strings.HasPrefix(m.ID, prefix) {
				out = append(out, m)
			}
		}
		return out, nil
	}
	upper[len(upper)-1]++
	rows, err := a.db.QueryContext(ctx, `SELECT id, length(data), updated_at FROM documents WHERE id >= ? AND id < ?`, prefix, string(upper))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var metas []Meta
	for rows.Next() {
		var m Meta
		var ns int64
		if err := rows.Scan(&m.ID, &m.Size, &ns); err != nil {
			return nil, err
		}
		m.UpdatedAt = time.Unix(0, ns)
		if strings.HasPrefix(m.ID, prefix) {
			metas = append(metas, m)
		}
	}
	return metas, rows.Err()
}

// Delete implements Adapter.
func (a *SQLiteAdapter) Delete(ctx context.Context, id string) error {
	res, err := a.db.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}
