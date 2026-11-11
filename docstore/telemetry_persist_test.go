package docstore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/telemetry"
)

func fastSave(t *testing.T) {
	t.Helper()
	old := telemetrySaveDelay
	telemetrySaveDelay = 20 * time.Millisecond
	t.Cleanup(func() { telemetrySaveDelay = old })
}

func waitStored(t *testing.T, mem *MemoryAdapter) Document {
	t.Helper()
	for i := 0; i < 300; i++ {
		if doc, err := mem.Load(context.Background(), TelemetryKey); err == nil {
			return doc
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the totals were never written to the store")
	return Document{}
}

func TestUsageTotalsSurviveARestartThroughTheAdapter(t *testing.T) {
	fastSave(t)
	mem := NewMemoryAdapter()
	srv := NewServer(mem, "memory").WithTelemetry(telemetry.NewCounters())
	good := `{"day":"` + today() + `","counts":[{"name":"feature","value":"find","n":7}]}`
	if rec := sendTelemetry(srv.Handler(), good, "203.0.113.5:1000"); rec.Code != http.StatusNoContent {
		t.Fatalf("%d", rec.Code)
	}
	doc := waitStored(t, mem)

	raw := string(doc.Data)
	for _, leak := range []string{"203.0.113", "1000", "Mozilla", "Authorization"} {
		if strings.Contains(raw, leak) {
			t.Errorf("the stored totals mention %q: %s", leak, raw)
		}
	}
	if len(doc.Data) > 4<<10 {
		t.Errorf("a single count is a %d byte record", len(doc.Data))
	}

	c2 := telemetry.NewCounters()
	_ = NewServer(mem, "memory").WithTelemetry(c2)
	if got := c2.Report(1); len(got) != 1 || got[0].N != 7 || got[0].Value != "find" {
		t.Fatalf("after the restart: %v", got)
	}
	if c2.Totals().Accepted != 1 {
		t.Errorf("request totals after the restart: %+v", c2.Totals())
	}
}

func TestADamagedTotalsRecordIsReplacedNotTrusted(t *testing.T) {
	fastSave(t)
	mem := NewMemoryAdapter()
	_ = mem.Save(context.Background(), TelemetryKey, []byte(`{"v":1,"accepted":0,"refused":0,"days":{"`+today()+`":[{"name":"feature","value":"ana@example.com","n":4}]}}`))
	c := telemetry.NewCounters()
	srv := NewServer(mem, "memory").WithTelemetry(c)
	if got := c.Report(1); len(got) != 0 {
		t.Fatalf("a total outside the allowlist was loaded: %v", got)
	}
	good := `{"day":"` + today() + `","counts":[{"name":"feature","value":"find","n":2}]}`
	if rec := sendTelemetry(srv.Handler(), good, "203.0.113.5:1000"); rec.Code != http.StatusNoContent {
		t.Fatalf("%d", rec.Code)
	}
	for i := 0; i < 300; i++ {
		doc, _ := mem.Load(context.Background(), TelemetryKey)
		if !strings.Contains(string(doc.Data), "ana@example.com") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the damaged record was never replaced")
}

func TestTheTotalsRecordIsNotReachableThroughTheDocumentAPI(t *testing.T) {
	mem := NewMemoryAdapter()
	_ = mem.Save(context.Background(), TelemetryKey, []byte("{}"))
	h := NewServer(mem, "memory").Handler()
	for _, tc := range []struct{ method, body string }{{http.MethodGet, ""}, {http.MethodPut, `{"v":1}`}, {http.MethodDelete, ""}} {
		req := httptest.NewRequest(tc.method, "/docs/telemetry:totals", strings.NewReader(tc.body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code < 400 {
			t.Errorf("%s /docs/telemetry:totals answered %d", tc.method, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if strings.Contains(rec.Body.String(), "telemetry:") {
		t.Errorf("the listing shows the totals record: %s", rec.Body)
	}
	if IsDocumentKey(TelemetryKey) {
		t.Error("the totals record would expire as a document")
	}
}

func TestTheReportCanBeRoundedByTheOperator(t *testing.T) {
	srv := NewServer(NewMemoryAdapter(), "memory").WithDirectoryAdmin(func(r *http.Request) bool { return r.Header.Get("X-Admin") == "yes" })
	if err := srv.WithTelemetryNoise(5); err != nil { // before WithTelemetry: nothing to round yet, no error
		t.Fatal(err)
	}
	c := telemetry.NewCounters()
	srv.WithTelemetry(c)
	if err := srv.WithTelemetryNoise(5); err != nil {
		t.Fatal(err)
	}
	if err := srv.WithTelemetryNoise(5000); err == nil {
		t.Error("a step of 5000 was accepted")
	}
	h := srv.Handler()
	good := `{"day":"` + today() + `","counts":[{"name":"feature","value":"find","n":8},{"name":"feature","value":"share","n":3}]}`
	if rec := sendTelemetry(h, good, "203.0.113.5:1000"); rec.Code != http.StatusNoContent {
		t.Fatalf("%d", rec.Code)
	}
	for _, row := range c.Report(0) {
		if row.N%5 != 0 {
			t.Errorf("a figure that is not a multiple of 5: %v", row)
		}
	}
	if tot := c.Totals(); tot.Accepted != 1 { // request totals are counts of requests, not rounded
		t.Errorf("totals %+v", tot)
	}
}

func TestTwoServersOnOneStoreCountTogether(t *testing.T) {
	fastSave(t)
	store := NewMemoryAdapter()
	mk := func() (http.Handler, *telemetry.Counters) {
		c := telemetry.NewCounters()
		srv := NewServer(store, "memory").WithDirectoryAdmin(func(r *http.Request) bool { return r.Header.Get("X-Admin") == "yes" }).WithTelemetry(c)
		return srv.Handler(), c
	}
	a, ca := mk()
	b, cb := mk()
	if !ca.Shared() || !cb.Shared() {
		t.Fatal("the counters are not shared on a store with conditional writes")
	}
	body := func(n int) string {
		return `{"day":"` + today() + `","counts":[{"name":"feature","value":"find","n":` + strconv.Itoa(n) + `}]}`
	}
	sendTelemetry(a, body(6), "203.0.113.5:1")
	sendTelemetry(b, body(7), "203.0.113.6:1")
	read := func(h http.Handler) int {
		req := httptest.NewRequest(http.MethodGet, "/admin/telemetry", nil)
		req.Header.Set("X-Admin", "yes")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out struct{ Totals []telemetry.Reported }
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		for _, tt := range out.Totals {
			if tt.Name == "feature" && tt.Value == "find" {
				return tt.N
			}
		}
		return 0
	}
	read(a)
	read(b)
	if x, y := read(a), read(b); x != 13 || y != 13 {
		t.Errorf("the two reports say %d and %d, want 13 on both", x, y)
	}
}
