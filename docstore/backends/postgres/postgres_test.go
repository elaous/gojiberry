//go:build !js

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"github.com/elaous/gojiberry/docstore"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// Postgres is exercised with sqlmock instead of a live database: this
// sandbox has no Docker daemon available to run a real Postgres
// container, but the adapter's query-building and error-translation
// logic (docstore.ErrNotFound on a missing row, RowsAffected on delete) is
// fully native and worth covering without one.

func TestPostgresAdapterSaveInsertsWithUpsert(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = db.Close() }()
	a := newFromDB(db)

	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO documents (id, data, updated_at)`)).
		WithArgs("doc1", []byte("hello"), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := a.Save(context.Background(), "doc1", []byte("hello")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPostgresAdapterLoadReturnsErrNotFoundOnNoRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = db.Close() }()
	a := newFromDB(db)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT data, updated_at FROM documents WHERE id = $1`)).
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)

	_, err = a.Load(context.Background(), "missing")
	if !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("Load err = %v, want wrapping docstore.ErrNotFound", err)
	}
}

func TestPostgresAdapterLoadScansRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = db.Close() }()
	a := newFromDB(db)

	now := time.Now().Truncate(time.Second)
	rows := sqlmock.NewRows([]string{"data", "updated_at"}).AddRow([]byte("payload"), now)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT data, updated_at FROM documents WHERE id = $1`)).
		WithArgs("doc1").
		WillReturnRows(rows)

	doc, err := a.Load(context.Background(), "doc1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(doc.Data) != "payload" || doc.ID != "doc1" {
		t.Fatalf("Load = %+v, want data=payload id=doc1", doc)
	}
}

func TestPostgresAdapterDeleteReturnsErrNotFoundWhenNoRowsAffected(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = db.Close() }()
	a := newFromDB(db)

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM documents WHERE id = $1`)).
		WithArgs("missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = a.Delete(context.Background(), "missing")
	if !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("Delete err = %v, want docstore.ErrNotFound", err)
	}
}

func TestPostgresAdapterDeleteSucceedsWhenRowAffected(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = db.Close() }()
	a := newFromDB(db)

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM documents WHERE id = $1`)).
		WithArgs("doc1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := a.Delete(context.Background(), "doc1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestPostgresAdapterListScansAllRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = db.Close() }()
	a := newFromDB(db)

	now := time.Now().Truncate(time.Second)
	rows := sqlmock.NewRows([]string{"id", "length", "updated_at"}).
		AddRow("a", 3, now).
		AddRow("b", 5, now)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, length(data), updated_at FROM documents`)).
		WillReturnRows(rows)

	metas, err := a.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 2 || metas[0].ID != "a" || metas[1].ID != "b" {
		t.Fatalf("List = %+v, want [a b]", metas)
	}
}

func TestPostgresSaveIf(t *testing.T) {
	sel := regexp.QuoteMeta(`SELECT data FROM documents WHERE id = $1 FOR UPDATE`)
	ins := regexp.QuoteMeta(`INSERT INTO documents (id, data, updated_at) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING`)
	upd := regexp.QuoteMeta(`UPDATE documents SET data = $2, updated_at = $3 WHERE id = $1`)
	ctx := context.Background()
	newMock := func(t *testing.T) (*Adapter, sqlmock.Sqlmock) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return newFromDB(db), mock
	}
	t.Run("a new document", func(t *testing.T) {
		a, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(sel).WithArgs("x").WillReturnError(sql.ErrNoRows)
		mock.ExpectExec(ins).WithArgs("x", []byte("one"), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		if err := a.SaveIf(ctx, "x", []byte("one"), ""); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a new document someone else inserted first", func(t *testing.T) {
		a, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(sel).WithArgs("x").WillReturnError(sql.ErrNoRows)
		mock.ExpectExec(ins).WithArgs("x", []byte("one"), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectRollback()
		if err := a.SaveIf(ctx, "x", []byte("one"), ""); !errors.Is(err, docstore.ErrConflict) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("creating what exists, a stale digest, a document that is gone", func(t *testing.T) {
		a, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(sel).WithArgs("x").WillReturnRows(sqlmock.NewRows([]string{"data"}).AddRow([]byte("one")))
		mock.ExpectRollback()
		if err := a.SaveIf(ctx, "x", []byte("again"), ""); !errors.Is(err, docstore.ErrConflict) {
			t.Errorf("exists: %v", err)
		}
		mock.ExpectBegin()
		mock.ExpectQuery(sel).WithArgs("x").WillReturnRows(sqlmock.NewRows([]string{"data"}).AddRow([]byte("two")))
		mock.ExpectRollback()
		if err := a.SaveIf(ctx, "x", []byte("three"), docstore.Digest([]byte("one"))); !errors.Is(err, docstore.ErrConflict) {
			t.Errorf("stale: %v", err)
		}
		mock.ExpectBegin()
		mock.ExpectQuery(sel).WithArgs("x").WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()
		if err := a.SaveIf(ctx, "x", []byte("v"), docstore.Digest([]byte("v"))); !errors.Is(err, docstore.ErrConflict) {
			t.Errorf("gone: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a changed document", func(t *testing.T) {
		a, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(sel).WithArgs("x").WillReturnRows(sqlmock.NewRows([]string{"data"}).AddRow([]byte("one")))
		mock.ExpectExec(upd).WithArgs("x", []byte("two"), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		if err := a.SaveIf(ctx, "x", []byte("two"), docstore.Digest([]byte("one"))); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}
