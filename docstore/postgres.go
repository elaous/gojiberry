package docstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresConfig is the postgres backend's own config, decoded from
// Config.BackendConfig when Config.Backend == "postgres".
type PostgresConfig struct {
	// DSN is a standard Postgres connection string, e.g.
	// "postgres://user:pass@host:5432/dbname".
	DSN string `json:"dsn"`
}

func init() {
	RegisterAdapter("postgres", func(ctx context.Context, raw json.RawMessage) (Adapter, error) {
		var cfg PostgresConfig
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return nil, err
			}
		}
		if cfg.DSN == "" {
			return nil, errors.New(`docstore: postgres backend requires a "dsn"`)
		}
		return NewPostgresAdapter(ctx, cfg.DSN)
	})
}

// PostgresAdapter stores documents in a single table:
//
//	CREATE TABLE IF NOT EXISTS documents (
//	    id         TEXT PRIMARY KEY,
//	    data       BYTEA NOT NULL,
//	    updated_at TIMESTAMPTZ NOT NULL
//	)
//
// NewPostgresAdapter creates this table itself if it does not already
// exist, so an operator only needs a reachable database, not a
// separate migration step.
type PostgresAdapter struct {
	db    *sql.DB
	table string
}

// NewPostgresAdapter opens dsn (a standard Postgres connection
// string) via pgx's database/sql driver and ensures the documents
// table exists.
func NewPostgresAdapter(ctx context.Context, dsn string) (*PostgresAdapter, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	a := &PostgresAdapter{db: db, table: "documents"}
	if err := a.ensureTable(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return a, nil
}

// newPostgresAdapterFromDB wraps an already-open *sql.DB (e.g. one
// wired to sqlmock) without touching the schema, for native testing.
func newPostgresAdapterFromDB(db *sql.DB) *PostgresAdapter {
	return &PostgresAdapter{db: db, table: "documents"}
}

func (a *PostgresAdapter) ensureTable(ctx context.Context) error {
	_, err := a.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS documents (
		id TEXT PRIMARY KEY,
		data BYTEA NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL
	)`)
	return err
}

func (a *PostgresAdapter) Close() error { return a.db.Close() }

func (a *PostgresAdapter) Save(ctx context.Context, id string, data []byte) error {
	_, err := a.db.ExecContext(ctx, `INSERT INTO documents (id, data, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET data = EXCLUDED.data, updated_at = EXCLUDED.updated_at`,
		id, data, time.Now())
	return err
}

func (a *PostgresAdapter) Load(ctx context.Context, id string) (Document, error) {
	var doc Document
	doc.ID = id
	row := a.db.QueryRowContext(ctx, `SELECT data, updated_at FROM documents WHERE id = $1`, id)
	if err := row.Scan(&doc.Data, &doc.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Document{}, ErrNotFound
		}
		return Document{}, err
	}
	return doc, nil
}

func (a *PostgresAdapter) List(ctx context.Context) ([]Meta, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id, length(data), updated_at FROM documents`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var metas []Meta
	for rows.Next() {
		var m Meta
		if err := rows.Scan(&m.ID, &m.Size, &m.UpdatedAt); err != nil {
			return nil, err
		}
		metas = append(metas, m)
	}
	return metas, rows.Err()
}

func (a *PostgresAdapter) Delete(ctx context.Context, id string) error {
	res, err := a.db.ExecContext(ctx, `DELETE FROM documents WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
