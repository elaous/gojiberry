package observe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type capture struct {
	mu   sync.Mutex
	body []string
	code int
}

func (c *capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	c.body = append(c.body, r.URL.Path+" "+string(b))
	code := c.code
	c.mu.Unlock()
	if code != 0 {
		w.WriteHeader(code)
	}
}

func TestPostHogSendsAggregatesWithoutPersons(t *testing.T) {
	c := &capture{}
	srv := httptest.NewServer(c)
	defer srv.Close()
	p, err := NewPostHog(PostHogConfig{APIKey: "phc_secretkey", Host: srv.URL, Service: "svc", Interval: -1})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []float64{1, 3} {
		p.Metric(Point{Metric: "requests_total", Kind: Counter, Value: v, Source: Operator, Labels: []Label{{"route", "/x"}}})
	}
	p.Span(SpanData{Name: "request", TraceID: [16]byte{1}, ID: [8]byte{2}, Start: time.Unix(0, 0), End: time.Unix(1, 0), Attrs: []Attr{S("result", "ok"), N("n", 2)}})
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.body) != 1 || !strings.HasPrefix(c.body[0], "/batch/ ") {
		t.Fatalf("one batch to /batch/: %v", c.body)
	}
	var got struct {
		APIKey string    `json:"api_key"`
		Batch  []phEvent `json:"batch"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(c.body[0], "/batch/ ")), &got); err != nil {
		t.Fatal(err)
	}
	if got.APIKey != "phc_secretkey" || len(got.Batch) != 2 {
		t.Fatalf("batch: %+v", got)
	}
	for _, e := range got.Batch {
		if e.DistinctID != "svc" || e.Properties["$process_person_profile"] != false || e.Properties["$geoip_disable"] != true {
			t.Errorf("event must not create a person or look up a place: %+v", e)
		}
		for k := range e.Properties {
			if strings.Contains(strings.ToLower(k), "ip") && k != "$geoip_disable" && k != "duration_ms" {
				t.Errorf("unexpected property %q", k)
			}
		}
	}
	m := got.Batch[0].Properties
	if m["count"] != float64(2) || m["sum"] != float64(4) || m["label_route"] != "/x" {
		t.Errorf("aggregate: %v", m)
	}
	if d, _ := json.Marshal(p.Describe()); strings.Contains(string(d), "phc_") {
		t.Errorf("Describe must not hold the key: %s", d)
	}
}

func TestPostHogFailureIsCountedAndDropped(t *testing.T) {
	c := &capture{code: 500}
	srv := httptest.NewServer(c)
	defer srv.Close()
	p, _ := NewPostHog(PostHogConfig{APIKey: "phc_k", Host: srv.URL, Interval: -1})
	p.Metric(Point{Metric: "requests_total", Kind: Counter, Value: 1, Source: Operator})
	err := p.Flush(context.Background())
	if err == nil || strings.Contains(err.Error(), "phc_k") {
		t.Fatalf("a failure is reported without the key: %v", err)
	}
	if len(p.series) != 0 {
		t.Error("what failed is dropped, not kept without bound")
	}
	if p.Describe().(map[string]any)["events_failed"] != int64(1) {
		t.Error("failure counted")
	}
}

func TestPostHogHostAndKey(t *testing.T) {
	for _, h := range []string{"http://example.com", "ftp://x", "https://user:p@x.example", "https://x.example?a=1", "nonsense"} {
		if _, err := posthogHost(h); err == nil {
			t.Errorf("%q should be refused", h)
		}
	}
	for h, want := range map[string]string{"": PostHogUSHost, "EU": PostHogEUHost, "https://ph.example.org/": "https://ph.example.org", "http://localhost:8000": "http://localhost:8000"} {
		if got, err := posthogHost(h); err != nil || got != want {
			t.Errorf("%q -> %q, %v", h, got, err)
		}
	}
	env := func(m map[string]string) SinkEnv {
		return SinkEnv{Getenv: func(k string) string { return m[k] }, ReadFile: func(string) ([]byte, error) { return []byte(" phc_file\n"), nil }}
	}
	if _, err := newPostHogFromEnv(context.Background(), env(map[string]string{})); err == nil || !strings.Contains(err.Error(), envPostHogKey) {
		t.Errorf("a missing key names the variable: %v", err)
	}
	if _, err := newPostHogFromEnv(context.Background(), env(map[string]string{envPostHogKey: "a", envPostHogKeyFile: "/f"})); err == nil {
		t.Error("both key sources are refused")
	}
	s, err := newPostHogFromEnv(context.Background(), env(map[string]string{envPostHogKeyFile: "/f", envPostHogInterval: "1h"}))
	if err != nil || s.(*PostHog).cfg.APIKey != "phc_file" {
		t.Fatalf("key from file: %v", err)
	}
	_ = s.Close(context.Background())
	if _, err := newPostHogFromEnv(context.Background(), env(map[string]string{envPostHogKey: "k", envPostHogInterval: "1ms"})); err == nil {
		t.Error("a too short interval is refused")
	}
}

func TestPostHogBoundsSpans(t *testing.T) {
	p, _ := NewPostHog(PostHogConfig{APIKey: "k", Interval: -1})
	for range posthogMaxSpans + 5 {
		p.Span(SpanData{Name: "request"})
	}
	if len(p.spans) != posthogMaxSpans || p.lost != 5 {
		t.Errorf("spans %d lost %d", len(p.spans), p.lost)
	}
}
