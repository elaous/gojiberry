package observe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// capture is a stand-in for PostHog that keeps every request.
type capture struct {
	mu       sync.Mutex
	requests []capturedRequest
	status   int
}

type capturedRequest struct {
	Path    string
	Header  http.Header
	Body    postHogBatch
	RawBody []byte
}

func (c *capture) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body postHogBatch
		_ = json.Unmarshal(b, &body)
		c.mu.Lock()
		c.requests = append(c.requests, capturedRequest{Path: r.URL.Path, Header: r.Header.Clone(), Body: body, RawBody: b})
		st := c.status
		c.mu.Unlock()
		if st == 0 {
			st = 200
		}
		w.WriteHeader(st)
		_, _ = w.Write([]byte(`{"status":"Ok"}`))
	})
}

func (c *capture) all() []capturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]capturedRequest(nil), c.requests...)
}

func (c *capture) setStatus(s int) { c.mu.Lock(); c.status = s; c.mu.Unlock() }

func (c *capture) events() (out []postHogEvent) {
	for _, r := range c.all() {
		out = append(out, r.Body.Batch...)
	}
	return out
}

func newPostHogFor(t *testing.T, c *capture) *PostHog {
	t.Helper()
	srv := httptest.NewServer(c.handler())
	t.Cleanup(srv.Close)
	p := NewPostHog(PostHogConfig{Host: srv.URL, Key: "phc_TESTKEY_123", DistinctID: "install-1", Interval: time.Minute}, srv.Client())
	p.now = func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) }
	return p
}

func TestPostHogSendsAggregatesWithTheFlagsAndNoIdentity(t *testing.T) {
	c := &capture{}
	p := newPostHogFor(t, c)
	o := New(Options{Operator: p, Usage: p})
	o.SetUsageAllowlist(map[string][]string{"feature": {"find"}})
	for range 5 {
		o.Add(HTTPRequests, 1, "route", "/docs/{id}", "method", "GET", "status_class", "2xx")
	}
	o.Observe(HTTPDuration, 0.5, "route", "/docs/{id}", "method", "GET")
	o.Observe(HTTPDuration, 1.5, "route", "/docs/{id}", "method", "GET")
	o.Set(StoreBytes, 10)
	o.Set(StoreBytes, 99)
	o.Usage("feature", "find", 4)
	_, sp := o.Start(context.Background(), SpanHTTP) // sampled out (TraceSample 0), and PostHog ignores spans anyway
	sp.End()
	if err := o.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	reqs := c.all()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Path != "/batch/" || r.Body.APIKey != "phc_TESTKEY_123" {
		t.Fatalf("path=%q key=%q", r.Path, r.Body.APIKey)
	}
	if got := r.Header.Get("User-Agent"); got != posthogUserAgent {
		t.Fatalf("user agent = %q", got)
	}
	for _, h := range []string{"Cookie", "Authorization", "X-Forwarded-For", "X-Real-Ip", "Referer"} {
		if r.Header.Get(h) != "" {
			t.Fatalf("header %s was sent", h)
		}
	}
	byName := map[string]postHogEvent{}
	for _, e := range r.Body.Batch {
		byName[e.Event+"|"+e.Properties["source"].(string)] = e
		if e.DistinctID != "install-1" {
			t.Fatalf("distinct id = %q", e.DistinctID)
		}
		if e.Properties["$process_person_profile"] != false || e.Properties["$geoip_disable"] != true {
			t.Fatalf("flags missing: %v", e.Properties)
		}
		if e.Timestamp != "2030-01-02T03:04:05Z" {
			t.Fatalf("timestamp = %q", e.Timestamp)
		}
		for k := range e.Properties {
			switch k {
			case "$process_person_profile", "$geoip_disable", "$lib", "source", "kind", "interval_seconds", "value", "count", "sum", "min", "max":
			default:
				if _, isLabel := labelAllowed[k]; !isLabel && !usageLabels[k] {
					t.Fatalf("property %q is not a flag, a statistic or a label: %v", k, e.Properties)
				}
				if k == "$ip" {
					t.Fatal("an address property")
				}
			}
		}
	}
	if len(byName) != 4 {
		t.Fatalf("events = %v", byName)
	}
	if e := byName["gojiberry.http_requests_total|operator"]; e.Properties["value"] != 5.0 || e.Properties["route"] != "/docs/{id}" || e.Properties["kind"] != "counter" {
		t.Fatalf("counter event = %v", e.Properties)
	}
	if e := byName["gojiberry.http_request_duration_seconds|operator"]; e.Properties["count"] != 2.0 || e.Properties["sum"] != 2.0 || e.Properties["min"] != 0.5 || e.Properties["max"] != 1.5 {
		t.Fatalf("histogram event = %v", e.Properties)
	}
	if e := byName["gojiberry.store_bytes|operator"]; e.Properties["value"] != 99.0 {
		t.Fatalf("gauge event = %v", e.Properties)
	}
	if e := byName["gojiberry.usage_events_total|usage"]; e.Properties["value"] != 4.0 || e.Properties["fact"] != "feature" || e.Properties["choice"] != "find" {
		t.Fatalf("usage event = %v", e.Properties)
	}
	// a second flush with nothing new sends nothing
	if err := p.Flush(context.Background()); err != nil || len(c.all()) != 1 {
		t.Fatalf("empty flush: %v, %d requests", err, len(c.all()))
	}
}

func TestPostHogSendsNothingWhileOffAndNothingFromUnselectedSinks(t *testing.T) {
	c := &capture{}
	srv := httptest.NewServer(c.handler())
	defer srv.Close()
	keyVars := []string{"GOJIBERRY_OBS_POSTHOG_HOST", srv.URL, "GOJIBERRY_OBS_POSTHOG_KEY", "phc_x", "GOJIBERRY_OBS_POSTHOG_INTERVAL", "10s"}
	for name, kv := range map[string][]string{
		"unset":              keyVars,
		"off":                append([]string{"GOJIBERRY_OBS", "off", "GOJIBERRY_OBS_SINKS", "posthog"}, keyVars...),
		"on without posthog": append([]string{"GOJIBERRY_OBS", "on", "GOJIBERRY_OBS_SINKS", "builtin"}, keyVars...),
		"sinks without on":   append([]string{"GOJIBERRY_OBS_SINKS", "posthog", "GOJIBERRY_OBS_USAGE_SINKS", "posthog"}, keyVars...),
	} {
		o, _, err := FromEnv(context.Background(), env(kv...))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		o.Add(Errors, 1, "code", "panic")
		_ = o.Flush(context.Background())
		_ = o.Close(context.Background())
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(c.all()); n != 0 {
		t.Fatalf("%d requests reached PostHog while it was not selected", n)
	}
}

func TestPostHogFlushesOnShutdownAndOnTheInterval(t *testing.T) {
	c := &capture{}
	srv := httptest.NewServer(c.handler())
	defer srv.Close()
	o, _, err := FromEnv(context.Background(), env("GOJIBERRY_OBS", "on", "GOJIBERRY_OBS_SINKS", "posthog", "GOJIBERRY_OBS_POSTHOG_HOST", srv.URL,
		"GOJIBERRY_OBS_POSTHOG_KEY", "phc_x"))
	if err != nil {
		t.Fatal(err)
	}
	o.Add(Errors, 3, "code", "panic")
	if n := len(c.all()); n != 0 {
		t.Fatalf("sent before the interval or a flush: %d", n)
	}
	if err := o.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	ev := c.events()
	if len(ev) != 1 || ev[0].Properties["value"] != 3.0 {
		t.Fatalf("shutdown did not flush: %v", ev)
	}
	if err := o.Close(context.Background()); err != nil { // closing twice is harmless
		t.Fatal(err)
	}

	c2 := &capture{}
	p := newPostHogFor(t, c2)
	p.cfg.Interval = 20 * time.Millisecond
	p.Start()
	p.Metric(Point{Metric: Errors, Kind: Counter, Value: 1, Labels: []Label{{"code", "panic"}}, Source: Operator})
	deadline := time.Now().Add(3 * time.Second)
	for len(c2.all()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(c2.all()) == 0 {
		t.Fatal("the interval did not flush")
	}
	_ = p.Close(context.Background())
}

func TestPostHogKeepsWhatFailedForRetryAndDropsWhatCannotPass(t *testing.T) {
	c := &capture{}
	p := newPostHogFor(t, c)
	add := func(n float64) {
		p.Metric(Point{Metric: Errors, Kind: Counter, Value: n, Labels: []Label{{"code", "panic"}}, Source: Operator})
	}
	c.setStatus(503)
	add(2)
	if err := p.Flush(context.Background()); err == nil || strings.Contains(err.Error(), "phc_") {
		t.Fatalf("err = %v", err)
	}
	add(3)
	c.setStatus(200)
	if err := p.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	reqs := c.all()
	last := reqs[len(reqs)-1].Body.Batch
	if len(last) != 1 || last[0].Properties["value"] != 5.0 {
		t.Fatalf("after a failure the counts must add up: %v", last)
	}
	// a refusal that cannot pass (a wrong key) drops the data instead of retrying forever
	c.setStatus(401)
	add(1)
	if err := p.Flush(context.Background()); err == nil {
		t.Fatal("401 must be an error")
	}
	c.setStatus(200)
	n := len(c.all())
	if err := p.Flush(context.Background()); err != nil || len(c.all()) != n {
		t.Fatalf("a refused batch was kept: %v", err)
	}
}

func TestPostHogMemoryAndRequestsAreBounded(t *testing.T) {
	c := &capture{}
	p := newPostHogFor(t, c)
	for i := range maxSeries * 2 {
		p.Metric(Point{Metric: Errors, Kind: Counter, Value: 1, Labels: []Label{{"code", strings.Repeat("c", i+1)}}, Source: Operator})
	}
	p.mu.Lock()
	n, over := len(p.series), p.overflow
	p.mu.Unlock()
	if n != maxSeries || over != int64(maxSeries) {
		t.Fatalf("series=%d overflow=%d", n, over)
	}
	if err := p.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	reqs := c.all()
	if len(reqs) != (maxSeries+maxEventsPerRequest-1)/maxEventsPerRequest {
		t.Fatalf("requests = %d", len(reqs))
	}
	for _, r := range reqs {
		if len(r.Body.Batch) > maxEventsPerRequest {
			t.Fatalf("a request held %d events", len(r.Body.Batch))
		}
	}
	// a long outage does not grow memory: failed series are put back into the same bounded map
	c.setStatus(500)
	for range 5 {
		for i := range 300 {
			p.Metric(Point{Metric: Errors, Kind: Counter, Value: 1, Labels: []Label{{"code", strings.Repeat("c", i+1)}}, Source: Operator})
		}
		_ = p.Flush(context.Background())
	}
	if p.buffered() > 300 {
		t.Fatalf("buffered = %d after repeated failures", p.buffered())
	}
}

func TestFanoutWithAFailingPostHogStillFeedsTheBuiltinSink(t *testing.T) {
	c := &capture{}
	c.setStatus(500)
	p := newPostHogFor(t, c)
	b := NewBuiltin()
	o := New(Options{Operator: Fanout(p, b)})
	o.Add(Errors, 1, "code", "panic")
	if err := o.Flush(context.Background()); err == nil {
		t.Fatal("the PostHog failure must be reported")
	}
	series, _, _ := b.Snapshot()
	if len(series) != 1 || series[0].Count != 1 {
		t.Fatalf("the builtin sink lost the point: %+v", series)
	}
}

func TestPostHogConfigFromEnv(t *testing.T) {
	read := func(p string) ([]byte, error) {
		if p == "/run/secrets/key" {
			return []byte("phc_FROM_FILE\n"), nil
		}
		return nil, io.ErrUnexpectedEOF
	}
	cfg := func(kv ...string) (PostHogConfig, error) {
		return PostHogConfigFromEnv(SinkEnv{Getenv: env(kv...), ReadFile: read})
	}
	for host, want := range map[string]string{"": PostHogUS, "us": PostHogUS, "US": PostHogUS, "eu": PostHogEU, "https://ph.example.org": "https://ph.example.org",
		"https://ph.example.org/": "https://ph.example.org", "https://ph.example.org:8443/base/": "https://ph.example.org:8443/base", "http://localhost:8000": "http://localhost:8000",
		"http://127.0.0.1:8000": "http://127.0.0.1:8000"} {
		c, err := cfg("GOJIBERRY_OBS_POSTHOG_HOST", host, "GOJIBERRY_OBS_POSTHOG_KEY", "phc_k")
		if err != nil || c.Host != want {
			t.Errorf("host %q: %q, %v (want %q)", host, c.Host, err, want)
		}
	}
	for _, host := range []string{"http://ph.example.org", "ftp://x", "https://user:pw@ph.example.org", "https://ph.example.org?x=1", "https://ph.example.org#f", "ph.example.org", "https://", "eu-west"} {
		_, err := cfg("GOJIBERRY_OBS_POSTHOG_HOST", host, "GOJIBERRY_OBS_POSTHOG_KEY", "phc_k")
		if err == nil || !strings.Contains(err.Error(), "GOJIBERRY_OBS_POSTHOG_HOST") || strings.Contains(err.Error(), host) && host != "" {
			t.Errorf("host %q: err = %v", host, err)
		}
	}
	// the key: value, file, both, none, malformed; no message holds it
	if c, err := cfg("GOJIBERRY_OBS_POSTHOG_KEY_FILE", "/run/secrets/key"); err != nil || c.Key != "phc_FROM_FILE" {
		t.Errorf("file key: %q %v", c.Key, err)
	}
	for _, kv := range [][]string{
		{"GOJIBERRY_OBS_POSTHOG_KEY", "phc_secret", "GOJIBERRY_OBS_POSTHOG_KEY_FILE", "/run/secrets/key"},
		{},
		{"GOJIBERRY_OBS_POSTHOG_KEY", "phc secret with spaces"},
		{"GOJIBERRY_OBS_POSTHOG_KEY_FILE", "/nonexistent"},
		{"GOJIBERRY_OBS_POSTHOG_KEY", "phc_k", "GOJIBERRY_OBS_POSTHOG_ID", "has space"},
		{"GOJIBERRY_OBS_POSTHOG_KEY", "phc_k", "GOJIBERRY_OBS_POSTHOG_INTERVAL", "1s"},
		{"GOJIBERRY_OBS_POSTHOG_KEY", "phc_k", "GOJIBERRY_OBS_POSTHOG_INTERVAL", "soon"},
	} {
		_, err := cfg(kv...)
		if err == nil || !strings.Contains(err.Error(), "GOJIBERRY_OBS_POSTHOG_") {
			t.Errorf("%v: err = %v", kv, err)
			continue
		}
		for _, leak := range []string{"phc_secret", "phc secret", "has space", "soon", "nonexistent"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%v: the error repeats %q: %v", kv, leak, err)
			}
		}
	}
	c, err := cfg("GOJIBERRY_OBS_POSTHOG_KEY", "phc_k")
	if err != nil || c.DistinctID != DefaultPostHogID || c.Interval != DefaultPostHogInterval {
		t.Errorf("defaults: %+v %v", c, err)
	}
	c, err = cfg("GOJIBERRY_OBS_POSTHOG_KEY", "phc_k", "GOJIBERRY_OBS_POSTHOG_ID", "my-install_2", "GOJIBERRY_OBS_POSTHOG_INTERVAL", "30s")
	if err != nil || c.DistinctID != "my-install_2" || c.Interval != 30*time.Second {
		t.Errorf("custom: %+v %v", c, err)
	}
}

func TestPostHogDescribeHoldsNoKeyAndShowsTheLastShape(t *testing.T) {
	c := &capture{}
	p := newPostHogFor(t, c)
	p.Metric(Point{Metric: Errors, Kind: Counter, Value: 1, Labels: []Label{{"code", "panic"}}, Source: Operator})
	if err := p.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	o := New(Options{Operator: p})
	o.cfg = &Config{Enabled: true, Sinks: []string{"posthog"}}
	o.named = map[string]Sink{"posthog": p}
	b, _ := json.Marshal(o.Describe())
	s := string(b)
	if strings.Contains(s, "phc_TESTKEY") {
		t.Fatalf("the key is in the description: %s", s)
	}
	for _, want := range []string{`"distinct_id":"install-1"`, `"last_flush"`, `redacted`, `$process_person_profile`, `$geoip_disable`, `"status":"ok"`} {
		if !strings.Contains(s, want) {
			t.Errorf("description lacks %s: %s", want, s)
		}
	}
	// before anything is enabled the example shows the shape
	ex, _ := json.Marshal(ExamplePostHogPayload())
	if !strings.Contains(string(ex), `"$geoip_disable":true`) || strings.Contains(string(ex), "phc_") {
		t.Fatalf("example = %s", ex)
	}
	var names []string
	for _, e := range ExamplePostHogPayload()["batch"].([]postHogEvent) {
		names = append(names, e.Event)
	}
	sort.Strings(names)
	if len(names) != 2 {
		t.Fatalf("example events = %v", names)
	}
}
