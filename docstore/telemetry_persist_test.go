package docstore

import (
	"context"
	"net/http"
	"net/http/httptest"
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

	// Bounded and without identifiers: the record is the totals and nothing else.
	raw := string(doc.Data)
	for _, leak := range []string{"203.0.113", "1000", "Mozilla", "Authorization"} {
		if strings.Contains(raw, leak) {
			t.Errorf("the stored totals mention %q: %s", leak, raw)
		}
	}
	if len(doc.Data) > 4<<10 {
		t.Errorf("a single count is a %d byte record", len(doc.Data))
	}

	// A new process on the same store starts from them.
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
