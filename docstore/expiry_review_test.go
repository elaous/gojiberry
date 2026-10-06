package docstore

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// stampAdapter gives every key the fake clock's time as its UpdatedAt (the memory adapter uses
// the real one), so UpdatedAt-based policies can be tested with an injected clock.
type stampAdapter struct {
	Adapter
	clk *fakeClock
	mu  sync.Mutex
	at  map[string]time.Time
}

func newStamped(clk *fakeClock) *stampAdapter {
	return &stampAdapter{Adapter: NewMemoryAdapter(), clk: clk, at: map[string]time.Time{}}
}

func (s *stampAdapter) Save(ctx context.Context, id string, data []byte) error {
	if err := s.Adapter.Save(ctx, id, data); err != nil {
		return err
	}
	s.mu.Lock()
	s.at[id] = s.clk.Now()
	s.mu.Unlock()
	return nil
}

func (s *stampAdapter) stamp(id string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.at[id]
}

func (s *stampAdapter) Load(ctx context.Context, id string) (Document, error) {
	d, err := s.Adapter.Load(ctx, id)
	d.UpdatedAt = s.stamp(id)
	return d, err
}

func (s *stampAdapter) List(ctx context.Context) ([]Meta, error) {
	metas, err := s.Adapter.List(ctx)
	for i := range metas {
		metas[i].UpdatedAt = s.stamp(metas[i].ID)
	}
	return metas, err
}

func newExpiringStamped(t *testing.T) (*ExpiringAdapter, *stampAdapter, *fakeClock) {
	t.Helper()
	clk := newClock()
	inner := newStamped(clk)
	ea := NewExpiringAdapter(inner, FixedTTL(day))
	ea.Now = clk.Now
	return ea, inner, clk
}

func exists(t *testing.T, a Adapter, id string) bool {
	t.Helper()
	_, err := a.Load(context.Background(), id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

// --- M2: orphaned hub state

func TestOrphanSweepOffByDefault(t *testing.T) {
	ctx := context.Background()
	ea, inner, clk := newExpiringStamped(t)
	_ = ea.Save(ctx, "collab:hubonly", []byte("[]"))
	clk.Add(30 * day)
	if n, err := ea.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("Sweep = %d, %v", n, err)
	}
	if !exists(t, inner, "collab:hubonly") {
		t.Fatal("orphan removed without the policy")
	}
}

func TestOrphanSweepGracePeriod(t *testing.T) {
	ctx := context.Background()
	ea, inner, clk := newExpiringStamped(t)
	ea.Orphans = CollabOrphans(2 * time.Hour)
	_ = ea.Save(ctx, "collab:hubonly", []byte("[]"))

	clk.Add(day + time.Hour) // past the TTL, inside the grace period
	if n, _ := ea.Sweep(ctx); n != 0 || !exists(t, inner, "collab:hubonly") {
		t.Fatalf("removed inside the grace period (n=%d)", n)
	}
	clk.Add(time.Hour + time.Minute) // TTL + 2h + 1m
	if n, err := ea.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v; want the orphan removed", n, err)
	}
	if exists(t, inner, "collab:hubonly") {
		t.Fatal("orphan survived past TTL + grace")
	}
}

func TestOrphanDefaultGraceIsOneHour(t *testing.T) {
	ctx := context.Background()
	ea, inner, clk := newExpiringStamped(t)
	ea.Orphans = CollabOrphans(0)
	_ = ea.Save(ctx, "collab:h", []byte("[]"))
	clk.Add(day + 59*time.Minute)
	_, _ = ea.Sweep(ctx)
	if !exists(t, inner, "collab:h") {
		t.Fatal("removed before TTL + 1h")
	}
	clk.Add(2 * time.Minute)
	_, _ = ea.Sweep(ctx)
	if exists(t, inner, "collab:h") {
		t.Fatal("kept after TTL + 1h")
	}
}

// Every hub save bumps UpdatedAt: a hub-only document that is being edited is never swept.
func TestOrphanSweepKeepsALiveEditedHubOnlyDocument(t *testing.T) {
	ctx := context.Background()
	ea, inner, clk := newExpiringStamped(t)
	ea.Orphans = CollabOrphans(time.Hour)
	for i := 0; i < 6; i++ { // 6 x 12h = 72h of editing
		if err := ea.Save(ctx, "collab:live", []byte("[]")); err != nil { // the hub's save
			t.Fatal(err)
		}
		clk.Add(12 * time.Hour)
		if n, err := ea.Sweep(ctx); err != nil || n != 0 {
			t.Fatalf("round %d: Sweep = %d, %v", i, n, err)
		}
		if !exists(t, inner, "collab:live") {
			t.Fatalf("round %d: an edited hub-only document was swept", i)
		}
	}
	clk.Add(day + 2*time.Hour) // editing stops
	if n, _ := ea.Sweep(ctx); n != 1 || exists(t, inner, "collab:live") {
		t.Fatalf("an abandoned one survived (n=%d)", n)
	}
}

func TestOrphanSweepNeverTouchesStateOfALiveDocument(t *testing.T) {
	ctx := context.Background()
	ea, inner, clk := newExpiringStamped(t)
	ea.Orphans = CollabOrphans(time.Hour)
	_ = ea.Save(ctx, "collab:exact", []byte("[]"))
	_ = ea.Save(ctx, "collab:gen", []byte("[]"))
	_ = ea.Save(ctx, "collab:ghost", []byte("[]"))
	_ = ea.Save(ctx, "exact", []byte("x"))
	_ = ea.Save(ctx, "gen~1f2e3d4c5b6a.docx", []byte("x"))
	clk.Add(day - time.Hour)
	// Both documents are renewed by the hub (collab: state is old, the documents are live).
	_ = ea.Touch(ctx, "exact")
	_ = ea.Touch(ctx, "gen")
	clk.Add(3 * time.Hour) // collab: entries are older than TTL + grace; the documents are not expired
	n, err := ea.Sweep(ctx)
	if err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v; want only the ghost", n, err)
	}
	for id, want := range map[string]bool{"collab:exact": true, "collab:gen": true, "collab:ghost": false, "exact": true, "gen~1f2e3d4c5b6a.docx": true} {
		if got := exists(t, inner, id); got != want {
			t.Errorf("%s exists = %v, want %v", id, got, want)
		}
	}
}

// A document the policy gives no lifetime (a key with a colon) is not swept as an orphan either.
func TestOrphanSweepOnlyForIdsThePolicyCovers(t *testing.T) {
	ctx := context.Background()
	ea, inner, clk := newExpiringStamped(t)
	ea.Orphans = CollabOrphans(time.Hour)
	_ = ea.Save(ctx, "collab:odd:id", []byte("[]"))
	clk.Add(30 * day)
	_, _ = ea.Sweep(ctx)
	if !exists(t, inner, "collab:odd:id") {
		t.Fatal("state of an id with no lifetime was swept")
	}
}

func TestParseExpiryOrphans(t *testing.T) {
	for _, tc := range []struct {
		raw, grace string
		on         bool
		g          time.Duration
		bad        bool
	}{
		{"", "", false, 0, false},
		{"collab", "", true, time.Hour, false},
		{"Collab", "30m", true, 30 * time.Minute, false},
		{"all", "", false, 0, true},
		{"", "1h", false, 0, true},
		{"collab", "-1h", false, 0, true},
		{"collab", "soon", false, 0, true},
	} {
		on, g, err := ParseExpiryOrphans(tc.raw, tc.grace)
		if (err != nil) != tc.bad || on != tc.on || g != tc.g {
			t.Errorf("ParseExpiryOrphans(%q,%q) = %v %v %v", tc.raw, tc.grace, on, g, err)
		}
	}
}

func TestWithConfiguredExpiryOrphansAndMaxAge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, ea := WithConfiguredExpiry(ctx, NewMemoryAdapter(), Config{ExpiryTTL: time.Hour, ExpirySweep: time.Hour, ExpiryOrphans: true, ExpiryOrphanGrace: time.Minute, ExpiryMaxAge: 48 * time.Hour})
	if ea.Orphans == nil || ea.MaxAge != 48*time.Hour {
		t.Fatalf("orphans=%v maxage=%v", ea.Orphans, ea.MaxAge)
	}
	_, ea = WithConfiguredExpiry(ctx, NewMemoryAdapter(), Config{ExpiryTTL: time.Hour, ExpirySweep: time.Hour})
	if ea.Orphans != nil || ea.MaxAge != 0 {
		t.Fatal("orphans / max age on by default")
	}
}

// --- MaxAge

func TestMaxAgeBoundsAnEndlesslyEditedDocument(t *testing.T) {
	ctx := context.Background()
	ea, clk := newExpiring(NewMemoryAdapter())
	ea.MaxAge = 3 * day
	if err := ea.Save(ctx, "doc", []byte("v0")); err != nil {
		t.Fatal(err)
	}
	created := clk.Now()
	for i := 0; i < 5; i++ { // an edit every 20 hours keeps a plain document alive for ever
		clk.Add(20 * time.Hour)
		if clk.Now().Before(created.Add(3 * day)) {
			if err := ea.Save(ctx, "doc", []byte("edit")); err != nil {
				t.Fatal(err)
			}
			if i%2 == 1 {
				if err := ea.Touch(ctx, "doc"); err != nil {
					t.Fatal(err)
				}
			}
			if got := mustExpiry(t, ea, "doc"); got.After(created.Add(3 * day)) {
				t.Fatalf("deadline %v is past creation + MaxAge", got)
			}
		}
	}
	// t = 100h > 72h: the cap has passed although the last edit was 20h ago.
	if _, err := ea.Load(ctx, "doc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load after MaxAge = %v, want ErrNotFound", err)
	}
	if err := ea.Touch(ctx, "doc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Touch after MaxAge = %v, want ErrNotFound", err)
	}
	if n, err := ea.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v", n, err)
	}
}

func TestMaxAgeDefaultOffAndRenewalKeepsCreation(t *testing.T) {
	ctx := context.Background()
	ea, clk := newExpiring(NewMemoryAdapter())
	_ = ea.Save(ctx, "doc", []byte("v0"))
	for i := 0; i < 10; i++ {
		clk.Add(20 * time.Hour)
		_ = ea.Save(ctx, "doc", []byte("v"))
	}
	if _, err := ea.Load(ctx, "doc"); err != nil {
		t.Fatalf("without MaxAge a regularly edited document lives on: %v", err)
	}
}

func TestMaxAgeSaveAtTheCapStartsANewDocument(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryAdapter()
	ea, clk := newExpiring(inner)
	ea.MaxAge = 2 * day
	_ = ea.Save(ctx, "doc", []byte("old"))
	_ = ea.Save(ctx, "acl:doc", []byte("{}"))
	clk.Add(20 * time.Hour)
	_ = ea.Save(ctx, "doc", []byte("old2"))
	clk.Add(20 * time.Hour)
	_ = ea.Save(ctx, "doc", []byte("old3"))
	clk.Add(9 * time.Hour) // 49h: past the cap, the 24h life of the last save is not over
	if err := ea.Save(ctx, "doc", []byte("new")); err != nil {
		t.Fatal(err)
	}
	d, err := ea.Load(ctx, "doc")
	if err != nil || string(d.Data) != "new" {
		t.Fatalf("after the cap: %q %v", d.Data, err)
	}
	if exists(t, inner, "acl:doc") {
		t.Fatal("the old document's access list was inherited by the new one")
	}
	if got := mustExpiry(t, ea, "doc"); !got.Equal(clk.Now().Add(day)) {
		t.Fatalf("the new document's deadline = %v", got)
	}
}

func TestMaxAgeSweepsALiveRecordPastTheCap(t *testing.T) {
	// MaxAge switched on over documents whose recorded deadline is later than the cap.
	ctx := context.Background()
	ea, clk := newExpiring(NewMemoryAdapter())
	_ = ea.Save(ctx, "doc", []byte("x"))
	clk.Add(20 * time.Hour)
	_ = ea.Save(ctx, "doc", []byte("y")) // deadline t=44h, created t=0
	ea.MaxAge = 30 * time.Hour
	clk.Add(11 * time.Hour) // t=31h
	if _, err := ea.Load(ctx, "doc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load = %v", err)
	}
	if n, _ := ea.Sweep(ctx); n != 1 {
		t.Fatalf("Sweep = %d", n)
	}
}

func TestRecordFormatBothWays(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryAdapter()
	ea, clk := newExpiring(inner)
	// An old-format record (just the deadline) still reads.
	_ = inner.Save(ctx, "doc", []byte("x"))
	_ = inner.Save(ctx, "expiry:doc", []byte(clk.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)))
	if got := mustExpiry(t, ea, "doc"); !got.Equal(clk.Now().Add(time.Hour)) {
		t.Fatalf("old record = %v", got)
	}
	// A new one keeps the creation time in a sibling record (the deadline record stays one line,
	// see TestExpiryRecordsReadableByOldBuild), and a bad one reads as absent (never an error).
	_ = ea.Save(ctx, "doc", []byte("y"))
	if _, err := inner.Load(ctx, "expiry-created:doc"); err != nil {
		t.Fatalf("no creation time record: %v", err)
	}
	_ = inner.Save(ctx, "expiry:doc", []byte("tomorrow\nnever"))
	if _, ok, err := ea.Expiry(ctx, "doc"); err != nil || ok {
		t.Fatalf("a corrupt record: ok=%v err=%v", ok, err)
	}
}

// --- m1: Touch is exact-first and renews only the newest generation

func TestTouchExactKeyFirst(t *testing.T) {
	ctx := context.Background()
	ea, clk := newExpiring(NewMemoryAdapter())
	_ = ea.Save(ctx, "d", []byte("exact"))
	_ = ea.Save(ctx, "d~1f2e3d4c5b6a.docx", []byte("gen"))
	clk.Add(10 * time.Hour)
	if err := ea.Touch(ctx, "d"); err != nil {
		t.Fatal(err)
	}
	if got := mustExpiry(t, ea, "d"); !got.Equal(clk.Now().Add(day)) {
		t.Fatalf("exact deadline = %v", got)
	}
	if got := mustExpiry(t, ea, "d~1f2e3d4c5b6a.docx"); !got.Equal(clk.Now().Add(-10 * time.Hour).Add(day)) {
		t.Fatalf("a generation was renewed although the exact key exists: %v", got)
	}
}

func TestTouchRenewsOnlyTheNewestGeneration(t *testing.T) {
	ctx := context.Background()
	ea, inner, clk := newExpiringStamped(t)
	old, cur, curPDF := "d~aaaaaaaaaaaa.docx", "d~bbbbbbbbbbbb.docx", "d~bbbbbbbbbbbb.pdf"
	_ = ea.Save(ctx, old, []byte("stale: the saver's cleanup failed"))
	clk.Add(time.Hour)
	_ = ea.Save(ctx, cur, []byte("current"))
	_ = ea.Save(ctx, curPDF, []byte("current, other codec"))
	_ = inner
	oldDeadline := mustExpiry(t, ea, old)

	for i := 0; i < 3; i++ { // edits keep renewing the logical id
		clk.Add(10 * time.Hour)
		if err := ea.Touch(ctx, "d"); err != nil {
			t.Fatal(err)
		}
	}
	if got := mustExpiry(t, ea, old); !got.Equal(oldDeadline) {
		t.Fatalf("the stale generation was renewed: %v -> %v", oldDeadline, got)
	}
	for _, k := range []string{cur, curPDF} {
		if got := mustExpiry(t, ea, k); !got.Equal(clk.Now().Add(day)) {
			t.Fatalf("%s deadline = %v, want renewed", k, got)
		}
	}
	// The stale one is gone at its own deadline; the current one is not.
	if n, err := ea.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v; want the stale generation", n, err)
	}
	if _, err := ea.Load(ctx, old); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale generation = %v", err)
	}
	if _, err := ea.Load(ctx, cur); err != nil {
		t.Fatalf("current generation: %v", err)
	}
}

// --- m2, case-insensitive reserved ids

func routeReq(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader("x"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestIDsEndingInARouteAreRefused(t *testing.T) {
	ea, _ := newExpiring(NewMemoryAdapter())
	_ = ea.Save(context.Background(), "a", []byte("doc a"))
	h := NewServer(ea, "memory").WithExpiry(ea).Handler()
	for _, tc := range []struct{ method, target string }{
		{http.MethodPut, "/docs/a%2Frenew"},
		{http.MethodPost, "/docs/a%2Frenew"},
		{http.MethodPut, "/docs/a%2Facl"},
		{http.MethodPost, "/docs/a%2Facl"},
		{http.MethodPut, "/docs/a%2FRenew"},
		{http.MethodGet, "/docs/a%2Facl"},
		{http.MethodPut, "/docs/x%2Fy%2Frenew"},
	} {
		rec := routeReq(t, h, tc.method, tc.target)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "/renew or /acl") {
			t.Errorf("%s %s = %d %q, want 400 with the reason", tc.method, tc.target, rec.Code, rec.Body.String())
		}
	}
	if d, err := ea.Load(context.Background(), "a"); err != nil || string(d.Data) != "doc a" {
		t.Fatalf("document a was touched: %q %v", d.Data, err)
	}
	// Nested ids that do not end in a route, and the real routes, still work.
	if rec := routeReq(t, h, http.MethodPut, "/docs/a%2Frenewed"); rec.Code != http.StatusNoContent {
		t.Errorf("nested id a/renewed = %d", rec.Code)
	}
	if rec := routeReq(t, h, http.MethodPost, "/docs/a/renew"); rec.Code != http.StatusNoContent {
		t.Errorf("real renew route = %d", rec.Code)
	}
}

func TestRenewRouteReservedIDsAreCaseInsensitive(t *testing.T) {
	ea, _ := newExpiring(NewMemoryAdapter())
	h := NewServer(ea, "memory").WithExpiry(ea).Handler()
	for _, id := range []string{"collab:x", "Collab:x", "COLLAB:x", "expiry:x", "EXPIRY:x", "Expiry:x", "ACL:x", "Acl:x"} {
		if rec := routeReq(t, h, http.MethodPost, "/docs/"+id+"/renew"); rec.Code != http.StatusBadRequest {
			t.Errorf("POST /docs/%s/renew = %d, want 400", id, rec.Code)
		}
		if rec := routeReq(t, h, http.MethodPut, "/docs/"+id); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT /docs/%s = %d, want 400", id, rec.Code)
		}
	}
}

// --- n1: nothing secret in logs and errors

func TestRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://svc:hunter2@docs.example/api":    "https://docs.example/api",
		"https://tok@docs.example":                "https://docs.example",
		"https://docs.example/api?token=secret#f": "https://docs.example/api",
		"http://127.0.0.1:8080":                   "http://127.0.0.1:8080",
		"not a url":                               "<invalid URL>",
		"https://user:p%zzw@host/":                "<invalid URL>",
	} {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHTTPAdapterRenewErrorsCarryNoCredentials(t *testing.T) {
	a := NewHTTPAdapter("http://svcuser:hunter2@127.0.0.1:1", nil) // nothing listens there
	a.Header.Set("X-Gojiberry-Token", "TOPSECRETTOKEN")
	err := a.Renew(WithAuthorization(context.Background(), "Bearer EDITORSECRET"), "d")
	if err == nil {
		t.Fatal("expected a connection error")
	}
	for _, s := range []string{"hunter2", "svcuser", "TOPSECRETTOKEN", "EDITORSECRET"} {
		if strings.Contains(err.Error(), s) {
			t.Errorf("error %q leaks %q", err, s)
		}
	}
}

func TestHTTPAdapterRenewStatusError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "write access needed", http.StatusForbidden)
	}))
	defer ts.Close()
	err := NewHTTPAdapter(ts.URL, nil).Renew(context.Background(), "d")
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusForbidden {
		t.Fatalf("err = %v, want a StatusError 403", err)
	}
	if !strings.Contains(err.Error(), "403 Forbidden") || !strings.Contains(err.Error(), "write access needed") {
		t.Fatalf("message changed: %v", err)
	}
}

func TestTouchNeverShortensALife(t *testing.T) {
	ctx := context.Background()
	ea, clk := newExpiring(NewMemoryAdapter())
	_ = ea.Save(ctx, "doc", []byte("x")) // deadline t+24h
	first := mustExpiry(t, ea, "doc")
	ea.Policy = FixedTTL(time.Hour) // a shorter lifetime from now on
	clk.Add(time.Minute)
	if err := ea.Touch(ctx, "doc"); err != nil {
		t.Fatal(err)
	}
	if got := mustExpiry(t, ea, "doc"); !got.Equal(first) {
		t.Fatalf("Touch moved the deadline %v -> %v", first, got)
	}
}
