package docstore

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRateLimiterEvictsTheLeastRecentlyUsedKey(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	l := NewRateLimiter(0.0001, 1)
	l.Now = func() time.Time { return now }
	for i := range maxBuckets {
		if ok, _ := l.Allow(ctx, fmt.Sprintf("k%d", i)); !ok {
			t.Fatalf("k%d refused", i)
		}
	}
	if ok, _ := l.Allow(ctx, "k0"); ok {
		t.Fatal("k0 was not spent")
	}
	if ok, _ := l.Allow(ctx, "new"); !ok {
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

func TestRateLimiterDoesNotScanTheTableForANewKey(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	l := NewRateLimiter(60, 1)
	l.Now = func() time.Time { return now }
	for i := range maxBuckets {
		l.Allow(ctx, fmt.Sprintf("k%d", i))
	}
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
