package observe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The PostHog adapter is plain net/http against PostHog's capture API (POST <host>/batch/), no SDK.
// It sends aggregates: every metric series is summed for one interval and leaves as ONE event, with
// the labels as properties. Every event carries "$process_person_profile": false (PostHog creates no
// person) and "$geoip_disable": true, one fixed distinct id for the whole deployment (the operator
// sees it in GET /admin/observe and changes it with GOJIBERRY_OBS_POSTHOG_ID), and no address, header
// or identifier of anyone. Spans are not sent: PostHog is for aggregate events; traces go to OpenTelemetry.
//
//	GOJIBERRY_OBS_POSTHOG_HOST          us (default) | eu | the base URL of a self-hosted PostHog
//	GOJIBERRY_OBS_POSTHOG_KEY[_FILE]    the project API key (phc_...); required
//	GOJIBERRY_OBS_POSTHOG_ID            the fixed distinct id (default gojiberry-docstore)
//	GOJIBERRY_OBS_POSTHOG_INTERVAL      how often aggregates are sent, 10s to 1h (default 1m)
const (
	envPostHogHost     = "GOJIBERRY_OBS_POSTHOG_HOST"
	envPostHogKey      = "GOJIBERRY_OBS_POSTHOG_KEY"
	envPostHogID       = "GOJIBERRY_OBS_POSTHOG_ID"
	envPostHogInterval = "GOJIBERRY_OBS_POSTHOG_INTERVAL"

	// PostHogUS and PostHogEU are the PostHog cloud ingestion hosts.
	PostHogUS = "https://us.i.posthog.com"
	PostHogEU = "https://eu.i.posthog.com"
	// DefaultPostHogID is the distinct id when the operator sets none: one constant for the deployment.
	DefaultPostHogID = "gojiberry-docstore"
	// DefaultPostHogInterval is how often aggregates are sent.
	DefaultPostHogInterval = time.Minute

	maxEventsPerRequest = 100
	posthogTimeout      = 10 * time.Second
	posthogUserAgent    = "gojiberry-observe/1"
)

func init() { RegisterSink("posthog", newPostHogFromEnv) }

// PostHogConfig is the parsed configuration of the PostHog sink.
type PostHogConfig struct {
	// Host is the base URL, without a trailing slash.
	Host string
	// Key is the project API key. It is a secret: it is never logged, described or put in an error.
	Key        string
	DistinctID string
	Interval   time.Duration
}

var distinctIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// PostHogConfigFromEnv reads and validates the GOJIBERRY_OBS_POSTHOG_* variables. Errors name the
// variable and never repeat a value.
func PostHogConfigFromEnv(env SinkEnv) (PostHogConfig, error) {
	get := func(n string) string { return strings.TrimSpace(env.Getenv(n)) }
	var c PostHogConfig
	var err error
	if c.Host, err = resolvePostHogHost(get(envPostHogHost)); err != nil {
		return c, err
	}
	if c.Key, err = ReadSecret(env, envPostHogKey); err != nil {
		return c, err
	}
	if c.Key == "" {
		return c, fmt.Errorf("observe: the posthog sink needs %s or %s_FILE (the project API key)", envPostHogKey, envPostHogKey)
	}
	if len(c.Key) > 200 || strings.ContainsFunc(c.Key, func(r rune) bool { return r <= ' ' || r == 0x7f || r > 0x7e }) {
		return c, fmt.Errorf("observe: %s is not an API key (no spaces or control characters, at most 200 characters)", envPostHogKey)
	}
	c.DistinctID = DefaultPostHogID
	if v := get(envPostHogID); v != "" {
		if !distinctIDPattern.MatchString(v) {
			return c, fmt.Errorf("observe: %s must be 1 to 64 letters, digits, dots, dashes or underscores", envPostHogID)
		}
		c.DistinctID = v
	}
	c.Interval = DefaultPostHogInterval
	if v := get(envPostHogInterval); v != "" {
		d, perr := time.ParseDuration(v)
		if perr != nil || d < 10*time.Second || d > time.Hour {
			return c, fmt.Errorf("observe: %s must be a duration from 10s to 1h, such as 1m", envPostHogInterval)
		}
		c.Interval = d
	}
	return c, nil
}

// resolvePostHogHost turns us, eu or a URL into a base URL. A URL must be https (http is allowed for
// a loopback host, for a PostHog on the same machine), with no credentials, query or fragment.
func resolvePostHogHost(raw string) (string, error) {
	switch strings.ToLower(raw) {
	case "", "us":
		return PostHogUS, nil
	case "eu":
		return PostHogEU, nil
	}
	bad := fmt.Errorf("observe: %s must be us, eu or the https base URL of a self-hosted PostHog", envPostHogHost)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", bad
	}
	host := u.Hostname()
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", bad
	}
	return u.Scheme + "://" + u.Host + strings.TrimRight(u.Path, "/"), nil
}

func newPostHogFromEnv(_ context.Context, env SinkEnv) (Sink, error) {
	cfg, err := PostHogConfigFromEnv(env)
	if err != nil {
		return nil, err
	}
	p := NewPostHog(cfg, nil)
	p.Start()
	return p, nil
}

type postHogEvent struct {
	Event      string         `json:"event"`
	DistinctID string         `json:"distinct_id"`
	Properties map[string]any `json:"properties"`
	Timestamp  string         `json:"timestamp"`
}

type postHogBatch struct {
	APIKey string         `json:"api_key"`
	Batch  []postHogEvent `json:"batch"`
}

// flushReport is what Describe shows of the last flush: the shape, with the key redacted.
type flushReport struct {
	At         string         `json:"at"`
	Status     string         `json:"status"`
	HTTPStatus int            `json:"http_status,omitempty"`
	Events     int            `json:"events"`
	Sample     map[string]any `json:"payload_shape,omitempty"`
}

// PostHog is the sink for PostHog. Create it with NewPostHog, call Start for the periodic flush.
type PostHog struct {
	cfg    PostHogConfig
	client *http.Client
	now    func() time.Time
	target string

	mu       sync.Mutex
	series   map[string]*agg
	overflow int64
	last     *flushReport

	flushMu sync.Mutex // one flush at a time
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
	running atomic.Bool
}

// NewPostHog is a PostHog sink; a nil client is a plain http.Client with a timeout that does not
// follow redirects (the key travels in the body).
func NewPostHog(cfg PostHogConfig, client *http.Client) *PostHog {
	if client == nil {
		client = &http.Client{Timeout: posthogTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if cfg.DistinctID == "" {
		cfg.DistinctID = DefaultPostHogID
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultPostHogInterval
	}
	return &PostHog{cfg: cfg, client: client, now: time.Now, target: strings.TrimRight(cfg.Host, "/") + "/batch/",
		series: map[string]*agg{}, stop: make(chan struct{}), done: make(chan struct{})}
}

// Name implements Sink.
func (*PostHog) Name() string { return "posthog" }

// Metric implements Sink: it adds the point to its series; nothing is sent here.
func (p *PostHog) Metric(pt Point) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k := seriesKey(pt)
	a := p.series[k]
	if a == nil {
		if len(p.series) >= maxSeries {
			p.overflow++
			return
		}
		a = newAgg(pt)
		p.series[k] = a
	}
	if pt.Kind == Gauge {
		a.last = pt.Value
		a.count = 1
		return
	}
	a.add(pt.Value)
}

// Span implements Sink: PostHog gets aggregate events only, so a span is ignored.
func (*PostHog) Span(SpanData) {}

// Start begins the periodic flush. Close stops it.
func (p *PostHog) Start() {
	if !p.running.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer close(p.done)
		t := time.NewTicker(p.cfg.Interval)
		defer t.Stop()
		for {
			select {
			case <-p.stop:
				return
			case <-t.C:
				Default().Set(QueueSize, float64(p.buffered()), "queue", "posthog_buffer")
				ctx, cancel := context.WithTimeout(context.Background(), 2*posthogTimeout)
				if err := p.Flush(ctx); err != nil {
					Default().Error("sink_failed")
					log.Printf("observe: %v", err)
				}
				cancel()
			}
		}
	}()
}

func (p *PostHog) buffered() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.series)
}

// Close stops the periodic flush and sends what is left.
func (p *PostHog) Close(ctx context.Context) error {
	p.once.Do(func() { close(p.stop) })
	if p.running.Load() {
		select {
		case <-p.done:
		case <-ctx.Done():
		}
	}
	return p.Flush(ctx)
}

// event makes the PostHog event of one series.
func (p *PostHog) event(a *agg, at string) postHogEvent {
	props := map[string]any{
		"source":                  a.source.String(),
		"kind":                    a.kind.String(),
		"interval_seconds":        int(p.cfg.Interval / time.Second),
		"$process_person_profile": false,
		"$geoip_disable":          true,
		"$lib":                    "gojiberry-observe",
	}
	for _, l := range a.labels {
		props[l.Name] = l.Value
	}
	switch a.kind {
	case Counter:
		props["value"] = a.sum
	case Gauge:
		props["value"] = a.last
	case Histogram:
		props["count"], props["sum"], props["min"], props["max"] = a.count, a.sum, a.min, a.max
	}
	return postHogEvent{Event: "gojiberry." + string(a.metric), DistinctID: p.cfg.DistinctID, Properties: props, Timestamp: at}
}

// Flush sends the aggregates held now, as one event per series, in requests of at most 100 events. A
// request that fails for a reason that can pass (a network error, 429, 5xx) puts its series back to be
// sent with the next flush; a refusal that cannot pass (another 4xx, a bad key) drops them. It
// returns an error that never contains the API key.
func (p *PostHog) Flush(ctx context.Context) error {
	p.flushMu.Lock()
	defer p.flushMu.Unlock()
	p.mu.Lock()
	held := p.series
	p.series = map[string]*agg{}
	p.mu.Unlock()
	if len(held) == 0 {
		return nil
	}
	keys := make([]string, 0, len(held))
	for k := range held {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	at := p.now().UTC().Format(time.RFC3339)
	var firstErr error
	for start := 0; start < len(keys); start += maxEventsPerRequest {
		end := min(start+maxEventsPerRequest, len(keys))
		batch := postHogBatch{APIKey: p.cfg.Key}
		for _, k := range keys[start:end] {
			batch.Batch = append(batch.Batch, p.event(held[k], at))
		}
		status, retry, err := p.post(ctx, batch)
		p.remember(batch, status, err)
		if err == nil {
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
		if retry {
			p.putBack(held, keys[start:end])
		}
	}
	return firstErr
}

// putBack merges series that were not sent into what has been collected since.
func (p *PostHog) putBack(held map[string]*agg, keys []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, k := range keys {
		old := held[k]
		if cur := p.series[k]; cur != nil {
			cur.merge(old)
			continue
		}
		if len(p.series) >= maxSeries {
			p.overflow++
			continue
		}
		p.series[k] = old
	}
}

func (p *PostHog) post(ctx context.Context, b postHogBatch) (status int, retry bool, err error) {
	body, err := json.Marshal(b)
	if err != nil {
		return 0, false, errors.New("posthog: the batch could not be encoded")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.target, bytes.NewReader(body))
	if err != nil {
		return 0, false, errors.New("posthog: the capture URL is not valid")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", posthogUserAgent)
	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, true, ctx.Err()
		}
		// the text of a transport error can name the host; the operator already knows it, but it
		// is not repeated here so that nothing configured is echoed
		return 0, true, errors.New("posthog: the capture request failed (network)")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, false, nil
	}
	retry = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500
	return resp.StatusCode, retry, fmt.Errorf("posthog: the capture request was refused with status %d", resp.StatusCode)
}

func (p *PostHog) remember(b postHogBatch, status int, err error) {
	r := &flushReport{At: p.now().UTC().Format(time.RFC3339), Status: "ok", HTTPStatus: status, Events: len(b.Batch)}
	if err != nil {
		r.Status = "failed"
	}
	r.Sample = shapeOf(b)
	p.mu.Lock()
	p.last = r
	p.mu.Unlock()
}

// shapeOf is a batch with the key redacted and at most three events: the shape of a request.
func shapeOf(b postHogBatch) map[string]any {
	events := b.Batch
	if len(events) > 3 {
		events = events[:3]
	}
	return map[string]any{"api_key": "<redacted>", "batch": events}
}

// Describe implements Describer: the destination, the fixed id and the shape of the last request. It
// holds no key.
func (p *PostHog) Describe() any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]any{
		"host":             p.cfg.Host,
		"distinct_id":      p.cfg.DistinctID,
		"interval_seconds": int(p.cfg.Interval / time.Second),
		"buffered_series":  len(p.series),
		"series_overflow":  p.overflow,
		"sends":            "one event per metric series per interval, with $process_person_profile false and $geoip_disable true; spans are not sent",
	}
	if p.last != nil {
		out["last_flush"] = p.last
	}
	return out
}

// ExamplePostHogPayload is the shape of what the PostHog sink sends, with made-up numbers, for the
// admin route to show before anything is enabled.
func ExamplePostHogPayload() map[string]any {
	p := &PostHog{cfg: PostHogConfig{DistinctID: DefaultPostHogID, Interval: DefaultPostHogInterval}}
	req := &agg{metric: HTTPRequests, kind: Counter, source: Operator, count: 3, sum: 42, labels: []Label{{"route", "/docs/{id}"}, {"method", "GET"}, {"status_class", "2xx"}}}
	dur := &agg{metric: HTTPDuration, kind: Histogram, source: Operator, count: 42, sum: 1.3, min: 0.002, max: 0.21, labels: []Label{{"route", "/docs/{id}"}, {"method", "GET"}}}
	at := "2030-01-01T00:00:00Z"
	return shapeOf(postHogBatch{Batch: []postHogEvent{p.event(req, at), p.event(dur, at)}})
}

// merge adds the aggregate b (a flush that failed, put back) into a.
func (a *agg) merge(b *agg) {
	if b.count == 0 {
		return
	}
	if a.count == 0 {
		// nothing newer: b's gauge value stands; otherwise a's newer value wins
		a.last = b.last
	}
	if a.count == 0 || b.min < a.min {
		a.min = b.min
	}
	if a.count == 0 || b.max > a.max {
		a.max = b.max
	}
	a.count += b.count
	a.sum += b.sum
}
