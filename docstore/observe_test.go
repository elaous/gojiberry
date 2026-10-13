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

	"github.com/elaous/gojiberry/observe"
	"github.com/elaous/gojiberry/telemetry"
)

// obsRec is a sink that keeps everything it gets.
type obsRec struct {
	mu     sync.Mutex
	points []observe.Point
	spans  []observe.SpanData
}

func (*obsRec) Name() string { return "rec" }
func (r *obsRec) Metric(p observe.Point) {
	r.mu.Lock()
	r.points = append(r.points, p)
	r.mu.Unlock()
}
func (r *obsRec) Span(s observe.SpanData) {
	r.mu.Lock()
	r.spans = append(r.spans, s)
	r.mu.Unlock()
}
func (*obsRec) Flush(context.Context) error { return nil }
func (*obsRec) Close(context.Context) error { return nil }

// sum totals the counter points of a metric whose labels include all of want ("name=value").
func (r *obsRec) sum(m observe.Metric, want ...string) (n float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
next:
	for _, p := range r.points {
		if p.Metric != m {
			continue
		}
		for _, w := range want {
			found := false
			for _, l := range p.Labels {
				found = found || l.Name+"="+l.Value == w
			}
			if !found {
				continue next
			}
		}
		n += p.Value
	}
	return n
}

func (r *obsRec) dump() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, _ := json.Marshal([]any{r.points, r.spans})
	return string(b)
}

func observedServer(t *testing.T) (http.Handler, *obsRec, *obsRec, *Server) {
	t.Helper()
	op, us := &obsRec{}, &obsRec{}
	o := observe.New(observe.Options{Operator: op, Usage: us, TraceSample: 1})
	srv := NewServer(NewMemoryAdapter(), "memory").
		WithDirectoryAdmin(func(r *http.Request) bool { return r.Header.Get("X-Admin") == "yes" }).
		WithTelemetry(telemetry.NewCounters()).WithObserver(o)
	return srv.Handler(), op, us, srv
}

func do(h http.Handler, method, target, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.9:5555"
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestObservedServerRecordsRoutesAndStoreOpsWithoutIdentifiers(t *testing.T) {
	h, op, _, _ := observedServer(t)
	const id = "my-private-doc-7731"
	if rec := do(h, "PUT", "/docs/"+id, "hello", "Authorization", "Bearer SECRET-TOKEN-1"); rec.Code != http.StatusNoContent {
		t.Fatalf("put: %d", rec.Code)
	}
	do(h, "GET", "/docs/"+id, "", "User-Agent", "SecretAgent/1.0")
	do(h, "GET", "/docs/does-not-exist", "")
	do(h, "GET", "/docs?x=secret-query", "")
	do(h, "PUT", "/docs/"+id, "v2", "If-Match", `"nope"`) // the memory adapter has no conditional save: 501
	do(h, "DELETE", "/docs/"+id, "")

	for want, n := range map[string]float64{"save": 1, "load": 2, "list": 1, "delete": 1} {
		if got := op.sum(observe.StoreOps, "op="+want); got != n {
			t.Errorf("store ops %s = %v, want %v", want, got, n)
		}
	}
	if got := op.sum(observe.StoreOps, "op=load", "result=not_found"); got != 1 {
		t.Errorf("not_found loads = %v", got)
	}
	if got := op.sum(observe.HTTPRequests, "route=/docs/{id}", "method=GET", "status_class=4xx"); got != 1 {
		t.Errorf("GET /docs/{id} 4xx = %v", got)
	}
	if got := op.sum(observe.HTTPRequests, "route=/docs/{id}", "method=PUT"); got != 2 {
		t.Errorf("PUT /docs/{id} = %v", got)
	}
	if len(op.spans) == 0 {
		t.Fatal("no spans")
	}
	// a store span is a child of the request's span
	var child *observe.SpanData
	for i := range op.spans {
		if op.spans[i].Name == observe.SpanStoreLoad {
			child = &op.spans[i]
		}
	}
	if child == nil || child.Parent == ([8]byte{}) {
		t.Fatal("the store span has no parent")
	}
	d := op.dump()
	for _, secret := range []string{id, "SECRET-TOKEN", "SecretAgent", "203.0.113.9", "secret-query", "does-not-exist"} {
		if strings.Contains(d, secret) {
			t.Fatalf("%q reached the sink: %s", secret, d)
		}
	}
}

func TestObservedServerWithoutObserverIsUnchanged(t *testing.T) {
	srv := NewServer(NewMemoryAdapter(), "memory")
	if rec := do(srv.Handler(), "PUT", "/docs/a", "x"); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code)
	}
	if rec := do(srv.WithObserver(nil).Handler(), "GET", "/docs/a", ""); rec.Code != http.StatusOK || rec.Body.String() != "x" {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
}

func TestAdminObserveIsForTheOperatorAndWorksWhileOff(t *testing.T) {
	h, _, _, _ := observedServer(t)
	if rec := do(h, "GET", "/admin/observe", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous: %d", rec.Code)
	}
	if rec := do(h, "POST", "/admin/observe", "", "X-Admin", "yes"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post: %d", rec.Code)
	}
	rec := do(h, "GET", "/admin/observe", "", "X-Admin", "yes")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("operator: %d %v", rec.Code, rec.Header())
	}
	var d observe.Description
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil || len(d.Metrics) == 0 || len(d.Spans) == 0 || len(d.NeverSent) == 0 {
		t.Fatalf("description: %v %+v", err, d)
	}
	// the usage facts the telemetry package allows are the ones listed
	var facts []string
	for _, m := range d.Metrics {
		if m.Name == observe.UsageEvents {
			facts = m.Labels["fact"]
		}
	}
	if len(facts) != len(telemetry.Allowlist()) {
		t.Fatalf("usage facts = %v", facts)
	}

	// with observability off (no observer) the route still shows the allowlist, enabled false
	off := NewServer(NewMemoryAdapter(), "memory").WithDirectoryAdmin(func(*http.Request) bool { return true })
	rec = do(off.Handler(), "GET", "/admin/observe", "")
	var d2 observe.Description
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &d2) != nil || d2.Enabled || len(d2.Metrics) == 0 {
		t.Fatalf("off: %d %s", rec.Code, rec.Body)
	}
}

func TestUsageCountsAreRoutedToTheUsageSinkOnlyWhenAccepted(t *testing.T) {
	h, op, us, srv := observedServer(t)
	good := `{"day":"` + today() + `","counts":[{"name":"feature","value":"find","n":7},{"name":"locale","value":"de","n":2}]}`
	if rec := sendTelemetry(h, good, "203.0.113.5:1000"); rec.Code != http.StatusNoContent {
		t.Fatalf("%d", rec.Code)
	}
	if got := us.sum(observe.UsageEvents, "fact=feature", "choice=find"); got != 7 {
		t.Fatalf("usage find = %v", got)
	}
	if got := us.sum(observe.UsageEvents, "fact=locale", "choice=de"); got != 2 {
		t.Fatalf("usage de = %v", got)
	}
	// refused batches route nothing, whole
	for _, bad := range []string{
		`{"day":"` + today() + `","counts":[{"name":"feature","value":"find","n":1},{"name":"feature","value":"ana@example.com","n":1}]}`,
		`{"day":"` + today() + `","user":"ana","counts":[{"name":"feature","value":"find","n":1}]}`,
	} {
		if rec := sendTelemetry(h, bad, "203.0.113.5:1000"); rec.Code != http.StatusBadRequest {
			t.Fatal(rec.Code)
		}
	}
	if got := us.sum(observe.UsageEvents); got != 9 {
		t.Fatalf("usage total = %v, want 9 (a refused batch must route nothing)", got)
	}
	if strings.Contains(us.dump(), "ana") || strings.Contains(us.dump(), "203.0.113.5") {
		t.Fatal("request data reached the usage sink")
	}
	// the operator sink never sees user counts, and the built-in totals are unchanged by routing
	if got := op.sum(observe.UsageEvents); got != 0 {
		t.Fatalf("usage reached the operator sink: %v", got)
	}
	if srv.telemetry.Report(1)[0].N != 7 && srv.telemetry.Report(1)[1].N != 7 {
		t.Fatalf("builtin totals changed: %v", srv.telemetry.Report(1))
	}
}

func TestObserveLimitersCountsDecisionsByKindOnly(t *testing.T) {
	op := &obsRec{}
	o := observe.New(observe.Options{Operator: op})
	f := ObserveLimiters(MemoryLimiters(), o)
	l := f("passkey", 1, 1)
	ctx := context.Background()
	if ok, _ := l.Allow(ctx, "client-203.0.113.9"); !ok {
		t.Fatal("first call must pass")
	}
	if ok, _ := l.Allow(ctx, "client-203.0.113.9"); ok {
		t.Fatal("second call must be limited")
	}
	if op.sum(observe.LimiterResults, "limiter=passkey", "outcome=allowed") != 1 || op.sum(observe.LimiterResults, "limiter=passkey", "outcome=limited") != 1 {
		t.Fatalf("decisions: %s", op.dump())
	}
	if strings.Contains(op.dump(), "203.0.113") {
		t.Fatal("a limiter key reached the sink")
	}
	for name, want := range map[string]string{"passkey": "passkey", "login": "login", "auth-miss": "auth_miss", "telemetry": "telemetry",
		"dir-lookup": "directory", "dir-lookup-all-3": "directory", "dir-admin": "admin", "something-else": "other", "": "other"} {
		if got := limiterKind(name); got != want {
			t.Errorf("limiterKind(%q) = %q, want %q", name, got, want)
		}
	}
	if ObserveLimiters(MemoryLimiters(), nil) == nil {
		t.Fatal("nil observer must return the factory")
	}
}

func TestObservedSweepAndUsageSamplingReport(t *testing.T) {
	op := &obsRec{}
	o := observe.New(observe.Options{Operator: op})
	observe.SetDefault(o)
	t.Cleanup(func() { observe.SetDefault(nil) })
	mem := NewMemoryAdapter()
	e := NewExpiringAdapter(mem, FixedTTL(time.Hour))
	if _, err := e.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if op.sum(observe.SweepRuns, "sweep=expiry", "result=ok") != 1 {
		t.Fatalf("sweep not reported: %s", op.dump())
	}
	srv := NewServer(mem, "memory").WithObserver(o)
	_ = mem.Save(context.Background(), "doc", []byte("12345"))
	u, err := srv.measureUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	srv.sampleUsage(u)
	var bytes float64
	for _, p := range op.points {
		if p.Metric == observe.StoreBytes {
			bytes = p.Value
		}
	}
	if bytes != 5 {
		t.Fatalf("store bytes gauge = %v", bytes)
	}
}
