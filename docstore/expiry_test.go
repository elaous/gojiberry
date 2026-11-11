package docstore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

const day = 24 * time.Hour

func newExpiring(inner Adapter) (*ExpiringAdapter, *fakeClock) {
	clk := newClock()
	ea := NewExpiringAdapter(inner, FixedTTL(day))
	ea.Now = clk.Now
	return ea, clk
}

func ids(t *testing.T, a Adapter) []string {
	t.Helper()
	metas, err := a.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range metas {
		out = append(out, m.ID)
	}
	sort.Strings(out)
	return out
}

func TestExpiringAdapterConformance(t *testing.T) {
	CheckAdapter(t, func() Adapter { ea, _ := newExpiring(NewMemoryAdapter()); return ea })
}

func TestExpirySaveRenewsLoadAndListDoNot(t *testing.T) {
	ctx := context.Background()
	ea, clk := newExpiring(NewMemoryAdapter())
	if err := ea.Save(ctx, "d", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	t0 := clk.Now()
	if at, ok, _ := ea.Expiry(ctx, "d"); !ok || !at.Equal(t0.Add(day)) {
		t.Fatalf("expiry = %v %v", at, ok)
	}
	clk.Add(20 * time.Hour)
	if _, err := ea.Load(ctx, "d"); err != nil {
		t.Fatal(err)
	}
	if got := ids(t, ea); len(got) != 1 {
		t.Fatalf("list = %v", got)
	}
	if at, _, _ := ea.Expiry(ctx, "d"); !at.Equal(t0.Add(day)) {
		t.Errorf("viewing renewed: %v", at)
	}
	if err := ea.Save(ctx, "d", []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if at, _, _ := ea.Expiry(ctx, "d"); !at.Equal(t0.Add(20*time.Hour + day)) {
		t.Errorf("save did not renew: %v", at)
	}
	clk.Add(10 * time.Hour)
	if _, err := ea.Load(ctx, "d"); err != nil {
		t.Errorf("renewed document gone: %v", err)
	}
}

func TestExpiryInvisibleBeforeSweep(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryAdapter()
	ea, clk := newExpiring(inner)
	_ = ea.Save(ctx, "d", []byte("x"))
	_ = ea.Save(ctx, "keep.open", []byte("y"))
	clk.Add(day)
	if _, err := ea.Load(ctx, "d"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Load expired: %v", err)
	}
	if got := ids(t, ea); len(got) != 0 {
		t.Errorf("List shows %v", got)
	}
	if got, _ := ea.ListPrefix(ctx, "d"); len(got) != 0 {
		t.Errorf("ListPrefix shows %v", got)
	}
	if err := ea.Delete(ctx, "d"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete expired: %v", err)
	}
	if _, err := inner.Load(ctx, "keep.open"); err != nil {
		t.Errorf("inner: %v", err)
	}
	if _, err := ea.Load(ctx, ExpiryPrefix+"keep.open"); !errors.Is(err, ErrNotFound) {
		t.Errorf("record visible: %v", err)
	}
	if err := ea.Save(ctx, ExpiryPrefix+"x", []byte("2030-01-01T00:00:00Z")); err == nil {
		t.Error("record forged through Save")
	}
}

func TestExpirySweepDeletesDocumentVersionsAndRecords(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryAdapter()
	ea, clk := newExpiring(inner)
	for _, id := range []string{"a.open", "b.open", "acl:a.open", "collab:a.open", "versions/a/1.open", "versions/a/2.open", "versions/b/1.open", "catalog:x"} {
		if err := ea.Save(ctx, id, []byte(id)); err != nil {
			t.Fatal(err)
		}
	}
	clk.Add(12 * time.Hour)
	_ = ea.Save(ctx, "b.open", []byte("edited")) // renewed
	clk.Add(13 * time.Hour)                      // a is 25 h old, b 13 h
	n, err := ea.Sweep(ctx)
	if err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v", n, err)
	}
	var left []string
	metas, _ := inner.List(ctx)
	for _, m := range metas {
		left = append(left, m.ID)
	}
	sort.Strings(left)
	want := []string{"b.open", "catalog:x", "expiry-created:b.open", "expiry:b.open", "versions/b/1.open"}
	if strings.Join(left, ",") != strings.Join(want, ",") {
		t.Errorf("left %v, want %v", left, want)
	}
	if n, err := ea.Sweep(ctx); err != nil || n != 0 {
		t.Errorf("second sweep = %d, %v", n, err)
	}
}

func TestExpiryCrashOrder(t *testing.T) {
	ctx := context.Background()
	t.Run("record written, document missing", func(t *testing.T) {
		inner := NewMemoryAdapter()
		ea, clk := newExpiring(inner)
		_ = inner.Save(ctx, "expiry:ghost", []byte(clk.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)))
		if _, err := ea.Load(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
			t.Errorf("load: %v", err)
		}
		if n, err := ea.Sweep(ctx); err != nil || n != 0 {
			t.Fatalf("sweep: %d %v", n, err)
		}
		if _, err := inner.Load(ctx, "expiry:ghost"); err != nil {
			t.Errorf("record dropped early: %v", err)
		}
		clk.Add(2 * time.Hour)
		if n, err := ea.Sweep(ctx); err != nil || n != 0 {
			t.Fatalf("sweep: %d %v", n, err)
		}
		if _, err := inner.Load(ctx, "expiry:ghost"); !errors.Is(err, ErrNotFound) {
			t.Errorf("orphan record kept: %v", err)
		}
	})
	t.Run("document present, record missing is adopted at the first sweep", func(t *testing.T) {
		inner := NewMemoryAdapter()
		_ = inner.Save(ctx, "old", []byte("x"))
		_ = inner.Save(ctx, "acl:old", []byte("{}"))
		_ = inner.Save(ctx, "passkey/u", []byte("{}"))
		ea := NewExpiringAdapter(inner, FixedTTL(day))
		ea.AdoptUpdatedAt = true
		clk := &fakeClock{t: time.Now()}
		ea.Now = clk.Now
		if _, ok, _ := ea.Expiry(ctx, "old"); ok {
			t.Fatal("expiry before adoption")
		}
		clk.Add(10 * day)
		if _, err := ea.Load(ctx, "old"); err != nil {
			t.Fatalf("unrecorded document hidden: %v", err)
		}
		up, _ := inner.Load(ctx, "old")
		n, err := ea.Sweep(ctx)
		if err != nil || n != 1 {
			t.Fatalf("sweep = %d %v (updated %v, now %v)", n, err, up.UpdatedAt, clk.Now())
		}
		if _, err := inner.Load(ctx, "old"); !errors.Is(err, ErrNotFound) {
			t.Error("adopted document not deleted")
		}
		if _, err := inner.Load(ctx, "passkey/u"); err != nil {
			t.Errorf("exempt keyspace touched: %v", err)
		}
	})
	t.Run("adopted document gets UpdatedAt plus TTL", func(t *testing.T) {
		inner := NewMemoryAdapter()
		_ = inner.Save(ctx, "doc", []byte("x"))
		up, _ := inner.Load(ctx, "doc")
		ea := NewExpiringAdapter(inner, FixedTTL(time.Hour))
		ea.AdoptUpdatedAt = true
		if _, err := ea.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		if at, ok, _ := ea.Expiry(ctx, "doc"); !ok || !at.Equal(up.UpdatedAt.Add(time.Hour).UTC().Truncate(0)) && at.Sub(up.UpdatedAt.Add(time.Hour)) != 0 {
			t.Errorf("deadline %v, updated %v", at, up.UpdatedAt)
		}
	})
}

type failingSave struct {
	Adapter
	fail bool
}

func (f *failingSave) Save(ctx context.Context, id string, data []byte) error {
	if f.fail && !isExpiryKey(id) {
		return errors.New("disk full")
	}
	return f.Adapter.Save(ctx, id, data)
}

func TestExpiryFailedSaveDoesNotExtendLife(t *testing.T) {
	ctx := context.Background()
	f := &failingSave{Adapter: NewMemoryAdapter()}
	ea, clk := newExpiring(f)
	_ = ea.Save(ctx, "d", []byte("v1"))
	t0 := clk.Now()
	clk.Add(10 * time.Hour)
	f.fail = true
	if err := ea.Save(ctx, "d", []byte("v2")); err == nil {
		t.Fatal("expected failure")
	}
	if at, _, _ := ea.Expiry(ctx, "d"); !at.Equal(t0.Add(day)) {
		t.Errorf("deadline moved to %v", at)
	}
	if err := ea.Save(ctx, "new", []byte("x")); err == nil {
		t.Fatal("expected failure")
	}
	if _, ok, _ := ea.Expiry(ctx, "new"); ok {
		t.Error("record left for a document never written")
	}
}

func TestExpiryPolicyCanExemptAndSaveClearsRecord(t *testing.T) {
	ctx := context.Background()
	exempt := false
	ea := NewExpiringAdapter(NewMemoryAdapter(), ExpiryPolicyFunc(func(id string, _ time.Time) (time.Duration, bool) {
		return time.Hour, !exempt
	}))
	clk := newClock()
	ea.Now = clk.Now
	_ = ea.Save(ctx, "d", []byte("1"))
	exempt = true
	_ = ea.Save(ctx, "d", []byte("2"))
	if _, ok, _ := ea.Expiry(ctx, "d"); ok {
		t.Error("record kept for an exempt document")
	}
	clk.Add(48 * time.Hour)
	if n, _ := ea.Sweep(ctx); n != 0 {
		t.Errorf("swept %d", n)
	}
	if _, err := ea.Load(ctx, "d"); err != nil {
		t.Error(err)
	}
}

func TestExpirySavingAnExpiredDocumentStartsFresh(t *testing.T) {
	ctx := context.Background()
	ea, clk := newExpiring(NewMemoryAdapter())
	_ = ea.Save(ctx, "a.open", []byte("old"))
	_ = ea.Save(ctx, "versions/a/1.open", []byte("snap"))
	clk.Add(2 * day)
	if err := ea.SaveIf(ctx, "a.open", []byte("new"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := ea.Load(ctx, "versions/a/1.open"); !errors.Is(err, ErrNotFound) {
		t.Errorf("old versions inherited: %v", err)
	}
	if d, err := ea.Load(ctx, "a.open"); err != nil || string(d.Data) != "new" {
		t.Errorf("%v %q", err, d.Data)
	}
}

func TestExpiryPassthroughOfOptionalCapabilities(t *testing.T) {
	ctx := context.Background()
	t.Run("SaveIf", func(t *testing.T) {
		ea, clk := newExpiring(NewMemoryAdapter())
		if err := ea.SaveIf(ctx, "x", []byte("one"), ""); err != nil {
			t.Fatal(err)
		}
		t0 := clk.Now()
		clk.Add(time.Hour)
		if err := ea.SaveIf(ctx, "x", []byte("two"), Digest([]byte("stale"))); !errors.Is(err, ErrConflict) {
			t.Errorf("stale: %v", err)
		}
		if at, _, _ := ea.Expiry(ctx, "x"); !at.Equal(t0.Add(day)) {
			t.Errorf("a conflict renewed: %v", at)
		}
		if err := ea.SaveIf(ctx, "x", []byte("two"), Digest([]byte("one"))); err != nil {
			t.Fatal(err)
		}
		if at, _, _ := ea.Expiry(ctx, "x"); !at.Equal(t0.Add(time.Hour + day)) {
			t.Errorf("a success did not renew: %v", at)
		}
		if err := ea.SaveIf(ctx, "x", []byte("three"), Digest([]byte("one"))); !errors.Is(err, ErrConflict) {
			t.Errorf("%v", err)
		}
		plainEA, _ := newExpiring(plainAdapter{NewMemoryAdapter()})
		if err := plainEA.SaveIf(ctx, "x", nil, ""); !errors.Is(err, ErrConditionalUnsupported) {
			t.Errorf("plain inner: %v", err)
		}
	})
	t.Run("Locker", func(t *testing.T) {
		ea, _ := newExpiring(&lockingAdapter{Adapter: NewMemoryAdapter()})
		unlock, err := ea.Lock(ctx)
		if err != nil {
			t.Fatal(err)
		}
		second := make(chan struct{})
		go func() { u, _ := ea.Lock(ctx); u(); close(second) }()
		select {
		case <-second:
			t.Error("the lock was not exclusive: it is not the inner store's")
		case <-time.After(50 * time.Millisecond):
		}
		unlock()
		<-second
		none, _ := newExpiring(plainAdapter{NewMemoryAdapter()})
		u, err := none.Lock(ctx)
		if err != nil {
			t.Fatal(err)
		}
		u()
	})
	t.Run("PrefixLister", func(t *testing.T) {
		ea, clk := newExpiring(NewMemoryAdapter())
		for _, id := range []string{"docs/a/1", "docs/a/2", "other"} {
			_ = ea.Save(ctx, id, []byte(id))
		}
		got, err := ListPrefix(ctx, ea, "docs/a/")
		if err != nil || len(got) != 2 {
			t.Fatalf("%v %v", got, err)
		}
		clk.Add(day)
		if got, _ := ListPrefix(ctx, ea, "docs/a/"); len(got) != 0 {
			t.Errorf("expired listed by prefix: %v", got)
		}
	})
}

type lockingAdapter struct {
	Adapter
	mu sync.Mutex
}

func (l *lockingAdapter) Lock(context.Context) (func(), error) {
	l.mu.Lock()
	return l.mu.Unlock, nil
}

func TestExpiryOverLocalFSAndSQLite(t *testing.T) {
	ctx := context.Background()
	makers := map[string]func() Adapter{"localfs": func() Adapter {
		lf, err := NewLocalFSAdapter(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return lf
	}}
	if sqliteForTest(t) != nil {
		makers["sqlite"] = func() Adapter { return sqliteForTest(t) }
	}
	for name, mk := range makers {
		CheckAdapter(t, func() Adapter { ea, _ := newExpiring(mk()); return ea })
		inner := mk()
		ea, clk := newExpiring(inner)
		_ = ea.Save(ctx, "e.open", []byte("x"))
		_ = ea.Save(ctx, "versions/e/1.open", []byte("v"))
		clk.Add(day)
		if n, err := ea.Sweep(ctx); err != nil || n < 1 {
			t.Errorf("%s: sweep %d %v", name, n, err)
		}
		if got, _ := inner.List(ctx); len(got) != 0 {
			t.Errorf("%s: left %v", name, got)
		}
	}
}

func TestExpiryRunSweepsInTheBackground(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ea, clk := newExpiring(NewMemoryAdapter())
	_ = ea.Save(ctx, "d", []byte("x"))
	clk.Add(day)
	done := make(chan struct{})
	go func() { ea.Run(ctx, 10*time.Millisecond); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := ea.Inner.Load(ctx, "d"); errors.Is(err, ErrNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not swept")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestExpiryConcurrentSaveAndSweep(t *testing.T) {
	ctx := context.Background()
	ea, clk := newExpiring(NewMemoryAdapter())
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := string(rune('a' + w))
			for i := 0; i < 200; i++ {
				if err := ea.Save(ctx, id, []byte("x")); err != nil {
					t.Error(err)
					return
				}
				if _, err := ea.Load(ctx, id); err != nil {
					t.Errorf("renewed document gone: %v", err)
					return
				}
				_, _ = ea.List(ctx)
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			clk.Add(time.Minute)
			if _, err := ea.Sweep(ctx); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wg.Wait()
}

func TestServerExpiryHeaderAndListingField(t *testing.T) {
	ea, clk := newExpiring(NewMemoryAdapter())
	srv := NewServer(NewMemoryAdapter(), "memory").WithExpiry(ea)
	h := srv.Handler()
	put := doRequest(h, "PUT", "/docs/d1", "hello")
	if put.Code != 204 {
		t.Fatal(put.Code)
	}
	want := clk.Now().Add(day).UTC().Format(time.RFC3339)
	if got := put.Header().Get("X-Gojiberry-Expires"); got != want {
		t.Errorf("PUT header %q, want %q", got, want)
	}
	clk.Add(5 * time.Hour)
	get := doRequest(h, "GET", "/docs/d1", "")
	if get.Code != 200 || get.Header().Get("X-Gojiberry-Expires") != want {
		t.Errorf("GET %d, header %q (viewing must not renew)", get.Code, get.Header().Get("X-Gojiberry-Expires"))
	}
	list := doRequest(h, "GET", "/docs", "")
	var items []map[string]any
	if err := json.Unmarshal(list.Body.Bytes(), &items); err != nil || len(items) != 1 {
		t.Fatalf("%v %s", err, list.Body)
	}
	if items[0]["expires"] != want || items[0]["ID"] != "d1" {
		t.Errorf("listing item %v", items[0])
	}
	if rec := doRequest(h, "PUT", "/docs/d1", "again"); rec.Header().Get("X-Gojiberry-Expires") != clk.Now().Add(day).UTC().Format(time.RFC3339) {
		t.Errorf("a write did not renew: %q", rec.Header().Get("X-Gojiberry-Expires"))
	}
	clk.Add(day)
	if rec := doRequest(h, "GET", "/docs/d1", ""); rec.Code != http.StatusNotFound {
		t.Errorf("expired GET = %d", rec.Code)
	}
	if rec := doRequest(h, "DELETE", "/docs/d1", ""); rec.Code != http.StatusNotFound {
		t.Errorf("expired DELETE = %d", rec.Code)
	}
	if rec := doRequest(h, "GET", "/docs", ""); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("expired listed: %s", rec.Body)
	}
	for _, m := range []string{"GET", "PUT", "DELETE"} {
		if rec := doRequest(h, m, "/docs/expiry:d1", "x"); rec.Code != 400 {
			t.Errorf("%s expiry: key = %d", m, rec.Code)
		}
	}
	_ = doRequest(h, "PUT", "/docs/d2", "x")
	if rec := doRequest(h, "DELETE", "/docs/d2", ""); rec.Code != 204 {
		t.Errorf("DELETE = %d", rec.Code)
	}
	plainSrv := doRequest(newTestServer().Handler(), "PUT", "/docs/p", "x")
	if plainSrv.Header().Get("X-Gojiberry-Expires") != "" {
		t.Error("header without expiry")
	}
}

func TestServerConditionalPutOverNonConditionalStoreIs501(t *testing.T) {
	ea, _ := newExpiring(plainAdapter{NewMemoryAdapter()})
	rec := httpPutIfNoneMatch(ea)
	if rec != http.StatusNotImplemented {
		t.Errorf("= %d", rec)
	}
}

func httpPutIfNoneMatch(a Adapter) int {
	req, _ := http.NewRequest("PUT", "/docs/x", strings.NewReader("v"))
	req.Header.Set("If-None-Match", "*")
	rec := httptest.NewRecorder()
	NewServer(a, "x").Handler().ServeHTTP(rec, req)
	return rec.Code
}

func TestParseExpiryEnv(t *testing.T) {
	for _, c := range []struct {
		ttl, sweep string
		wantTTL    time.Duration
		wantSweep  time.Duration
		bad        bool
	}{
		{"", "", 0, 0, false},
		{"24h", "", day, DefaultExpirySweep, false},
		{"24h", "1m", day, time.Minute, false},
		{"2m", "", 2 * time.Minute, 2 * time.Minute, false},
		{" 1h ", "30s", time.Hour, 30 * time.Second, false},
		{"", "1m", 0, 0, true},
		{"soon", "", 0, 0, true},
		{"-1h", "", 0, 0, true},
		{"0", "", 0, 0, true},
		{"24h", "x", 0, 0, true},
		{"24h", "0s", 0, 0, true},
	} {
		ttl, sweep, err := ParseExpiryEnv(c.ttl, c.sweep)
		if (err != nil) != c.bad || ttl != c.wantTTL || sweep != c.wantSweep {
			t.Errorf("%q %q = %v %v %v", c.ttl, c.sweep, ttl, sweep, err)
		}
	}
	t.Setenv("GOJIBERRY_DOCSTORE_EXPIRY_TTL", "36h")
	t.Setenv("GOJIBERRY_DOCSTORE_EXPIRY_SWEEP", "10m")
	if cfg := ConfigFromEnv(); cfg.ExpiryTTL != 36*time.Hour || cfg.ExpirySweep != 10*time.Minute {
		t.Errorf("config = %v %v", cfg.ExpiryTTL, cfg.ExpirySweep)
	}
}

func TestPublicKeyspacesReservedFromDocs(t *testing.T) {
	ea, _ := newExpiring(NewMemoryAdapter())
	for _, k := range []string{"public/doc/x", "public/tomb/x", "report/x", "Public/doc/x", "REPORT/x"} {
		if err := ea.Save(context.Background(), k, []byte("rec")); err != nil {
			t.Fatal(err)
		}
	}
	h := NewServer(ea, "memory").Handler()
	for _, k := range []string{"public/doc/x", "public/tomb/x", "report/x", "Public/doc/x", "REPORT/x"} {
		for _, m := range []string{"GET", "PUT", "DELETE"} {
			if rec := doRequest(h, m, "/docs/"+k, "x"); rec.Code != 400 {
				t.Errorf("%s %s = %d, want 400", m, k, rec.Code)
			}
		}
	}
	if rec := doRequest(h, "GET", "/docs", ""); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("listed: %s", rec.Body)
	}
	if _, err := ea.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if exp, _ := ea.Expiries(context.Background()); len(exp) != 0 {
		t.Errorf("the sweeper gave a deadline to %v", exp)
	}
}
