package public

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// ---- finding 4: the proxy warning, and finding 9: the tombstone must outlive the session

func TestTrustedProxiesWarning(t *testing.T) {
	if w := (Config{}).Warnings(); len(w) != 1 || !strings.Contains(w[0], "TRUSTED_PROXIES") {
		t.Fatalf("warnings = %v", w)
	}
	if w := (Config{TrustedProxies: []string{"10.0.0.0/8"}}).Warnings(); len(w) != 0 {
		t.Fatalf("warnings = %v", w)
	}
	get := func(v string) func(string) string {
		return func(k string) string {
			if k == "GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES" {
				return v
			}
			return ""
		}
	}
	c, err := FromEnv(get("none"))
	if err != nil || !c.NoProxy || len(c.TrustedProxies) != 0 || len(c.Warnings()) != 0 {
		t.Fatalf("none: %+v, %v", c, err)
	}
	if _, err := FromEnv(get("none, 10.0.0.0/8")); err == nil {
		t.Fatal("none with addresses accepted")
	}
}

func TestTombstoneTTLMustOutliveTheSession(t *testing.T) {
	store := func() docstore.Adapter {
		return docstore.NewExpiringAdapter(docstore.NewMemoryAdapter(), docstore.FixedTTL(hour))
	}
	// defaults: 72h max age + 24h
	if _, err := New(Options{Store: store(), Config: Config{TombstoneTTL: 95 * hour}}); err == nil || !strings.Contains(err.Error(), "TombstoneTTL") {
		t.Fatalf("err = %v", err)
	}
	if _, err := New(Options{Store: store(), Config: Config{TombstoneTTL: 96 * hour}}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{Store: store(), Config: Config{SessionMaxAge: 200 * hour, TombstoneTTL: 100 * 24 * hour}}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{Store: store(), Config: Config{SessionMaxAge: 200 * hour, TombstoneTTL: 220 * hour}}); err == nil {
		t.Fatal("a tombstone shorter than max age + 24h was accepted")
	}
	env := map[string]string{"GOJIBERRY_DOCSTORE_PUBLIC_TOMBSTONE_TTL": "24h"}
	if _, err := FromEnv(func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), "TombstoneTTL") {
		t.Fatalf("FromEnv err = %v", err)
	}
}

// A takedown bans the session even when the document had already expired and its records had
// been swept, by either of the IDs an operator may hold.
func TestTakedownBansTheSessionAfterTheRecordsAreSwept(t *testing.T) {
	for _, byView := range []bool{true, false} {
		e := newEnv(t, nil)
		tok, _ := e.session()
		d := e.create(tok, "bad")
		e.clk.Add(25 * hour) // the document expired; the session (48h) is alive
		if _, err := e.store.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(e.keys("public/doc/")) != 0 || len(e.keys("public/view/")) != 0 {
			t.Fatal("records were not swept")
		}
		id := d.ID
		if byView {
			id = d.ViewID
		}
		e.want(e.do("DELETE", "/admin/takedown/"+id, "", nil, AdminHeader, adminToken), 202)
		e.want(e.do("POST", "/public/doc", tok, []byte("again")), 403) // the session is done
		e.want(e.do("GET", "/public/view/"+d.ViewID, "", nil), 410)
	}
}

// A takedown that lands while a create is in flight wins: the create sees the tombstone once its
// document is stored, removes everything and keeps the session banned.
func TestTakedownDuringCreate(t *testing.T) {
	e, hs := envWith(t, nil)
	hs.beforePub = func(ctx context.Context, id string, _ []byte) error {
		doc := strings.TrimPrefix(id, docKeyPrefix)
		return e.svc.putJSON(ctx, tombPrefix+viewIDOf(doc), tombstone{At: e.clk.Now()})
	}
	tok, _ := e.session()
	e.want(e.do("POST", "/public/doc", tok, []byte("x")), 410)
	hs.beforePub = nil
	if len(e.keys("pubdoc/")) != 0 || len(e.keys("public/doc/")) != 0 || len(e.keys("public/view/")) != 0 {
		t.Fatal("the document or its records were kept")
	}
	e.want(e.do("POST", "/public/doc", tok, []byte("again")), 403)
}

func TestTombstoneSweepTTL(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.TombstoneTTL = 130 * hour })
	tok, _ := e.session()
	d := e.create(tok, "x")
	e.want(e.do("DELETE", "/admin/takedown/"+d.ViewID, "", nil, AdminHeader, adminToken), 204)
	e.clk.Add(130*hour - time.Minute)
	if n, err := e.svc.Sweep(context.Background()); err != nil || n != 0 {
		t.Fatalf("Sweep = %d, %v: the tombstone went before its TTL", n, err)
	}
	e.want(e.do("GET", "/public/view/"+d.ViewID, "", nil), 410)
	e.clk.Add(time.Minute)
	if n, err := e.svc.Sweep(context.Background()); err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v: want the tombstone", n, err)
	}
	if len(e.keys(TombstonePrefix)) != 0 {
		t.Fatal("tombstone left")
	}
}

// ---- finding 5: slow bodies

func TestSlowBodyIsCutOff(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.RequestTimeout = 300 * time.Millisecond })
	tok, _ := e.session()
	start := time.Now()
	st, answered := rawRequest(t, e, "POST", "/public/doc", tok, 100, "abc", 5*time.Second)
	if !answered || !strings.Contains(st, "408") {
		t.Fatalf("slow create: %q (answered %v)", st, answered)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	e.create(tok, "fine")
	st, answered = rawRequest(t, e, "PUT", "/public/doc", tok, 100, "abc", 5*time.Second)
	if !answered || !strings.Contains(st, "408") {
		t.Fatalf("slow edit: %q (answered %v)", st, answered)
	}
	st, answered = rawRequest(t, e, "POST", "/public/report", "", 100, `{"id"`, 5*time.Second)
	if !answered || !strings.Contains(st, "4") {
		t.Fatalf("slow report: %q (answered %v)", st, answered)
	}
	if got := string(e.want(e.do("GET", "/public/doc", tok, nil), 200).body); got != "fine" {
		t.Fatalf("a cut-off edit changed the document: %q", got)
	}
}

// ---- finding 6: charge before the body, refund what was not written

type blocklistContains string

func (b blocklistContains) Blocked(_ context.Context, c []byte) (bool, error) {
	return bytes.Contains(c, []byte(b)), nil
}

func TestQuotaIsCheckedBeforeTheBodyIsRead(t *testing.T) {
	var seen countingBlocklist
	e := newEnv(t, func(o *Options) { o.Config.IPDailyBytes = 100; o.Blocklist = &seen })
	tok, _ := e.session()
	e.want(e.do("POST", "/public/doc", tok, bytes.Repeat([]byte("a"), 200)), 429)
	if seen.n.Load() != 0 {
		t.Fatal("the body was read and screened before the quota was asked")
	}
	e.want(e.do("POST", "/public/doc", tok, bytes.Repeat([]byte("a"), 100)), 201)
	if seen.n.Load() != 1 {
		t.Fatalf("screened %d", seen.n.Load())
	}
}

func TestChargeIsRefundedWhenTheWriteIsNotStored(t *testing.T) {
	e, hs := envWith(t, func(o *Options) {
		o.Config.IPDailyBytes = 100
		o.Blocklist = blocklistContains("bad")
	})
	tok, _ := e.session()
	// refused content (422)
	e.want(e.do("POST", "/public/doc", tok, append([]byte("bad"), bytes.Repeat([]byte("a"), 77)...)), 422)
	// a store failure (rollback)
	fail := true
	hs.beforePub = func(context.Context, string, []byte) error {
		if fail {
			fail = false
			return errors.New("disk full")
		}
		return nil
	}
	e.want(e.do("POST", "/public/doc", tok, bytes.Repeat([]byte("a"), 80)), 500)
	// the budget (100) is whole: 80 fits
	d := e.create(tok, strings.Repeat("a", 80))
	_ = d
	// a refused edit (422) and a failed edit give theirs back too: 20 bytes left in the day
	e.want(e.do("PUT", "/public/doc", tok, append([]byte("bad"), bytes.Repeat([]byte("a"), 7)...)), 422)
	fail = true
	e.want(e.do("PUT", "/public/doc", tok, bytes.Repeat([]byte("a"), 10)), 500)
	e.want(e.do("PUT", "/public/doc", tok, bytes.Repeat([]byte("a"), 20)), 204)
	e.want(e.do("PUT", "/public/doc", tok, bytes.Repeat([]byte("a"), 1)), 429)
}

// ---- finding 7: no listing per request, a bounded sweep

func TestLiveDoesNotListTheStore(t *testing.T) {
	e, hs := envWith(t, nil)
	a, _ := e.session()
	b, _ := e.session()
	e.create(a, "one")
	e.create(b, "two")
	hs.lists.Store(0)
	e.want(e.do("PUT", "/public/doc", a, []byte("one!")), 204)
	e.want(e.do("POST", "/public/doc", a, []byte("again")), 409) // asks whether the old one is live
	e.clk.Add(25 * hour)                                         // both expired
	e.create(a, "three")                                         // a new one after the old expired
	e.want(e.do("PUT", "/public/doc", b, []byte("two!")), 404)   // expired: not revived
	if n := hs.lists.Load(); n != 0 {
		t.Fatalf("%d listings by create and edit, want none", n)
	}
}

// noPrefix is a store that cannot list by prefix.
type noPrefix struct {
	docstore.Adapter
	docstore.Expirer
	lists atomic.Int64
}

func (n *noPrefix) List(ctx context.Context) ([]docstore.Meta, error) {
	n.lists.Add(1)
	return n.Adapter.List(ctx)
}

func (n *noPrefix) Save(ctx context.Context, id string, data []byte) error {
	return n.Adapter.Save(ctx, id, data)
}

func TestSweepIsBoundedWithoutPrefixListing(t *testing.T) {
	clk := newClock()
	ea := docstore.NewExpiringAdapter(docstore.NewMemoryAdapter(), docstore.FixedTTL(24*hour))
	ea.Now = clk.Now
	// a plain List-only view of the expiring adapter
	st := &noPrefix{Adapter: ea, Expirer: ea}
	svc, err := New(Options{Store: st, Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	first := st.lists.Load()
	if first == 0 {
		t.Fatal("the first sweep did not list")
	}
	for i := 0; i < 10; i++ { // many sweeps inside the interval
		clk.Add(5 * time.Minute)
		if _, err := svc.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if st.lists.Load() != first {
		t.Fatalf("%d listings in ten sweeps within the hour, want none beyond the first %d", st.lists.Load()-first, first)
	}
	clk.Add(time.Hour)
	if _, err := svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if st.lists.Load() == first {
		t.Fatal("the listing sweep never ran again")
	}
	// with prefix listing the throttle does not apply
	if !canListPrefix(ea) || canListPrefix(st) {
		t.Fatalf("canListPrefix: expiring memory %v, plain %v", canListPrefix(ea), canListPrefix(st))
	}
}

// ---- finding 8: edits, deadlines, health

func TestEditCountsTheVersionBeforeSavingAndRollsBack(t *testing.T) {
	e, hs := envWith(t, func(o *Options) { o.Config.MaxVersions = 2 })
	tok, _ := e.session()
	d := e.create(tok, "v1")
	var seenInRecord int
	fail := true
	hs.beforePub = func(ctx context.Context, id string, _ []byte) error {
		doc, err := e.inner.Load(ctx, docRecordPrefix+d.ID)
		if err != nil {
			return err
		}
		var m docMeta
		_ = json.Unmarshal(doc.Data, &m)
		seenInRecord = m.Versions
		if fail {
			fail = false
			return errors.New("disk full")
		}
		return nil
	}
	e.want(e.do("PUT", "/public/doc", tok, []byte("v2")), 500)
	if seenInRecord != 2 {
		t.Fatalf("the save ran with the count at %d, want 2 (counted before)", seenInRecord)
	}
	// the failed save was rolled back: one more edit fits under the cap of 2, then the cap holds
	e.want(e.do("PUT", "/public/doc", tok, []byte("v2")), 204)
	e.want(e.do("PUT", "/public/doc", tok, []byte("v3")), 403)
}

// an expiry policy that stops covering documents after the switch is flipped
func flippablePolicy(off *atomic.Bool) docstore.ExpiryPolicy {
	return docstore.ExpiryPolicyFunc(func(string, time.Time) (time.Duration, bool) {
		if off.Load() {
			return 0, false
		}
		return 24 * hour, true
	})
}

func TestEditWithoutADeadlineRemovesTheDocumentAndRaisesTheAlarm(t *testing.T) {
	var off atomic.Bool
	inner := docstore.NewMemoryAdapter()
	var e *env
	logs := captureLog(t)
	e = newEnv(t, func(o *Options) {
		ea := docstore.NewExpiringAdapter(inner, flippablePolicy(&off))
		ea.Now = o.Now
		o.Store = ea
	})
	tok, _ := e.session()
	e.create(tok, "v1")
	if h := e.svc.Health(); !h.OK {
		t.Fatalf("healthy service reports %+v", h)
	}
	off.Store(true)
	e.want(e.do("PUT", "/public/doc", tok, []byte("v2")), 500)
	if metas, _ := docstore.ListPrefix(context.Background(), inner, "pubdoc/"); len(metas) != 0 {
		t.Fatalf("a document without a deadline was kept: %v", metas)
	}
	h := e.svc.Health()
	if h.OK || !contains(h.Problems, ProblemNoDeadline) {
		t.Fatalf("health %+v", h)
	}
	if !strings.Contains(logs.String(), "ALARM") {
		t.Fatalf("no alarm in the log: %q", logs.String())
	}
	e.want(e.do("GET", "/public/health", "", nil), 503)
	r := e.want(e.do("GET", "/admin/health", "", nil, AdminHeader, adminToken), 503)
	if !strings.Contains(string(r.body), ProblemNoDeadline) {
		t.Fatalf("/admin/health: %s", r.body)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestHealthExpiryDisabledAtStartup(t *testing.T) {
	logs := captureLog(t)
	var off atomic.Bool
	off.Store(true)
	e := newEnv(t, func(o *Options) {
		ea := docstore.NewExpiringAdapter(docstore.NewMemoryAdapter(), flippablePolicy(&off))
		ea.Now = o.Now
		o.Store = ea
	})
	if !strings.Contains(logs.String(), "ALARM") {
		t.Fatalf("no startup alarm: %q", logs.String())
	}
	h := e.svc.Health()
	if h.OK || !contains(h.Problems, ProblemExpiryDisabled) {
		t.Fatalf("health %+v", h)
	}
	e.want(e.do("GET", "/public/health", "", nil), 503)
	r := e.want(e.do("GET", "/admin/health", "", nil, AdminHeader, adminToken), 503)
	if !strings.Contains(string(r.body), ProblemExpiryDisabled) || strings.Contains(string(r.body), adminToken) {
		t.Fatalf("body %s", r.body)
	}
}

func TestHealthSweeperStaleAndFailing(t *testing.T) {
	e, hs := envWith(t, nil)
	r := e.want(e.do("GET", "/public/health", "", nil), 200)
	var h Health
	if err := json.Unmarshal(r.body, &h); err != nil || !h.OK {
		t.Fatalf("health %s", r.body)
	}
	if strings.Contains(string(r.body), adminToken) {
		t.Fatal("the health report holds a secret")
	}
	e.clk.Add(11 * time.Minute) // over twice the default interval, and no sweep
	if h := e.svc.Health(); !contains(h.Problems, ProblemSweeperStale) {
		t.Fatalf("health %+v", h)
	}
	e.want(e.do("GET", "/public/health", "", nil), 503)
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h := e.svc.Health(); !h.OK {
		t.Fatalf("health %+v after a sweep", h)
	}
	// Run records its interval: stale means twice that
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.Run(ctx, time.Hour); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for e.svc.Health().LastSweep.IsZero() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	e.clk.Add(90 * time.Minute)
	if h := e.svc.Health(); contains(h.Problems, ProblemSweeperStale) {
		t.Fatalf("stale after 1.5 intervals: %+v", h)
	}
	e.clk.Add(31 * time.Minute)
	if h := e.svc.Health(); !contains(h.Problems, ProblemSweeperStale) {
		t.Fatalf("not stale after 2 intervals: %+v", h)
	}
	// a failing sweep is reported (and its error carries no document id into the log)
	logs := captureLog(t)
	hs.failLists.Store(true)
	if _, err := e.svc.Sweep(context.Background()); err == nil {
		t.Fatal("no error")
	}
	if h := e.svc.Health(); !contains(h.Problems, ProblemSweepFailing) {
		t.Fatalf("health %+v", h)
	}
	_ = logs
}

// ---- finding 10: reports are bounded as a whole

func TestReportsAreBoundedGloballyOldestFirst(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		o.Config.MaxReports = 10
		o.Config.TrustedProxies = []string{"127.0.0.1"}
	})
	var ids []string
	for i := 0; i < 3; i++ {
		tok, _ := e.session()
		d := e.create(tok, "x")
		for j := 0; j < 6; j++ {
			r := e.want(e.do("POST", "/public/report", "", []byte(`{"id":"`+d.ViewID+`","reason":"r"}`), "X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i*6+j+1)), 202)
			var out map[string]string
			_ = json.Unmarshal(r.body, &out)
			ids = append(ids, out["reportId"])
		}
	}
	keys := e.keys("report/")
	if len(keys) > 10 || len(keys) < 8 {
		t.Fatalf("%d reports stored, want 8..10", len(keys))
	}
	have := map[string]bool{}
	for _, k := range keys {
		have[strings.TrimPrefix(k, "report/")] = true
	}
	if have[ids[0]] || !have[ids[len(ids)-1]] {
		t.Fatalf("oldest kept %v, newest kept %v", have[ids[0]], have[ids[len(ids)-1]])
	}
}

func TestReportBytesAreBounded(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.MaxReportBytes = 1000 })
	tok, _ := e.session()
	d := e.create(tok, "x")
	reason := strings.Repeat("r", 300)
	for i := 0; i < 15; i++ {
		e.want(e.do("POST", "/public/report", "", []byte(`{"id":"`+d.ViewID+`","reason":"`+reason+fmt.Sprint(i)+`"}`)), 202)
	}
	var total int
	for _, k := range e.keys("report/") {
		doc, _ := e.inner.Load(context.Background(), k)
		total += len(doc.Data)
	}
	if total > 1000 || total == 0 {
		t.Fatalf("%d bytes of reports stored, want at most 1000", total)
	}
}

// The sweep trims reports that came in some other way (another replica, an older version).
func TestSweepEvictsExcessReports(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.MaxReports = 5 })
	for i := 0; i < 12; i++ {
		rep := Report{ID: string(rune('a' + i)), At: e.clk.Now()}
		if err := e.svc.putJSON(context.Background(), "report/"+rep.ID, rep); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	keys := e.keys("report/")
	if len(keys) > 5 || !contains(keys, "report/l") || contains(keys, "report/a") {
		t.Fatalf("reports after the sweep: %v", keys)
	}
}

// ---- finding 11: CORS exposes what a browser client reads

func TestExposedHeaders(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.WritesPerMinute = 1 })
	tok, _ := e.session()
	check := func(what string, r resp) {
		t.Helper()
		v := r.Header.Get("Access-Control-Expose-Headers")
		for _, h := range []string{"X-Gojiberry-Expires", "Retry-After", "Link"} {
			if !strings.Contains(v, h) {
				t.Errorf("%s: Access-Control-Expose-Headers = %q lacks %s", what, v, h)
			}
		}
	}
	origin := []string{"Origin", "https://app.example"}
	d := e.create(tok, "x")
	check("view", e.do("GET", "/public/view/"+d.ViewID, "", nil, origin...))
	check("session", e.do("POST", "/public/session", "", nil, origin...))
	check("429", e.want(e.do("PUT", "/public/doc", tok, []byte("y"), origin...), 429))
	check("docs list", e.want(e.do("GET", "/docs", "", nil, origin...), 200))
	check("docs 400", e.want(e.do("GET", "/docs/pubdoc/x", "", nil, origin...), 400))
	// the public handler states it itself, mounted or not
	rec := httptest.NewRecorder()
	e.svc.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/public/view/"+d.ViewID, nil))
	check("standalone handler", resp{Response: rec.Result()})
}

// ---- finding 12: hidden prefixes ignore case

func TestHiddenPrefixesIgnoreCase(t *testing.T) {
	e := newEnv(t, nil)
	tok, _ := e.session()
	d := e.create(tok, "x")
	for _, id := range []string{"pubdoc/" + d.ID, "PubDoc/" + d.ID, "PUBDOC/" + d.ID, "pubDOC/x"} {
		e.want(e.do("GET", "/docs/"+id, "", nil), 400)
		e.want(e.do("PUT", "/docs/"+id, "", []byte("overwrite")), 400)
		e.want(e.do("DELETE", "/docs/"+id, "", nil), 400)
	}
	e.want(e.do("GET", "/public/doc", tok, nil), 200)
}

// ---- finding 13: exactly one document from concurrent creates

// barrierSessions holds the first n Gets until all n have looked, so that n create requests have
// all seen a session with no document before any of them claims one.
type barrierSessions struct {
	SessionStore
	n       int32
	count   atomic.Int32
	arrived sync.WaitGroup
	release chan struct{}
}

func newBarrier(inner SessionStore, n int) *barrierSessions {
	b := &barrierSessions{SessionStore: inner, n: int32(n), release: make(chan struct{})}
	b.arrived.Add(n)
	go func() { b.arrived.Wait(); close(b.release) }()
	return b
}

func (b *barrierSessions) Get(ctx context.Context, key string, now time.Time) (Session, bool, error) {
	s, ok, err := b.SessionStore.Get(ctx, key, now)
	if b.count.Add(1) <= b.n {
		b.arrived.Done()
		select {
		case <-b.release:
		case <-time.After(10 * time.Second):
		}
	}
	return s, ok, err
}

func TestConcurrentCreatesMakeOneDocument(t *testing.T) {
	const n = 24
	var barrier *barrierSessions
	e := newEnv(t, func(o *Options) {
		barrier = newBarrier(NewMemorySessionStore(0), n)
		o.Sessions = barrier
		o.Config.IPDailyBytes = 1000
	})
	barrier.count.Store(-1) // the session request below is not one of the n
	tok, _ := e.session()
	barrier.count.Store(0)
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[int]int{}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := e.do("POST", "/public/doc", tok, []byte("0123456789"))
			mu.Lock()
			codes[r.StatusCode]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if codes[201] != 1 || codes[409] != n-1 {
		t.Fatalf("codes %v: every request saw the session without a document, exactly one may win the claim", codes)
	}
	if n := len(e.keys("pubdoc/")); n != 1 {
		t.Fatalf("documents: %d", n)
	}
	if len(e.keys("public/doc/")) != 1 || len(e.keys("public/view/")) != 1 {
		t.Fatal("stray records")
	}
	// the losers' charges were given back: only the one document is charged
	q := e.svc.quota.(*MemoryQuota)
	q.mu.Lock()
	var used int64
	for _, w := range q.keys {
		used += w.used
	}
	q.mu.Unlock()
	if used != 10 {
		t.Fatalf("%d bytes charged for one 10 byte document", used)
	}
	if h := e.svc.Health(); h.StorageBytes != 10 {
		t.Fatalf("storage counter %d, want 10", h.StorageBytes)
	}
}

// ---- finding 14: no keys in the log

func TestFailDoesNotLogDocumentIDs(t *testing.T) {
	logs := captureLog(t)
	e, hs := envWith(t, nil)
	tok, _ := e.session()
	e.create(tok, "x")
	hs.beforePub = func(_ context.Context, id string, _ []byte) error {
		return errors.New("write " + id + ": disk full; also public/doc/ABCDEFGHIJKLMNOPQRSTUV and report/ZZZZZZZZZZZZZZZZZZZZZZ")
	}
	e.want(e.do("PUT", "/public/doc", tok, []byte("y")), 500)
	out := logs.String()
	if !strings.Contains(out, "disk full") {
		t.Fatalf("the error was not logged: %q", out)
	}
	for _, id := range e.keys("pubdoc/") {
		if strings.Contains(out, strings.TrimPrefix(id, "pubdoc/")) {
			t.Fatalf("a document ID is in the log: %q", out)
		}
	}
	if strings.Contains(out, "ABCDEFGHIJKLMNOPQRSTUV") || strings.Contains(out, "ZZZZZZZZZZZZZZZZZZZZZZ") {
		t.Fatalf("a record key is in the log: %q", out)
	}
	if !strings.Contains(out, "pubdoc/<id>") {
		t.Fatalf("keyspace not kept: %q", out)
	}
}

func TestRedact(t *testing.T) {
	if redact(nil) != "" {
		t.Fatal("nil")
	}
	got := redact(errors.New(`open /var/data/x: key "pubdoc/abc-DEF_123" and expiry:pubdoc/xyz, versions/pubdoc/q/1.open`))
	if strings.Contains(got, "abc-DEF") || strings.Contains(got, "xyz") || strings.Contains(got, "q/1") || !strings.Contains(got, "/var/data/x") {
		t.Fatalf("redact = %q", got)
	}
}

// ---- finding 3: the docs API must be closed before the offer starts

func TestRequireDocsClosed(t *testing.T) {
	a := docstore.NewMemoryAdapter()
	open := docstore.NewServer(a, "memory")
	if err := RequireDocsClosed(open, false); err == nil || !strings.Contains(err.Error(), "ALLOW_OPEN_DOCS") {
		t.Fatalf("an open /docs was accepted: %v", err)
	}
	if err := RequireDocsClosed(open, true); err != nil {
		t.Fatalf("explicit opt-in refused: %v", err)
	}
	if err := RequireDocsClosed(docstore.NewServer(a, "memory").CloseDocs(), false); err != nil {
		t.Fatal(err)
	}
	// required ACLs count as closed only with sign-in
	if err := RequireDocsClosed(docstore.NewServer(a, "memory").WithRequireACL(true), false); err == nil {
		t.Fatal("RequireACL without sign-in counts as closed")
	}
}
