package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type budgetLimiter struct {
	mu   sync.Mutex
	n    int
	seen map[string]int
	keys []string
}

func (l *budgetLimiter) Allow(_ context.Context, key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string]int{}
	}
	l.keys = append(l.keys, key)
	l.seen[key]++
	if l.seen[key] > l.n {
		return false, 2300 * time.Millisecond
	}
	return true, 0
}

type countingStates struct {
	*MemorySessionStore
	mu   sync.Mutex
	puts int
}

func (c *countingStates) PutState(ctx context.Context, s LoginState, ttl time.Duration) (string, error) {
	c.mu.Lock()
	c.puts++
	c.mu.Unlock()
	return c.MemorySessionStore.PutState(ctx, s, ttl)
}

func loginFrom(h http.Handler, remote string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/auth/test/login", nil)
	req.RemoteAddr = remote
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestLoginLimiter(t *testing.T) {
	f := newFakeOIDCProvider(t)
	m := newTestManager(t, f.srv.URL)
	store := &countingStates{MemorySessionStore: NewMemorySessionStore()}
	m.SetSessionStore(store)
	lim := &budgetLimiter{n: 20}
	m.SetLoginLimiter(lim, nil) // nil: the host part of the remote address
	h := m.Routes()

	for i := 1; i <= 20; i++ {
		if w := loginFrom(h, "198.51.100.1:4000"+string(rune('0'+i%10)), nil); w.Code != http.StatusFound {
			t.Fatalf("login %d: status %d, want 302", i, w.Code)
		}
	}
	if store.puts != 20 {
		t.Fatalf("states written = %d, want 20", store.puts)
	}
	w := loginFrom(h, "198.51.100.1:50000", nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("21st login: status %d, want 429", w.Code)
	}
	if ra := w.Header().Get("Retry-After"); ra != "3" {
		t.Fatalf("Retry-After = %q, want 3 (whole seconds, rounded up)", ra)
	}
	if store.puts != 20 {
		t.Fatalf("an over-budget login reached PutState: %d states, want 20", store.puts)
	}
	w = loginFrom(h, "198.51.100.1:50001", map[string]string{"X-Forwarded-For": "203.0.113.9"})
	if w.Code != http.StatusTooManyRequests || store.puts != 20 {
		t.Fatalf("X-Forwarded-For from an untrusted peer: status %d, states %d; want 429 and 20", w.Code, store.puts)
	}
	if w := loginFrom(h, "198.51.100.2:4000", nil); w.Code != http.StatusFound {
		t.Fatalf("second address: status %d, want 302", w.Code)
	}
	if store.puts != 21 {
		t.Fatalf("states written = %d, want 21", store.puts)
	}
	if got := lim.keys[0]; got != "login|198.51.100.1" {
		t.Fatalf("limiter key = %q, want login|198.51.100.1", got)
	}
}

func TestLoginLimiterRetryAfterAtLeastOneSecond(t *testing.T) {
	f := newFakeOIDCProvider(t)
	m := newTestManager(t, f.srv.URL)
	m.SetLoginLimiter(denyAll{}, func(*http.Request) string { return "x" })
	w := loginFrom(m.Routes(), "198.51.100.1:1", nil)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("status %d Retry-After %q, want 429 and 1", w.Code, w.Header().Get("Retry-After"))
	}
}

type denyAll struct{}

func (denyAll) Allow(context.Context, string) (bool, time.Duration) { return false, 0 }

func TestLoginUnlimitedByDefault(t *testing.T) {
	f := newFakeOIDCProvider(t)
	m := newTestManager(t, f.srv.URL)
	h := m.Routes()
	for i := 0; i < 50; i++ {
		if w := loginFrom(h, "198.51.100.1:1", nil); w.Code != http.StatusFound {
			t.Fatalf("login %d: status %d", i, w.Code)
		}
	}
}

func TestLoginLimiterFallsBackToTheRemoteAddressWhenClientIPAnswersNothing(t *testing.T) {
	f := newFakeOIDCProvider(t)
	m := newTestManager(t, f.srv.URL)
	m.SetSessionStore(NewMemorySessionStore())
	lim := &budgetLimiter{n: 100}
	m.SetLoginLimiter(lim, func(*http.Request) string { return "" })
	h := m.Routes()
	loginFrom(h, "198.51.100.1:40000", nil)
	loginFrom(h, "198.51.100.2:40000", nil)
	if len(lim.keys) != 2 || lim.keys[0] != "login|198.51.100.1" || lim.keys[1] != "login|198.51.100.2" {
		t.Fatalf("keys %v: clients with no forwarded address share one budget", lim.keys)
	}
}

func TestLoginLimiterGroupsAnIPv6SubscriberByItsSlash64(t *testing.T) {
	f := newFakeOIDCProvider(t)
	m := newTestManager(t, f.srv.URL)
	lim := &budgetLimiter{n: 2}
	m.SetLoginLimiter(lim, nil)
	h := m.Routes()
	for i, remote := range []string{"[2001:db8:1:2::1]:1", "[2001:db8:1:2:aaaa::9]:2"} {
		if w := loginFrom(h, remote, nil); w.Code != http.StatusFound {
			t.Fatalf("login %d from %s: status %d, want 302", i, remote, w.Code)
		}
	}
	if w := loginFrom(h, "[2001:db8:1:2:ffff::7]:3", nil); w.Code != http.StatusTooManyRequests {
		t.Errorf("a third address of the same /64: status %d, want 429", w.Code)
	}
	if w := loginFrom(h, "[2001:db8:1:3::1]:4", nil); w.Code != http.StatusFound {
		t.Errorf("another /64 shares the budget: status %d, want 302", w.Code)
	}
	if got := lim.keys[0]; got != "login|2001:db8:1:2::/64" {
		t.Errorf("key %q, want the /64", got)
	}
}
