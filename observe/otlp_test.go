package observe

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

type otlpCapture struct {
	mu   sync.Mutex
	got  map[string]string
	auth string
	code int
}

func (c *otlpCapture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	c.mu.Lock()
	if c.got == nil {
		c.got = map[string]string{}
	}
	c.got[r.URL.Path] = b.String()
	c.auth = r.Header.Get("Authorization")
	code := c.code
	c.mu.Unlock()
	if code != 0 {
		w.WriteHeader(code)
	}
}

func TestOTLPExportsMetricsAndSpans(t *testing.T) {
	c := &otlpCapture{}
	srv := httptest.NewServer(c)
	defer srv.Close()
	o, err := NewOTLP(OTLPConfig{MetricsURL: srv.URL + "/v1/metrics", TracesURL: srv.URL + "/v1/traces", Headers: map[string]string{"Authorization": "Bearer sekret"}, Service: "svc", Interval: -1})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []float64{1, 3} {
		o.Metric(Point{Metric: "requests_total", Kind: Counter, Value: v, Source: Operator, Labels: []Label{{"route", "/x"}}})
	}
	o.Metric(Point{Metric: "request_seconds", Kind: Histogram, Value: 0.5, Source: Operator})
	o.Metric(Point{Metric: "queue_size", Kind: Gauge, Value: 7, Source: Operator})
	o.Span(SpanData{Name: "request", TraceID: [16]byte{1}, ID: [8]byte{2}, Parent: [8]byte{3}, Start: time.Unix(1, 0), End: time.Unix(2, 0), Attrs: []Attr{S("result", "error"), N("count", 2)}})
	if err := o.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.auth != "Bearer sekret" {
		t.Error("headers are sent")
	}
	var m struct {
		ResourceMetrics []struct {
			Resource     struct{ Attributes []otlpAttr }
			ScopeMetrics []struct{ Metrics []map[string]any }
		}
	}
	if err := json.Unmarshal([]byte(c.got["/v1/metrics"]), &m); err != nil {
		t.Fatal(err)
	}
	ms := m.ResourceMetrics[0].ScopeMetrics[0].Metrics
	if len(ms) != 3 || *m.ResourceMetrics[0].Resource.Attributes[0].Value.String != "svc" {
		t.Fatalf("metrics: %s", c.got["/v1/metrics"])
	}
	body := c.got["/v1/metrics"]
	for _, want := range []string{`"gojiberry.requests_total"`, `"isMonotonic":true`, `"asDouble":4`, `"histogram"`, `"gauge"`, `"asDouble":7`, `"key":"route"`} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %s: %s", want, body)
		}
	}
	tr := c.got["/v1/traces"]
	for _, want := range []string{`"traceId":"01000000000000000000000000000000"`, `"parentSpanId":"0300000000000000"`, `"code":2`, `"startTimeUnixNano":"1000000000"`, `"doubleValue":2`} {
		if !strings.Contains(tr, want) {
			t.Errorf("trace lacks %s: %s", want, tr)
		}
	}
	if d, _ := json.Marshal(o.Describe()); strings.Contains(string(d), "sekret") || !strings.Contains(string(d), "Authorization") {
		t.Errorf("Describe names header names, never values: %s", d)
	}
}

func TestOTLPFailureIsCountedAndDropped(t *testing.T) {
	c := &otlpCapture{code: 503}
	srv := httptest.NewServer(c)
	defer srv.Close()
	o, _ := NewOTLP(OTLPConfig{MetricsURL: srv.URL + "/m", TracesURL: srv.URL + "/t", Headers: map[string]string{"X-Key": "sekret"}, Interval: -1})
	o.Metric(Point{Metric: "requests_total", Kind: Counter, Value: 1, Source: Operator})
	err := o.Flush(context.Background())
	if err == nil || strings.Contains(err.Error(), "sekret") {
		t.Fatalf("a failure is reported without secrets: %v", err)
	}
	if len(o.series) != 0 || o.Describe().(map[string]any)["failed"] != int64(1) {
		t.Error("dropped and counted")
	}
}

func TestOTLPEnv(t *testing.T) {
	env := func(m map[string]string) SinkEnv {
		return SinkEnv{Getenv: func(k string) string { return m[k] }, Service: "svc"}
	}
	if _, err := newOTLPFromEnv(context.Background(), env(nil)); err == nil || !strings.Contains(err.Error(), envOTLPEndpoint) {
		t.Errorf("a missing endpoint names the variable: %v", err)
	}
	for _, bad := range []string{"http://collector.example.com", "https://u:p@c.example", "https://c.example?k=1", "nonsense"} {
		if _, err := newOTLPFromEnv(context.Background(), env(map[string]string{envOTLPEndpoint: bad})); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	if _, err := newOTLPFromEnv(context.Background(), env(map[string]string{envOTLPEndpoint: "https://c.example", envOTLPHeaders: "novalue"})); err == nil {
		t.Error("bad headers are refused")
	}
	s, err := newOTLPFromEnv(context.Background(), env(map[string]string{envOTLPEndpoint: "http://localhost:4318/", envOTLPHeaders: "api-key=a%20b, x=y", envOTLPInterval: "1h"}))
	if err != nil {
		t.Fatal(err)
	}
	o := s.(*OTLP)
	if o.cfg.MetricsURL != "http://localhost:4318/v1/metrics" || o.cfg.TracesURL != "http://localhost:4318/v1/traces" || o.cfg.Headers["api-key"] != "a b" {
		t.Errorf("config: %+v", o.cfg)
	}
	_ = o.Close(context.Background())
	if _, err := newOTLPFromEnv(context.Background(), env(map[string]string{envOTLPEndpoint: "https://c.example", envOTLPInterval: "1ms"})); err == nil {
		t.Error("short interval refused")
	}
}

func TestOTLPBoundsSpans(t *testing.T) {
	o, _ := NewOTLP(OTLPConfig{MetricsURL: "http://localhost/m", TracesURL: "http://localhost/t", Interval: -1})
	for range otlpMaxSpans + 3 {
		o.Span(SpanData{Name: "request"})
	}
	if len(o.spans) != otlpMaxSpans || o.lost != 3 {
		t.Errorf("spans %d lost %d", len(o.spans), o.lost)
	}
}

func TestOTLPThroughFromEnv(t *testing.T) {
	env := map[string]string{"GOJIBERRY_OBS": "on", "GOJIBERRY_OBS_SINKS": "builtin,otlp", envOTLPEndpoint: "http://127.0.0.1:1", envOTLPInterval: "1h"}
	o, _, err := FromEnv(context.Background(), func(k string) string { return env[k] })
	if err != nil || o == nil {
		t.Fatalf("FromEnv: %v", err)
	}
	_ = o.Close(context.Background())
}
