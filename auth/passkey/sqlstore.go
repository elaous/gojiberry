package passkey

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Dialect says how a SQL database spells the few things SQLStore needs.
type Dialect int

const (
	// DialectQuestion is "?" placeholders (SQLite, MySQL).
	DialectQuestion Dialect = iota
	// DialectDollar is "$1" placeholders (PostgreSQL).
	DialectDollar
)

// SQLStore keeps accounts in two columns' worth of a SQL table, not in a docstore: one row per
// account with its ID, the user handle (unique, indexed), a version counter and the account as
// JSON. It implements Store, Updater and Creator over any database/sql driver, with no
// dependency on one: pass the *sql.DB and the Dialect. Update is an optimistic compare-and-set
// on the version, so replicas on one database never lose a change (the same guarantee
// AdapterStore gives over a conditional write); Create is the primary key.
//
// The rows hold public keys and e-mail addresses, not secrets, but they are personal data.
type SQLStore struct {
	DB      *sql.DB
	Dialect Dialect
	// Table is the table's name; "passkey_accounts" if empty. It is put into statements as is,
	// so it must come from the program, never from a request.
	Table string
}

// Schema is the statement that makes the table (and the handle index) if they are not there; run
// it once at start, or put the same in a migration.
func (s *SQLStore) Schema() string {
	t := s.table()
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %[1]s (
	id TEXT PRIMARY KEY,
	handle TEXT NOT NULL UNIQUE,
	version BIGINT NOT NULL,
	data TEXT NOT NULL
)`, t)
}

// Init runs Schema.
func (s *SQLStore) Init(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, s.Schema())
	return err
}

func (s *SQLStore) table() string {
	if s.Table == "" {
		return "passkey_accounts"
	}
	return s.Table
}

// q turns "?" placeholders into the dialect's.
func (s *SQLStore) q(query string) string {
	if s.Dialect != DialectDollar {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func handleText(h []byte) string { return base64.RawURLEncoding.EncodeToString(h) }

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *SQLStore) read(ctx context.Context, q queryer, where string, arg any) (*Account, int64, error) {
	var data string
	var version int64
	err := q.QueryRowContext(ctx, s.q(`SELECT data, version FROM `+s.table()+` WHERE `+where+` = ?`), arg).Scan(&data, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	var a Account
	if err := json.Unmarshal([]byte(data), &a); err != nil {
		return nil, 0, err
	}
	return &a, version, nil
}

// Get implements Store.
func (s *SQLStore) Get(ctx context.Context, id string) (*Account, error) {
	a, _, err := s.read(ctx, s.DB, "id", id)
	return a, err
}

// ByHandle implements Store.
func (s *SQLStore) ByHandle(ctx context.Context, handle []byte) (*Account, error) {
	a, _, err := s.read(ctx, s.DB, "handle", handleText(handle))
	return a, err
}

// Create implements Creator: the primary key (or the unique handle) refuses a second account.
func (s *SQLStore) Create(ctx context.Context, a *Account) error {
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, s.q(`INSERT INTO `+s.table()+` (id, handle, version, data) VALUES (?, ?, 1, ?) ON CONFLICT DO NOTHING`),
		a.ID, handleText(a.Handle), string(data))
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrExists
	}
	return nil
}

// Put implements Store: create the account or replace what it had.
func (s *SQLStore) Put(ctx context.Context, a *Account) error {
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, s.q(`INSERT INTO `+s.table()+` (id, handle, version, data) VALUES (?, ?, 1, ?)
		ON CONFLICT (id) DO UPDATE SET handle = excluded.handle, data = excluded.data, version = `+s.table()+`.version + 1`),
		a.ID, handleText(a.Handle), string(data))
	return err
}

// Update implements Updater: read the row, change it, write it only if its version is still the
// one read, else read again and call fn again.
func (s *SQLStore) Update(ctx context.Context, id string, fn func(*Account) error) error {
	for try := 0; try < maxUpdateTries; try++ {
		a, version, err := s.read(ctx, s.DB, "id", id)
		if err != nil {
			return err
		}
		if err := fn(a); err != nil {
			return err
		}
		data, err := json.Marshal(a)
		if err != nil {
			return err
		}
		res, err := s.DB.ExecContext(ctx, s.q(`UPDATE `+s.table()+` SET data = ?, handle = ?, version = version + 1 WHERE id = ? AND version = ?`),
			string(data), handleText(a.Handle), id, version)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 1 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(1+try) * time.Millisecond):
		}
	}
	return errors.New("passkey: the account kept changing under the update; try again")
}

// Delete implements Store.
func (s *SQLStore) Delete(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, s.q(`DELETE FROM `+s.table()+` WHERE id = ?`), id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

var (
	_ Store   = (*SQLStore)(nil)
	_ Updater = (*SQLStore)(nil)
	_ Creator = (*SQLStore)(nil)
)
