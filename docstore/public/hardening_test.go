package public

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// ---- helpers

// captureLog returns what the standard logger wrote until the test ends.
func captureLog(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(io.Discard) })
	return buf
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// hookStore wraps the expiring store: it counts listings and lets a test intercept the save of a
// public document.
type hookStore struct {
	*docstore.ExpiringAdapter
	lists     atomic.Int64
	failLists atomic.Bool                                             // listings by prefix fail (a store in trouble)
	beforePub func(ctx context.Context, id string, data []byte) error // called before saving "pubdoc/..."
	listHook  func(ctx context.Context) error                         // called by every ListPrefix before it lists (set before use)
	saltFail  atomic.Bool                                             // loading or saving the reporter salt fails
	saltRace  func()                                                  // called before the conditional save of the salt (set before use)
}

func (h *hookStore) Load(ctx context.Context, id string) (docstore.Document, error) {
	if id == saltKey && h.saltFail.Load() {
		return docstore.Document{}, errors.New("loading public/salt failed")
	}
	return h.ExpiringAdapter.Load(ctx, id)
}

func (h *hookStore) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	if id == saltKey {
		if h.saltFail.Load() {
			return errors.New("saving public/salt failed")
		}
		if f := h.saltRace; f != nil {
			f()
		}
	}
	return h.ExpiringAdapter.SaveIf(ctx, id, data, prev)
}

func (h *hookStore) Save(ctx context.Context, id string, data []byte) error {
	if f := h.beforePub; f != nil && strings.HasPrefix(id, docKeyPrefix) {
		if err := f(ctx, id, data); err != nil {
			return err
		}
	}
	return h.ExpiringAdapter.Save(ctx, id, data)
}

func (h *hookStore) List(ctx context.Context) ([]docstore.Meta, error) {
	h.lists.Add(1)
	return h.ExpiringAdapter.List(ctx)
}

func (h *hookStore) ListPrefix(ctx context.Context, p string) ([]docstore.Meta, error) {
	h.lists.Add(1)
	if f := h.listHook; f != nil {
		if err := f(ctx); err != nil {
			return nil, err
		}
	}
	if h.failLists.Load() {
		return nil, errors.New("listing pubdoc/ABCDEFGHIJKLMNOPQRSTUV failed")
	}
	return h.ExpiringAdapter.ListPrefix(ctx, p)
}

// envWith builds an env over a hookStore.
func envWith(t *testing.T, mod func(*Options)) (*env, *hookStore) {
	t.Helper()
	var hs *hookStore
	e := newEnv(t, func(o *Options) {
		hs = &hookStore{ExpiringAdapter: o.Store.(*docstore.ExpiringAdapter)}
		o.Store = hs
		if mod != nil {
			mod(o)
		}
	})
	return e, hs
}

// rawRequest sends a request head and the given part of its body on a fresh connection and returns
// the status line, and whether an answer came within wait. It never finishes the body, so a server
// that wants the rest of it keeps waiting.
func rawRequest(t *testing.T, e *env, method, path, token string, contentLength int, sent string, wait time.Duration) (status string, answered bool) {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(e.srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }() // best-effort cleanup of a test connection
	if _, err := fmt.Fprintf(conn, "%s %s HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nContent-Length: %d\r\n\r\n%s", method, path, token, contentLength, sent); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(line), true
}

// ---- finding 1: one address cannot fill the session cap

func TestClientKey(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.5":                      "203.0.113.5",
		"::ffff:203.0.113.5":               "203.0.113.5",
		"2001:db8:1:2::1":                  "2001:db8:1:2::/64",
		"2001:DB8:1:2:ffff:ffff:ffff:ffff": "2001:db8:1:2::/64",
		"2001:db8:1:3::1":                  "2001:db8:1:3::/64",
		"fe80::1%eth0":                     "fe80::/64",
		"Not An Address":                   "not an address",
	} {
		if got := ClientKey(in); got != want {
			t.Errorf("ClientKey(%q) = %q, want %q", in, got, want)
		}
	}
	if k := ClientKey(strings.Repeat("x", 500)); len(k) != 64 {
		t.Fatalf("long key: %d", len(k))
	}
}

func TestSingleAddressCannotFillTheSessionCap(t *testing.T) {
	sessions := NewMemorySessionStore(6)
	e := newEnv(t, func(o *Options) {
		o.Sessions = sessions
		o.Config.MaxSessions = 6
		o.Config.MaxSessionsPerAddress = 2
		o.Config.TrustedProxies = []string{"127.0.0.1"}
	})
	attacker := []string{"X-Forwarded-For", "203.0.113.5"}
	e.session(attacker...)
	e.session(attacker...)
	for i := 0; i < 30; i++ { // as many more attempts as a day allows
		r := e.want(e.do("POST", "/public/session", "", nil, attacker...), 429)
		if r.Header.Get("Retry-After") == "" {
			t.Fatal("no Retry-After")
		}
	}
	if sessions.Len() != 2 {
		t.Fatalf("the attacker holds %d sessions, want 2", sessions.Len())
	}
	// everybody else still gets one: the global cap (6) is not used up (a 503 would say so)
	for _, ip := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		e.session("X-Forwarded-For", ip)
	}
	// and when the attacker's sessions end, its address may start again
	e.clk.Add(2 * hour)
	e.session(attacker...)
}

func TestIPv6RotationWithinA64IsOneClient(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		o.Config.MaxSessionsPerAddress = 2
		o.Config.TrustedProxies = []string{"127.0.0.1"}
	})
	ok := 0
	for i := 1; i <= 20; i++ {
		r := e.do("POST", "/public/session", "", nil, "X-Forwarded-For", fmt.Sprintf("2001:db8:abcd:1::%x", i))
		if r.StatusCode == 201 {
			ok++
		}
	}
	if ok != 2 {
		t.Fatalf("%d sessions from one /64, want 2", ok)
	}
	e.session("X-Forwarded-For", "2001:db8:abcd:2::1") // another /64
}

func TestIssueRateAndByteQuotaAreKeyedBy64(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		o.Config.SessionsPerHour = 2
		o.Config.IPDailyBytes = 100
		o.Config.TrustedProxies = []string{"127.0.0.1"}
	})
	e.session("X-Forwarded-For", "2001:db8:aaaa:1::1")
	b, _ := e.session("X-Forwarded-For", "2001:db8:aaaa:1:1::2") // same /64
	e.want(e.do("POST", "/public/session", "", nil, "X-Forwarded-For", "2001:db8:aaaa:1:2::3"), 429)
	e.want(e.do("POST", "/public/doc", b, bytes.Repeat([]byte("a"), 80), "X-Forwarded-For", "2001:db8:aaaa:1:1::2"), 201)
	c, _ := e.session("X-Forwarded-For", "2001:db8:aaaa:2::1")
	// the daily bytes of the first /64 are shared by every address in it
	e.want(e.do("POST", "/public/doc", c, bytes.Repeat([]byte("a"), 80), "X-Forwarded-For", "2001:db8:aaaa:1:9::9"), 429)
}

func TestWriteBudgetIsPerAddressToo(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		o.Config.WritesPerMinutePerAddress = 3
		o.Config.TrustedProxies = []string{"127.0.0.1"}
	})
	hdr := func(n int) []string { return []string{"X-Forwarded-For", fmt.Sprintf("2001:db8:5::%d", n)} }
	a, _ := e.session(hdr(1)...)
	b, _ := e.session(hdr(2)...)
	e.want(e.do("POST", "/public/doc", a, []byte("1"), hdr(1)...), 201)
	e.want(e.do("PUT", "/public/doc", a, []byte("2"), hdr(1)...), 204)
	e.want(e.do("POST", "/public/doc", b, []byte("3"), hdr(2)...), 201)
	// b has a fresh per-session budget but shares the address budget (3 writes a minute)
	e.want(e.do("PUT", "/public/doc", b, []byte("4"), hdr(2)...), 429)
}

func TestSessionWithoutADocumentIsShortLived(t *testing.T) {
	e := newEnv(t, nil)
	a, sr := e.session()
	b, _ := e.session()
	var s sessionResponse
	_ = json.Unmarshal(sr.body, &s)
	if want := e.clk.Now().Add(DefaultSessionIdleTTL); !s.Expires.Equal(want) {
		t.Fatalf("a new session expires %v, want %v", s.Expires, want)
	}
	e.clk.Add(30 * time.Minute)
	e.create(a, "x") // the first write extends it
	e.clk.Add(40 * time.Minute)
	e.want(e.do("POST", "/public/doc", b, []byte("y")), 401) // idle for over an hour
	e.clk.Add(10 * hour)
	e.want(e.do("PUT", "/public/doc", a, []byte("z")), 204) // lives on
}

func TestSweepReapsIdleSessions(t *testing.T) {
	sessions := NewMemorySessionStore(0)
	e := newEnv(t, func(o *Options) { o.Sessions = sessions })
	for i := 0; i < 5; i++ {
		e.session()
	}
	keep, _ := e.session()
	e.create(keep, "x")
	if sessions.Len() != 6 {
		t.Fatalf("Len = %d", sessions.Len())
	}
	e.clk.Add(2 * hour)
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sessions.Len() != 1 {
		t.Fatalf("after the sweep %d sessions are held, want 1 (the one with a document)", sessions.Len())
	}
}

func TestMemorySessionStorePerClientCap(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st := NewMemorySessionStore(10)
	mk := func(client string, d time.Duration) Session {
		return Session{ID: "x", Client: client, Created: now, Expires: now.Add(d)}
	}
	for i, d := range []time.Duration{time.Hour, time.Minute} {
		if err := st.Create(ctx, fmt.Sprint("a", i), mk("A", d), now, SessionCaps{PerClient: 2}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Create(ctx, "a2", mk("A", time.Hour), now, SessionCaps{PerClient: 2}); err != ErrClientSessionsFull {
		t.Fatalf("err = %v", err)
	}
	if err := st.Create(ctx, "b0", mk("B", time.Hour), now, SessionCaps{PerClient: 2}); err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, "a2", mk("A", time.Hour), now.Add(2*time.Minute), SessionCaps{PerClient: 2}); err != nil { // a1 expired
		t.Fatalf("an expired session still counts: %v", err)
	}
	_ = st.Delete(ctx, "a0")
	if err := st.Create(ctx, "a3", mk("A", time.Hour), now.Add(2*time.Minute), SessionCaps{PerClient: 2}); err != nil {
		t.Fatalf("a deleted session still counts: %v", err)
	}
}

// ---- finding 2: the global storage budget

// countingBlocklist counts the bodies that reached the content check, which comes after the body
// has been read: a request refused before its body is read never gets there.
type countingBlocklist struct{ n atomic.Int64 }

func (c *countingBlocklist) Blocked(context.Context, []byte) (bool, error) {
	c.n.Add(1)
	return false, nil
}

func TestGlobalStorageBudget(t *testing.T) {
	var seen countingBlocklist
	e := newEnv(t, func(o *Options) { o.Config.MaxTotalBytes = 100; o.Blocklist = &seen })
	a, _ := e.session()
	b, _ := e.session()
	e.want(e.do("POST", "/public/doc", a, bytes.Repeat([]byte("a"), 60)), 201)
	r := e.want(e.do("POST", "/public/doc", b, bytes.Repeat([]byte("b"), 60)), 503)
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	if k := e.keys("pubdoc/"); len(k) != 1 {
		t.Fatalf("documents: %v", k)
	}
	// an edit that grows the total past the budget is refused, one that shrinks it is not
	e.want(e.do("PUT", "/public/doc", a, bytes.Repeat([]byte("a"), 120)), 503)
	e.want(e.do("PUT", "/public/doc", a, bytes.Repeat([]byte("a"), 90)), 204)
	e.want(e.do("PUT", "/public/doc", a, bytes.Repeat([]byte("a"), 10)), 204)
	e.want(e.do("POST", "/public/doc", b, bytes.Repeat([]byte("b"), 90)), 201)
	if h := e.svc.Health(); h.StorageBytes != 100 {
		t.Fatalf("health %+v", h)
	}
	// full: refused before the body is read, so before the content check
	c, _ := e.session()
	before := seen.n.Load()
	e.want(e.do("POST", "/public/doc", c, []byte("ccccc")), 503)
	if seen.n.Load() != before {
		t.Fatal("a full store read and screened the body first")
	}
	// a discard gives the bytes back
	e.want(e.do("DELETE", "/public/doc", a, nil), 204)
	e.want(e.do("POST", "/public/doc", c, bytes.Repeat([]byte("c"), 10)), 201)
}

func TestTakedownFreesStorage(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.MaxTotalBytes = 100 })
	a, _ := e.session()
	d := e.create(a, strings.Repeat("a", 90))
	b, _ := e.session()
	e.want(e.do("POST", "/public/doc", b, bytes.Repeat([]byte("b"), 50)), 503)
	e.want(e.do("DELETE", "/admin/takedown/"+d.ViewID, "", nil, AdminHeader, adminToken), 204)
	e.want(e.do("POST", "/public/doc", b, bytes.Repeat([]byte("b"), 50)), 201)
}

// The counter is rebuilt from the store: a restart does not give the budget back, and snapshots of
// versions that a store keeps count with their documents.
func TestStorageBudgetSurvivesARestartAndCountsVersions(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.MaxTotalBytes = 100 })
	a, _ := e.session()
	d := e.create(a, strings.Repeat("a", 60))
	svc2, err := New(Options{Store: e.store, Now: e.clk.Now, Config: Config{MaxTotalBytes: 100, SessionsPerHour: 1000, MaxSessionsPerAddress: 1000, WritesPerMinute: 1000, WritesPerMinutePerAddress: 1000}})
	if err != nil {
		t.Fatal(err)
	}
	srv2 := docstore.NewServer(e.store, "memory")
	svc2.Mount(srv2)
	ts := httptest.NewServer(srv2.Handler())
	defer ts.Close()
	e2 := &env{t: t, clk: e.clk, inner: e.inner, store: e.store, svc: svc2, srv: ts}
	b, _ := e2.session()
	e2.want(e2.do("POST", "/public/doc", b, bytes.Repeat([]byte("b"), 60)), 503) // a restarted service remembers
	e2.want(e2.do("POST", "/public/doc", b, bytes.Repeat([]byte("b"), 30)), 201)
	// a version snapshot kept for the document counts: the sweep sees it
	if err := e.inner.Save(context.Background(), versionsKeyPrefix+d.ID+"/1.open", bytes.Repeat([]byte("v"), 40)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h := e.svc.Health(); h.StorageBytes != 60+30+40 {
		t.Fatalf("StorageBytes = %d, want 130 (two documents and 40 of versions)", h.StorageBytes)
	}
}
