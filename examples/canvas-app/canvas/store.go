// Package canvas is the xango-canvas demo: a bare-minimum Google-Docs
// -style typing canvas. Store holds the plain-Go business logic (DB
// queries, no framework dependency) that both the Encore-annotated
// handlers (api.go) and the plain net/http dev server
// (cmd/devserver) call into, so the two entry points share one
// implementation instead of duplicating it.
//
// Queries are plain database/sql, not xango's sqlc-backed .sql import:
// sqlc isn't installed in the environment this was built in, so using
// it here would mean shipping an example that can't actually be
// regenerated or verified. They're annotated with the xango VS Code
// extension's `//language=SQL` marker instead, for real editor value
// without depending on a tool this session couldn't install.
package canvas

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("canvas: document not found")

// Store is the canvas document store.
type Store struct {
	db *sql.DB
}

// NewStore wraps an existing *sql.DB (already migrated).
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// sqlQuery strips a leading `//language=SQL` Go comment line if
// present. The marker is a real editor annotation (see the xango VS
// Code extension), not SQL comment syntax -- Postgres only recognizes
// `--`, not `//` -- so it has to come out of the string before it's
// sent to the driver, or the "query" is actually "a Go comment
// followed by SQL," which is a syntax error.
func sqlQuery(q string) string {
	if i := strings.IndexByte(q, '\n'); i >= 0 {
		if first := strings.TrimSpace(q[:i]); strings.HasPrefix(first, "//") {
			return q[i+1:]
		}
	}
	return q
}

// DocumentSummary is one row of the home page's document list.
type DocumentSummary struct {
	ID      string
	Preview string
}

// CreateDocument inserts a new, empty document and returns its ID.
func (s *Store) CreateDocument(ctx context.Context) (string, error) {
	id := uuid.NewString()
	query := sqlQuery(`//language=SQL
		INSERT INTO documents (id, content) VALUES ($1, '')
	`)
	if _, err := s.db.ExecContext(ctx, query, id); err != nil {
		return "", fmt.Errorf("canvas: creating document: %w", err)
	}
	return id, nil
}

func (s *Store) GetDocument(ctx context.Context, id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	query := sqlQuery(`//language=SQL
		SELECT content FROM documents WHERE id = $1
	`)
	var content string
	if err := s.db.QueryRowContext(ctx, query, id).Scan(&content); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return "", fmt.Errorf("canvas: getting document %s: %w", id, err)
	}
	return content, nil
}

// SaveDocument overwrites a document's content.
func (s *Store) SaveDocument(ctx context.Context, id, content string) error {
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	query := sqlQuery(`//language=SQL
		UPDATE documents SET content = $1, updated_at = now() WHERE id = $2
	`)
	res, err := s.db.ExecContext(ctx, query, content, id)
	if err != nil {
		return fmt.Errorf("canvas: saving document %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("canvas: saving document %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return nil
}

func (s *Store) ListDocuments(ctx context.Context) ([]DocumentSummary, error) {
	query := sqlQuery(`//language=SQL
		SELECT id, left(content, 60) FROM documents ORDER BY updated_at DESC
	`)
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("canvas: listing documents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []DocumentSummary
	for rows.Next() {
		var d DocumentSummary
		if err := rows.Scan(&d.ID, &d.Preview); err != nil {
			return nil, fmt.Errorf("canvas: listing documents: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
