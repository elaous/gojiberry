package observe

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The OpenTelemetry sink.
const (
	envOTLPEndpoint        = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOTLPMetricsEndpoint = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	envOTLPTracesEndpoint  = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	envOTLPHeaders         = "OTEL_EXPORTER_OTLP_HEADERS"
	envOTLPInterval        = "GOJIBERRY_OBS_OTLP_INTERVAL"

	otlpMaxSpans  = 1000
	otlpBatchSize = 500
)

func init() { RegisterSink("otlp", newOTLPFromEnv) }

// OTLPConfig configures the OTLP sink.
type OTLPConfig struct {
	// MetricsURL and TracesURL are the full URLs ({endpoint}/v1/metrics and /v1/traces).
	MetricsURL, TracesURL string
	Headers               map[string]string
	// Service is the service.name resource attribute.
	Service string
	// Interval is how often aggregates are sent.
	Interval time.Duration
	Client   *http.Client
}

type OTLP struct {
	cfg    OTLPConfig
	mu     sync.Mutex
	series map[string]*agg
	since  time.Time
	spans  []SpanData
	lost   int64
	sent   int64
	failed int64
	stop   chan struct{}
	done   chan struct{}
}

// NewOTLP builds the sink and starts its sender.
func NewOTLP(cfg OTLPConfig) (*OTLP, error) {
	for _, u := range []string{cfg.MetricsURL, cfg.TracesURL} {
		if err := checkOTLPURL(u); err != nil {
			return nil, err
		}
	}
	if cfg.Service == "" {
		cfg.Service = "gojiberry"
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Interval == 0 {
		cfg.Interval = 30 * time.Second
	}
	o := &OTLP{cfg: cfg, series: map[string]*agg{}, since: time.Now(), stop: make(chan struct{}), done: make(chan struct{})}
	if cfg.Interval > 0 {
		go o.loop()
	} else {
		close(o.done)
	}
	return o, nil
}

func checkOTLPURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("observe: %s must be the base URL of an OTLP/HTTP endpoint", envOTLPEndpoint)
	}
	loop := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && (u.Scheme != "http" || !loop) {
		return fmt.Errorf("observe: %s must be an https URL (http only for localhost)", envOTLPEndpoint)
	}
	return nil
}

func parseOTLPHeaders(s string) (map[string]string, error) {
	h := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" || strings.ContainsAny(k+v, "\r\n") {
			return nil, fmt.Errorf("observe: %s must be comma separated name=value pairs", envOTLPHeaders)
		}
		if dv, err := url.PathUnescape(strings.TrimSpace(v)); err == nil {
			v = dv
		}
		h[k] = strings.TrimSpace(v)
	}
	return h, nil
}

func newOTLPFromEnv(_ context.Context, env SinkEnv) (Sink, error) {
	base := strings.TrimRight(strings.TrimSpace(env.Getenv(envOTLPEndpoint)), "/")
	cfg := OTLPConfig{Service: env.Service, MetricsURL: env.Getenv(envOTLPMetricsEndpoint), TracesURL: env.Getenv(envOTLPTracesEndpoint)}
	if base == "" && (cfg.MetricsURL == "" || cfg.TracesURL == "") {
		return nil, errors.New("observe: the otlp sink needs " + envOTLPEndpoint + " (or both " + envOTLPMetricsEndpoint + " and " + envOTLPTracesEndpoint + ")")
	}
	if cfg.MetricsURL == "" {
		cfg.MetricsURL = base + "/v1/metrics"
	}
	if cfg.TracesURL == "" {
		cfg.TracesURL = base + "/v1/traces"
	}
	var err error
	if cfg.Headers, err = parseOTLPHeaders(env.Getenv(envOTLPHeaders)); err != nil {
		return nil, err
	}
	if v := env.Getenv(envOTLPInterval); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Second {
			return nil, fmt.Errorf("observe: %s must be a duration of at least 1s, such as 30s", envOTLPInterval)
		}
		cfg.Interval = d
	}
	return NewOTLP(cfg)
}

// Name implements Sink.
func (*OTLP) Name() string { return "otlp" }

// Metric implements Sink: it only aggregates in memory.
func (o *OTLP) Metric(p Point) {
	o.mu.Lock()
	defer o.mu.Unlock()
	k := seriesKey(p)
	a := o.series[k]
	if a == nil {
		if len(o.series) >= maxSeries {
			o.lost++
			return
		}
		a = newAgg(p)
		o.series[k] = a
	}
	a.add(p.Value)
}

// Span implements Sink: a bounded buffer that drops the oldest.
func (o *OTLP) Span(s SpanData) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.spans) >= otlpMaxSpans {
		o.spans = o.spans[1:]
		o.lost++
	}
	o.spans = append(o.spans, s)
}

func (o *OTLP) loop() {
	defer close(o.done)
	t := time.NewTicker(o.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_ = o.Flush(ctx) // a failure is counted and shows in Describe
			cancel()
		case <-o.stop:
			return
		}
	}
}

type otlpValue struct {
	String *string  `json:"stringValue,omitempty"`
	Double *float64 `json:"doubleValue,omitempty"`
}
type otlpAttr struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

func sAttr(k, v string) otlpAttr         { return otlpAttr{Key: k, Value: otlpValue{String: &v}} }
func nAttr(k string, v float64) otlpAttr { return otlpAttr{Key: k, Value: otlpValue{Double: &v}} }

func nano(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }

func (o *OTLP) resource() map[string]any {
	return map[string]any{"attributes": []otlpAttr{sAttr("service.name", o.cfg.Service)}}
}

func (o *OTLP) metricsBody(now time.Time) []byte {
	o.mu.Lock()
	series, start := o.series, o.since
	o.series, o.since = map[string]*agg{}, now
	o.mu.Unlock()
	if len(series) == 0 {
		return nil
	}
	keys := make([]string, 0, len(series))
	for k := range series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	byName := map[string][]map[string]any{}
	kinds := map[string]Kind{}
	var names []string
	for _, k := range keys {
		a := series[k]
		attrs := []otlpAttr{sAttr("gojiberry.source", a.source.String())}
		for _, l := range a.labels {
			attrs = append(attrs, sAttr(l.Name, l.Value))
		}
		dp := map[string]any{"attributes": attrs, "startTimeUnixNano": nano(start), "timeUnixNano": nano(now)}
		switch a.kind {
		case Histogram:
			dp["count"], dp["sum"], dp["min"], dp["max"] = strconv.FormatInt(a.count, 10), a.sum, a.min, a.max
		case Gauge:
			dp["asDouble"] = a.last
		default:
			dp["asDouble"] = a.sum
		}
		n := "gojiberry." + string(a.metric)
		if _, ok := byName[n]; !ok {
			names = append(names, n)
			kinds[n] = a.kind
		}
		byName[n] = append(byName[n], dp)
	}
	var metrics []map[string]any
	for _, n := range names {
		m := map[string]any{"name": n}
		switch kinds[n] {
		case Histogram:
			m["histogram"] = map[string]any{"aggregationTemporality": 1, "dataPoints": byName[n]}
		case Gauge:
			m["gauge"] = map[string]any{"dataPoints": byName[n]}
		default:
			m["sum"] = map[string]any{"aggregationTemporality": 1, "isMonotonic": true, "dataPoints": byName[n]}
		}
		metrics = append(metrics, m)
	}
	b, _ := json.Marshal(map[string]any{"resourceMetrics": []any{map[string]any{
		"resource": o.resource(), "scopeMetrics": []any{map[string]any{"scope": map[string]any{"name": "gojiberry"}, "metrics": metrics}},
	}}})
	return b
}

func spanJSON(s SpanData) map[string]any {
	attrs := []otlpAttr{}
	status := 0
	for _, a := range s.Attrs {
		if a.IsNum {
			attrs = append(attrs, nAttr(a.Key, a.Num))
		} else {
			attrs = append(attrs, sAttr(a.Key, a.Str))
			if a.Key == "result" && a.Str == "error" {
				status = 2
			}
		}
	}
	m := map[string]any{"traceId": hex.EncodeToString(s.TraceID[:]), "spanId": hex.EncodeToString(s.ID[:]), "name": string(s.Name), "kind": 1,
		"startTimeUnixNano": nano(s.Start), "endTimeUnixNano": nano(s.End), "attributes": attrs, "status": map[string]any{"code": status}}
	if s.Parent != ([8]byte{}) {
		m["parentSpanId"] = hex.EncodeToString(s.Parent[:])
	}
	return m
}

func (o *OTLP) tracesBody(spans []SpanData) []byte {
	list := make([]map[string]any, len(spans))
	for i, s := range spans {
		list[i] = spanJSON(s)
	}
	b, _ := json.Marshal(map[string]any{"resourceSpans": []any{map[string]any{
		"resource": o.resource(), "scopeSpans": []any{map[string]any{"scope": map[string]any{"name": "gojiberry"}, "spans": list}},
	}}})
	return b
}

// Flush implements Sink: it sends what is held now.
func (o *OTLP) Flush(ctx context.Context) error {
	var errs []error
	if b := o.metricsBody(time.Now()); b != nil {
		errs = append(errs, o.account(1, o.post(ctx, o.cfg.MetricsURL, b)))
	}
	o.mu.Lock()
	spans := o.spans
	o.spans = nil
	o.mu.Unlock()
	for len(spans) > 0 {
		n := min(len(spans), otlpBatchSize)
		errs = append(errs, o.account(n, o.post(ctx, o.cfg.TracesURL, o.tracesBody(spans[:n]))))
		spans = spans[n:]
	}
	return errors.Join(errs...)
}

func (o *OTLP) account(n int, err error) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err != nil {
		o.failed += int64(n)
	} else {
		o.sent += int64(n)
	}
	return err
}

func (o *OTLP) post(ctx context.Context, target string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return errors.New("observe: otlp: bad request")
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range o.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := o.cfg.Client.Do(req)
	if err != nil {
		return errors.New("observe: otlp: the request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("observe: otlp: answered %d", resp.StatusCode)
	}
	return nil
}

func (o *OTLP) Close(ctx context.Context) error {
	select {
	case <-o.stop:
	default:
		close(o.stop)
	}
	<-o.done
	return o.Flush(ctx)
}

func (o *OTLP) Describe() any {
	o.mu.Lock()
	defer o.mu.Unlock()
	return map[string]any{
		"metrics_url": o.cfg.MetricsURL, "traces_url": o.cfg.TracesURL, "interval_seconds": o.cfg.Interval.Seconds(),
		"held_series": len(o.series), "held_spans": len(o.spans), "sent": o.sent, "failed": o.failed, "dropped": o.lost, "header_names": o.headerNames(),
	}
}

func (o *OTLP) headerNames() []string {
	var n []string
	for k := range o.cfg.Headers {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}
