package docstore

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// At the table's capacity the least recently used key is dropped, not an arbitrary one: a busy
// caller that keeps being used survives a flood of new keys, the key nobody touched goes.
func TestRateLimiterEvictsTheLeastRecentlyUsedKey(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	l := NewRateLimiter(0.0001, 1) // a token a week: nothing refills, so nothing counts as idle
	l.Now = func() time.Time { return now }
	for i := range maxBuckets {
		if ok, _ := l.Allow(ctx, fmt.Sprintf("k%d", i)); !ok {
			t.Fatalf("k%d refused", i)
		}
	}
	if ok, _ := l.Allow(ctx, "k0"); ok { // k0 is used again: the most recent now, and still spent
		t.Fatal("k0 was not spent")
	}
	if ok, _ := l.Allow(ctx, "new"); !ok { // the table is full: the least recently used (k1) is dropped
		t.Fatal("new refused")
	}
	if ok, _ := l.Allow(ctx, "k0"); ok {
		t.Error("k0 was used last and must survive the flood, but its bucket was reset")
	}
	if ok, _ := l.Allow(ctx, "k1"); !ok {
		t.Error("k1 was the least recently used key and must have been dropped (a fresh bucket)")
	}
	if len(l.buckets) > maxBuckets || l.order.Len() != len(l.buckets) {
		t.Errorf("table %d list %d", len(l.buckets), l.order.Len())
	}
}

// A new key at capacity must not scan the table: the cost of one Allow is constant.
func TestRateLimiterDoesNotScanTheTableForANewKey(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	l := NewRateLimiter(60, 1)
	l.Now = func() time.Time { return now }
	for i := range maxBuckets {
		l.Allow(ctx, fmt.Sprintf("k%d", i))
	}
	// Everything has refilled by now, so a sweep would empty the table: only one that runs on
	// every new key at capacity would be visible as the table shrinking at once.
	now = now.Add(time.Hour)
	l.lastSweep = now // a sweep ran just now
	l.Allow(ctx, "fresh")
	if len(l.buckets) < maxBuckets-1 {
		t.Errorf("a new key at capacity swept the table: %d keys left", len(l.buckets))
	}
	now = now.Add(2 * sweepEvery) // the periodic sweep is what removes the idle ones
	l.Allow(ctx, "later")
	if len(l.buckets) > 10 {
		t.Errorf("the periodic sweep left %d idle keys", len(l.buckets))
	}
}
