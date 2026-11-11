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

func TestPostgresDeleteIf(t *testing.T) {
	sel := regexp.QuoteMeta(`SELECT data FROM documents WHERE id = $1 FOR UPDATE`)
	del := regexp.QuoteMeta(`DELETE FROM documents WHERE id = $1`)
	ctx := context.Background()
	newMock := func(t *testing.T) (*Adapter, sqlmock.Sqlmock) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return newFromDB(db), mock
	}
	var _ docstore.ConditionalDeleter = (*Adapter)(nil)
	t.Run("the document that was read", func(t *testing.T) {
		a, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(sel).WithArgs("x").WillReturnRows(sqlmock.NewRows([]string{"data"}).AddRow([]byte("one")))
		mock.ExpectExec(del).WithArgs("x").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		if err := a.DeleteIf(ctx, "x", docstore.Digest([]byte("one"))); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a document that changed, and one that is gone", func(t *testing.T) {
		a, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(sel).WithArgs("x").WillReturnRows(sqlmock.NewRows([]string{"data"}).AddRow([]byte("two")))
		mock.ExpectRollback()
		if err := a.DeleteIf(ctx, "x", docstore.Digest([]byte("one"))); !errors.Is(err, docstore.ErrConflict) {
			t.Errorf("changed: %v", err)
		}
		mock.ExpectBegin()
		mock.ExpectQuery(sel).WithArgs("x").WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()
		if err := a.DeleteIf(ctx, "x", docstore.Digest([]byte("one"))); !errors.Is(err, docstore.ErrNotFound) {
			t.Errorf("gone: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPostgresListPrefixEscapesTheLikePattern(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	a := newFromDB(db)
	var _ docstore.PrefixLister = a
	now := time.Now()
	for _, c := range []struct{ prefix, pattern string }{
		{"directory/ab/", `directory/ab/%`},
		{`a_b%c\d/`, `a\_b\%c\\d/%`}, // wildcards and the escape character are literal
		{"", `%`},
	} {
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, length(data), updated_at FROM documents WHERE id LIKE $1 ESCAPE '\'`)).
			WithArgs(c.pattern).
			WillReturnRows(sqlmock.NewRows([]string{"id", "length", "updated_at"}).AddRow(c.prefix+"1", 3, now))
		got, err := a.ListPrefix(context.Background(), c.prefix)
		if err != nil || len(got) != 1 || got[0].ID != c.prefix+"1" || got[0].Size != 3 {
			t.Fatalf("%q: %v %v", c.prefix, got, err)
		}
	}
	mock.ExpectQuery(`SELECT id`).WillReturnError(errors.New("boom"))
	if _, err := a.ListPrefix(context.Background(), "x/"); err == nil {
		t.Error("a database error is returned")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresLoadHeadCutsInTheDatabase(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	a := newFromDB(db)
	var _ docstore.HeadReader = a
	q := regexp.QuoteMeta(`SELECT substr(data, 1, $2) FROM documents WHERE id = $1`)
	mock.ExpectQuery(q).WithArgs("doc1", 4).
		WillReturnRows(sqlmock.NewRows([]string{"substr"}).AddRow([]byte("head")))
	got, err := a.LoadHead(context.Background(), "doc1", 4)
	if err != nil || string(got) != "head" {
		t.Fatalf("%q %v", got, err)
	}
	mock.ExpectQuery(q).WithArgs("nope", 4).WillReturnRows(sqlmock.NewRows([]string{"substr"}))
	if _, err := a.LoadHead(context.Background(), "nope", 4); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	mock.ExpectQuery(q).WillReturnError(errors.New("boom"))
	if _, err := a.LoadHead(context.Background(), "x", 4); err == nil {
		t.Error("a database error is returned")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
