// Package canvas: Store logic shared by Encore and net/http.
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

// sqlQuery strips a leading //language=SQL line: drivers reject it.
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
