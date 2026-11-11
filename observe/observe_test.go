package observe

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

type rec struct {
	mu      sync.Mutex
	points  []Point
	spans   []SpanData
	flushed int
	closed  int
	flushEr error
}

func (r *rec) Name() string { return "rec" }
func (r *rec) Metric(p Point) {
	r.mu.Lock()
	r.points = append(r.points, p)
	r.mu.Unlock()
}
func (r *rec) Span(s SpanData) {
	r.mu.Lock()
	r.spans = append(r.spans, s)
	r.mu.Unlock()
}
func (r *rec) Flush(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushed++
	return r.flushEr
}
func (r *rec) Close(context.Context) error {
	r.mu.Lock()
	r.closed++
	r.mu.Unlock()
	return nil
}

func (r *rec) snapshot() ([]Point, []SpanData) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Point(nil), r.points...), append([]SpanData(nil), r.spans...)
}

func (r *rec) count(m Metric) (n int) {
	pts, _ := r.snapshot()
	for _, p := range pts {
		if p.Metric == m {
			n++
		}
	}
	return n
}

func newObs() (*Observer, *rec) {
	r := &rec{}
	return New(Options{Operator: r, Usage: r, TraceSample: 1}), r
}

func TestAllowedPointsPassWithLabelsInDefinitionOrder(t *testing.T) {
	o, r := newObs()
	o.Add(HTTPRequests, 1, "status_class", "2xx", "method", "GET", "route", "/docs")
	pts, _ := r.snapshot()
	if len(pts) != 1 {
		t.Fatalf("points = %v", pts)
	}
	want := []Label{{"route", "/docs"}, {"method", "GET"}, {"status_class", "2xx"}}
	for i, l := range pts[0].Labels {
		if l != want[i] {
			t.Fatalf("labels = %v, want %v", pts[0].Labels, want)
		}
	}
	if len(o.Dropped()) != 0 {
		t.Fatalf("dropped = %v", o.Dropped())
	}
}

func TestAnythingOutsideTheAllowlistIsDroppedAndCounted(t *testing.T) {
	o, r := newObs()
	cases := []struct {
		name   string
		reason string
		do     func()
	}{
		{"unknown metric", "unknown_metric", func() { o.Add("user_emails_total", 1) }},
		{"wrong kind", "wrong_kind", func() { o.Observe(HTTPRequests, 1, "route", "/docs", "method", "GET", "status_class", "2xx") }},
		{"unknown label", "unknown_label", func() { o.Add(Errors, 1, "user", "code") }},
		{"missing label", "unknown_label", func() { o.Add(HTTPRequests, 1, "route", "/docs") }},
		{"extra label", "unknown_label", func() { o.Add(Errors, 1, "code", "panic", "ip", "10.0.0.1") }},
		{"odd pairs", "unknown_label", func() { o.Add(Errors, 1, "code") }},
		{"free-text value", "bad_label_value", func() { o.Add(Errors, 1, "code", "disk /var/lib/x failed for alice@example.com") }},
		{"raw path as route", "bad_label_value", func() {
			o.Add(HTTPRequests, 1, "route", "/docs/abc123", "method", "GET", "status_class", "2xx")
		}},
		{"nan", "bad_value", func() { o.Observe(StoreDuration, math.NaN(), "op", "load") }},
		{"inf", "bad_value", func() { o.Observe(StoreDuration, math.Inf(1), "op", "load") }},
		{"negative counter", "bad_value", func() { o.Add(Errors, -1, "code", "panic") }},
		{"unknown span", "unknown_span", func() { o.Start(context.Background(), "user.login") }},
		{"unknown attribute", "unknown_attribute", func() {
			_, sp := o.Start(context.Background(), SpanHTTP)
			sp.Set(S("user_id", "u-1"))
		}},
		{"attribute not on the span", "unknown_attribute", func() {
			_, sp := o.Start(context.Background(), SpanSweep)
			sp.Set(S("route", "/docs"))
		}},
		{"string outside enumeration", "bad_attribute_value", func() {
			_, sp := o.Start(context.Background(), SpanHTTP)
			sp.Set(S("route", "/docs/secret"))
		}},
		{"string for a number", "bad_attribute_value", func() {
			_, sp := o.Start(context.Background(), SpanHTTP)
			sp.Set(S("status_code", "200"))
		}},
		{"number for an enumeration", "bad_attribute_value", func() {
			_, sp := o.Start(context.Background(), SpanHTTP)
			sp.Set(N("route", 1))
		}},
		{"number out of range", "bad_attribute_value", func() {
			_, sp := o.Start(context.Background(), SpanHTTP)
			sp.Set(N("status_code", 99999))
		}},
	}
	for _, c := range cases {
		before := o.Dropped()[c.reason]
		pts, _ := r.snapshot()
		nBefore := len(pts)
		c.do()
		if got := o.Dropped()[c.reason]; got != before+1 {
			t.Errorf("%s: dropped[%s] = %d, want %d", c.name, c.reason, got, before+1)
		}
		pts, _ = r.snapshot()
		for _, p := range pts[nBefore:] {
			if p.Metric != DroppedPoints {
				t.Errorf("%s: a point got through: %+v", c.name, p)
			}
		}
	}
	if r.count(DroppedPoints) == 0 {
		t.Error("the refusals were not reported as obs_dropped_total")
	}
	_, sp := o.Start(context.Background(), SpanHTTP)
	sp.Set(S("route", "/docs/secret"), S("route", "/docs"))
	sp.End()
	_, spans := r.snapshot()
	last := spans[len(spans)-1]
	if len(last.Attrs) != 1 || last.Attrs[0].Str != "/docs" {
		t.Errorf("attrs = %+v", last.Attrs)
	}
}

func TestNilObserverDoesNothing(t *testing.T) {
	var o *Observer
	o.Add(Errors, 1, "code", "panic")
	o.Observe(StoreDuration, 1, "op", "load")
	o.Set(HTTPInflight, 1)
	o.Usage("feature", "find", 1)
	ctx, sp := o.Start(context.Background(), SpanHTTP)
	sp.Set(S("route", "/docs"))
	sp.End()
	if ctx == nil || o.Enabled() || len(o.Dropped()) != 0 {
		t.Fatal("a nil observer must be inert")
	}
	o.Sweep("expiry")(1, nil)
	o.StoreOp(context.Background(), "load")("ok", -1)
	if err := o.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := o.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := o.Describe(); d.Enabled || len(d.Metrics) == 0 {
		t.Fatalf("a disabled observer still describes the allowlist: %+v", d)
	}
}

func TestSpansLinkParentAndChildAndFollowTheSample(t *testing.T) {
	o, r := newObs()
	ctx, parent := o.Start(context.Background(), SpanHTTP, S("route", "/docs"))
	_, child := o.Start(ctx, SpanStoreLoad, S("op", "load"))
	child.End()
	child.End() // a second End is ignored
	parent.End()
	_, spans := r.snapshot()
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want 2", len(spans))
	}
	c, p := spans[0], spans[1]
	if c.Parent != p.ID || c.TraceID != p.TraceID || p.Parent != ([8]byte{}) {
		t.Fatalf("child %+v is not under parent %+v", c, p)
	}

	r2 := &rec{}
	none := New(Options{Operator: r2, TraceSample: 0})
	ctx, sp := none.Start(context.Background(), SpanHTTP)
	_, kid := none.Start(ctx, SpanStoreLoad)
	sp.End()
	kid.End()
	if _, s := r2.snapshot(); len(s) != 0 {
		t.Fatalf("sample 0 kept spans: %v", s)
	}
	r3 := &rec{}
	half := New(Options{Operator: r3, TraceSample: 0.5})
	roots := 0
	for range 400 {
		ctx, sp := half.Start(context.Background(), SpanHTTP)
		_, kid := half.Start(ctx, SpanStoreLoad)
		if (sp == nil) != (kid == nil) {
			t.Fatal("a child must follow its parent's decision")
		}
		if sp != nil {
			roots++
		}
		sp.End()
		kid.End()
	}
	if roots < 100 || roots > 300 {
		t.Fatalf("sample 0.5 kept %d of 400", roots)
	}
}

func TestUsageIsRoutedOnlyToTheUsageSinkAndOnlyWhatIsAllowed(t *testing.T) {
	op, us := &rec{}, &rec{}
	o := New(Options{Operator: op, Usage: us})
	o.SetUsageAllowlist(map[string][]string{"feature": {"find", "spell"}, "locale": {"en"}})
	o.Usage("feature", "find", 3)
	o.Usage("locale", "en", 1)
	o.Usage("feature", "en", 1)                // a value of another fact
	o.Usage("feature", "alice@example.com", 1) // anything else
	o.Usage("session", "find", 1)              // not a fact
	o.Usage("feature", "find", 0)              // not a count
	pts, _ := us.snapshot()
	if len(pts) != 2 || pts[0].Source != Usage || pts[0].Value != 3 || pts[0].Metric != UsageEvents {
		t.Fatalf("usage points = %+v", pts)
	}
	if got := o.Dropped()["bad_label_value"]; got != 4 {
		t.Fatalf("dropped = %d, want 4", got)
	}
	for _, p := range func() []Point { x, _ := op.snapshot(); return x }() {
		if p.Metric == UsageEvents {
			t.Fatal("usage points must not reach the operator sink")
		}
	}
	none := New(Options{Operator: op})
	none.SetUsageAllowlist(map[string][]string{"feature": {"find"}})
	none.Usage("feature", "find", 1)
	if got := op.count(UsageEvents); got != 0 {
		t.Fatal("usage reached the operator sink")
	}
}

func TestFanoutOneFailingSinkDoesNotBreakTheOther(t *testing.T) {
	good := &rec{}
	f := Fanout(panicky{}, good, nil, failing{})
	f.Metric(Point{Metric: Errors})
	f.Span(SpanData{Name: SpanHTTP})
	pts, spans := good.snapshot()
	if len(pts) != 1 || len(spans) != 1 {
		t.Fatalf("the good sink got %d points, %d spans", len(pts), len(spans))
	}
	err := f.Flush(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failing") || !strings.Contains(err.Error(), "panicky") {
		t.Fatalf("flush error = %v, want both failures", err)
	}
	if good.flushed != 1 {
		t.Fatal("the good sink was not flushed after a failing one")
	}
	if err := f.Close(context.Background()); err == nil || good.closed != 1 {
		t.Fatalf("close: err=%v closed=%d", err, good.closed)
	}
	if f.(*fanout).Failures() != 2 {
		t.Fatalf("recovered panics = %d, want 2", f.(*fanout).Failures())
	}
}

type panicky struct{ Nop }

func (panicky) Name() string                { return "panicky" }
func (panicky) Metric(Point)                { panic("boom") }
func (panicky) Span(SpanData)               { panic("boom") }
func (panicky) Flush(context.Context) error { panic("boom") }

type failing struct{ Nop }

func (failing) Name() string                { return "failing" }
func (failing) Flush(context.Context) error { return errors.New("down") }
func (failing) Close(context.Context) error { return errors.New("down") }

func TestObserverFlushAndCloseReachEverySinkOnce(t *testing.T) {
	both := &rec{}
	o := New(Options{Operator: both, Usage: both})
	if err := o.Flush(context.Background()); err != nil || both.flushed != 1 {
		t.Fatalf("flush: %v %d", err, both.flushed)
	}
	if err := o.Close(context.Background()); err != nil || both.closed != 1 {
		t.Fatalf("close: %v %d", err, both.closed)
	}
}

func TestBuiltinIsBounded(t *testing.T) {
	b := NewBuiltin()
	for i := range maxSeries * 3 {
		b.Metric(Point{Metric: Errors, Kind: Counter, Value: 1, Labels: []Label{{"code", string(rune('a'+i%26)) + strings.Repeat("x", i)}}})
	}
	series, _, over := b.Snapshot()
	if len(series) != maxSeries || over != int64(maxSeries*2) {
		t.Fatalf("series=%d overflow=%d", len(series), over)
	}
}

func TestCardinalityIsBoundedByTheAllowlist(t *testing.T) {
	b := NewBuiltin()
	o := New(Options{Operator: b})
	for i := range 20000 {
		o.Add(HTTPRequests, 1, "route", "/docs/"+string(rune(i)), "method", "GET", "status_class", "2xx")
		o.Add(HTTPRequests, 1, "route", "/docs", "method", "GET", "status_class", "2xx")
		o.Add(Errors, 1, "code", strings.Repeat("e", i%97))
	}
	series, _, _ := b.Snapshot()
	valid := 0
	for _, s := range series {
		if s.Metric == HTTPRequests {
			valid++
		}
	}
	if valid != 1 {
		t.Fatalf("http series = %d, want 1 (all raw paths were dropped)", valid)
	}
	if o.Dropped()["bad_label_value"] == 0 {
		t.Fatal("drops were not counted")
	}
	worst := 0
	for _, d := range defs {
		n := 1
		for _, l := range d.Labels {
			if usageLabels[l] {
				continue
			}
			n *= len(labelValues[l])
		}
		worst += n
	}
	if worst > maxSeries*8 {
		t.Fatalf("the allowlist allows %d series: too many for a bounded sink", worst)
	}
}

func TestSweepAndStoreOpRecord(t *testing.T) {
	o, r := newObs()
	o.Sweep("expiry")(3, nil)
	o.Sweep("expiry")(0, errors.New("secret path /var/db"))
	o.StoreOp(context.Background(), "list")("ok", 7)
	o.StoreOp(context.Background(), "save_if")("conflict", -1)
	o.StoreOp(context.Background(), "load")("error", -1)
	pts, spans := r.snapshot()
	got := map[string]float64{}
	for _, p := range pts {
		k := string(p.Metric)
		for _, l := range p.Labels {
			k += "|" + l.Name + "=" + l.Value
		}
		got[k] += p.Value
	}
	for k, want := range map[string]float64{
		"sweep_runs_total|sweep=expiry|result=ok":    1,
		"sweep_runs_total|sweep=expiry|result=error": 1,
		"sweep_removed_total|sweep=expiry":           3,
		"errors_total|code=sweep_failed":             1,
		"errors_total|code=store_failed":             1,
		"store_ops_total|op=save_if|result=conflict": 1,
		"store_ops_total|op=list|result=ok":          1,
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v (all: %v)", k, got[k], want, got)
		}
	}
	if len(spans) != 5 {
		t.Fatalf("spans = %d", len(spans))
	}
	b, _ := json.Marshal(spans)
	if strings.Contains(string(b), "secret") {
		t.Fatal("an error message reached a span")
	}
}

func TestSpanDurationUsesTheClock(t *testing.T) {
	r := &rec{}
	now := time.Unix(1000, 0)
	o := New(Options{Operator: r, TraceSample: 1, Now: func() time.Time { return now }})
	_, sp := o.Start(context.Background(), SpanHTTP)
	now = now.Add(1500 * time.Millisecond)
	sp.End()
	_, spans := r.snapshot()
	if d := spans[0].End.Sub(spans[0].Start); d != 1500*time.Millisecond {
		t.Fatalf("duration = %v", d)
	}
}

func TestAllowlistDataIsConsistent(t *testing.T) {
	for _, d := range defs {
		if d.Why == "" || d.Unit == "" {
			t.Errorf("%s has no reason or unit", d.Name)
		}
		for _, l := range d.Labels {
			if _, ok := labelValues[l]; !ok && !usageLabels[l] {
				t.Errorf("%s: label %s has no values", d.Name, l)
			}
			switch l {
			case "value", "count", "sum", "min", "max", "kind", "source", "interval_seconds", "event":
				t.Errorf("%s: label %s collides with a property of the PostHog events", d.Name, l)
			}
		}
	}
	for _, sd := range spanDefs {
		for _, a := range sd.Attrs {
			if _, ok := attrDefs[a]; !ok {
				t.Errorf("span %s: attribute %s is not defined", sd.Name, a)
			}
		}
	}
	for _, n := range attrNames {
		if _, ok := attrDefs[n]; !ok {
			t.Errorf("attribute %s is not defined", n)
		}
	}
	if len(attrNames) != len(attrDefs) {
		t.Error("attrNames and attrDefs differ")
	}
}
