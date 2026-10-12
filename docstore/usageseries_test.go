package docstore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestUsagePointHoldsOnlyNumbersAndATime(t *testing.T) {
	typ := reflect.TypeOf(UsagePoint{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		switch f.Type.Kind() {
		case reflect.Int, reflect.Int64:
		case reflect.Struct:
			if f.Type != reflect.TypeOf(time.Time{}) {
				t.Fatalf("field %s is a struct that is not a time", f.Name)
			}
		default:
			t.Fatalf("field %s (%s) could carry an identifier: a sample is numbers and a time only", f.Name, f.Type)
		}
	}
}

func TestUsageSeriesIsBoundedAndKeepsTheLastSampleOfAPeriod(t *testing.T) {
	var u usageSeries
	start := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)
	// Two samples in one hour: the later one stands for the hour.
	u.record(start, UsagePoint{Documents: 1})
	u.record(start.Add(10*time.Minute), UsagePoint{Documents: 2})
	h := u.snapshot()
	if len(h.Hourly) != 1 || h.Hourly[0].Documents != 2 || !h.Hourly[0].Time.Equal(start.Truncate(time.Hour)) {
		t.Fatalf("%+v", h.Hourly)
	}
	// 200 days of hourly samples: the series never exceeds its limits, and keeps the newest.
	n := 200 * 24
	for i := 1; i <= n; i++ {
		u.record(start.Add(time.Duration(i)*time.Hour), UsagePoint{Documents: i})
	}
	h = u.snapshot()
	if len(h.Hourly) != UsageHourlyPoints || len(h.Daily) != UsageDailyPoints {
		t.Fatalf("hourly %d daily %d", len(h.Hourly), len(h.Daily))
	}
	if last := h.Hourly[len(h.Hourly)-1]; last.Documents != n {
		t.Fatalf("the newest hour is not kept: %+v", last)
	}
	if first := h.Hourly[0]; first.Documents != n-UsageHourlyPoints+1 {
		t.Fatalf("the oldest kept hour is wrong: %+v", first)
	}
	for i := 1; i < len(h.Daily); i++ {
		if d := h.Daily[i].Time.Sub(h.Daily[i-1].Time); d != 24*time.Hour {
			t.Fatalf("days are not consecutive: %v", d)
		}
	}
	if h.Retention.Hours != 48 || h.Retention.Days != 90 {
		t.Fatalf("%+v", h.Retention)
	}
	// A clock that goes back never rewrites or reorders the past.
	before := u.snapshot()
	u.record(start, UsagePoint{Documents: -1})
	after := u.snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("an earlier time changed the series")
	}
	// A snapshot is a copy.
	h.Hourly[0].Documents = 12345
	if u.snapshot().Hourly[0].Documents == 12345 {
		t.Fatal("the snapshot shares the series' memory")
	}
}

func historyServer(t *testing.T, clock *time.Time) (*Server, http.Handler, *MemoryAdapter) {
	t.Helper()
	mem := NewMemoryAdapter()
	srv := NewServer(mem, "memory").WithDirectoryAdmin(func(r *http.Request) bool { return r.Header.Get("X-Admin") == "yes" })
	srv.usageClock = func() time.Time { return *clock }
	return srv, srv.Handler(), mem
}

func adminGet(h http.Handler, path string, admin bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if admin {
		req.Header.Set("X-Admin", "yes")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUsageHistoryRouteShowsTheTrendAndNamesNothing(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 3, 1, 9, 5, 0, 0, time.UTC)
	_, h, mem := historyServer(t, &clock)
	_ = mem.Save(ctx, "ana-private-report", []byte("12345"))

	read := func() UsageHistory {
		rec := adminGet(h, "/admin/usage/history", true)
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %v", rec.Code, rec.Header())
		}
		for _, leak := range []string{"ana", "report"} {
			if strings.Contains(rec.Body.String(), leak) {
				t.Fatalf("the history names %q: %s", leak, rec.Body)
			}
		}
		var out UsageHistory
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := read()
	if len(first.Hourly) != 1 || first.Hourly[0].Documents != 1 || first.Hourly[0].Bytes != 5 {
		t.Fatalf("%+v", first)
	}
	_ = mem.Save(ctx, "bo-notes", []byte("123"))
	clock = clock.Add(time.Hour)
	second := read()
	if len(second.Hourly) != 2 || second.Hourly[0].Documents != 1 || second.Hourly[1].Documents != 2 || second.Hourly[1].Bytes != 8 || second.Hourly[1].Largest != 5 {
		t.Fatalf("the second hour does not show the growth: %+v", second.Hourly)
	}
	clock = clock.Add(24 * time.Hour)
	third := read()
	if len(third.Daily) != 2 {
		t.Fatalf("days: %+v", third.Daily)
	}

	if rec := adminGet(h, "/admin/usage/history", false); rec.Code != http.StatusForbidden {
		t.Fatalf("without the guard: %d", rec.Code)
	}
	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/admin/usage/history", nil))
	if post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", post.Code)
	}
	plain := NewServer(mem, "memory").Handler()
	for _, p := range []string{"/admin/usage/history", "/admin/usage/page"} {
		if rec := adminGet(plain, p, true); rec.Code != http.StatusNotFound {
			t.Fatalf("%s without an operator guard must not exist: %d", p, rec.Code)
		}
	}
	broken := NewServer(brokenStore{mem}, "b").WithDirectoryAdmin(func(*http.Request) bool { return true }).Handler()
	if rec := adminGet(broken, "/admin/usage/history", true); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a failing store: %d", rec.Code)
	}
}

func TestUsagePageIsAccessibleHTMLWithoutScript(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 3, 1, 9, 5, 0, 0, time.UTC)
	_, h, mem := historyServer(t, &clock)
	_ = mem.Save(ctx, "ana-private-report", []byte(strings.Repeat("x", 3000)))
	clock = clock.Add(-time.Hour)
	adminGet(h, "/admin/usage/page", true) // an earlier sample
	clock = clock.Add(time.Hour)
	rec := adminGet(h, "/admin/usage/page", true)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	body := rec.Body.String()
	for _, want := range []string{`<html lang="en">`, "<title>Store usage over time</title>", `name="viewport"`, "<caption>Latest hour first</caption>",
		`<th scope="col">Documents</th>`, `<th scope="row"><time datetime="2026-03-01T09:00:00Z">2026-03-01 09:00 UTC</time></th>`,
		"2.9 KiB", `<th scope="row"><time datetime="2026-03-01">2026-03-01</time></th>`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	// Newest first.
	if strings.Index(body, "09:00 UTC") > strings.Index(body, "08:00 UTC") {
		t.Error("the hours are not listed newest first")
	}
	for _, bad := range []string{"<script", "onclick", "ana-private", "report"} {
		if strings.Contains(body, bad) {
			t.Errorf("the page has %q", bad)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP: %q", csp)
	}
	if rec := adminGet(h, "/admin/usage/page", false); rec.Code != http.StatusForbidden {
		t.Fatalf("without the guard: %d", rec.Code)
	}
}

func TestRunUsageSamplingTakesASampleAtOnceAndStops(t *testing.T) {
	clock := time.Date(2026, 3, 1, 9, 5, 0, 0, time.UTC)
	srv, _, mem := historyServer(t, &clock)
	_ = mem.Save(context.Background(), "d", []byte("abc"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.RunUsageSampling(ctx, time.Hour); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(srv.usage.snapshot().Hourly) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no sample was taken")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if p := srv.usage.snapshot().Hourly[0]; p.Documents != 1 || p.Bytes != 3 {
		t.Fatalf("%+v", p)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the sampler did not stop")
	}
}
