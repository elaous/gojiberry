package docstore

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Over the cap, the sweep deletes the least recently used keys.
func TestSweepEvictsRefilledBucketsBeforeLiveOnes(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryAdapter()
	clk := newClock()
	l := newStoreLimiter(store, clk, "evict", 1, 5) // one token a minute, burst 5
	for range 4 {                                   // the live, slow caller spends four tokens first
		l.Allow(ctx, "live")
	}
	time.Sleep(2 * time.Millisecond) // distinct write times: "live" is the oldest record
	l.Allow(ctx, "refilled")         // one token spent
	clk.Add(90 * time.Second)        // "refilled" has been full for 30 seconds (inside the one minute grace), "live" is not full

	st, err := SweepRateLimitsStats(ctx, store, clk.Now(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if st.Evicted != 1 || st.EvictedLive != 0 || st.Kept != 1 {
		t.Fatalf("%+v: one record evicted, and it was not a live one", st)
	}
	metas, _ := ListPrefix(ctx, store, RateLimitPrefix)
	if len(metas) != 1 || metas[0].ID != l.recordID("live") {
		t.Fatalf("the live caller must survive, left: %v", metas)
	}
}

func TestSweepEvictsTheLeastSpentLiveBucketsFirstAndCountsThem(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryAdapter()
	clk := newClock()
	l := newStoreLimiter(store, clk, "evict", 1, 5)
	for range 4 { // the slow caller has the most spent budget and is the oldest record
		l.Allow(ctx, "slow")
	}
	time.Sleep(2 * time.Millisecond)
	for i := range 30 { // a flood of one-off callers, one token each
		l.Allow(ctx, fmt.Sprint("ip-", i))
	}
	st, err := SweepRateLimitsStats(ctx, store, clk.Now(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if st.Evicted != 21 || st.EvictedLive != 21 || st.Kept != 10 {
		t.Fatalf("%+v: 31 records, 10 kept, all 21 evicted had budget spent", st)
	}
	metas, _ := ListPrefix(ctx, store, RateLimitPrefix)
	left := map[string]bool{}
	for _, m := range metas {
		left[m.ID] = true
	}
	if !left[l.recordID("slow")] {
		t.Error("the slow caller, with the most budget spent, was evicted by a flood of one-off callers")
	}
}

// A sweep that evicts nothing live keeps everything.
func TestSweepStatsMatchSweepRateLimitsMax(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryAdapter()
	clk := newClock()
	l := newStoreLimiter(store, clk, "evict", 1, 5)
	for i := range 12 {
		l.Allow(ctx, fmt.Sprint("k-", i))
	}
	n, err := SweepRateLimitsMax(ctx, store, clk.Now(), 5)
	if err != nil || n != 7 {
		t.Fatalf("%d %v", n, err)
	}
	if st, err := SweepRateLimitsStats(ctx, store, clk.Now(), 5); err != nil || st.Deleted != 0 || st.Kept != 5 {
		t.Fatalf("%+v %v", st, err)
	}
}
