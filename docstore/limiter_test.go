package docstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newStoreLimiter(store Adapter, clk *fakeClock, name string, perMinute float64, burst int) *StoreLimiter {
	l := NewStoreLimiter(store, name, perMinute, burst)
	l.Now = clk.Now
	return l
}

func TestStoreLimiterBurstRefillAndReset(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	l := newStoreLimiter(NewMemoryAdapter(), clk, "t", 60, 3) // one a second, 3 at once
	for i := range 3 {
		if ok, _ := l.Allow(ctx, "a"); !ok {
			t.Fatalf("burst %d refused", i)
		}
	}
	ok, wait := l.Allow(ctx, "a")
	if ok || wait <= 0 || wait > 2*time.Second {
		t.Fatalf("over the burst: %v %v", ok, wait)
	}
	if ok, _ := l.Allow(ctx, "b"); !ok {
		t.Error("another key has its own budget")
	}
	clk.Add(1100 * time.Millisecond)
	if ok, _ := l.Allow(ctx, "a"); !ok {
		t.Error("a token came back after a second")
	}
	if ok, _ := l.Allow(ctx, "a"); ok {
		t.Error("only one token came back")
	}
	// The window resets: a long wait refills the bucket to the burst, not beyond.
	clk.Add(time.Hour)
	for i := range 3 {
		if ok, _ := l.Allow(ctx, "a"); !ok {
			t.Fatalf("after the reset, %d refused", i)
		}
	}
	if ok, _ := l.Allow(ctx, "a"); ok {
		t.Error("the burst is the cap after a reset")
	}
}

func TestStoreLimiterNoRefillBudget(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	l := newStoreLimiter(NewMemoryAdapter(), clk, "t", 0, 1)
	if ok, _ := l.Allow(ctx, "a"); !ok {
		t.Fatal("the burst")
	}
	if ok, wait := l.Allow(ctx, "a"); ok || wait != time.Hour {
		t.Errorf("%v %v", ok, wait)
	}
}

// Two limiters (two replicas) over one store share the budget: it is spent once, not twice.
func TestStoreLimiterSharedBudgetAcrossInstances(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryAdapter()
	clk := newClock()
	a := newStoreLimiter(store, clk, "pub", 1, 4)
	b := newStoreLimiter(store, clk, "pub", 1, 4)
	allowed := 0
	for i := range 8 {
		l := a
		if i%2 == 1 {
			l = b
		}
		if ok, _ := l.Allow(ctx, "user"); ok {
			allowed++
		}
	}
	if allowed != 4 {
		t.Fatalf("two replicas allowed %d, want the shared burst of 4", allowed)
	}
	// A limiter with another name is another budget, over the same store.
	if ok, _ := newStoreLimiter(store, clk, "lookup", 1, 4).Allow(ctx, "user"); !ok {
		t.Error("another name, another budget")
	}
	// The per-process limiter is what the store limiter exists to replace: two of them
	// would have allowed 8.
	m1, m2 := NewRateLimiter(1, 4), NewRateLimiter(1, 4)
	m1.Now, m2.Now = clk.Now, clk.Now
	n := 0
	for i := range 8 {
		l := Limiter(m1)
		if i%2 == 1 {
			l = m2
		}
		if ok, _ := l.Allow(ctx, "user"); ok {
			n++
		}
	}
	if n != 8 {
		t.Fatalf("control: %d", n)
	}
	// The records are under the reserved prefix and do not contain the key.
	metas, _ := store.List(ctx)
	for _, m := range metas {
		if !strings.HasPrefix(m.ID, RateLimitPrefix) || strings.Contains(m.ID, "user") {
			t.Errorf("record %q", m.ID)
		}
	}
}

func TestStoreLimiterConcurrentCallersSpendEachTokenOnce(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryAdapter()
	clk := newClock()
	limiters := []*StoreLimiter{newStoreLimiter(store, clk, "c", 0.001, 25), newStoreLimiter(store, clk, "c", 0.001, 25), newStoreLimiter(store, clk, "c", 0.001, 25)}
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := range 90 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := limiters[i%3].Allow(ctx, "hot"); ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 25 {
		t.Fatalf("allowed %d of 90 callers, want exactly the burst of 25", allowed.Load())
	}
}

func TestStoreLimiterFallsBackToMemory(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	inner := NewMemoryAdapter()
	l := newStoreLimiter(plainAdapter{inner}, clk, "m", 1, 2)
	for i := range 2 {
		if ok, _ := l.Allow(ctx, "a"); !ok {
			t.Fatalf("%d", i)
		}
	}
	if ok, _ := l.Allow(ctx, "a"); ok {
		t.Error("the in-process fallback still limits")
	}
	if metas, _ := inner.List(ctx); len(metas) != 0 {
		t.Errorf("a store that is not conditional is not written: %v", metas)
	}
	// A store that has SaveIf but cannot use it (ExpiringAdapter over a plain one) is the same.
	ea := NewExpiringAdapter(plainAdapter{NewMemoryAdapter()}, FixedTTL(time.Hour))
	l = newStoreLimiter(ea, clk, "m", 1, 1)
	if ok, _ := l.Allow(ctx, "a"); !ok {
		t.Error("first")
	}
	if ok, _ := l.Allow(ctx, "a"); ok {
		t.Error("second is over the budget")
	}
	if !l.noCAS.Load() {
		t.Error("ErrConditionalUnsupported switches to memory for good")
	}
}

// failing makes the store's reads or conditional writes fail.
type failing struct {
	*MemoryAdapter
	failLoad, failSave bool
}

func (f *failing) Load(ctx context.Context, id string) (Document, error) {
	if f.failLoad {
		return Document{}, errors.New("disk on fire")
	}
	return f.MemoryAdapter.Load(ctx, id)
}

func (f *failing) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	if f.failSave {
		return errors.New("disk on fire")
	}
	return f.MemoryAdapter.SaveIf(ctx, id, data, prev)
}

func TestStoreLimiterStoreErrorsDecideInProcess(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	for name, f := range map[string]*failing{"load": {MemoryAdapter: NewMemoryAdapter(), failLoad: true}, "save": {MemoryAdapter: NewMemoryAdapter(), failSave: true}} {
		l := newStoreLimiter(f, clk, "e", 1, 2)
		got := 0
		for range 5 {
			if ok, _ := l.Allow(ctx, "a"); ok {
				got++
			}
		}
		if got != 2 {
			t.Errorf("%s: a failing store allowed %d, want the in-process burst of 2", name, got)
		}
	}
}

func TestStoreLimiterReplacesAnUnreadableRecord(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryAdapter()
	clk := newClock()
	l := newStoreLimiter(store, clk, "x", 1, 2)
	_ = store.Save(ctx, l.recordID("a"), []byte("garbage"))
	if ok, _ := l.Allow(ctx, "a"); !ok {
		t.Error("a record that is not valid counts as new")
	}
	var st limitState
	doc, _ := store.Load(ctx, l.recordID("a"))
	if err := jsonUnmarshal(doc.Data, &st); err != nil || st.Tokens != 1 {
		t.Errorf("%s %v", doc.Data, err)
	}
}

func TestSweepRateLimitsDeletesStaleRecords(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryAdapter()
	clk := newClock()
	l := newStoreLimiter(store, clk, "s", 60, 3) // full again 1s per token after use
	l.Allow(ctx, "idle")
	l.Allow(ctx, "idle")
	clk.Add(30 * time.Second)
	l.Allow(ctx, "busy")                                              // recently used: not full for a bit
	_ = store.Save(ctx, RateLimitPrefix+"s/junk", []byte("not json")) // unreadable, just written
	_ = store.Save(ctx, "doc1", []byte("a document"))                 // not ours

	// 30s after "idle": its bucket has been full for ~28s, under the one-minute grace.
	if n, err := SweepRateLimits(ctx, store, clk.Now()); err != nil || n != 0 {
		t.Fatalf("early sweep: %d %v", n, err)
	}
	clk.Add(2 * time.Minute)
	n, err := SweepRateLimits(ctx, store, clk.Now())
	if err != nil || n != 2 {
		t.Fatalf("sweep: %d %v (want the two idle callers; the junk is younger than an hour)", n, err)
	}
	metas, _ := store.List(ctx)
	ids := map[string]bool{}
	for _, m := range metas {
		ids[m.ID] = true
	}
	if !ids["doc1"] || !ids[RateLimitPrefix+"s/junk"] || len(ids) != 2 {
		t.Errorf("left: %v", ids)
	}
	// An hour later the unreadable record goes too; the documents never do.
	n, err = SweepRateLimits(ctx, store, time.Now().Add(2*time.Hour)) // the memory adapter stamps real time
	if err != nil || n != 1 {
		t.Fatalf("late sweep: %d %v", n, err)
	}
	if _, err := store.Load(ctx, "doc1"); err != nil {
		t.Error("the sweep touched a document")
	}
	// A swept caller starts with a full bucket again, as before.
	for range 3 {
		if ok, _ := l.Allow(ctx, "idle"); !ok {
			t.Error("a swept caller has the full burst")
		}
	}
}

func TestLimiterModes(t *testing.T) {
	for in, want := range map[string]string{"": "memory", "Memory": "memory", " store ": "store"} {
		if got, err := ParseLimiterMode(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	if _, err := ParseLimiterMode("redis"); err == nil {
		t.Error("an unknown mode is an error")
	}
	store := NewMemoryAdapter()
	if _, ok := LimiterFactoryFor("store", store)("n", 1, 1).(*StoreLimiter); !ok {
		t.Error("store mode makes store limiters")
	}
	if _, ok := LimiterFactoryFor("memory", store)("n", 1, 1).(*RateLimiter); !ok {
		t.Error("memory mode makes in-process limiters")
	}
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func TestRateLimiterBurstRefillAndForgetting(t *testing.T) {
	clk := newClock()
	l := NewRateLimiter(60, 3)
	l.Now = clk.Now
	ctx := context.Background()
	for range 3 {
		l.Allow(ctx, "a")
	}
	if ok, wait := l.Allow(ctx, "a"); ok || wait <= 0 || wait > 2*time.Second {
		t.Fatalf("%v %v", ok, wait)
	}
	clk.Add(10 * time.Minute)
	l.Allow(ctx, "c") // a later call sweeps the buckets that refilled completely
	l.mu.Lock()
	n := len(l.buckets)
	l.mu.Unlock()
	if n != 1 {
		t.Errorf("refilled buckets are swept, %d left", n)
	}
	if ok, wait := (&RateLimiter{}).Allow(ctx, "z"); ok || wait <= 0 {
		t.Error("a zero budget refuses")
	}
	var _ Limiter = Unlimited{}
	if ok, _ := (Unlimited{}).Allow(ctx, "x"); !ok {
		t.Error("Unlimited allows")
	}
}

func TestConfigFromEnvReadsTheLimiterMode(t *testing.T) {
	if got := ConfigFromEnv().Limiter; got != "memory" {
		t.Errorf("default = %q", got)
	}
	t.Setenv("GOJIBERRY_DOCSTORE_LIMITER", "store")
	if got := ConfigFromEnv().Limiter; got != "store" {
		t.Errorf("%q", got)
	}
}
