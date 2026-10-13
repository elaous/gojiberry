package docstore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUsageHistorySurvivesARestart(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 3, 1, 9, 5, 0, 0, time.UTC)
	_, h, mem := historyServer(t, &clock)
	_ = mem.Save(ctx, "ana-private-report", []byte("12345"))
	adminGet(h, "/admin/usage/history", true)
	clock = clock.Add(time.Hour)
	_ = mem.Save(ctx, "bo-notes", []byte("123"))
	adminGet(h, "/admin/usage/history", true)

	stored, err := mem.Load(ctx, UsageSeriesKey)
	if err != nil {
		t.Fatalf("the series was not written: %v", err)
	}
	// No identifier: the record names no document and holds no field beyond the points.
	for _, leak := range []string{"ana", "bo-notes", "report"} {
		if strings.Contains(string(stored.Data), leak) {
			t.Errorf("the stored series names %q: %s", leak, stored.Data)
		}
	}

	// A new server process on the same store, an hour later, continues the series.
	clock = clock.Add(time.Hour)
	srv2 := NewServer(mem, "memory").WithDirectoryAdmin(func(r *http.Request) bool { return true })
	srv2.usageClock = func() time.Time { return clock }
	h2 := srv2.Handler()
	got := adminGet(h2, "/admin/usage/history", true)
	if got.Code != 200 {
		t.Fatalf("%d", got.Code)
	}
	var hist UsageHistory
	if err := decodeJSON(got.Body.Bytes(), &hist); err != nil {
		t.Fatal(err)
	}
	if len(hist.Hourly) != 3 || hist.Hourly[0].Documents != 1 || hist.Hourly[1].Documents != 2 || hist.Hourly[2].Documents != 2 {
		t.Fatalf("after the restart: %+v", hist.Hourly)
	}
	// The monitor's own records are not part of what it measures.
	if hist.Hourly[2].Records != 0 {
		t.Errorf("the series counted its own record: %+v", hist.Hourly[2])
	}
}

func TestUsageSeriesIsWrittenAtANewHourAndNotOnEveryRead(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 3, 1, 9, 5, 0, 0, time.UTC)
	_, h, mem := historyServer(t, &clock)
	counting := &countingAdapter{Adapter: mem}
	srv := NewServer(counting, "memory").WithDirectoryAdmin(func(r *http.Request) bool { return true })
	srv.usageClock = func() time.Time { return clock }
	h = srv.Handler()
	adminGet(h, "/admin/usage/history", true) // first sample: written
	if counting.saves(UsageSeriesKey) != 1 {
		t.Fatalf("first read wrote %d times", counting.saves(UsageSeriesKey))
	}
	for i := 0; i < 5; i++ {
		clock = clock.Add(time.Minute)
		adminGet(h, "/admin/usage/history", true)
	}
	if counting.saves(UsageSeriesKey) != 1 {
		t.Errorf("six reads within the hour wrote %d times", counting.saves(UsageSeriesKey))
	}
	clock = clock.Add(10 * time.Minute) // past the longest wait
	adminGet(h, "/admin/usage/history", true)
	if counting.saves(UsageSeriesKey) != 2 {
		t.Errorf("after ten minutes: %d writes", counting.saves(UsageSeriesKey))
	}
	clock = clock.Add(time.Hour) // a new hour is written at once
	adminGet(h, "/admin/usage/history", true)
	if counting.saves(UsageSeriesKey) != 3 {
		t.Errorf("at a new hour: %d writes", counting.saves(UsageSeriesKey))
	}
	_ = ctx
}

func TestAStoredSeriesThatBreaksTheRulesIsRefusedWhole(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 9, 5, 0, 0, time.UTC)
	good := `{"v":1,"hourly":[{"time":"2026-03-01T08:00:00Z","documents":1,"records":0,"bytes":5,"largest":5,"quotaUsed":0,"accepted":0,"refused":0}],"daily":[]}`
	if _, err := decodeUsage([]byte(good), now); err != nil {
		t.Fatalf("a good series: %v", err)
	}
	pt := func(tm string) string {
		return `{"time":"` + tm + `","documents":1,"records":0,"bytes":5,"largest":5,"quotaUsed":0,"accepted":0,"refused":0}`
	}
	for name, bad := range map[string]string{
		"extra field":      `{"v":1,"hourly":[],"daily":[],"who":"ana"}`,
		"extra in a point": `{"v":1,"hourly":[{"time":"2026-03-01T08:00:00Z","documents":1,"records":0,"bytes":5,"largest":5,"quotaUsed":0,"accepted":0,"refused":0,"doc":"x"}],"daily":[]}`,
		"not on the hour":  `{"v":1,"hourly":[` + pt("2026-03-01T08:30:00Z") + `],"daily":[]}`,
		"not on the day":   `{"v":1,"hourly":[],"daily":[` + pt("2026-03-01T08:00:00Z") + `]}`,
		"out of order":     `{"v":1,"hourly":[` + pt("2026-03-01T08:00:00Z") + `,` + pt("2026-03-01T07:00:00Z") + `],"daily":[]}`,
		"duplicate":        `{"v":1,"hourly":[` + pt("2026-03-01T08:00:00Z") + `,` + pt("2026-03-01T08:00:00Z") + `],"daily":[]}`,
		"far future":       `{"v":1,"hourly":[` + pt("2030-03-01T08:00:00Z") + `],"daily":[]}`,
		"negative":         `{"v":1,"hourly":[{"time":"2026-03-01T08:00:00Z","documents":-1,"records":0,"bytes":5,"largest":5,"quotaUsed":0,"accepted":0,"refused":0}],"daily":[]}`,
		"version":          `{"v":2,"hourly":[],"daily":[]}`,
		"trailing":         good + `{}`,
		"not json":         `hello`,
	} {
		if _, err := decodeUsage([]byte(bad), now); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	var tooMany strings.Builder
	tooMany.WriteString(`{"v":1,"hourly":[`)
	base := now.Add(-100 * time.Hour).Truncate(time.Hour)
	for i := 0; i <= UsageHourlyPoints; i++ {
		if i > 0 {
			tooMany.WriteString(",")
		}
		tooMany.WriteString(pt(base.Add(time.Duration(i) * time.Minute * 0).Add(time.Duration(i) * time.Hour).Format(time.RFC3339)))
	}
	tooMany.WriteString(`],"daily":[]}`)
	if _, err := decodeUsage([]byte(tooMany.String()), now); err == nil {
		t.Error("a series longer than the retention was accepted")
	}

	// A server whose stored series is damaged starts again and replaces it.
	mem := NewMemoryAdapter()
	_ = mem.Save(ctx, UsageSeriesKey, []byte(`{"v":1,"hourly":[],"daily":[],"who":"ana"}`))
	clock := now
	srv := NewServer(mem, "memory").WithDirectoryAdmin(func(r *http.Request) bool { return true })
	srv.usageClock = func() time.Time { return clock }
	rec := adminGet(srv.Handler(), "/admin/usage/history", true)
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	stored, _ := mem.Load(ctx, UsageSeriesKey)
	if strings.Contains(string(stored.Data), "ana") {
		t.Errorf("the damaged record was kept: %s", stored.Data)
	}
}

func TestTheStoredSeriesIsNotReachableThroughTheDocumentAPI(t *testing.T) {
	mem := NewMemoryAdapter()
	_ = mem.Save(context.Background(), UsageSeriesKey, []byte("{}"))
	h := NewServer(mem, "memory").Handler()
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(m, "/docs/usage:series", strings.NewReader("{}")))
		if rec.Code < 400 {
			t.Errorf("%s /docs/usage:series answered %d", m, rec.Code)
		}
	}
	if IsDocumentKey(UsageSeriesKey) {
		t.Error("the series would expire as a document")
	}
}

func decodeJSON(b []byte, v any) error { return json.Unmarshal(b, v) }

// countingAdapter counts the saves of each key.
type countingAdapter struct {
	Adapter
	mu sync.Mutex
	n  map[string]int
}

func (c *countingAdapter) Save(ctx context.Context, key string, data []byte) error {
	c.mu.Lock()
	if c.n == nil {
		c.n = map[string]int{}
	}
	c.n[key]++
	c.mu.Unlock()
	return c.Adapter.Save(ctx, key, data)
}

func (c *countingAdapter) saves(key string) int { c.mu.Lock(); defer c.mu.Unlock(); return c.n[key] }
