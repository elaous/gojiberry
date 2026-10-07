//go:build !js

package docstore

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SaveIf implements ConditionalAdapter, in one transaction.
func (a *SQLiteAdapter) SaveIf(ctx context.Context, id string, data []byte, prev string) (err error) {
	if data == nil {
		data = []byte{}
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	var cur []byte
	row := tx.QueryRowContext(ctx, `SELECT data FROM documents WHERE id = ?`, id)
	switch scanErr := row.Scan(&cur); {
	case errors.Is(scanErr, sql.ErrNoRows):
		if prev != "" {
			return ErrConflict
		}
	case scanErr != nil:
		return scanErr
	case prev == "" || Digest(cur) != prev:
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO documents (id, data, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
		id, data, time.Now().UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteIf implements ConditionalDeleter, in one transaction.
func (a *SQLiteAdapter) DeleteIf(ctx context.Context, id string, prev string) (err error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	var cur []byte
	switch scanErr := tx.QueryRowContext(ctx, `SELECT data FROM documents WHERE id = ?`, id).Scan(&cur); {
	case errors.Is(scanErr, sql.ErrNoRows):
		return ErrNotFound
	case scanErr != nil:
		return scanErr
	case prev == "" || Digest(cur) != prev:
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
