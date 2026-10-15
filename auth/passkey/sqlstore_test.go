//go:build !js

package passkey

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

func newSQLStore(t *testing.T) *SQLStore {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "p.db")+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := &SQLStore{DB: db}
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSQLStoreKeepsAccountsAndFindsThemByHandle(t *testing.T) {
	ctx := context.Background()
	s := newSQLStore(t)
	a := &Account{ID: "passkey:1", Handle: []byte("handle-1"), Email: "ana@x.io", Name: "Ana"}
	if err := s.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, a); !errors.Is(err, ErrExists) {
		t.Errorf("a second create: %v", err)
	}
	if err := s.Create(ctx, &Account{ID: "passkey:2", Handle: []byte("handle-1")}); err == nil {
		t.Error("two accounts with one handle")
	}
	got, err := s.ByHandle(ctx, []byte("handle-1"))
	if err != nil || got.ID != "passkey:1" || got.Email != "ana@x.io" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := s.Get(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	a.Name = "Ana B"
	if err := s.Put(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, "passkey:1"); got.Name != "Ana B" {
		t.Errorf("%+v", got)
	}
	if err := s.Update(ctx, "nobody", func(*Account) error { return nil }); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	boom := errors.New("boom")
	if err := s.Update(ctx, "passkey:1", func(a *Account) error { a.Name = "no"; return boom }); !errors.Is(err, boom) {
		t.Errorf("%v", err)
	}
	if got, _ := s.Get(ctx, "passkey:1"); got.Name != "Ana B" {
		t.Errorf("a failed update writes nothing: %+v", got)
	}
	if err := s.Delete(ctx, "passkey:1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "passkey:1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if _, err := s.ByHandle(ctx, []byte("handle-1")); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func TestSQLStoreUpdatesFromSeveralReplicasLoseNothing(t *testing.T) {
	ctx := context.Background()
	s := newSQLStore(t)
	if err := s.Create(ctx, &Account{ID: "passkey:1", Handle: []byte("h")}); err != nil {
		t.Fatal(err)
	}
	const writers, each = 5, 8
	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				if err := s.Update(ctx, "passkey:1", func(a *Account) error {
					a.Credentials = append(a.Credentials, Credential{Label: "k"})
					return nil
				}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	a, _ := s.Get(ctx, "passkey:1")
	if len(a.Credentials) != writers*each {
		t.Errorf("%d credentials, want %d", len(a.Credentials), writers*each)
	}
}

func TestSQLStoreDollarPlaceholders(t *testing.T) {
	s := &SQLStore{Dialect: DialectDollar}
	if got := s.q("a = ? AND b = ? AND c = ?"); got != "a = $1 AND b = $2 AND c = $3" {
		t.Error(got)
	}
}

func TestTheWholeServiceRunsOnASQLStore(t *testing.T) {
	r := newRig(t, newSQLStore(t))
	tok := r.register("ana@opendocs.io", "")
	if tok == "" {
		t.Fatal("no session")
	}
	if code, out := r.login(); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	// A second first-passkey ceremony for the account loses to the first.
	code, out := r.login()
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	cs, err := r.svc.Credentials(context.Background(), out["user"].(map[string]any)["ID"].(string))
	if err != nil || len(cs) != 1 || cs[0].Cred.Authenticator.SignCount != 2 {
		t.Errorf("%+v %v", cs, err)
	}
}
