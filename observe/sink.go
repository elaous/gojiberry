package observe

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Sink is where validated points and spans go.
type Sink interface {
	// Name is the name the operator uses in GOJIBERRY_OBS_SINKS.
	Name() string
	Metric(p Point)
	Span(s SpanData)
	Flush(ctx context.Context) error
	Close(ctx context.Context) error
}

type Describer interface {
	Describe() any
}

// Nop is a Sink that discards everything.
type Nop struct{}

func (Nop) Name() string                { return "nop" }
func (Nop) Metric(Point)                {}
func (Nop) Span(SpanData)               {}
func (Nop) Flush(context.Context) error { return nil }
func (Nop) Close(context.Context) error { return nil }

// Fanout sends everything to every sink.
func Fanout(sinks ...Sink) Sink {
	var live []Sink
	for _, s := range sinks {
		if s != nil {
			live = append(live, s)
		}
	}
	return &fanout{sinks: live}
}

type fanout struct {
	sinks    []Sink
	mu       sync.Mutex
	failures int
}

func (f *fanout) Name() string {
	names := make([]string, len(f.sinks))
	for i, s := range f.sinks {
		names[i] = s.Name()
	}
	return fmt.Sprint(names)
}

func (f *fanout) guard() {
	if recover() != nil {
		f.mu.Lock()
		f.failures++
		f.mu.Unlock()
	}
}

func (f *fanout) Metric(p Point) {
	for _, s := range f.sinks {
		func() {
			defer f.guard()
			s.Metric(p)
		}()
	}
}

func (f *fanout) Span(d SpanData) {
	for _, s := range f.sinks {
		func() {
			defer f.guard()
			s.Span(d)
		}()
	}
}

// Failures is how many panics of the sinks were recovered.
func (f *fanout) Failures() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failures
}

func (f *fanout) Flush(ctx context.Context) error { return joinErr(flushAll(ctx, f.sinks)) }
func (f *fanout) Close(ctx context.Context) error { return joinErr(closeAll(ctx, f.sinks)) }

func (f *fanout) Describe() any {
	out := map[string]any{}
	for _, s := range f.sinks {
		if d, ok := s.(Describer); ok {
			out[s.Name()] = d.Describe()
		}
	}
	return out
}

func safely(name string, f func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("observe: sink %s panicked", name)
		}
	}()
	return f()
}

func flushAll(ctx context.Context, sinks []Sink) []error {
	var errs []error
	for _, s := range sinks {
		if err := safely(s.Name(), func() error { return s.Flush(ctx) }); err != nil {
			errs = append(errs, fmt.Errorf("flush %s: %w", s.Name(), err))
		}
	}
	return errs
}

func closeAll(ctx context.Context, sinks []Sink) []error {
	var errs []error
	for _, s := range sinks {
		if err := safely(s.Name(), func() error { return s.Close(ctx) }); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", s.Name(), err))
		}
	}
	return errs
}

func joinErr(errs []error) error { return errors.Join(errs...) }

const maxSeries = 2048

type agg struct {
	metric   Metric
	kind     Kind
	source   Source
	labels   []Label
	count    int64
	sum      float64
	min, max float64
	last     float64
}

func (a *agg) add(v float64) {
	if a.count == 0 || v < a.min {
		a.min = v
	}
	if a.count == 0 || v > a.max {
		a.max = v
	}
	a.count++
	a.sum += v
	a.last = v
}

func seriesKey(p Point) string {
	k := string(p.Metric) + "|" + p.Source.String()
	for _, l := range p.Labels {
		k += "|" + l.Name + "=" + l.Value
	}
	return k
}

func newAgg(p Point) *agg {
	return &agg{metric: p.Metric, kind: p.Kind, source: p.Source, labels: append([]Label(nil), p.Labels...)}
}

type Series struct {
	Metric Metric            `json:"metric"`
	Kind   string            `json:"kind"`
	Source string            `json:"source"`
	Labels map[string]string `json:"labels,omitempty"`
	Count  int64             `json:"count"`
	Sum    float64           `json:"sum"`
	Min    float64           `json:"min,omitempty"`
	Max    float64           `json:"max,omitempty"`
	Last   float64           `json:"last"`
}

type SpanStat struct {
	Name         SpanName `json:"name"`
	Count        int64    `json:"count"`
	TotalSeconds float64  `json:"total_seconds"`
	MaxSeconds   float64  `json:"max_seconds"`
	Errors       int64    `json:"errors"`
}

type Builtin struct {
	mu       sync.Mutex
	series   map[string]*agg
	spans    map[SpanName]*SpanStat
	overflow int64

	path       string
	stop, done chan struct{}
}

// NewBuiltin is an empty built-in sink.
func NewBuiltin() *Builtin {
	return &Builtin{series: map[string]*agg{}, spans: map[SpanName]*SpanStat{}}
}

// Name implements Sink.
func (b *Builtin) Name() string { return "builtin" }

// Metric implements Sink.
func (b *Builtin) Metric(p Point) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := seriesKey(p)
	a := b.series[k]
	if a == nil {
		if len(b.series) >= maxSeries {
			b.overflow++
			return
		}
		a = newAgg(p)
		b.series[k] = a
	}
	if p.Kind == Gauge {
		a.last = p.Value
		a.count++
		return
	}
	a.add(p.Value)
}

// Span implements Sink.
func (b *Builtin) Span(s SpanData) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.spans[s.Name]
	if st == nil {
		st = &SpanStat{Name: s.Name}
		b.spans[s.Name] = st
	}
	d := s.End.Sub(s.Start).Seconds()
	st.Count++
	st.TotalSeconds += d
	st.MaxSeconds = max(st.MaxSeconds, d)
	for _, a := range s.Attrs {
		if a.Key == "result" && a.Str == "error" {
			st.Errors++
		}
	}
}

func (b *Builtin) Flush(context.Context) error { return b.save() }

// Close implements Sink.
func (b *Builtin) Close(ctx context.Context) error { return b.closeState(ctx) }

// Snapshot is the totals so far, in a stable order.
func (b *Builtin) Snapshot() (series []Series, spans []SpanStat, overflow int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, a := range b.series {
		s := Series{Metric: a.metric, Kind: a.kind.String(), Source: a.source.String(), Count: a.count, Sum: a.sum, Min: a.min, Max: a.max, Last: a.last}
		if len(a.labels) > 0 {
			s.Labels = map[string]string{}
			for _, l := range a.labels {
				s.Labels[l.Name] = l.Value
			}
		}
		series = append(series, s)
	}
	sort.Slice(series, func(i, j int) bool {
		if series[i].Metric != series[j].Metric {
			return series[i].Metric < series[j].Metric
		}
		return fmt.Sprint(series[i].Labels) < fmt.Sprint(series[j].Labels)
	})
	for _, st := range b.spans {
		spans = append(spans, *st)
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].Name < spans[j].Name })
	return series, spans, b.overflow
}

// Describe implements Describer: the totals.
func (b *Builtin) Describe() any {
	series, spans, over := b.Snapshot()
	return map[string]any{"series": series, "spans": spans, "series_overflow": over}
}

// SinkEnv is what a sink factory is given.
type SinkEnv struct {
	// Getenv reads the environment (os.Getenv in production).
	Getenv func(string) string
	// ReadFile reads a secret file (os.ReadFile in production).
	ReadFile func(string) ([]byte, error)
	// Service is the name the sink reports the server as.
	Service string
}

// SinkFactory builds a sink named in GOJIBERRY_OBS_SINKS.
type SinkFactory func(ctx context.Context, env SinkEnv) (Sink, error)

var (
	regMu     sync.Mutex
	factories = map[string]SinkFactory{}
)

// RegisterSink makes a sink available by name.
func RegisterSink(name string, f SinkFactory) {
	regMu.Lock()
	defer regMu.Unlock()
	factories[name] = f
}

func factory(name string) (SinkFactory, bool) {
	regMu.Lock()
	defer regMu.Unlock()
	f, ok := factories[name]
	return f, ok
}

func Registered() []string {
	regMu.Lock()
	defer regMu.Unlock()
	out := []string{"builtin"}
	for n := range factories {
		out = append(out, n)
	}
	sort.Strings(out[1:])
	return out
}

func (b *Builtin) Count(m Metric, kv ...string) int64 {
	series, _, _ := b.Snapshot()
	var n int64
	for _, s := range series {
		if s.Metric != m {
			continue
		}
		ok := true
		for i := 0; i+1 < len(kv); i += 2 {
			if s.Labels[kv[i]] != kv[i+1] {
				ok = false
			}
		}
		if ok {
			n += s.Count
		}
	}
	return n
}
