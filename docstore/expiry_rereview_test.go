package docstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func renewAnswering(t *testing.T, h http.HandlerFunc) error {
	t.Helper()
	ts := httptest.NewServer(h)
	defer ts.Close()
	return NewHTTPAdapter(ts.URL, nil).Renew(context.Background(), "d")
}

func TestHTTPAdapterRenewOnlyTheMarked404IsGone(t *testing.T) {
	marked := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(GoneHeader, "1")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"gone"}`))
	}
	if err := renewAnswering(t, marked); !errors.Is(err, ErrNotFound) || errors.Is(err, ErrRenewUnsupported) {
		t.Fatalf("marked 404 = %v, want ErrNotFound", err)
	}
	for name, h := range map[string]http.HandlerFunc{
		"plain 404":      func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		"proxy 404 page": func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no route", 404) },
		"404 with json only": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"gone"}`))
		},
		"404 with header only": func(w http.ResponseWriter, _ *http.Request) { w.Header().Set(GoneHeader, "1"); w.WriteHeader(404) },
		"404 with wrong body": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(GoneHeader, "1")
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"other"}`))
		},
		"405": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(405) },
		"501": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "this store does not expire documents", http.StatusNotImplemented)
		},
		"marker on a non-404": func(w http.ResponseWriter, _ *http.Request) { w.Header().Set(GoneHeader, "1"); w.WriteHeader(500) },
	} {
		err := renewAnswering(t, h)
		if err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("%s = %v, must not be ErrNotFound", name, err)
		}
		if name != "marker on a non-404" && !errors.Is(err, ErrRenewUnsupported) {
			t.Errorf("%s = %v, want ErrRenewUnsupported", name, err)
		}
		var se *StatusError
		if !errors.As(err, &se) {
			t.Errorf("%s: no StatusError in %v", name, err)
		}
	}
}

func TestHTTPAdapterRenewUnsupportedIsLoggedOnceWithAdvice(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(io.Discard)
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()
	a := NewHTTPAdapter(ts.URL, nil)
	for i := 0; i < 5; i++ {
		_ = a.Renew(context.Background(), "d")
	}
	if n := strings.Count(buf.String(), "renew route answered 404"); n != 1 {
		t.Fatalf("%d log lines, want 1:\n%s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "POST /docs/{id}/renew") {
		t.Fatalf("no advice:\n%s", buf.String())
	}
}

func TestRenewRouteMarksARealGone(t *testing.T) {
	ea, _ := newExpiring(NewMemoryAdapter())
	srv := NewServer(ea, "memory").WithExpiry(ea)
	rec := routeReq(t, srv.Handler(), http.MethodPost, "/docs/missing/renew")
	if rec.Code != 404 || rec.Header().Get(GoneHeader) != "1" || strings.TrimSpace(rec.Body.String()) != `{"error":"gone"}` {
		t.Fatalf("renew of a missing document = %d %v %q", rec.Code, rec.Header(), rec.Body.String())
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	if err := NewHTTPAdapter(ts.URL, nil).Renew(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("real gone through HTTPAdapter = %v", err)
	}
	closed := NewServer(ea, "memory").WithExpiry(ea).CloseDocs()
	ts2 := httptest.NewServer(closed.Handler())
	defer ts2.Close()
	if err := NewHTTPAdapter(ts2.URL, nil).Renew(context.Background(), "missing"); !errors.Is(err, ErrRenewUnsupported) || errors.Is(err, ErrNotFound) {
		t.Fatalf("renew against a closed /docs = %v, want ErrRenewUnsupported", err)
	}
}

func oldParseRecord(b []byte) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
}

func TestExpiryRecordsReadableByOldBuild(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryAdapter()
	ea, clk := newExpiring(inner)
	ea.MaxAge = 3 * day
	_ = ea.Save(ctx, "a", []byte("1"))
	clk.Add(time.Hour)
	_ = ea.Save(ctx, "a", []byte("2"))
	_ = ea.Touch(ctx, "a")
	_ = ea.Save(ctx, "b.docx", []byte("1"))
	metas, err := ListPrefix(ctx, inner, ExpiryPrefix)
	if err != nil || len(metas) != 2 {
		t.Fatalf("records = %v %v", metas, err)
	}
	for _, m := range metas {
		d, _ := inner.Load(ctx, m.ID)
		if strings.Contains(string(d.Data), "\n") {
			t.Errorf("%s is not a single line: %q", m.ID, d.Data)
		}
		if _, err := oldParseRecord(d.Data); err != nil {
			t.Errorf("an older build cannot parse %s = %q: %v", m.ID, d.Data, err)
		}
	}
	if _, err := inner.Load(ctx, ExpiryCreatedPrefix+"a"); err != nil {
		t.Fatalf("no creation record: %v", err)
	}
	if strings.HasPrefix(ExpiryCreatedPrefix+"a", ExpiryPrefix) {
		t.Fatal("the creation keyspace is inside the deadline keyspace an older build scans")
	}
	clk.Add(3 * day)
	if _, err := ea.Load(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MaxAge not enforced: %v", err)
	}
}

func TestExpiryTwoLineRecordStillReadsAndIsRewritten(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryAdapter()
	ea, clk := newExpiring(inner)
	ea.MaxAge = 2 * day
	_ = inner.Save(ctx, "d", []byte("x"))
	created := clk.Now().Add(-day)
	_ = inner.Save(ctx, "expiry:d", []byte(clk.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)+"\n"+created.UTC().Format(time.RFC3339Nano)))
	if got := mustExpiry(t, ea, "d"); !got.Equal(clk.Now().Add(time.Hour)) {
		t.Fatalf("deadline = %v", got)
	}
	if err := ea.Touch(ctx, "d"); err != nil {
		t.Fatal(err)
	}
	rec, _ := inner.Load(ctx, "expiry:d")
	if strings.Contains(string(rec.Data), "\n") {
		t.Fatalf("not rewritten as one line: %q", rec.Data)
	}
	if got := mustExpiry(t, ea, "d"); !got.Equal(created.Add(2*day)) && !got.Equal(clk.Now().Add(day)) {
		t.Fatalf("deadline after touch = %v", got)
	}
}

func TestSweepSkipsUnreadableRecordsAndHealsThem(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(io.Discard)
	inner := NewMemoryAdapter()
	ea, clk := newExpiring(inner)
	_ = ea.Save(ctx, "old", []byte("1")) // will expire
	_ = ea.Save(ctx, "damaged", []byte("1"))
	_ = ea.Save(ctx, "other", []byte("1"))
	_ = inner.Save(ctx, "expiry:damaged", []byte("not a time"))
	_ = inner.Save(ctx, "expiry:ghost", []byte("\x00\x01garbage"))
	clk.Add(25 * time.Hour)
	_ = ea.Save(ctx, "other", []byte("2")) // renewed: lives
	if _, err := ea.Load(ctx, "damaged"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok, err := ea.Expiry(ctx, "damaged"); err != nil || ok {
		t.Fatalf("Expiry: %v %v", ok, err)
	}
	n, err := ea.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep aborted by a damaged record: %v", err)
	}
	if n != 1 {
		t.Fatalf("Sweep deleted %d, want the one expired document", n)
	}
	if _, err := ea.Load(ctx, "old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired document kept: %v", err)
	}
	if got := mustExpiry(t, ea, "damaged"); !got.Equal(clk.Now().Add(day)) {
		t.Fatalf("healed deadline = %v", got)
	}
	if _, err := inner.Load(ctx, "expiry:ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ghost record kept: %v", err)
	}
	if c := strings.Count(buf.String(), `unreadable expiry record of "damaged"`); c != 1 {
		t.Fatalf("logged %d times, want once:\n%s", c, buf.String())
	}
	_ = inner.Save(ctx, "expiry:other", []byte("???"))
	if err := ea.Save(ctx, "other", []byte("3")); err != nil {
		t.Fatalf("Save over a damaged record: %v", err)
	}
	rec, _ := inner.Load(ctx, "expiry:other")
	if _, err := oldParseRecord(rec.Data); err != nil {
		t.Fatalf("record not repaired: %q", rec.Data)
	}
}

func TestCreationRecordLifecycle(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryAdapter()
	ea, clk := newExpiring(inner)
	_ = ea.Save(ctx, "d", []byte("1"))
	if _, err := inner.Load(ctx, "expiry-created:d"); err != nil {
		t.Fatal(err)
	}
	if err := ea.Delete(ctx, "d"); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"expiry:d", "expiry-created:d"} {
		if _, err := inner.Load(ctx, k); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s left after Delete: %v", k, err)
		}
	}
	_ = inner.Save(ctx, "expiry-created:ghost", []byte(clk.Now().UTC().Format(time.RFC3339Nano)))
	if _, err := ea.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.Load(ctx, "expiry-created:ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dangling creation record kept: %v", err)
	}
	_ = inner.Save(ctx, "expiry-created:n", []byte(clk.Now().Add(-100*day).UTC().Format(time.RFC3339Nano)))
	ea.MaxAge = 2 * day
	_ = ea.Save(ctx, "n", []byte("1"))
	if got := mustExpiry(t, ea, "n"); !got.Equal(clk.Now().Add(day)) {
		t.Fatalf("a stale creation record was inherited: %v", got)
	}
	srv := NewServer(ea, "memory").WithExpiry(ea)
	for _, id := range []string{"expiry-created:x", "Expiry-Created:x"} {
		if rec := routeReq(t, srv.Handler(), http.MethodPut, "/docs/"+id); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT /docs/%s = %d, want 400", id, rec.Code)
		}
	}
	if IsDocumentKey("expiry-created:x") {
		t.Error("IsDocumentKey accepts it")
	}
	metas, _ := ea.List(ctx)
	for _, m := range metas {
		if strings.HasPrefix(m.ID, "expiry") {
			t.Errorf("listing shows %s", m.ID)
		}
	}
}

func TestParseExpiryOrphansDefault(t *testing.T) {
	for _, tc := range []struct {
		raw, grace string
		def        bool
		on         bool
		g          time.Duration
		bad        bool
	}{
		{"", "", false, false, 0, false},
		{"", "", true, true, DefaultOrphanGrace, false},
		{"", "2h", true, true, 2 * time.Hour, false},
		{"off", "", true, false, 0, false},
		{"none", "", true, false, 0, false},
		{"off", "1h", true, false, 0, true},
		{"collab", "", false, true, DefaultOrphanGrace, false},
		{"", "1h", false, false, 0, true},
		{"yes", "", true, false, 0, true},
	} {
		on, g, err := ParseExpiryOrphansDefault(tc.raw, tc.grace, tc.def)
		if (err != nil) != tc.bad || on != tc.on || g != tc.g {
			t.Errorf("%+v = %v %v %v", tc, on, g, err)
		}
	}
}

func TestConfigFromEnvOrphanSweepIsOnInPublicMode(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_EXPIRY_TTL", "24h")
	for _, tc := range []struct {
		public, orphans string
		want            bool
	}{
		{"", "", false}, {"true", "", true}, {"1", "", true}, {"on", "", true},
		{"true", "off", false}, {"true", "collab", true}, {"false", "", false}, {"", "collab", true},
	} {
		t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", tc.public)
		t.Setenv("GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS", tc.orphans)
		cfg := ConfigFromEnv()
		if cfg.ExpiryOrphans != tc.want {
			t.Errorf("PUBLIC=%q ORPHANS=%q: ExpiryOrphans = %v, want %v", tc.public, tc.orphans, cfg.ExpiryOrphans, tc.want)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, ea := WithConfiguredExpiry(ctx, NewMemoryAdapter(), func() Config { c := cfg; c.ExpirySweep = time.Hour; return c }())
		if (ea.Orphans != nil) != tc.want {
			t.Errorf("PUBLIC=%q ORPHANS=%q: the adapter's policy = %v", tc.public, tc.orphans, ea.Orphans)
		}
	}
}
