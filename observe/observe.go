// Package observe is OPERATOR observability for a Gojiberry server: metrics and traces about the
// server's own behaviour (requests per route template, store operation timings, sweep timings,
// sign-in and limiter outcomes, errors by a fixed code), for the person who runs the server. It is
// about the machine, not about people, and it is a different producer from package telemetry (usage
// counts that a browser sends with the person's opt-in); they share the Sink abstraction (sink.go)
// so either can be routed to the built-in admin pages, OpenTelemetry, PostHog, or several.
//
// Nothing here has a place for personal data, by construction:
//
//   - Names are closed. A metric, a label, a span and a span attribute exist only if they are in
//     the tables of this file; anything else is dropped and counted (Dropped), never forwarded.
//   - Values are closed. A label value must be one of the enumerated values of its label, a span
//     attribute is an enumerated value or a number in a fixed range. There is no string parameter
//     that a request could fill: a route is a template ("/docs/{id}"), never a path.
//   - The HTTP middleware reads the method and the path to choose a template and the status the
//     handler wrote. It never reads the remote address, a header, a cookie, the query or the body.
//   - It is off unless the operator turns it on (GOJIBERRY_OBS=on, see config.go); a nil *Observer
//     is valid and does nothing.
//
// Adding a name, a label or a value is a reviewed change to this file and to docs/observability.md,
// which a test keeps in step with it.
package observe

import (
	"context"
	"math"
	"math/rand/v2"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Kind is the kind of a metric.
type Kind uint8

// The kinds of metric.
const (
	// Counter only goes up; a point carries the amount to add.
	Counter Kind = iota + 1
	// Histogram is a distribution of observations (durations, in seconds).
	Histogram
	// Gauge is a value that goes up and down; a point carries its current value.
	Gauge
)

func (k Kind) String() string {
	switch k {
	case Counter:
		return "counter"
	case Histogram:
		return "histogram"
	case Gauge:
		return "gauge"
	}
	return "unknown"
}

// Metric is the name of a metric of the allowlist.
type Metric string

// The metrics of the allowlist.
const (
	HTTPRequests   Metric = "http_requests_total"
	HTTPDuration   Metric = "http_request_duration_seconds"
	HTTPInflight   Metric = "http_inflight_requests"
	AuthOutcomes   Metric = "auth_outcomes_total"
	LimiterResults Metric = "limiter_decisions_total"
	Errors         Metric = "errors_total"
	StoreOps       Metric = "store_ops_total"
	StoreDuration  Metric = "store_op_duration_seconds"
	SweepRuns      Metric = "sweep_runs_total"
	SweepRemoved   Metric = "sweep_removed_total"
	SweepDuration  Metric = "sweep_duration_seconds"
	StoreDocuments Metric = "store_documents"
	StoreRecords   Metric = "store_records"
	StoreBytes     Metric = "store_bytes"
	QueueSize      Metric = "queue_size"
	WorkRuns       Metric = "work_runs_total"
	WorkDuration   Metric = "work_duration_seconds"
	WorkOpen       Metric = "work_open"
	// UsageEvents is the user-usage producer (package telemetry) routed through the same sinks:
	// counts of allowlisted facts, only ever fed after a person opted in at the browser.
	UsageEvents Metric = "usage_events_total"
	// Dropped counts what the allowlist refused, by reason.
	DroppedPoints Metric = "obs_dropped_total"
)

// Def describes one metric: what it is and which labels it has (all of them are required).
type Def struct {
	Name   Metric
	Kind   Kind
	Unit   string
	Labels []string
	Why    string
}

// defs is the whole metric schema, in a stable order.
var defs = []Def{
	{HTTPRequests, Counter, "requests", []string{"route", "method", "status_class"}, "how much each route template is used and how it ends"},
	{HTTPDuration, Histogram, "seconds", []string{"route", "method"}, "which routes are slow"},
	{HTTPInflight, Gauge, "requests", nil, "how many requests are being served now"},
	{AuthOutcomes, Counter, "requests", []string{"flow", "outcome"}, "whether sign-in, passkey, directory and public routes work or are refused"},
	{LimiterResults, Counter, "decisions", []string{"limiter", "outcome"}, "whether rate limits are too tight or being hit"},
	{Errors, Counter, "errors", []string{"code"}, "failures by a fixed code, never by message"},
	{StoreOps, Counter, "operations", []string{"op", "result"}, "how the store behaves"},
	{StoreDuration, Histogram, "seconds", []string{"op"}, "how slow the store is"},
	{SweepRuns, Counter, "runs", []string{"sweep", "result"}, "whether the background sweeps run and succeed"},
	{SweepRemoved, Counter, "items", []string{"sweep"}, "how much each sweep removes"},
	{SweepDuration, Histogram, "seconds", []string{"sweep"}, "how long each sweep takes"},
	{StoreDocuments, Gauge, "documents", nil, "how many documents the store holds (a count, no identifier)"},
	{StoreRecords, Gauge, "records", nil, "how many bookkeeping records the store holds"},
	{StoreBytes, Gauge, "bytes", nil, "how much the store holds in total"},
	{QueueSize, Gauge, "items", []string{"queue"}, "whether a buffer is filling up"},
	{WorkRuns, Counter, "runs", []string{"work", "result"}, "what the other servers do (hub ops and joins, sync merges, font rescans, translations, speech, braille) and how it ends"},
	{WorkDuration, Histogram, "seconds", []string{"work"}, "how long that work takes"},
	{WorkOpen, Gauge, "items", []string{"work"}, "how much of it is open now (hub rooms)"},
	{UsageEvents, Counter, "events", []string{"fact", "choice"}, "user usage counts (package telemetry), after the person opted in"},
	{DroppedPoints, Counter, "points", []string{"reason"}, "what the allowlist refused, so a wrong call is visible"},
}

// labelValues are the allowed values of each label. A label whose values are filled in at run time
// (fact and choice of the usage producer) is listed in usageLabels instead.
var labelValues = map[string][]string{
	"route": {"/health", "/ready", "/quota", "/docs", "/docs/{id}", "/docs/{id}/acl", "/docs/{id}/renew",
		"/auth/{provider}/login", "/auth/{provider}/callback", "/auth/me", "/auth/logout", "/auth/passkey/*", "/auth/other",
		"/directory/*", "/admin/*", "/telemetry", "/me/config", "/public/*",
		// the other servers (collaboration hub, sync relay, font, translation, speech and braille services)
		"/docs/{id}/events", "/docs/{id}/snapshot", "/docs/{id}/ws", "/docs/{id}/ops", "/docs/{id}/presence", "/docs/{id}/role",
		"/ops", "/presence", "/local/*", "/google/*", "/translate", "/speak", "/transcribe", "other"},
	"method":       {"GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS", "other"},
	"status_class": {"1xx", "2xx", "3xx", "4xx", "5xx", "other"},
	"flow":         {"signin", "passkey", "directory", "public", "admin"},
	"outcome":      {"ok", "denied", "limited", "rejected", "error", "allowed"},
	"limiter":      {"directory", "passkey", "login", "auth_miss", "telemetry", "admin", "other"},
	"code":         {"server_error", "panic", "store_failed", "sweep_failed", "sink_failed", "not_ready"},
	"op":           {"load", "save", "save_if", "delete", "list", "renew"},
	"result":       {"ok", "not_found", "conflict", "canceled", "error", "rejected"},
	"work":         {"hub_ops", "hub_join", "hub_rooms", "sync_merge", "font_rescan", "font_file", "font_symbols", "font_download", "google_fonts", "hub_presence", "translate", "speak", "transcribe", "braille"},
	"sweep":        {"expiry", "ratelimit", "directory", "sessions", "passkey_challenges", "public", "usage_sampling"},
	"queue":        {"posthog_buffer"},
	"reason":       {"unknown_metric", "wrong_kind", "unknown_label", "bad_label_value", "bad_value", "unknown_span", "unknown_attribute", "bad_attribute_value"},
}

// usageLabels are the labels whose values the usage producer fills in (from the telemetry
// allowlist, set with SetUsageAllowlist).
var usageLabels = map[string]bool{"fact": true, "choice": true}

// SpanName is the name of a span of the allowlist.
type SpanName string

// The spans of the allowlist.
const (
	SpanHTTP        SpanName = "http.request"
	SpanStoreLoad   SpanName = "store.load"
	SpanStoreSave   SpanName = "store.save"
	SpanStoreDelete SpanName = "store.delete"
	SpanStoreList   SpanName = "store.list"
	SpanStoreRenew  SpanName = "store.renew"
	SpanSweep       SpanName = "sweep.run"
)

// SpanDef describes a span: which attributes it may carry.
type SpanDef struct {
	Name  SpanName
	Attrs []string
	Why   string
}

var spanDefs = []SpanDef{
	{SpanHTTP, []string{"route", "method", "status_class", "status_code", "result"}, "one request, by route template"},
	{SpanStoreLoad, []string{"op", "result"}, "a read from the store"},
	{SpanStoreSave, []string{"op", "result"}, "a write to the store"},
	{SpanStoreDelete, []string{"op", "result"}, "a delete in the store"},
	{SpanStoreList, []string{"op", "result", "count"}, "a listing of the store"},
	{SpanStoreRenew, []string{"op", "result"}, "a deadline renewal"},
	{SpanSweep, []string{"sweep", "result", "count"}, "one run of a background sweep"},
}

// attrDefs are the span attributes: an enumeration (the label of the same name) or a number in a range.
var attrDefs = map[string]struct {
	numeric  bool
	lo, hi   float64
	enumFrom string // the labelValues key
}{
	"route":        {enumFrom: "route"},
	"method":       {enumFrom: "method"},
	"status_class": {enumFrom: "status_class"},
	"op":           {enumFrom: "op"},
	"result":       {enumFrom: "result"},
	"sweep":        {enumFrom: "sweep"},
	"status_code":  {numeric: true, lo: 100, hi: 599},
	"count":        {numeric: true, lo: 0, hi: 1e9},
}

// attrNames is the stable order of the attributes.
var attrNames = []string{"route", "method", "status_class", "op", "result", "sweep", "status_code", "count"}

var (
	defByName    = map[Metric]*Def{}
	labelAllowed = map[string]map[string]bool{}
	spanByName   = map[SpanName]*SpanDef{}
)

func init() {
	for i := range defs {
		defByName[defs[i].Name] = &defs[i]
	}
	for l, vs := range labelValues {
		m := map[string]bool{}
		for _, v := range vs {
			m[v] = true
		}
		labelAllowed[l] = m
	}
	for i := range spanDefs {
		spanByName[spanDefs[i].Name] = &spanDefs[i]
	}
}

// Label is one name and value of a point.
type Label struct{ Name, Value string }

// Source says which producer a point comes from.
type Source uint8

// The producers that share the sinks.
const (
	// Operator points are the server's own behaviour (this package).
	Operator Source = iota + 1
	// Usage points are the browsers' usage counts (package telemetry), opt-in by the person.
	Usage
)

func (s Source) String() string {
	if s == Usage {
		return "usage"
	}
	return "operator"
}

// Point is one validated sample: a counter amount, a histogram observation or a gauge value.
type Point struct {
	Metric Metric
	Kind   Kind
	Value  float64
	// Labels are in the order of the metric's definition, every one present.
	Labels []Label
	Source Source
}

// Attr is a span attribute: an enumerated value (Str) or a number (Num).
type Attr struct {
	Key   string
	Str   string
	Num   float64
	IsNum bool
}

// S is an enumerated attribute (the value must be on the attribute's list).
func S(key, value string) Attr { return Attr{Key: key, Str: value} }

// N is a numeric attribute (the value must be in the attribute's range).
func N(key string, v float64) Attr { return Attr{Key: key, Num: v, IsNum: true} }

// SpanData is a finished span, as the sinks get it. The ids are random per span (not derived from
// anything of the request) and exist to join a child to its parent.
type SpanData struct {
	Name    SpanName
	TraceID [16]byte
	ID      [8]byte
	Parent  [8]byte // zero for a root
	Start   time.Time
	End     time.Time
	Attrs   []Attr
}

// Options of New.
type Options struct {
	// Operator receives the server's own metrics and spans; nil drops them.
	Operator Sink
	// Usage receives the usage counts of package telemetry; nil drops them.
	Usage Sink
	// TraceSample is the share of root spans that are kept (0 to 1; the zero value keeps none, so a
	// caller says 1 for all). A child follows its parent.
	TraceSample float64
	// Now replaces time.Now (tests).
	Now func() time.Time
}

// Observer validates what the server reports and hands it to the sinks. A nil *Observer is valid
// and does nothing, so call sites never check.
type Observer struct {
	operator Sink
	usage    Sink
	sample   float64
	now      func() time.Time

	dropped []atomic.Int64

	mu         sync.Mutex
	usageAllow map[string]map[string]bool

	// cfg and named are set by FromEnv (what the admin route describes).
	cfg   *Config
	named map[string]Sink
}

// New is an Observer over the given sinks.
func New(o Options) *Observer {
	ob := &Observer{operator: o.Operator, usage: o.Usage, sample: o.TraceSample, now: o.Now, dropped: make([]atomic.Int64, len(labelValues["reason"]))}
	if ob.now == nil {
		ob.now = time.Now
	}
	return ob
}

// Enabled reports whether o does anything.
func (o *Observer) Enabled() bool { return o != nil && (o.operator != nil || o.usage != nil) }

var def atomic.Pointer[Observer]

// Default is the process-wide Observer that code without a handle on a Server (a sweep loop) reports
// to; nil until SetDefault. A nil result is valid.
func Default() *Observer { return def.Load() }

// SetDefault sets the process-wide Observer.
func SetDefault(o *Observer) { def.Store(o) }

func reasonIndex(reason string) int {
	for i, r := range labelValues["reason"] {
		if r == reason {
			return i
		}
	}
	return -1
}

// drop counts a refusal. It goes to the operator sink as a point of its own, built without the
// validation that could drop it again.
func (o *Observer) drop(reason string) {
	if i := reasonIndex(reason); i >= 0 {
		o.dropped[i].Add(1)
	}
	if o.operator != nil {
		o.operator.Metric(Point{Metric: DroppedPoints, Kind: Counter, Value: 1, Labels: []Label{{"reason", reason}}, Source: Operator})
	}
}

// Dropped is how many points and attributes the allowlist refused, by reason (reasons never seen are absent).
func (o *Observer) Dropped() map[string]int64 {
	out := map[string]int64{}
	if o == nil {
		return out
	}
	for i, r := range labelValues["reason"] {
		if n := o.dropped[i].Load(); n > 0 {
			out[r] = n
		}
	}
	return out
}

// validate checks a point against the allowlist and returns it with its labels in definition order.
func (o *Observer) validate(m Metric, kind Kind, v float64, kv []string) (Point, bool) {
	d, ok := defByName[m]
	if !ok {
		o.drop("unknown_metric")
		return Point{}, false
	}
	if d.Kind != kind {
		o.drop("wrong_kind")
		return Point{}, false
	}
	if math.IsNaN(v) || math.IsInf(v, 0) || (kind != Gauge && v < 0) {
		o.drop("bad_value")
		return Point{}, false
	}
	if len(kv) != 2*len(d.Labels) {
		o.drop("unknown_label")
		return Point{}, false
	}
	labels := make([]Label, len(d.Labels))
	for i, name := range d.Labels {
		found := false
		for j := 0; j < len(kv); j += 2 {
			if kv[j] != name {
				continue
			}
			found = true
			if !o.labelOK(name, kv[j+1]) {
				o.drop("bad_label_value")
				return Point{}, false
			}
			labels[i] = Label{name, kv[j+1]}
			break
		}
		if !found {
			o.drop("unknown_label")
			return Point{}, false
		}
	}
	return Point{Metric: m, Kind: kind, Value: v, Labels: labels, Source: Operator}, true
}

func (o *Observer) labelOK(name, value string) bool {
	if usageLabels[name] {
		return o.usageLabelOK(name, value)
	}
	return labelAllowed[name][value]
}

func (o *Observer) send(m Metric, kind Kind, v float64, kv []string) {
	if o == nil || o.operator == nil {
		return
	}
	if p, ok := o.validate(m, kind, v, kv); ok {
		o.operator.Metric(p)
	}
}

// Add adds n to a counter. kv are label name and value pairs, e.g. ("route", "/docs", "method", "GET", ...).
func (o *Observer) Add(m Metric, n float64, kv ...string) { o.send(m, Counter, n, kv) }

// Observe records one value of a histogram (seconds for durations).
func (o *Observer) Observe(m Metric, v float64, kv ...string) { o.send(m, Histogram, v, kv) }

// Set sets a gauge.
func (o *Observer) Set(m Metric, v float64, kv ...string) { o.send(m, Gauge, v, kv) }

// Error counts a failure by its fixed code.
func (o *Observer) Error(code string) { o.Add(Errors, 1, "code", code) }

// Span is a span being recorded. A nil *Span is valid (unsampled, or the observer is off).
type Span struct {
	o     *Observer
	data  SpanData
	ended atomic.Bool
	mu    sync.Mutex
}

type spanKey struct{}

type spanCtx struct {
	trace   [16]byte
	id      [8]byte
	sampled bool
}

// Start begins a span named from the allowlist, a child of the span in ctx if there is one. The
// returned context carries it. Attributes outside the allowlist are dropped (and counted).
func (o *Observer) Start(ctx context.Context, name SpanName, attrs ...Attr) (context.Context, *Span) {
	if o == nil || o.operator == nil {
		return ctx, nil
	}
	if _, ok := spanByName[name]; !ok {
		o.drop("unknown_span")
		return ctx, nil
	}
	parent, hasParent := ctx.Value(spanKey{}).(spanCtx)
	sampled := false
	switch {
	case hasParent:
		sampled = parent.sampled
	case o.sample >= 1:
		sampled = true
	case o.sample > 0:
		sampled = rand.Float64() < o.sample
	}
	if !sampled {
		return context.WithValue(ctx, spanKey{}, spanCtx{}), nil
	}
	s := &Span{o: o}
	s.data.Name = name
	s.data.Start = o.now()
	if hasParent {
		s.data.TraceID, s.data.Parent = parent.trace, parent.id
	} else {
		fillRandom(s.data.TraceID[:])
	}
	fillRandom(s.data.ID[:])
	s.Set(attrs...)
	return context.WithValue(ctx, spanKey{}, spanCtx{trace: s.data.TraceID, id: s.data.ID, sampled: true}), s
}

func fillRandom(b []byte) {
	for i := 0; i < len(b); i += 8 {
		v := rand.Uint64()
		for j := 0; j < 8 && i+j < len(b); j++ {
			b[i+j] = byte(v >> (8 * j))
		}
	}
	allZero := true
	for _, x := range b {
		if x != 0 {
			allZero = false
		}
	}
	if allZero {
		b[0] = 1
	}
}

// Set adds attributes. One that is not on the span's list, or whose value is not an allowed value
// (a string outside its enumeration, a number outside its range), is dropped and counted.
func (s *Span) Set(attrs ...Attr) {
	if s == nil {
		return
	}
	d := spanByName[s.data.Name]
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range attrs {
		if !s.o.attrOK(d, a) {
			continue
		}
		replaced := false
		for i := range s.data.Attrs {
			if s.data.Attrs[i].Key == a.Key {
				s.data.Attrs[i], replaced = a, true
			}
		}
		if !replaced {
			s.data.Attrs = append(s.data.Attrs, a)
		}
	}
}

func (o *Observer) attrOK(d *SpanDef, a Attr) bool {
	listed := false
	for _, k := range d.Attrs {
		if k == a.Key {
			listed = true
		}
	}
	ad, known := attrDefs[a.Key]
	if !listed || !known {
		o.drop("unknown_attribute")
		return false
	}
	if ad.numeric {
		if !a.IsNum || math.IsNaN(a.Num) || a.Num < ad.lo || a.Num > ad.hi {
			o.drop("bad_attribute_value")
			return false
		}
		return true
	}
	if a.IsNum || !labelAllowed[ad.enumFrom][a.Str] {
		o.drop("bad_attribute_value")
		return false
	}
	return true
}

// End finishes the span and hands it to the sinks. A second call does nothing.
func (s *Span) End() {
	if s == nil || !s.ended.CompareAndSwap(false, true) {
		return
	}
	s.mu.Lock()
	s.data.End = s.o.now()
	d := s.data
	d.Attrs = append([]Attr(nil), s.data.Attrs...)
	s.mu.Unlock()
	s.o.operator.Span(d)
}

// SetUsageAllowlist tells the observer which usage facts exist: the producer (package telemetry)
// passes its own allowlist, so the two cannot disagree. Facts outside it are dropped by Usage.
func (o *Observer) SetUsageAllowlist(allowed map[string][]string) {
	if o == nil {
		return
	}
	m := make(map[string]map[string]bool, len(allowed))
	for fact, vs := range allowed {
		m[fact] = map[string]bool{}
		for _, v := range vs {
			m[fact][v] = true
		}
	}
	o.mu.Lock()
	o.usageAllow = m
	o.mu.Unlock()
}

func (o *Observer) usageLabelOK(name, value string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if name == "fact" {
		_, ok := o.usageAllow[value]
		return ok
	}
	for _, vs := range o.usageAllow {
		if vs[value] {
			return true
		}
	}
	return false
}

// Usage routes n occurrences of an allowlisted usage fact (the counts a browser sent with the
// person's opt-in) to the usage sinks. A pair outside the allowlist is dropped and counted.
func (o *Observer) Usage(fact, choice string, n int) {
	if o == nil || o.usage == nil {
		return
	}
	o.mu.Lock()
	ok := o.usageAllow[fact][choice]
	o.mu.Unlock()
	if !ok || n < 1 {
		o.drop("bad_label_value")
		return
	}
	o.usage.Metric(Point{Metric: UsageEvents, Kind: Counter, Value: float64(n), Labels: []Label{{"fact", fact}, {"choice", choice}}, Source: Usage})
}

// Sweep times one run of a background sweep. Call it when the run starts and the returned function
// when it ends with how many items it removed and its error.
func (o *Observer) Sweep(name string) func(removed int, err error) {
	if o == nil || o.operator == nil {
		return func(int, error) {}
	}
	start := o.now()
	_, sp := o.Start(context.Background(), SpanSweep, S("sweep", name))
	return func(removed int, err error) {
		result := "ok"
		if err != nil {
			result = "error"
			o.Error("sweep_failed")
		}
		o.Add(SweepRuns, 1, "sweep", name, "result", result)
		if removed > 0 {
			o.Add(SweepRemoved, float64(removed), "sweep", name)
		}
		o.Observe(SweepDuration, o.now().Sub(start).Seconds(), "sweep", name)
		sp.Set(S("result", result), N("count", float64(max(removed, 0))))
		sp.End()
	}
}

// Work times one piece of a server's own work (a name of the "work" list) and counts how it ended. Call it
// when the work starts and the returned function when it ends with its result: ok, rejected (refused
// by a rule), canceled or error.
func (o *Observer) Work(name string) func(result string) {
	if o == nil || o.operator == nil {
		return func(string) {}
	}
	start := o.now()
	return func(result string) {
		o.Add(WorkRuns, 1, "work", name, "result", result)
		o.Observe(WorkDuration, o.now().Sub(start).Seconds(), "work", name)
	}
}

// SetOpen sets how much of a kind of work is open now (the gauge of the "work" list, for example hub_rooms).
func (o *Observer) SetOpen(name string, n int) { o.Set(WorkOpen, float64(n), "work", name) }

// StoreOp times one store operation under the span in ctx. Call the returned function when it ends
// with its result (one of the "result" values: ok, not_found, conflict, canceled, error) and, for a
// listing, the number of items (-1 for none).
func (o *Observer) StoreOp(ctx context.Context, op string) func(result string, count int) {
	if o == nil || o.operator == nil {
		return func(string, int) {}
	}
	start := o.now()
	name := SpanName("store." + op)
	if op == "save_if" {
		name = SpanStoreSave
	}
	_, sp := o.Start(ctx, name, S("op", op))
	return func(result string, count int) {
		if result == "error" {
			o.Error("store_failed")
		}
		o.Add(StoreOps, 1, "op", op, "result", result)
		o.Observe(StoreDuration, o.now().Sub(start).Seconds(), "op", op)
		sp.Set(S("result", result))
		if count >= 0 {
			sp.Set(N("count", float64(count)))
		}
		sp.End()
	}
}

// Flush asks every sink to send what it holds now.
func (o *Observer) Flush(ctx context.Context) error {
	if o == nil {
		return nil
	}
	return joinErr(flushAll(ctx, o.sinks()))
}

// Close flushes and closes the sinks (at shutdown).
func (o *Observer) Close(ctx context.Context) error {
	if o == nil {
		return nil
	}
	return joinErr(closeAll(ctx, o.sinks()))
}

// sinks are the distinct sinks behind both producers.
func (o *Observer) sinks() []Sink {
	var out []Sink
	for _, s := range []Sink{o.operator, o.usage} {
		if s != nil {
			out = append(out, s)
		}
	}
	if len(out) == 2 && out[0] == out[1] {
		out = out[:1]
	}
	return out
}

// MetricInfo, SpanInfo and AttributeInfo are the allowlist as data, for the admin route and the docs.
type (
	// MetricInfo is one metric with the allowed values of each label.
	MetricInfo struct {
		Name   Metric              `json:"name"`
		Kind   string              `json:"kind"`
		Unit   string              `json:"unit"`
		Labels map[string][]string `json:"labels,omitempty"`
		Why    string              `json:"why"`
	}
	// SpanInfo is one span with the attributes it may carry.
	SpanInfo struct {
		Name       SpanName `json:"name"`
		Attributes []string `json:"attributes"`
		Why        string   `json:"why"`
	}
	// AttributeInfo is one span attribute: its allowed values, or its range.
	AttributeInfo struct {
		Name   string   `json:"name"`
		Values []string `json:"values,omitempty"`
		Min    *float64 `json:"min,omitempty"`
		Max    *float64 `json:"max,omitempty"`
	}
)

// Metrics lists the metric allowlist. The usage labels list what the producer registered (usage).
func Metrics(usage map[string][]string) []MetricInfo {
	var out []MetricInfo
	for _, d := range defs {
		mi := MetricInfo{Name: d.Name, Kind: d.Kind.String(), Unit: d.Unit, Why: d.Why}
		if len(d.Labels) > 0 {
			mi.Labels = map[string][]string{}
		}
		for _, l := range d.Labels {
			switch l {
			case "fact":
				var facts []string
				for f := range usage {
					facts = append(facts, f)
				}
				sort.Strings(facts)
				mi.Labels[l] = facts
			case "choice":
				seen := map[string]bool{}
				var cs []string
				for _, vs := range usage {
					for _, v := range vs {
						if !seen[v] {
							seen[v] = true
							cs = append(cs, v)
						}
					}
				}
				sort.Strings(cs)
				mi.Labels[l] = cs
			default:
				mi.Labels[l] = append([]string(nil), labelValues[l]...)
			}
		}
		out = append(out, mi)
	}
	return out
}

// Spans lists the span allowlist.
func Spans() []SpanInfo {
	out := make([]SpanInfo, 0, len(spanDefs))
	for _, d := range spanDefs {
		out = append(out, SpanInfo{Name: d.Name, Attributes: append([]string(nil), d.Attrs...), Why: d.Why})
	}
	return out
}

// Attributes lists the span attribute allowlist.
func Attributes() []AttributeInfo {
	out := make([]AttributeInfo, 0, len(attrNames))
	for _, n := range attrNames {
		d := attrDefs[n]
		ai := AttributeInfo{Name: n}
		if d.numeric {
			lo, hi := d.lo, d.hi
			ai.Min, ai.Max = &lo, &hi
		} else {
			ai.Values = append([]string(nil), labelValues[d.enumFrom]...)
		}
		out = append(out, ai)
	}
	return out
}
