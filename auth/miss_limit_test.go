package auth

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// readCounting counts the session reads.
type readCounting struct {
	*MemorySessionStore
	reads atomic.Int64
}

func (r *readCounting) GetSession(ctx context.Context, token string) (User, bool, error) {
	r.reads.Add(1)
	return r.MemorySessionStore.GetSession(ctx, token)
}

func bearerFrom(m *Manager, addr, token string) bool {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = addr
	req.Header.Set("Authorization", "Bearer "+token)
	_, ok := m.CurrentUser(req)
	return ok
}

// A client that sends tokens that are not sessions spends from its budget; over it, its requests are no
// longer looked up in the store, another client is unaffected, and the budget comes back.
func TestMissLimiterStopsReadsFromAClientThatKeepsGuessing(t *testing.T) {
	f := newFakeOIDCProvider(t)
	m := newTestManager(t, f.srv.URL)
	store := &readCounting{MemorySessionStore: NewMemorySessionStore()}
	m.SetSessionStore(store)
	good, err := m.StartSession(User{ID: "google:1", Email: "ana@x.io"})
	if err != nil {
		t.Fatal(err)
	}
	lim := &budgetLimiter{n: 5}
	m.SetMissLimiter(lim, nil)

	for i := range 5 {
		if bearerFrom(m, "198.51.100.1:1000", "guess"+string(rune('a'+i))) {
			t.Fatal("a guess was accepted")
		}
	}
	if got := store.reads.Load(); got != 5 {
		t.Fatalf("%d reads for 5 misses", got)
	}
	// the 6th miss is read (it is the one that finds the budget spent), the next ones are not
	bearerFrom(m, "198.51.100.1:1000", "guess-6")
	before := store.reads.Load()
	for range 50 {
		if bearerFrom(m, "198.51.100.1:1000", "guess-more") {
			t.Fatal("accepted")
		}
	}
	if got := store.reads.Load(); got != before {
		t.Errorf("a client over its budget still cost %d reads", got-before)
	}
	// even its valid token is not looked up while blocked (the stated cost of a shared budget)
	if bearerFrom(m, "198.51.100.1:1000", good) {
		t.Error("a blocked client's request was authenticated")
	}
	// another client is fine, and valid tokens are not counted as misses
	if !bearerFrom(m, "203.0.113.9:2000", good) {
		t.Error("another client was affected")
	}
	for range 20 {
		if !bearerFrom(m, "203.0.113.9:2000", good) {
			t.Fatal("a valid token failed")
		}
	}
	if n := lim.seen["miss|203.0.113.9"]; n != 0 {
		t.Errorf("valid tokens spent %d from the miss budget", n)
	}
	// the limiter said wait 2.3s: the block lasts about that long, then reads resume
	m.missMu.Lock()
	m.missBlocked["198.51.100.1"] = time.Now().Add(-time.Millisecond)
	m.missMu.Unlock()
	if !bearerFrom(m, "198.51.100.1:1000", good) {
		t.Error("the block did not end")
	}
}

func TestWithoutAMissLimiterNothingChanges(t *testing.T) {
	f := newFakeOIDCProvider(t)
	m := newTestManager(t, f.srv.URL)
	store := &readCounting{MemorySessionStore: NewMemorySessionStore()}
	m.SetSessionStore(store)
	for range 30 {
		bearerFrom(m, "198.51.100.1:1000", "guess")
	}
	if got := store.reads.Load(); got != 30 {
		t.Errorf("%d reads, want 30", got)
	}
}
