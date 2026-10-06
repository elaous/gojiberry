//go:build !js

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/elaous/gojiberry/docstore"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Config is the postgres backend's own config, decoded from
// Config.BackendConfig when Config.Backend == "postgres".
type Config struct {
	// DSN is a standard Postgres connection string, e.g.
	// "postgres://user:pass@host:5432/dbname".
	DSN string `json:"dsn"`
}

func init() {
	docstore.RegisterAdapter("postgres", func(ctx context.Context, raw json.RawMessage) (docstore.Adapter, error) {
		var cfg Config
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return nil, err
			}
		}
		if cfg.DSN == "" {
			return nil, errors.New(`docstore: postgres backend requires a "dsn"`)
		}
		return New(ctx, cfg.DSN)
	})
}

// Adapter stores documents in a single table:
//
//	CREATE TABLE IF NOT EXISTS documents (
//	    id         TEXT PRIMARY KEY,
//	    data       BYTEA NOT NULL,
//	    updated_at TIMESTAMPTZ NOT NULL
//	)
//
// New creates this table itself if it does not already
// exist, so an operator only needs a reachable database, not a
// separate migration step.
type Adapter struct {
	db    *sql.DB
	table string
}

// New opens dsn (a standard Postgres connection
// string) via pgx's database/sql driver and ensures the documents
// table exists.
func New(ctx context.Context, dsn string) (*Adapter, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	a := &Adapter{db: db, table: "documents"}
	if err := a.ensureTable(ctx); err != nil {
		_ = db.Close() // best-effort cleanup on the ensureTable failure path
		return nil, err
	}
	return a, nil
}

// newFromDB wraps an already-open *sql.DB (e.g. one
// wired to sqlmock) without touching the schema, for native testing.
func newFromDB(db *sql.DB) *Adapter {
	return &Adapter{db: db, table: "documents"}
}

func (a *Adapter) ensureTable(ctx context.Context) error {
	_, err := a.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS documents (
		id TEXT PRIMARY KEY,
		data BYTEA NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL
	)`)
	return err
}

func (a *Adapter) Close() error { return a.db.Close() }

func (a *Adapter) Save(ctx context.Context, id string, data []byte) error {
	_, err := a.db.ExecContext(ctx, `INSERT INTO documents (id, data, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET data = EXCLUDED.data, updated_at = EXCLUDED.updated_at`,
		id, data, time.Now())
	return err
}

// SaveIf implements docstore.ConditionalAdapter in one transaction: the row is read FOR
// UPDATE, compared with prevDigest (its SHA-256, "" when it must not exist) and written; a
// row inserted by someone else in the meantime makes the insert affect nothing, which is a
// conflict.
func (a *Adapter) SaveIf(ctx context.Context, id string, data []byte, prevDigest string) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var cur []byte
	err = tx.QueryRowContext(ctx, `SELECT data FROM documents WHERE id = $1 FOR UPDATE`, id).Scan(&cur)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if prevDigest != "" {
			return docstore.ErrConflict
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO documents (id, data, updated_at) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING`, id, data, time.Now())
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return docstore.ErrConflict
		}
	case err != nil:
		return err
	default:
		if prevDigest == "" || docstore.Digest(cur) != prevDigest {
			return docstore.ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE documents SET data = $2, updated_at = $3 WHERE id = $1`, id, data, time.Now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *Adapter) Load(ctx context.Context, id string) (docstore.Document, error) {
	var doc docstore.Document
	doc.ID = id
	row := a.db.QueryRowContext(ctx, `SELECT data, updated_at FROM documents WHERE id = $1`, id)
	if err := row.Scan(&doc.Data, &doc.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return docstore.Document{}, docstore.ErrNotFound
		}
		return docstore.Document{}, err
	}
	return doc, nil
}

func (a *Adapter) List(ctx context.Context) ([]docstore.Meta, error) {
	return a.list(ctx, `SELECT id, length(data), updated_at FROM documents`)
}

// likeEscaper escapes what LIKE treats as a pattern (backslash is the escape character).
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// ListPrefix implements docstore.PrefixLister: the database filters, with a LIKE whose
// pattern is the prefix with its wildcards escaped.
func (a *Adapter) ListPrefix(ctx context.Context, prefix string) ([]docstore.Meta, error) {
	return a.list(ctx, `SELECT id, length(data), updated_at FROM documents WHERE id LIKE $1 ESCAPE '\'`, likeEscaper.Replace(prefix)+"%")
}

func (a *Adapter) list(ctx context.Context, query string, args ...any) ([]docstore.Meta, error) {
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var metas []docstore.Meta
	for rows.Next() {
		var m docstore.Meta
		if err := rows.Scan(&m.ID, &m.Size, &m.UpdatedAt); err != nil {
			return nil, err
		}
		metas = append(metas, m)
	}
	return metas, rows.Err()
}

func (a *Adapter) Delete(ctx context.Context, id string) error {
	res, err := a.db.ExecContext(ctx, `DELETE FROM documents WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return docstore.ErrNotFound
	}
	return nil
}
