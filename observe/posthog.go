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
	"strings"
	"sync"
	"time"
)

// The PostHog sink. It needs nothing but the standard library: PostHog's capture API is one JSON POST.
//
// What it sends is aggregate events about the server, never about a person:
//
//   - "gojiberry_metric": one per series per flush, holding the label values, the count, the sum, the
//     smallest, the largest and the last value of the interval.
//   - "gojiberry_span": one per kept (sampled) span, with its name, duration, attributes and the random
//     trace and span ids.
//
// Every event carries "$process_person_profile": false (no person is created) and "$geoip_disable": true
// (no location is looked up), and one fixed distinct id that names the service, not a person. No address,
// header, query or body ever reaches a Sink, so none can be sent.
const (
	envPostHogKey      = "GOJIBERRY_OBS_POSTHOG_KEY"
	envPostHogKeyFile  = "GOJIBERRY_OBS_POSTHOG_KEY_FILE"
	envPostHogHost     = "GOJIBERRY_OBS_POSTHOG_HOST"
	envPostHogInterval = "GOJIBERRY_OBS_POSTHOG_INTERVAL"

	// PostHogUSHost and PostHogEUHost are PostHog Cloud's ingestion hosts.
	PostHogUSHost = "https://us.i.posthog.com"
	PostHogEUHost = "https://eu.i.posthog.com"

	posthogMaxSpans  = 1000
	posthogBatchSize = 200
)

func init() { RegisterSink("posthog", newPostHogFromEnv) }

// PostHogConfig configures the PostHog sink.
type PostHogConfig struct {
	// APIKey is the project API key (the "phc_..." one; it is write-only but still not logged).
	APIKey string
	// Host is the ingestion base URL: PostHogUSHost, PostHogEUHost or a self-hosted instance.
	Host string
	// Service is the distinct id of every event (the name of the server, not of a person).
	Service string
	// Interval is how often the aggregates are sent. Zero is 30 seconds; negative disables the timer
	// (the caller flushes).
	Interval time.Duration
	// Client is used for the requests; nil is a client with a 10 second timeout.
	Client *http.Client
}

// PostHog is a Sink that sends aggregates and sampled spans to PostHog.
type PostHog struct {
	cfg    PostHogConfig
	endpt  string
	mu     sync.Mutex
	series map[string]*agg
	spans  []SpanData
	lost   int64
	sent   int64
	failed int64
	last   time.Time
	stop   chan struct{}
	done   chan struct{}
}

// NewPostHog builds the sink and starts its sender.
func NewPostHog(cfg PostHogConfig) (*PostHog, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("observe: the PostHog sink needs a project key (" + envPostHogKey + " or " + envPostHogKeyFile + ")")
	}
	host, err := posthogHost(cfg.Host)
	if err != nil {
		return nil, err
	}
	cfg.Host = host
	if cfg.Service == "" {
		cfg.Service = "gojiberry"
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Interval == 0 {
		cfg.Interval = 30 * time.Second
	}
	p := &PostHog{cfg: cfg, endpt: host + "/batch/", series: map[string]*agg{}, stop: make(chan struct{}), done: make(chan struct{})}
	if cfg.Interval > 0 {
		go p.loop()
	} else {
		close(p.done)
	}
	return p, nil
}

// posthogHost resolves "us", "eu" (or empty: us) and a URL. A plain http URL is only accepted for a loopback
// host (a local PostHog for trying it out); anything else must be https so the project key is not sent in
// the clear.
func posthogHost(h string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(h)) {
	case "", "us":
		return PostHogUSHost, nil
	case "eu":
		return PostHogEUHost, nil
	}
	u, err := url.Parse(strings.TrimSpace(h))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("observe: %s must be us, eu or the base URL of a PostHog instance", envPostHogHost)
	}
	loop := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && (u.Scheme != "http" || !loop) {
		return "", fmt.Errorf("observe: %s must be an https URL (http only for localhost)", envPostHogHost)
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"), nil
}

func newPostHogFromEnv(_ context.Context, env SinkEnv) (Sink, error) {
	key := strings.TrimSpace(env.Getenv(envPostHogKey))
	if f := env.Getenv(envPostHogKeyFile); f != "" {
		if key != "" {
			return nil, fmt.Errorf("observe: set only one of %s and %s", envPostHogKey, envPostHogKeyFile)
		}
		b, err := env.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("observe: %s names a file that cannot be read", envPostHogKeyFile)
		}
		key = strings.TrimSpace(string(b))
	}
	cfg := PostHogConfig{APIKey: key, Host: env.Getenv(envPostHogHost), Service: env.Service}
	if v := env.Getenv(envPostHogInterval); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Second {
			return nil, fmt.Errorf("observe: %s must be a duration of at least 1s, such as 30s", envPostHogInterval)
		}
		cfg.Interval = d
	}
	return NewPostHog(cfg)
}

// Name implements Sink.
func (*PostHog) Name() string { return "posthog" }

// Metric implements Sink: it only aggregates in memory.
func (p *PostHog) Metric(pt Point) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k := seriesKey(pt)
	a := p.series[k]
	if a == nil {
		if len(p.series) >= maxSeries {
			p.lost++
			return
		}
		a = newAgg(pt)
		p.series[k] = a
	}
	a.add(pt.Value)
}

// Span implements Sink: it keeps a bounded buffer, dropping the oldest.
func (p *PostHog) Span(s SpanData) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.spans) >= posthogMaxSpans {
		p.spans = p.spans[1:]
		p.lost++
	}
	p.spans = append(p.spans, s)
}

func (p *PostHog) loop() {
	defer close(p.done)
	t := time.NewTicker(p.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_ = p.Flush(ctx) // a failure is counted and shows in Describe
			cancel()
		case <-p.stop:
			return
		}
	}
}

type phEvent struct {
	Event      string         `json:"event"`
	DistinctID string         `json:"distinct_id"`
	Timestamp  string         `json:"timestamp"`
	Properties map[string]any `json:"properties"`
}

func (p *PostHog) props(extra map[string]any) map[string]any {
	m := map[string]any{"$process_person_profile": false, "$geoip_disable": true, "service": p.cfg.Service, "$lib": "gojiberry-observe"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// held is what one flush takes: the aggregates and spans, and the events made from them (the series first, in
// key order, then the spans), so that what a failed request did not carry can be put back.
type held struct {
	keys   []string
	series map[string]*agg
	spans  []SpanData
	events []phEvent
}

// events takes what is held and turns it into capture events.
func (p *PostHog) events(now time.Time) held {
	p.mu.Lock()
	series, spans := p.series, p.spans
	p.series, p.spans = map[string]*agg{}, nil
	p.mu.Unlock()
	ts := now.UTC().Format(time.RFC3339Nano)
	keys := make([]string, 0, len(series))
	for k := range series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []phEvent
	for _, k := range keys {
		a := series[k]
		pr := map[string]any{"metric": string(a.metric), "kind": a.kind.String(), "source": a.source.String(), "count": a.count, "sum": a.sum, "min": a.min, "max": a.max, "last": a.last}
		for _, l := range a.labels {
			pr["label_"+l.Name] = l.Value
		}
		out = append(out, phEvent{Event: "gojiberry_metric", DistinctID: p.cfg.Service, Timestamp: ts, Properties: p.props(pr)})
	}
	for _, s := range spans {
		pr := map[string]any{"name": string(s.Name), "duration_ms": float64(s.End.Sub(s.Start).Microseconds()) / 1000, "trace_id": hex.EncodeToString(s.TraceID[:]), "span_id": hex.EncodeToString(s.ID[:])}
		if s.Parent != ([8]byte{}) {
			pr["parent_span_id"] = hex.EncodeToString(s.Parent[:])
		}
		for _, a := range s.Attrs {
			if a.IsNum {
				pr["attr_"+a.Key] = a.Num
			} else {
				pr["attr_"+a.Key] = a.Str
			}
		}
		out = append(out, phEvent{Event: "gojiberry_span", DistinctID: p.cfg.Service, Timestamp: s.End.UTC().Format(time.RFC3339Nano), Properties: p.props(pr)})
	}
	return held{keys: keys, series: series, spans: spans, events: out}
}

// putBack returns what a request did not carry to the sink, to be sent with the next interval: the aggregates are
// added to the ones that have grown since (counts and sums add, the newer "last" stays), the spans go before the
// newer ones, and both stay inside the bounds (maxSeries, posthogMaxSpans; what does not fit is counted as lost).
func (p *PostHog) putBack(h held, from int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, k := range h.keys {
		if i < from {
			continue
		}
		old := h.series[k]
		if cur := p.series[k]; cur != nil {
			cur.count += old.count
			cur.sum += old.sum
			cur.min, cur.max = min(cur.min, old.min), max(cur.max, old.max)
			continue
		}
		if len(p.series) >= maxSeries {
			p.lost++
			continue
		}
		p.series[k] = old
	}
	if sp := max(from-len(h.keys), 0); sp < len(h.spans) {
		all := append(append([]SpanData(nil), h.spans[sp:]...), p.spans...)
		if over := len(all) - posthogMaxSpans; over > 0 {
			all = all[over:]
			p.lost += int64(over)
		}
		p.spans = all
	}
}

// phStatusError is an answer that was not a success; a 429 or a 5xx is worth trying again at the next interval, any
// other client error (a wrong key, a refused payload) is not.
type phStatusError struct{ code int }

func (e phStatusError) Error() string { return fmt.Sprintf("observe: posthog: answered %d", e.code) }
func (e phStatusError) transient() bool {
	return e.code == http.StatusTooManyRequests || e.code >= 500
}

// phNetError is a request that did not get an answer; trying again later may work.
type phNetError struct{ msg string }

func (e phNetError) Error() string { return e.msg }

// Flush implements Sink: it sends what is held now, in batches. A request that fails for a reason that may pass (no
// answer, 429, 5xx) stops the flush and what was not sent is kept for the next interval, inside the bounds; one the
// server refused for good (any other 4xx: a wrong key, a payload it will not take) drops that batch and the rest,
// counted, so a wrong key cannot fill the memory.
func (p *PostHog) Flush(ctx context.Context) error {
	h := p.events(time.Now())
	var errs []error
	for off := 0; off < len(h.events); {
		n := min(len(h.events)-off, posthogBatchSize)
		err := p.post(ctx, h.events[off:off+n])
		p.mu.Lock()
		if err != nil {
			p.failed += int64(n)
		} else {
			p.sent += int64(n)
			p.last = time.Now()
		}
		p.mu.Unlock()
		if err == nil {
			off += n
			continue
		}
		errs = append(errs, err)
		var se phStatusError
		permanent := errors.As(err, &se) && !se.transient()
		if permanent {
			p.mu.Lock()
			p.lost += int64(len(h.events) - off - n) // the rest is not sent either
			p.mu.Unlock()
		} else {
			p.putBack(h, off) // this batch and the rest: the next interval carries them
		}
		break
	}
	return errors.Join(errs...)
}

func (p *PostHog) post(ctx context.Context, evs []phEvent) error {
	body, err := json.Marshal(map[string]any{"api_key": p.cfg.APIKey, "batch": evs})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpt, bytes.NewReader(body))
	if err != nil {
		return errors.New("observe: posthog: bad request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.cfg.Client.Do(req)
	if err != nil {
		// The error of net/http repeats the URL, which holds no secret (the key is in the body), but name only the cause.
		return phNetError{"observe: posthog: the request failed"}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return phStatusError{resp.StatusCode}
	}
	return nil
}

// Close implements Sink: it stops the timer and sends the rest.
func (p *PostHog) Close(ctx context.Context) error {
	select {
	case <-p.stop:
	default:
		close(p.stop)
	}
	<-p.done
	return p.Flush(ctx)
}

// Describe implements Describer: the destination and the counts, never the key.
func (p *PostHog) Describe() any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return map[string]any{
		"host": p.cfg.Host, "interval_seconds": p.cfg.Interval.Seconds(), "held_series": len(p.series), "held_spans": len(p.spans),
		"events_sent": p.sent, "events_failed": p.failed, "dropped": p.lost, "person_profiles": false, "geoip": false,
	}
}
