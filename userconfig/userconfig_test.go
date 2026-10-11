package userconfig

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"filippo.io/age"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

func newStore() (*Store, *docstore.MemoryAdapter) {
	m := docstore.NewMemoryAdapter()
	return &Store{A: m, User: "ana@example.com"}, m
}

func TestPutGetRevisionsAndConflicts(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore()
	if _, err := s.Get(ctx, "prefs"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	a, err := s.Put(ctx, "prefs", []byte(`{"font":18}`), 0)
	if err != nil || a.Rev != 1 {
		t.Fatalf("%+v %v", a, err)
	}
	got, err := s.Get(ctx, "prefs")
	if err != nil || string(got.Data) != `{"font":18}` || got.Rev != 1 || got.Name != "prefs" || got.Updated.IsZero() {
		t.Fatalf("%+v %v", got, err)
	}
	// device B still thinks the item does not exist; device C is on revision 1
	cur, err := s.Put(ctx, "prefs", []byte("b"), 0)
	if !errors.Is(err, ErrConflict) || cur.Rev != 1 || string(cur.Data) != `{"font":18}` {
		t.Fatalf("a stale write is refused and shows the current item: %+v %v", cur, err)
	}
	if b, err := s.Put(ctx, "prefs", []byte("c"), 1); err != nil || b.Rev != 2 {
		t.Fatalf("%+v %v", b, err)
	}
	if _, err := s.Put(ctx, "prefs", []byte("late"), 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("%v", err)
	}
}

func TestUpdateMergesAcrossDevices(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore()
	if _, err := s.Put(ctx, "tags", []byte("a"), 0); err != nil {
		t.Fatal(err)
	}
	// every device appends its own letter; two goroutines racing must both land
	var wg sync.WaitGroup
	for _, l := range []string{"b", "c", "d"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Update(ctx, "tags", func(cur Item) ([]byte, error) { return append(bytes.Clone(cur.Data), l...), nil })
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, _ := s.Get(ctx, "tags")
	if len(got.Data) != 4 || got.Rev != 4 || !strings.HasPrefix(string(got.Data), "a") {
		t.Fatalf("%q rev %d: every device's change must survive", got.Data, got.Rev)
	}
	// a new item through Update, and a failing merge writes nothing
	if it, err := s.Update(ctx, "fresh", func(cur Item) ([]byte, error) { return []byte("x"), nil }); err != nil || it.Rev != 1 {
		t.Fatalf("%+v %v", it, err)
	}
	boom := errors.New("boom")
	if _, err := s.Update(ctx, "fresh", func(Item) ([]byte, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Fatalf("%v", err)
	}
	// a merge that never settles gives up
	n := 0
	_, err := s.Update(ctx, "fresh", func(cur Item) ([]byte, error) {
		n++
		if _, perr := s.Put(ctx, "fresh", []byte("rival"), cur.Rev); perr != nil { // the other device always wins first
			t.Fatal(perr)
		}
		return []byte("mine"), nil
	})
	if !errors.Is(err, ErrConflict) || n != 5 {
		t.Fatalf("%v after %d tries", err, n)
	}
}

func TestQuotaNamesAndListing(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore()
	s.MaxItem, s.MaxTotal = 10, 15
	if _, err := s.Put(ctx, "a", make([]byte, 11), 0); !errors.Is(err, ErrQuota) {
		t.Fatalf("one item over its limit: %v", err)
	}
	if _, err := s.Put(ctx, "a", []byte("12345"), 0); err != nil {
		t.Fatal(err)
	}
	// the envelope adds bytes, so the total limit is reached by the stored size, not the payload
	if _, err := s.Put(ctx, "b", []byte("123456789"), 0); !errors.Is(err, ErrQuota) {
		t.Fatalf("over the total: %v", err)
	}
	s.MaxTotal = 0
	if _, err := s.Put(ctx, "b", []byte("123456789"), 0); err != nil {
		t.Fatal(err)
	}
	names, err := s.List(ctx)
	if err != nil || strings.Join(names, ",") != "a,b" {
		t.Fatalf("%v %v", names, err)
	}
	for _, bad := range []string{"", "Upper", "has space", "slash/x", "../x", strings.Repeat("a", 65)} {
		if _, err := s.Put(ctx, bad, []byte("x"), 0); !errors.Is(err, ErrBadName) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := (&Store{A: s.A}).Get(ctx, "a"); !errors.Is(err, ErrBadName) {
		t.Fatal("no user, no keys")
	}
	if _, err := (&Store{A: s.A}).List(ctx); !errors.Is(err, ErrBadName) {
		t.Fatal("no user, no listing")
	}
}

func TestUsersAreSeparatedAndKeysHoldNoAddress(t *testing.T) {
	ctx := context.Background()
	m := docstore.NewMemoryAdapter()
	ana := &Store{A: m, User: "ana@example.com"}
	bo := &Store{A: m, User: "bo@example.com"}
	_, _ = ana.Put(ctx, "prefs", []byte("ana's"), 0)
	if _, err := bo.Get(ctx, "prefs"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bo sees ana's item: %v", err)
	}
	metas, _ := m.List(ctx)
	for _, meta := range metas {
		if strings.Contains(meta.ID, "ana") || !strings.HasPrefix(meta.ID, docstore.ConfigPrefix) {
			t.Fatalf("key %q", meta.ID)
		}
	}
	_, _ = ana.Put(ctx, "styles", []byte("s"), 0)
	if n, err := ana.DeleteAll(ctx); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	if names, _ := ana.List(ctx); len(names) != 0 {
		t.Fatal("all gone")
	}
	if err := ana.Delete(ctx, "prefs"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestStoredThroughTheEncryptingAdapterIsCiphertext(t *testing.T) {
	ctx := context.Background()
	id, _ := age.GenerateX25519Identity()
	raw := docstore.NewMemoryAdapter()
	enc := encrypt.New(raw, encrypt.Static{To: []age.Recipient{id.Recipient()}, Keys: []age.Identity{id}})
	s := &Store{A: enc, User: "ana@example.com"}
	if _, err := s.Put(ctx, "prefs", []byte("high contrast: on; dyslexia font: on"), 0); err != nil {
		t.Fatal(err)
	}
	metas, _ := raw.List(ctx)
	if len(metas) != 1 {
		t.Fatalf("%d", len(metas))
	}
	d, _ := raw.Load(ctx, metas[0].ID)
	if bytes.Contains(d.Data, []byte("contrast")) || bytes.Contains(d.Data, []byte("dyslexia")) {
		t.Fatal("the store holds the setting in the clear")
	}
	got, err := s.Get(ctx, "prefs")
	if err != nil || !strings.Contains(string(got.Data), "dyslexia") {
		t.Fatalf("%q %v", got.Data, err)
	}
}

type noCond struct{ docstore.Adapter }

func TestWorksWithoutConditionalSaves(t *testing.T) {
	ctx := context.Background()
	s := &Store{A: noCond{docstore.NewMemoryAdapter()}, User: "u"}
	if _, err := s.Put(ctx, "a", []byte("1"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "a", []byte("2"), 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("%v", err)
	}
}

type brokenLoad struct{ docstore.Adapter }

func (brokenLoad) Load(context.Context, string) (docstore.Document, error) {
	return docstore.Document{}, errors.New("disk")
}

func TestStoreErrorsAndCorruptItems(t *testing.T) {
	ctx := context.Background()
	m := docstore.NewMemoryAdapter()
	s := &Store{A: m, User: "u"}
	if _, err := (&Store{A: brokenLoad{m}, User: "u"}).Get(ctx, "a"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if _, err := (&Store{A: brokenLoad{m}, User: "u"}).Put(ctx, "a", []byte("x"), 0); err == nil {
		t.Fatal("a failing read fails the write")
	}
	_ = m.Save(ctx, s.prefix()+"junk", []byte("not json"))
	if _, err := s.Get(ctx, "junk"); err == nil || strings.Contains(err.Error(), "not json") {
		t.Fatalf("a corrupt item is an error that does not echo it: %v", err)
	}
}

// the document API and expiry treat the keyspace as the system's
func TestConfigKeysAreSystemKeys(t *testing.T) {
	if docstore.IsDocumentKey(docstore.ConfigPrefix + "abc/prefs") {
		t.Fatal("config items must never get a document lifetime")
	}
}
