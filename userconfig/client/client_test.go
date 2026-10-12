package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/userconfig"
	"github.com/elaous/gojiberry/userconfig/client"
)

// server runs the real Service; the person is named by the X-User header. up false makes it refuse.
type server struct {
	*httptest.Server
	store *docstore.MemoryAdapter
	up    atomic.Bool
}

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{store: docstore.NewMemoryAdapter()}
	s.up.Store(true)
	svc := &userconfig.Service{A: s.store, Who: func(r *http.Request) (string, bool) {
		u := r.Header.Get("X-User")
		return u, u != ""
	}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.up.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		svc.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func device(s *server, user string, cache client.Cache) *client.Client {
	c := client.New(s.URL, cache)
	c.Header = func(r *http.Request) {
		if user != "" {
			r.Header.Set("X-User", user)
		}
	}
	return c
}

func TestOfflineChangesAreQueuedAndSentLater(t *testing.T) {
	ctx := context.Background()
	s := newServer(t)
	cache := &client.MemoryCache{}
	c := device(s, "ana", cache)

	s.up.Store(false)
	c.Set("prefs", []byte("high contrast"))
	got, err := c.Get("prefs")
	if err != nil || string(got) != "high contrast" {
		t.Fatalf("the change is there at once, offline: %q %v", got, err)
	}
	if err := c.Sync(ctx); !errors.Is(err, client.ErrOffline) || c.Pending() != 1 {
		t.Fatalf("%v pending %d", err, c.Pending())
	}

	// the app is closed and opened again: the queue is in the cache
	reopened := device(s, "ana", cache)
	if reopened.Pending() != 1 {
		t.Fatalf("pending after reopening: %d", reopened.Pending())
	}
	s.up.Store(true)
	if err := reopened.Sync(ctx); err != nil || reopened.Pending() != 0 {
		t.Fatalf("%v pending %d", err, reopened.Pending())
	}
	if _, err := (&userconfig.Store{A: s.store, User: "ana"}).Get(ctx, "prefs"); err != nil {
		t.Fatalf("the server has it: %v", err)
	}
}

func TestTwoDevicesMergeInsteadOfOverwriting(t *testing.T) {
	ctx := context.Background()
	s := newServer(t)
	phone, laptop := device(s, "ana", nil), device(s, "ana", nil)
	merge := func(name string, local, remote []byte) []byte {
		return append(append([]byte(nil), remote...), local...)
	}
	phone.Merge, laptop.Merge = merge, merge

	phone.Set("tags", []byte("a"))
	if err := phone.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := laptop.Sync(ctx); err != nil { // the laptop pulls "a"
		t.Fatal(err)
	}
	if g, _ := laptop.Get("tags"); string(g) != "a" {
		t.Fatalf("pulled %q", g)
	}
	// both change it from revision 1, the phone first
	phone.Set("tags", []byte("aP"))
	laptop.Set("tags", []byte("aL"))
	if err := phone.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := laptop.Sync(ctx); err != nil { // 412, then merges: server's "aP" + local "aL"
		t.Fatal(err)
	}
	if g, _ := laptop.Get("tags"); string(g) != "aPaL" {
		t.Fatalf("merged %q", g)
	}
	if err := phone.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if g, _ := phone.Get("tags"); string(g) != "aPaL" {
		t.Fatalf("the phone catches up: %q", g)
	}
}

func TestDefaultMergeKeepsTheLocalChangeAndDeletesSync(t *testing.T) {
	ctx := context.Background()
	s := newServer(t)
	a, b := device(s, "ana", nil), device(s, "ana", nil)
	a.Set("x", []byte("1"))
	_ = a.Sync(ctx)
	_ = b.Sync(ctx)
	a.Set("x", []byte("from a"))
	b.Set("x", []byte("from b"))
	_ = a.Sync(ctx)
	if err := b.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if g, _ := b.Get("x"); string(g) != "from b" {
		t.Fatalf("local wins by default: %q", g)
	}
	_ = a.Sync(ctx)
	if g, _ := a.Get("x"); string(g) != "from b" {
		t.Fatalf("%q", g)
	}

	// delete here, the other device learns it
	a.Delete("x")
	if _, err := a.Get("x"); !errors.Is(err, client.ErrNotFound) {
		t.Fatal("deleted at once")
	}
	if err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get("x"); !errors.Is(err, client.ErrNotFound) || len(b.Names()) != 0 {
		t.Fatalf("the removal arrives: %v %v", err, b.Names())
	}
	// an item the server never had is just dropped
	a.Set("local-only", []byte("z"))
	a.Delete("local-only")
	if a.Pending() != 0 {
		t.Fatalf("pending %d", a.Pending())
	}
	a.Delete("never-existed")
}

func TestSignedOutKeepsTheQueueAndAnotherPersonIsSeparate(t *testing.T) {
	ctx := context.Background()
	s := newServer(t)
	c := device(s, "", nil) // no credentials
	c.Set("prefs", []byte("p"))
	if err := c.Sync(ctx); !errors.Is(err, client.ErrSignedOut) || c.Pending() != 1 {
		t.Fatalf("%v %d", err, c.Pending())
	}
	c.Header = func(r *http.Request) { r.Header.Set("X-User", "ana") } // signs in
	if err := c.Sync(ctx); err != nil || c.Pending() != 0 {
		t.Fatalf("%v %d", err, c.Pending())
	}
	bo := device(s, "bo", nil)
	if err := bo.Sync(ctx); err != nil || len(bo.Names()) != 0 {
		t.Fatalf("bo must not receive ana's items: %v %v", err, bo.Names())
	}
}

func TestRemovedElsewhereAndConflictsThatNeverSettle(t *testing.T) {
	ctx := context.Background()
	s := newServer(t)
	a, b := device(s, "ana", nil), device(s, "ana", nil)
	a.Set("x", []byte("1"))
	_ = a.Sync(ctx)
	_ = b.Sync(ctx)
	a.Delete("x")
	_ = a.Sync(ctx)
	b.Set("x", []byte("b recreates it")) // b still has revision 1
	if err := b.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := (&userconfig.Store{A: s.store, User: "ana"}).Get(ctx, "x"); err != nil {
		t.Fatalf("the change recreates what was removed: %v", err)
	}

	// a server that always says "changed" cannot trap the client in a loop
	always := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.Header().Set("Etag", `"9"`)
			http.Error(w, "changed", http.StatusPreconditionFailed)
			return
		}
		w.Header().Set("Etag", `"9"`)
		_, _ = w.Write([]byte("remote"))
	}))
	defer always.Close()
	c := client.New(always.URL, nil)
	c.Set("y", []byte("mine"))
	if err := c.Sync(ctx); err == nil || !strings.Contains(err.Error(), "keeps changing") {
		t.Fatalf("%v", err)
	}
}

func TestBadServerAnswersAreErrorsNotPanics(t *testing.T) {
	ctx := context.Background()
	for name, h := range map[string]http.HandlerFunc{
		"500":            func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", 500) },
		"429":            func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", 429) },
		"teapot":         func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", 418) },
		"junk listing":   func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not json")) },
		"listing is 404": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
	} {
		srv := httptest.NewServer(h)
		c := client.New(srv.URL, nil)
		if err := c.Sync(ctx); err == nil {
			t.Errorf("%s: no error", name)
		}
		c.Set("a", []byte("x"))
		if err := c.Sync(ctx); err == nil {
			t.Errorf("%s with a change: no error", name)
		}
		srv.Close()
	}
	// unreachable server, cancelled context
	c := client.New("http://127.0.0.1:1", nil)
	c.Set("a", []byte("x"))
	if err := c.Sync(ctx); !errors.Is(err, client.ErrOffline) {
		t.Fatalf("%v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.Sync(cctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}
