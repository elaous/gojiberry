package docstore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/telemetry"
)

func telemetryServer(t *testing.T) (http.Handler, *MemoryAdapter, *telemetry.Counters) {
	t.Helper()
	mem := NewMemoryAdapter()
	c := telemetry.NewCounters()
	srv := NewServer(mem, "memory").WithDirectoryAdmin(func(r *http.Request) bool { return r.Header.Get("X-Admin") == "yes" }).WithTelemetry(c)
	return srv.Handler(), mem, c
}

func sendTelemetry(h http.Handler, body, addr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/telemetry", strings.NewReader(body))
	req.RemoteAddr = addr
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func today() string { return time.Now().UTC().Format(telemetry.DayFormat) }

func TestTelemetryRouteCountsAndNeverTouchesTheStore(t *testing.T) {
	h, mem, c := telemetryServer(t)
	before, _ := mem.List(context.Background())
	good := `{"day":"` + today() + `","counts":[{"name":"feature","value":"find","n":7}]}`
	if rec := sendTelemetry(h, good, "203.0.113.5:1000"); rec.Code != http.StatusNoContent {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	after, _ := mem.List(context.Background())
	if len(before) != len(after) {
		t.Fatalf("a usage report wrote to the store: %v", after)
	}
	if got := c.Report(1); len(got) != 1 || got[0].N != 7 {
		t.Fatalf("totals: %v", got)
	}
	// An event outside the allowlist, or with an extra field, is refused and counts nothing.
	for _, bad := range []string{
		`{"day":"` + today() + `","counts":[{"name":"feature","value":"ana@example.com","n":1}]}`,
		`{"day":"` + today() + `","user":"ana","counts":[{"name":"feature","value":"find","n":1}]}`,
	} {
		if rec := sendTelemetry(h, bad, "203.0.113.5:1000"); rec.Code != http.StatusBadRequest {
			t.Fatalf("refused request answered %d", rec.Code)
		}
	}
	if got := c.Report(1); len(got) != 1 || got[0].N != 7 {
		t.Fatalf("a refused request counted: %v", got)
	}
}

func TestTelemetryIsRateLimitedPerClientWithoutKeepingTheAddress(t *testing.T) {
	h, mem, _ := telemetryServer(t)
	good := `{"day":"` + today() + `","counts":[{"name":"feature","value":"find","n":1}]}`
	limited := 0
	for range telemetryPerMinute + 4 {
		if sendTelemetry(h, good, "203.0.113.5:1000").Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("a flood from one client was not limited")
	}
	if rec := sendTelemetry(h, good, "198.51.100.7:1000"); rec.Code != http.StatusNoContent {
		t.Fatalf("another client shares the budget: %d", rec.Code)
	}
	metas, _ := mem.List(context.Background())
	if len(metas) != 0 {
		t.Fatalf("the receiver left records in the store: %v", metas)
	}
}

func TestTelemetryReportAndUsageAreOperatorOnlyAndShowNoSmallCounts(t *testing.T) {
	h, _, _ := telemetryServer(t)
	sendTelemetry(h, `{"day":"`+today()+`","counts":[{"name":"feature","value":"find","n":2},{"name":"feature","value":"share","n":9}]}`, "203.0.113.5:1")
	sendTelemetry(h, `{"day":"`+today()+`","counts":[{"name":"feature","value":"nope","n":9}]}`, "203.0.113.6:1")

	get := func(path string, admin bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if admin {
			req.Header.Set("X-Admin", "yes")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := get("/admin/telemetry", false); rec.Code != http.StatusForbidden {
		t.Fatalf("without the guard: %d", rec.Code)
	}
	rec := get("/admin/telemetry?k=1", true) // a smaller threshold than the default is not honoured
	var out struct {
		K      int `json:"k"`
		Totals []telemetry.Reported
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || out.K != telemetry.DefaultK || len(out.Totals) != 1 || out.Totals[0].Value != "share" {
		t.Fatalf("%d %+v", rec.Code, out)
	}
	var u usageResponse
	_ = json.Unmarshal(get("/admin/usage", true).Body.Bytes(), &u)
	if u.Telemetry == nil || u.Telemetry.Accepted != 1 || u.Telemetry.Refused != 1 {
		t.Fatalf("usage: %+v", u.Telemetry)
	}
}

func TestTelemetryDoesNotExistUnlessTheOperatorTurnsItOn(t *testing.T) {
	h := NewServer(NewMemoryAdapter(), "memory").Handler()
	if rec := sendTelemetry(h, `{}`, "203.0.113.5:1"); rec.Code != http.StatusNotFound {
		t.Fatalf("off: %d", rec.Code)
	}
}
