package public

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// behindProxy makes the test server believe X-Forwarded-For from the loopback (where httptest
// connects from), so a test can act as many clients.
func behindProxy(o *Options) { o.Config.TrustedProxies = []string{"127.0.0.1"} }

func xff(ip string) []string { return []string{"X-Forwarded-For", ip} }

// ---- 1. a chunked body gives back what the pre-charge held beyond what was read

// chunked sends body with no Content-Length (Transfer-Encoding: chunked).
func (e *env) chunked(method, path, token, body string) resp {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, io.NopCloser(strings.NewReader(body)))
	if err != nil {
		e.t.Fatal(err)
	}
	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer "+token)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return resp{r, b}
}

func quotaUsed(e *env, key string) int64 {
	q := e.svc.quota.(*MemoryQuota)
	q.mu.Lock()
	defer q.mu.Unlock()
	if w := q.keys[key]; w != nil {
		return w.used
	}
	return 0
}

func TestChunkedBodyIsRefundedToWhatWasRead(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		o.Config.IPDailyBytes = 1000
		o.Config.MaxDocBytes = 500
	})
	tok, _ := e.session()
	// A chunked body of unknown length is pre-charged the document cap (500: half the day) ...
	e.want(e.chunked("POST", "/public/doc", tok, strings.Repeat("a", 10)), 201)
	// ... and what it did not use is given back: 10 bytes are charged, not 500.
	if used := quotaUsed(e, "ip:127.0.0.1"); used != 10 {
		t.Fatalf("a 10 byte chunked body left %d bytes charged, want 10", used)
	}
	e.want(e.chunked("PUT", "/public/doc", tok, strings.Repeat("b", 20)), 204)
	if used := quotaUsed(e, "ip:127.0.0.1"); used != 30 {
		t.Fatalf("charged %d, want 30", used)
	}
	// the rest of the day (970 bytes) is still there for bodies of known length
	e.want(e.do("PUT", "/public/doc", tok, bytes.Repeat([]byte("c"), 485)), 204)
	e.want(e.do("PUT", "/public/doc", tok, bytes.Repeat([]byte("c"), 485)), 204)
	e.want(e.do("PUT", "/public/doc", tok, []byte("d")), 429)
}

// ---- 2. a second-level cap per /48 and /24, and a reserve for newcomers

func TestNetKey(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.5":                "203.0.113.0/24",
		"::ffff:203.0.113.77":        "203.0.113.0/24",
		"2001:db8:1:2::1":            "2001:db8:1::/48",
		"2001:DB8:1:ffff:1:2:3:4":    "2001:db8:1::/48",
		"2001:db8:2:2::1":            "2001:db8:2::/48",
		"fe80::1%eth0":               "fe80::/48",
		"Not An Address":             "not an address",
		"2001:db8:1:9:aaaa:bbbb::1":  "2001:db8:1::/48",
		"2001:db8:1:ffff:ffff::ffff": "2001:db8:1::/48",
	} {
		if got := NetKey(in); got != want {
			t.Errorf("NetKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// sessionStatus asks for a session as ip and returns the status.
func sessionStatus(e *env, ip string) int {
	e.t.Helper()
	return e.do("POST", "/public/session", "", nil, xff(ip)...).StatusCode
}

func TestSlash48FloodCannotFillTheGlobalCap(t *testing.T) {
	sessions := NewMemorySessionStore(50)
	e := newEnv(t, func(o *Options) {
		behindProxy(o)
		o.Sessions = sessions
		o.Config.MaxSessions = 50
		o.Config.MaxSessionsPerAddress = 2
		o.Config.MaxSessionsPerNet = 6
	})
	// One attacker holds 2001:db8:aaaa::/48 (65,536 /64s) and starts a session from each of 200:
	// the per-/64 cap of 2 never triggers, the per-/48 cap of 6 does.
	got := map[int]int{}
	for i := 0; i < 200; i++ {
		got[sessionStatus(e, fmt.Sprintf("2001:db8:aaaa:%x::1", i))]++
	}
	if got[201] != 6 || got[429] != 194 {
		t.Fatalf("the /48 got %v, want 6 sessions and the rest 429", got)
	}
	if sessions.Len() != 6 {
		t.Fatalf("the attacker holds %d of 50", sessions.Len())
	}
	// a /24 is the IPv4 equivalent: 8 hosts of one /24, at most 6 sessions
	got = map[int]int{}
	for i := 1; i <= 8; i++ {
		got[sessionStatus(e, fmt.Sprintf("198.51.100.%d", i))]++
	}
	if got[201] != 6 || got[429] != 2 {
		t.Fatalf("the /24 got %v", got)
	}
	// other networks are not affected by either
	for _, ip := range []string{"2001:db8:bbbb::1", "192.0.2.1", "203.0.113.9"} {
		if st := sessionStatus(e, ip); st != 201 {
			t.Fatalf("%s: %d", ip, st)
		}
	}
}

func TestReserveKeepsRoomForNewNetworksUnderABotnet(t *testing.T) {
	sessions := NewMemorySessionStore(50)
	e := newEnv(t, func(o *Options) {
		behindProxy(o)
		o.Sessions = sessions
		o.Config.MaxSessions = 50
		o.Config.MaxSessionsPerAddress = 2
		o.Config.MaxSessionsPerNet = 6
		// the default reserve: 20% of 50 = 10, so 40 sessions are open to everybody
	})
	// A botnet of seven /48s, each as busy as the per-/48 cap lets it be, fills the open 40.
	for p := 0; p < 7; p++ {
		for i := 0; i < 6; i++ {
			sessionStatus(e, fmt.Sprintf("2001:db8:%x:%x::1", 0xa00+p, i))
		}
	}
	if sessions.Len() != 40 {
		t.Fatalf("the botnet holds %d sessions, want the 40 outside the reserve", sessions.Len())
	}
	// its prefixes cannot take the reserve: 429 for the ones at their cap, 503 (the offer is full
	// for them) for the one that is not
	for p := 0; p < 7; p++ {
		want := 429
		if p == 6 {
			want = 503
		}
		if st := sessionStatus(e, fmt.Sprintf("2001:db8:%x:ff::1", 0xa00+p)); st != want {
			t.Fatalf("botnet prefix %d got %d inside the reserve, want %d", p, st, want)
		}
	}
	// a second visitor from a network that already holds a session is turned away too ...
	if st := sessionStatus(e, "198.51.100.1"); st != 201 { // new /24: in the reserve
		t.Fatalf("a new network got %d, want 201", st)
	}
	if st := sessionStatus(e, "198.51.100.2"); st != 503 { // same /24, now live
		t.Fatalf("a second visitor of a live network got %d in the reserve, want 503", st)
	}
	// ... while legitimate newcomers from networks with no session fill the 10 places
	for i := 0; i < 9; i++ {
		if st := sessionStatus(e, fmt.Sprintf("100.%d.0.1", i+10) /* a different /16 each: the reserve looks at the coarsest level */); st != 201 {
			t.Fatalf("newcomer %d: %d", i, st)
		}
	}
	if st := sessionStatus(e, "192.0.99.1"); st != 503 || sessions.Len() != 50 {
		t.Fatalf("at the global cap: %d, %d sessions", st, sessions.Len())
	}
	// sessions that end free their places: the reserve is only for newcomers while it applies
	e.clk.Add(2 * hour)
	if st := sessionStatus(e, "2001:db8:a00:1::1"); st != 201 {
		t.Fatalf("after the sessions expired: %d", st)
	}
}

func TestMemorySessionStoreNetCapAndReserve(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func(client, net string, d time.Duration) Session {
		return Session{ID: "x", Client: client, Net: net, Created: now, Expires: now.Add(d)}
	}
	caps := SessionCaps{PerClient: 2, PerNet: 3}
	st := NewMemorySessionStore(10)
	for i := 0; i < 3; i++ {
		if err := st.Create(ctx, fmt.Sprint("a", i), mk(fmt.Sprint("c", i), "N1", time.Hour), now, caps); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Create(ctx, "a3", mk("c9", "N1", time.Hour), now, caps); err != ErrNetSessionsFull {
		t.Fatalf("err = %v, want ErrNetSessionsFull", err)
	}
	// the client cap is told apart from the net cap
	_ = st.Create(ctx, "b0", mk("cb", "N2", time.Hour), now, caps)
	_ = st.Create(ctx, "b1", mk("cb", "N2", time.Hour), now, caps)
	if err := st.Create(ctx, "b2", mk("cb", "N2", time.Hour), now, caps); err != ErrClientSessionsFull {
		t.Fatalf("err = %v, want ErrClientSessionsFull", err)
	}
	// expired sessions do not count against the net
	if err := st.Create(ctx, "a3", mk("c9", "N1", time.Hour), now.Add(2*time.Hour), caps); err != nil {
		t.Fatalf("expired sessions still count: %v", err)
	}
	// a session with no Net (a custom caller) is under no net cap
	for i := 0; i < 5; i++ {
		if err := st.Create(ctx, fmt.Sprint("n", i), mk(fmt.Sprint("x", i), "", time.Hour), now.Add(2*time.Hour), caps); err != nil {
			t.Fatal(err)
		}
	}

	// the reserve (2 of 10): from 8 live sessions on, only networks with no live session get in
	caps.ReservePercent = 20
	st = NewMemorySessionStore(10)
	for i := 0; i < 8; i++ {
		if err := st.Create(ctx, fmt.Sprint("f", i), mk(fmt.Sprint("f", i), fmt.Sprint("F", i), time.Hour), now, caps); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Create(ctx, "known", mk("k", "F0", time.Hour), now, caps); err != ErrSessionsFull {
		t.Fatalf("a network that holds sessions, inside the reserve: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := st.Create(ctx, fmt.Sprint("fresh", i), mk("k", fmt.Sprint("NEW", i), time.Hour), now, caps); err != nil {
			t.Fatalf("a new network inside the reserve: %v", err)
		}
	}
	if err := st.Create(ctx, "full", mk("k", "NEW9", time.Hour), now, caps); err != ErrSessionsFull {
		t.Fatalf("at the global cap: %v", err)
	}
	// no reserve configured: the same network may use every place
	st = NewMemorySessionStore(10)
	for i := 0; i < 10; i++ {
		if err := st.Create(ctx, fmt.Sprint("g", i), mk("g", "G", time.Hour), now, SessionCaps{}); err != nil {
			t.Fatal(err)
		}
	}
}

// ---- 3. the byte counter: reconcile vs reserve, refunds, takedowns, windows, fail closed

func TestSweepBetweenReserveAndSaveKeepsTheReservation(t *testing.T) {
	e, hs := envWith(t, nil)
	tok, _ := e.session()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	letGo := func() { once.Do(func() { close(release) }) }
	t.Cleanup(letGo) // a failing test must not leave the create waiting (the server's Close would hang)
	hs.beforePub = func(context.Context, string, []byte) error {
		close(entered)
		<-release
		return nil
	}
	done := make(chan resp, 1)
	go func() { done <- e.do("POST", "/public/doc", tok, bytes.Repeat([]byte("a"), 40)) }()
	<-entered // the bytes are reserved and the document is not stored yet
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Health().StorageBytes; got != 40 {
		t.Fatalf("after a sweep in between the reservation counts %d, want 40", got)
	}
	letGo()
	e.want(<-done, 201)
	if got := e.svc.Health().StorageBytes; got != 40 {
		t.Fatalf("counter %d after the create, want 40", got)
	}
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Health().StorageBytes; got != 40 {
		t.Fatalf("counter %d after the next sweep, want 40", got)
	}
}

func TestTakedownAndDiscardSubtractOnlyWhatTheCurrentGenerationCounts(t *testing.T) {
	e := newEnv(t, nil)
	a, _ := e.session()
	b, _ := e.session()
	da := e.create(a, strings.Repeat("a", 100))
	e.create(b, strings.Repeat("b", 200))
	// document A vanishes behind the service's back (expiry, another replica); its record is still
	// there (a fresh orphan is kept for a minute) when the sweep recounts: A is not counted
	// any more.
	if err := e.store.Delete(context.Background(), docKey(da.ID)); err != nil {
		t.Fatal(err)
	}
	gen := e.svc.bytes.generation()
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.svc.bytes.generation() == gen {
		t.Fatal("the sweep did not start a new generation")
	}
	if got := e.svc.Health().StorageBytes; got != 200 {
		t.Fatalf("counter %d, want 200", got)
	}
	// the takedown finds A's record (100 bytes) but A is not counted in this generation: B's
	// bytes must stay
	e.want(e.do("DELETE", "/admin/takedown/"+da.ViewID, "", nil, AdminHeader, adminToken), 204)
	if got := e.svc.Health().StorageBytes; got != 200 {
		t.Fatalf("a takedown of an uncounted document left %d, want 200", got)
	}
	// the same for a discard
	c, _ := e.session()
	dc := e.create(c, strings.Repeat("c", 50))
	if err := e.store.Delete(context.Background(), docKey(dc.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.want(e.do("DELETE", "/public/doc", c, nil), 204)
	if got := e.svc.Health().StorageBytes; got != 200 {
		t.Fatalf("counter %d, want 200", got)
	}
}

func TestBudgetNeverGoesNegativeAndLogsTheClamp(t *testing.T) {
	logs := captureLog(t)
	var b budget
	r, ok := b.reserve("d", 10, 10, 100)
	if !ok {
		t.Fatal("did not fit")
	}
	r.cancel()
	r.cancel() // a second end of the same reservation is nothing
	r.commit()
	if used, _ := b.get(); used != 0 {
		t.Fatalf("used %d after a cancelled reservation", used)
	}
	if got := b.release("never-counted"); got != 0 {
		t.Fatalf("released %d for a document that was never counted", got)
	}
	// a refund below zero (here a stray pending release) clamps and logs
	b.mu.Lock()
	b.unholdLocked(5)
	b.mu.Unlock()
	if used, _ := b.get(); used != 0 || !strings.Contains(logs.String(), "clamped") {
		t.Fatalf("used %d, log %q", used, logs.String())
	}
	var rs reportStats
	rs.add(-3, -100, "")
	rs.mu.Lock()
	n, by := rs.n, rs.bytes
	rs.mu.Unlock()
	if n != 0 || by != 0 {
		t.Fatalf("report counts %d, %d", n, by)
	}
}

func TestOverlappingReconcilesDoNotLoseAWrite(t *testing.T) {
	var b budget
	w1, w2 := b.begin(), b.begin() // two reconciles listing at the same time
	r, _ := b.reserve("doc", 30, 30, 1000)
	r.commit() // a write lands while both are listing: neither listing saw it
	b.set(w1, map[string]budgetEntry{"old": {doc: 5}})
	if used, _ := b.get(); used != 35 {
		t.Fatalf("after the first: %d, want 35", used)
	}
	b.set(w2, map[string]budgetEntry{"old": {doc: 5}})
	if used, _ := b.get(); used != 35 {
		t.Fatalf("after the second: %d, want 35 (the write's 30 + 5)", used)
	}
	// a removal during a listing wins over a listing that still saw the document
	w3 := b.begin()
	b.release("doc")
	b.set(w3, map[string]budgetEntry{"old": {doc: 5}, "doc": {doc: 30}})
	if used, _ := b.get(); used != 5 {
		t.Fatalf("after a removal during a listing: %d, want 5", used)
	}
	// version snapshots known from the listing stay with their document when it is edited
	w4 := b.begin()
	b.set(w4, map[string]budgetEntry{"old": {doc: 5, extra: 7}})
	r2, _ := b.reserve("old", 9, 4, 1000)
	r2.commit()
	if used, _ := b.get(); used != 9+7 {
		t.Fatalf("an edit: %d, want 16", used)
	}
}

func TestConcurrentSweepsAndWritesKeepTheCounterExact(t *testing.T) {
	e := newEnv(t, nil)
	const writers = 12
	stop := make(chan struct{})
	var swept sync.WaitGroup
	for i := 0; i < 2; i++ { // two sweeps at once, over and over
		swept.Add(1)
		go func() {
			defer swept.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = e.svc.Sweep(context.Background())
				}
			}
		}()
	}
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		tok, _ := e.session()
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e.do("POST", "/public/doc", tok, bytes.Repeat([]byte("x"), 10+i))
			e.do("PUT", "/public/doc", tok, bytes.Repeat([]byte("y"), 20+i))
		}(i)
	}
	wg.Wait()
	close(stop)
	swept.Wait()
	want := 0
	for i := 0; i < writers; i++ {
		want += 20 + i
	}
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Health().StorageBytes; got != int64(want) {
		t.Fatalf("counter %d after a quiet sweep, want %d", got, want)
	}
}

func TestFirstReconcileFailureFailsClosedForABackoff(t *testing.T) {
	logs := captureLog(t)
	e, hs := envWith(t, nil)
	tok, _ := e.session()
	hs.failLists.Store(true)
	r := e.want(e.do("POST", "/public/doc", tok, []byte("hello")), 503)
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	if !strings.Contains(logs.String(), "writes are refused") {
		t.Fatalf("log %q", logs.String())
	}
	if k := e.keys("pubdoc/"); len(k) != 0 {
		t.Fatalf("a document was stored while the counters were unknown: %v", k)
	}
	// the store is back, but the backoff has not passed: still closed (and no listing is tried)
	hs.failLists.Store(false)
	lists := hs.lists.Load()
	e.want(e.do("POST", "/public/doc", tok, []byte("hello")), 503)
	if hs.lists.Load() != lists {
		t.Fatal("the reconcile was retried inside the backoff")
	}
	e.clk.Add(reconcileBackoff + time.Second)
	e.want(e.do("POST", "/public/doc", tok, []byte("hello")), 201)
	if got := e.svc.Health().StorageBytes; got != 5 {
		t.Fatalf("counter %d, want 5", got)
	}
	// a failed refusal charged nothing to the daily bytes
	if used := quotaUsed(e, "ip:127.0.0.1"); used != 5 {
		t.Fatalf("quota used %d, want 5", used)
	}
}

func TestEditWithoutADeadlineSubtractsAtOnce(t *testing.T) {
	var off atomic.Bool
	e := newEnv(t, func(o *Options) {
		ea := docstore.NewExpiringAdapter(docstore.NewMemoryAdapter(), flippablePolicy(&off))
		ea.Now = o.Now
		o.Store = ea
	})
	tok, _ := e.session()
	e.create(tok, strings.Repeat("a", 64))
	off.Store(true) // the policy stops covering the document
	e.want(e.do("PUT", "/public/doc", tok, []byte(strings.Repeat("b", 80))), 500)
	if got := e.svc.Health().StorageBytes; got != 0 {
		t.Fatalf("the document was deleted but %d bytes are still counted", got)
	}
}

// ---- 4. /public/health says 200 or 503; the detail is behind the admin guard

func TestPublicHealthIsMinimalAndAdminHealthIsGuarded(t *testing.T) {
	e := newEnv(t, nil)
	tok, _ := e.session()
	e.create(tok, "some content")
	r := e.want(e.do("GET", "/public/health", "", nil), 200)
	if got := strings.TrimSpace(string(r.body)); got != `{"ok":true}` {
		t.Fatalf("public health body %q", got)
	}
	e.clk.Add(11 * time.Minute) // the sweeper is stale
	r = e.want(e.do("GET", "/public/health", "", nil), 503)
	if got := strings.TrimSpace(string(r.body)); got != `{"ok":false}` {
		t.Fatalf("public health body %q", got)
	}
	for _, leak := range []string{"storage", "problems", "sweep", ProblemSweeperStale} {
		if strings.Contains(strings.ToLower(string(r.body)), leak) {
			t.Fatalf("the public health leaks %q: %s", leak, r.body)
		}
	}
	// the detail needs the admin token
	e.want(e.do("GET", "/admin/health", "", nil), 403)
	e.want(e.do("GET", "/admin/health", "", nil, AdminHeader, "wrong"), 403)
	r = e.want(e.do("GET", "/admin/health", "", nil, AdminHeader, adminToken), 503)
	for _, want := range []string{"storageBytes", "storageMax", ProblemSweeperStale, "lastSweep"} {
		if !strings.Contains(string(r.body), want) {
			t.Fatalf("/admin/health lacks %q: %s", want, r.body)
		}
	}
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	r = e.want(e.do("GET", "/admin/health", "", nil, AdminHeader, adminToken), 200)
	if !strings.Contains(string(r.body), `"storageBytes":12`) {
		t.Fatalf("/admin/health: %s", r.body)
	}
	e.want(e.do("POST", "/admin/health", "", nil, AdminHeader, adminToken), 405)
}

func TestNoAdminTokenNoAdminHealth(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.AdminToken = "" })
	e.want(e.do("GET", "/admin/health", "", nil, AdminHeader, adminToken), 404)
}

// ---- 5. reports: dedupe, reserved slots, priority

func (e *env) report(id, reason string, ip string) resp {
	e.t.Helper()
	return e.do("POST", "/public/report", "", []byte(fmt.Sprintf(`{"id":%q,"reason":%q}`, id, reason)), xff(ip)...)
}

// reportCat is report with a category.
func (e *env) reportCat(id, category, reason string, ip string) resp {
	e.t.Helper()
	return e.do("POST", "/public/report", "", []byte(fmt.Sprintf(`{"id":%q,"reason":%q,"category":%q}`, id, reason, category)), xff(ip)...)
}

func (e *env) reportsOf(doc string) []Report {
	e.t.Helper()
	var out []Report
	for _, k := range e.keys("report/") {
		d, err := e.inner.Load(context.Background(), k)
		if err != nil {
			continue
		}
		var r Report
		if json.Unmarshal(d.Data, &r) == nil && (doc == "" || r.DocID == doc) {
			out = append(out, r)
		}
	}
	return out
}

func TestDuplicateReportsCountOnce(t *testing.T) {
	e := newEnv(t, behindProxy)
	tok, _ := e.session()
	d := e.create(tok, "x")
	first := e.want(e.report(d.ViewID, "spam", "203.0.113.1"), 202)
	for _, again := range []string{"spam", "  SPAM ", "Spam", "spam\t"} {
		r := e.want(e.report(d.ViewID, again, "203.0.113.1"), 202)
		if string(r.body) != string(first.body) {
			t.Fatalf("a duplicate was answered %s, want the first report's %s", r.body, first.body)
		}
	}
	e.want(e.report(d.ID, "spam", "203.0.113.1"), 202) // by document id: the same document
	if n := len(e.reportsOf(d.ID)); n != 1 {
		t.Fatalf("%d reports stored for one reporter and one reason, want 1", n)
	}
	// rewording the reason in the same category is still a duplicate (the key is reporter and category)
	if n := len(e.reportsOf(d.ID)); n != 1 {
		t.Fatalf("%d reports", n)
	}
	e.want(e.report(d.ViewID, "a different wording", "203.0.113.1"), 202)               // other again: duplicate
	e.want(e.reportCat(d.ViewID, CategorySpam, "another category", "203.0.113.1"), 202) // a new category: new
	e.want(e.report(d.ViewID, "spam", "203.0.113.2"), 202)                              // another reporter: new
	if n := len(e.reportsOf(d.ID)); n != 3 {
		t.Fatalf("%d reports, want 3", n)
	}
	// the stored report holds no address, only a hash
	for _, r := range e.reportsOf(d.ID) {
		if r.Reporter == "" || strings.Contains(r.Reporter, "203.0.113") {
			t.Fatalf("reporter %q", r.Reporter)
		}
	}
	// a reporter in the same /64 is the same reporter
	e.want(e.report(d.ViewID, "spam", "203.0.113.1"), 202)
	e.want(e.report(d.ViewID, "novel", "2001:db8:1:2::1"), 202)
	e.want(e.report(d.ViewID, "novel", "2001:db8:1:2::ffff"), 202)
	if n := len(e.reportsOf(d.ID)); n != 4 {
		t.Fatalf("%d reports, want 4", n)
	}
}

func TestReportSlotsAreReservedForOtherAddresses(t *testing.T) {
	e := newEnv(t, behindProxy)
	tok, _ := e.session()
	d := e.create(tok, "x")
	// holders of the view link (eight addresses, two categories each: the dedupe is per reporter and
	// category) can have 15 of the 20 slots, no more
	for _, cat := range []string{CategorySpam, CategoryOther} {
		for a := 1; a <= 8; a++ {
			e.want(e.reportCat(d.ViewID, cat, "x", fmt.Sprintf("203.0.113.%d", a)), 202)
		}
	}
	if n := len(e.reportsOf(d.ID)); n != maxReportsOfDoc-5 {
		t.Fatalf("one address stored %d reports, want %d", n, maxReportsOfDoc-5)
	}
	// five real reporters, each from a new address, still get through
	for i := 1; i <= 5; i++ {
		e.want(e.report(d.ViewID, "this is illegal here", fmt.Sprintf("198.51.100.%d", i)), 202)
	}
	if n := len(e.reportsOf(d.ID)); n != maxReportsOfDoc {
		t.Fatalf("%d reports after the real ones, want %d", n, maxReportsOfDoc)
	}
	// full: nothing more is stored (and the answer is the same)
	e.want(e.report(d.ViewID, "late", "198.51.100.99"), 202)
	if n := len(e.reportsOf(d.ID)); n != maxReportsOfDoc {
		t.Fatalf("%d reports, want %d", n, maxReportsOfDoc)
	}
}

func TestPriorityReportsTakeTheSlotOfAnOrdinaryOne(t *testing.T) {
	e := newEnv(t, behindProxy)
	tok, _ := e.session()
	d := e.create(tok, "x")
	for i := 0; i < maxReportsOfDoc; i++ {
		e.want(e.report(d.ViewID, "meh", fmt.Sprintf("203.0.113.%d", i+1)), 202)
		e.clk.Add(time.Second) // reports carry the clock's time: the first is the oldest
	}
	oldest := e.reportsOf(d.ID)
	if len(oldest) != maxReportsOfDoc {
		t.Fatalf("%d", len(oldest))
	}
	var firstID string
	for _, r := range oldest {
		if firstID == "" || r.At.Before(e.mustReport(firstID).At) {
			firstID = r.ID
		}
	}
	// the document is full, and a report whose CATEGORY is on the priority list gets in anyway
	// (case and spacing aside), evicting the oldest ordinary report
	e.want(e.reportCat(d.ViewID, "  CSAM ", "see the link", "198.51.100.1"), 202)
	now := e.reportsOf(d.ID)
	if len(now) != maxReportsOfDoc {
		t.Fatalf("%d reports, want %d", len(now), maxReportsOfDoc)
	}
	var csam int
	for _, r := range now {
		if r.ID == firstID {
			t.Fatal("the oldest ordinary report was kept")
		}
		if r.Priority && r.Category == CategoryCSAM {
			csam++
		}
	}
	if csam != 1 {
		t.Fatalf("%d priority reports", csam)
	}
	// an ordinary report still finds it full
	e.want(e.report(d.ViewID, "late", "198.51.100.2"), 202)
	if n := len(e.reportsOf(d.ID)); n != maxReportsOfDoc {
		t.Fatalf("%d", n)
	}
	// priority reports hold at most half of the slots: more of them from other reporters are kept
	// as ordinary ones, which a full document refuses
	for i := 0; i < maxReportsOfDoc+3; i++ {
		e.want(e.reportCat(d.ViewID, CategoryIllegal, "illegal here", fmt.Sprintf("192.0.2.%d", i+1)), 202)
	}
	all := e.reportsOf(d.ID)
	if len(all) != maxReportsOfDoc {
		t.Fatalf("%d", len(all))
	}
	prio := 0
	for _, r := range all {
		if r.Priority {
			prio++
		}
	}
	if prio != maxPriorityOfDoc {
		t.Fatalf("%d priority reports in a document, want the half cap %d", prio, maxPriorityOfDoc)
	}
	// the priority list is configurable
	e2 := newEnv(t, func(o *Options) { behindProxy(o); o.Config.ReportPriorityCategories = []string{"Spam"} })
	if !e2.svc.isPriority(" spam") || e2.svc.isPriority("csam") {
		t.Fatal("configured priority categories")
	}
	e3 := newEnv(t, func(o *Options) { o.Config.ReportPriorityCategories = []string{} })
	if e3.svc.isPriority("csam") {
		t.Fatal("an empty list means no priority categories")
	}
}

func (e *env) mustReport(id string) Report {
	d, err := e.inner.Load(context.Background(), "report/"+id)
	if err != nil {
		e.t.Fatal(err)
	}
	var r Report
	if err := json.Unmarshal(d.Data, &r); err != nil {
		e.t.Fatal(err)
	}
	return r
}

func TestGlobalEvictionKeepsPriorityReportsLongest(t *testing.T) {
	e := newEnv(t, func(o *Options) { behindProxy(o); o.Config.MaxReports = 10 })
	var docs []docResponse
	for i := 0; i < 3; i++ {
		tok, _ := e.session()
		docs = append(docs, e.create(tok, "x"))
	}
	r := e.want(e.reportCat(docs[0].ViewID, CategoryIllegal, "illegal", "203.0.113.200"), 202) // the oldest of all
	var prio map[string]string
	_ = json.Unmarshal(r.body, &prio)
	time.Sleep(2 * time.Millisecond)
	for i := 0; i < 14; i++ {
		e.want(e.report(docs[i%3].ViewID, "meh", fmt.Sprintf("203.0.113.%d", i+1)), 202)
		time.Sleep(time.Millisecond)
	}
	keys := e.keys("report/")
	if len(keys) > 10 {
		t.Fatalf("%d reports stored, the bound is 10", len(keys))
	}
	if !contains(keys, "report/"+prio["reportId"]) {
		t.Fatal("the oldest report was evicted although it is a priority one and ordinary ones remained")
	}
	// the sweep orders the same way
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !contains(e.keys("report/"), "report/"+prio["reportId"]) {
		t.Fatal("the sweep evicted the priority report")
	}
}

func TestGlobalReportReserveIsForNewReporters(t *testing.T) {
	e := newEnv(t, func(o *Options) { behindProxy(o); o.Config.MaxReports = 10 })
	tok, _ := e.session()
	d := e.create(tok, "x")
	// four addresses, two categories each: 8 are stored (10 minus the reserve of 20%)
	for _, cat := range []string{CategorySpam, CategoryOther} {
		for a := 50; a < 54; a++ {
			e.want(e.reportCat(d.ViewID, cat, "x", fmt.Sprintf("203.0.113.%d", a)), 202)
		}
	}
	if n := len(e.keys("report/")); n != 8 {
		t.Fatalf("four addresses hold %d of the 10 reports, want 8", n)
	}
	// new reporters still get the last two places
	e.want(e.report(d.ViewID, "real one", "198.51.100.1"), 202)
	e.want(e.report(d.ViewID, "real two", "198.51.100.2"), 202)
	if n := len(e.keys("report/")); n != 10 {
		t.Fatalf("%d reports, want 10", n)
	}
	// the reserve is configurable: 0 in the environment means none
	e2 := newEnv(t, func(o *Options) {
		behindProxy(o)
		o.Config.MaxReports = 10
		o.Config.ReportReservePercent = -1
		o.Config.ReportReservedPerDoc = -1
	})
	tok2, _ := e2.session()
	d2 := e2.create(tok2, "x")
	for _, cat := range []string{CategorySpam, CategoryOther} {
		for a := 50; a < 56; a++ {
			e2.want(e2.reportCat(d2.ViewID, cat, "x", fmt.Sprintf("203.0.113.%d", a)), 202)
		}
	}
	if n := len(e2.keys("report/")); n != 10 {
		t.Fatalf("without a reserve the reports stop at %d, want them to fill the 10", n)
	}
}

func TestReporterSaltPersistsAcrossRestarts(t *testing.T) {
	e := newEnv(t, behindProxy)
	tok, _ := e.session()
	d := e.create(tok, "x")
	r1 := e.want(e.report(d.ViewID, "spam", "203.0.113.1"), 202)
	// a second service over the same store: the same reporter is recognised
	svc2, err := New(Options{Store: e.store, Now: e.clk.Now, Config: Config{NoProxy: true, ReportKey: testReportKey}, ClientIP: func(r *http.Request) string { return "203.0.113.1" }})
	if err != nil {
		t.Fatal(err)
	}
	srv2 := httptest.NewServer(svc2.Handler())
	defer srv2.Close()
	resp2, err := http.Post(srv2.URL+"/public/report", "application/json", strings.NewReader(fmt.Sprintf(`{"id":%q,"reason":"spam"}`, d.ViewID)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	body, _ := io.ReadAll(resp2.Body)
	if string(body) != string(r1.body) {
		t.Fatalf("after a restart the duplicate was answered %s, want %s", body, r1.body)
	}
	if n := len(e.reportsOf(d.ID)); n != 1 {
		t.Fatalf("%d reports", n)
	}
}

// ---- 6. the admin route behind a proxy

func TestAdminRouteNeedsTrustedProxiesToBeSet(t *testing.T) {
	mk := func(mod func(*Options)) error {
		o := Options{Store: docstore.NewExpiringAdapter(docstore.NewMemoryAdapter(), docstore.FixedTTL(24*hour)), Config: Config{AdminToken: "a-long-admin-token"}}
		mod(&o)
		_, err := New(o)
		return err
	}
	if err := mk(func(*Options) {}); err != ErrAdminNeedsProxyConfig {
		t.Fatalf("an admin token with no word on proxies: %v", err)
	}
	if err := mk(func(o *Options) { o.Config.AdminToken = "" }); err != nil {
		t.Fatalf("no admin route, no demand: %v", err)
	}
	if err := mk(func(o *Options) { o.Config.NoProxy = true }); err != nil {
		t.Fatalf("explicit none: %v", err)
	}
	if err := mk(func(o *Options) { o.Config.TrustedProxies = []string{"10.0.0.0/8"} }); err != nil {
		t.Fatalf("named proxies: %v", err)
	}
	if err := mk(func(o *Options) { o.ClientIP = func(*http.Request) string { return "x" } }); err != nil {
		t.Fatalf("own client function: %v", err)
	}
	if err := mk(func(o *Options) { o.Config.AdminToken = ""; o.Admin = allowAll{} }); err != ErrAdminNeedsProxyConfig {
		t.Fatalf("a custom guard counts too: %v", err)
	}
	// the message names the variable
	if !strings.Contains(ErrAdminNeedsProxyConfig.Error(), "GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES") || !strings.Contains(ErrAdminNeedsProxyConfig.Error(), "none") {
		t.Fatal(ErrAdminNeedsProxyConfig)
	}
}

func TestOneClientsWrongGuessesDoNotLockOutTheOperator(t *testing.T) {
	e := newEnv(t, behindProxy)
	id := strings.Repeat("B", 22)
	// an attacker behind the same proxy guesses far more than the 20 a minute
	var limited int
	for i := 0; i < 60; i++ {
		switch e.do("DELETE", "/admin/takedown/"+id, "", nil, append(xff("203.0.113.66"), AdminHeader, fmt.Sprint("guess", i))...).StatusCode {
		case 429:
			limited++
		case 403:
		default:
			t.Fatal("unexpected status")
		}
	}
	if limited != 40 {
		t.Fatalf("%d of 60 wrong guesses were rate limited, want 40", limited)
	}
	// the operator, from the same address and from another one, gets in
	e.want(e.do("DELETE", "/admin/takedown/"+id, "", nil, append(xff("203.0.113.66"), AdminHeader, adminToken)...), 202)
	e.want(e.do("DELETE", "/admin/takedown/"+id, "", nil, append(xff("198.51.100.7"), AdminHeader, adminToken)...), 204)
	// and another address's wrong guesses have their own budget
	e.want(e.do("DELETE", "/admin/takedown/"+id, "", nil, append(xff("198.51.100.8"), AdminHeader, "nope")...), 403)
	// the detailed health is under the same guard
	e.want(e.do("GET", "/admin/health", "", nil, append(xff("203.0.113.66"), AdminHeader, "nope")...), 429)
	e.want(e.do("GET", "/admin/health", "", nil, append(xff("203.0.113.66"), AdminHeader, adminToken)...), 200)
}

func TestShortAdminTokenIsWarnedAbout(t *testing.T) {
	if w := (Config{AdminToken: "short", NoProxy: true, ReportKey: testReportKey}).Warnings(); len(w) != 1 || !strings.Contains(w[0], "ALLOW_SHORT_ADMIN_TOKEN") || !strings.Contains(w[0], "GUESSED ONLINE") {
		t.Fatalf("warnings %v", w)
	}
	if w := (Config{AdminToken: strings.Repeat("a", 32), NoProxy: true, ReportKey: testReportKey}).Warnings(); len(w) != 0 {
		t.Fatalf("warnings %v", w)
	}
}

// ---- 7. a global cap on the bytes of request bodies in flight

// holdUpload opens a connection, sends a request head declaring n bytes and a few of them, and
// keeps the connection open: the server is left waiting for the rest of the body.
func holdUpload(t *testing.T, e *env, token string, n int) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(e.srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() }) // best-effort cleanup of a test connection
	if _, err := fmt.Fprintf(conn, "POST /public/doc HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nContent-Length: %d\r\n\r\nabc", token, n); err != nil {
		t.Fatal(err)
	}
	return conn
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestInflightBytesAreCappedGlobally(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		o.Config.MaxDocBytes = 600
		o.Config.MaxInflightBytes = 1000
		o.Config.MaxInflightPerClient = 1000 // every request here is one client: only the global cap is under test
	})
	a, _ := e.session()
	b, _ := e.session()
	c, _ := e.session()
	conn := holdUpload(t, e, a, 600)
	waitFor(t, "the first upload to be admitted", func() bool { return e.svc.flight.held() == 600 })
	// a second 600 would make 1200 in flight: refused up front, nothing read, nothing charged
	r := e.want(e.do("POST", "/public/doc", b, bytes.Repeat([]byte("b"), 600)), 503)
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	if used := quotaUsed(e, "ip:127.0.0.1"); used != 600 {
		t.Fatalf("charged %d, want only the first upload's 600", used)
	}
	// a smaller one fits beside it (600 + 400), and a chunked one holds the cap of 600 and is
	// refused while the first is in flight
	e.want(e.do("POST", "/public/doc", b, bytes.Repeat([]byte("b"), 400)), 201)
	e.want(e.chunked("POST", "/public/doc", c, "chunked"), 503)
	waitFor(t, "the finished uploads to give theirs back", func() bool { return e.svc.flight.held() == 600 })
	// when the slow client goes, its bytes are released and others go through
	_ = conn.Close() // the point of the test: the client goes away
	waitFor(t, "the held upload to end", func() bool { return e.svc.flight.held() == 0 })
	e.want(e.chunked("POST", "/public/doc", c, "chunked"), 201)
	waitFor(t, "nothing in flight at rest", func() bool { return e.svc.flight.held() == 0 })
}

func TestInflightNeverBelowTheDocumentCap(t *testing.T) {
	if err := (Config{MaxDocBytes: 100, MaxInflightBytes: 100}).withDefaults().Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Config{MaxDocBytes: 100, MaxInflightBytes: 99}).withDefaults().Validate(); err == nil {
		t.Fatal("an in-flight cap below the document cap accepted")
	}
	if c := (Config{MaxDocBytes: 1 << 30}).withDefaults(); c.MaxInflightBytes != DefaultMaxInflightBytes || c.Validate() == nil {
		t.Fatalf("default in-flight cap %d with a 1 GiB document cap must not validate", c.MaxInflightBytes)
	}
	var f inflight
	f.max = 10
	if f.acquire("c", "n", 6) != flightOK || f.acquire("c", "n", 5) != flightGlobal || f.acquire("c", "n", 4) != flightOK {
		t.Fatal("acquire")
	}
	f.release("c", "n", 10)
	f.release("c", "n", 3) // more than held: clamped
	if f.held() != 0 {
		t.Fatal(f.held())
	}
}

// ---- 8. nits

func TestRefundAfterTheWindowRolledOverDoesNotCreditTheNewWindow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := NewMemoryQuota(100, time.Hour)
	q.Now = func() time.Time { return now }
	ok, w1, _, _ := q.TakeIn(ctx, "a", 80)
	if !ok {
		t.Fatal("refused")
	}
	now = now.Add(2 * time.Hour) // the window has ended
	ok, w2, _, _ := q.TakeIn(ctx, "a", 50)
	if !ok || w1 == w2 {
		t.Fatalf("second window: %v %d %d", ok, w1, w2)
	}
	q.RefundIn(ctx, "a", 80, w1) // the old charge's refund arrives late
	if ok, _, _ := q.Take(ctx, "a", 51); ok {
		t.Fatal("a refund of the previous window's charge was credited to the new window")
	}
	q.RefundIn(ctx, "a", 20, w2) // a refund of this window's charge counts
	if ok, _, _ := q.Take(ctx, "a", 70); !ok {
		t.Fatal("a refund within its own window was lost")
	}
}

func TestTicketRefundsIntoTheWindowItWasChargedIn(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.IPDailyBytes = 100 })
	admit := func(n int) *ticket {
		req := httptest.NewRequest("POST", "/public/doc", nil)
		req.ContentLength = int64(n)
		req.RemoteAddr = "192.0.2.1:1234"
		tk, ok := e.svc.admit(httptest.NewRecorder(), req)
		if !ok {
			t.Fatalf("admit(%d) refused", n)
		}
		return tk
	}
	slow := admit(80) // a slow upload that spans the rollover
	e.clk.Add(25 * time.Hour)
	fresh := admit(50)
	fresh.keep()
	slow.release() // it fails after the window rolled over: its 80 were charged to the old window
	req := httptest.NewRequest("POST", "/public/doc", nil)
	req.ContentLength = 51
	req.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	if _, ok := e.svc.admit(rec, req); ok || rec.Code != 429 {
		t.Fatalf("the late refund went to the new window: admit ok=%v code=%d", ok, rec.Code)
	}
}

func TestQuotaEvictsTheLeastRecentlyUsedKeyAndLogs(t *testing.T) {
	logs := captureLog(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := NewMemoryQuota(100, 24*time.Hour)
	q.Now = func() time.Time { return now }
	q.maxKeys = 3
	for _, k := range []string{"a", "b", "c"} {
		if ok, _, _ := q.Take(ctx, k, 90); !ok {
			t.Fatal(k)
		}
	}
	// a is used again, so b is the least recently used
	if ok, _, _ := q.Take(ctx, "a", 5); !ok {
		t.Fatal("a")
	}
	if ok, _, _ := q.Take(ctx, "d", 10); !ok { // the table is full: b goes
		t.Fatal("d")
	}
	if !strings.Contains(logs.String(), "least recently used") {
		t.Fatalf("the drop was not logged: %q", logs.String())
	}
	// a and c keep their spent budgets (an arbitrary choice could have reset them)
	for _, k := range []string{"a", "c"} {
		if ok, _, _ := q.Take(ctx, k, 50); ok {
			t.Fatalf("the window of %q was reset by the eviction", k)
		}
	}
	if q.lru.Len() != len(q.keys) || len(q.keys) > 3 {
		t.Fatalf("keys %d, lru %d", len(q.keys), q.lru.Len())
	}
}

func TestQuotaDropsExpiredKeysBeforeLiveOnes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := NewMemoryQuota(100, time.Hour)
	q.Now = func() time.Time { return now }
	q.maxKeys = 2
	_, _, _ = q.Take(ctx, "old", 90)
	now = now.Add(30 * time.Minute)
	_, _, _ = q.Take(ctx, "live", 90)
	now = now.Add(45 * time.Minute) // old has ended, live has not
	_, _, _ = q.Take(ctx, "new", 10)
	if ok, _, _ := q.Take(ctx, "live", 50); ok {
		t.Fatal("a live window was dropped while an expired one could go")
	}
}

func TestFromEnvMinorsVariables(t *testing.T) {
	get := func(env map[string]string) (Config, error) {
		return FromEnv(func(k string) string { return env[k] })
	}
	c, err := get(map[string]string{
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS_PER_NET":       "40",
		"GOJIBERRY_DOCSTORE_PUBLIC_SESSION_RESERVE_PERCENT":    "0",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_INFLIGHT_BYTES":         "1048576",
		"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_RESERVED_PER_DOC":    "3",
		"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_RESERVE_PERCENT":     "30",
		"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_PRIORITY_CATEGORIES": " CSAM , Spam ,",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_DOC_BYTES":              "1000",
		"GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES":            "none",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS_PER_ADDRESS":   "5",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS":               "1000",
	})
	if err != nil {
		t.Fatal(err)
	}
	d := c.withDefaults()
	if d.MaxSessionsPerNet != 40 || d.SessionReservePercent != -1 || d.MaxInflightBytes != 1<<20 ||
		d.ReportReservedPerDoc != 3 || d.ReportReservePercent != 30 || len(d.ReportPriorityCategories) != 2 ||
		d.ReportPriorityCategories[0] != "csam" || d.ReportPriorityCategories[1] != "spam" {
		t.Fatalf("config %+v", d)
	}
	d = Config{}.withDefaults()
	if d.MaxSessionsPerNet != DefaultMaxPerNet || d.SessionReservePercent != 20 || d.MaxInflightBytes != 256<<20 ||
		d.ReportReservedPerDoc != 5 || d.ReportReservePercent != 20 || len(d.ReportPriorityCategories) != 2 {
		t.Fatalf("defaults %+v", d)
	}
	if c, err := get(map[string]string{"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_PRIORITY_CATEGORIES": "none"}); err != nil || c.ReportPriorityCategories == nil || len(c.withDefaults().ReportPriorityCategories) != 0 {
		t.Fatalf("none: %+v %v", c, err)
	}
	for k, v := range map[string]string{
		"GOJIBERRY_DOCSTORE_PUBLIC_SESSION_RESERVE_PERCENT": "95",
		"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_RESERVE_PERCENT":  "x",
		"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_RESERVED_PER_DOC": "20",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_INFLIGHT_BYTES":      "-4",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS_PER_NET":    "0",
	} {
		if _, err := get(map[string]string{k: v}); err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("%s=%q: %v", k, v, err)
		}
	}
	// the net cap is never below the per-address cap, nor above the global one
	d = Config{MaxSessions: 50, MaxSessionsPerAddress: 20, MaxSessionsPerNet: 5}.withDefaults()
	if d.MaxSessionsPerNet != 20 {
		t.Fatalf("net cap %d", d.MaxSessionsPerNet)
	}
	d = Config{MaxSessions: 50, MaxSessionsPerNet: 500}.withDefaults()
	if d.MaxSessionsPerNet != 50 {
		t.Fatalf("net cap %d", d.MaxSessionsPerNet)
	}
}

func TestConfigWarnsAboutLegacyFloorAndWideCap(t *testing.T) {
	has := func(ws []string, sub string) bool {
		for _, w := range ws {
			if strings.Contains(w, sub) {
				return true
			}
		}
		return false
	}
	base := Config{NoProxy: true, ReportKey: testReportKey}
	// a variable that is no longer read
	c, err := FromEnv(func(k string) string {
		if k == "GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS_PER_MINUTE" {
			return "30"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	c.NoProxy, c.ReportKey = true, testReportKey
	if !has(c.Warnings(), "SESSIONS_PER_MINUTE is no longer read") {
		t.Fatalf("no warning for the old variable: %v", c.Warnings())
	}
	// a minimum rate under the floor is raised and said so
	low := base
	low.MinUploadRate = 5
	if !has(low.Warnings(), "below the floor") || low.withDefaults().MinUploadRate != MinUploadRateFloor {
		t.Fatalf("rate %d, warnings %v", low.withDefaults().MinUploadRate, low.Warnings())
	}
	off := base
	off.MinUploadRate = -1
	if has(off.Warnings(), "floor") || off.withDefaults().MinUploadRate != -1 {
		t.Fatal("a negative rate (no minimum) must be left alone")
	}
	// a wide cap under the net cap is changed, and an unset one moved by the net cap is said
	w := base
	w.MaxSessionsPerNet, w.MaxSessionsPerWide = 400, 200
	if !has(w.Warnings(), "MAX_SESSIONS_PER_WIDE 200") {
		t.Fatalf("no warning for a clamped wide cap: %v", w.Warnings())
	}
	moved := base
	moved.MaxSessionsPerNet = 900
	if !has(moved.Warnings(), "MAX_SESSIONS_PER_WIDE is not set") {
		t.Fatalf("no warning for a wide cap moved by the net cap: %v", moved.Warnings())
	}
	if len(base.Warnings()) != 0 {
		t.Fatalf("defaults warn: %v", base.Warnings())
	}
}
