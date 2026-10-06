package public

import (
	"context"
	"sync"
	"time"
)

// Quota is a byte budget per key, the per-IP daily budget of the public offer. Take charges n
// bytes against key's budget and reports whether they fit; over the budget it charges nothing
// and says how long until the budget has room again. The default is NewMemoryQuota (one
// process); replicas that share a budget plug in a store over Redis or a table.
type Quota interface {
	Take(ctx context.Context, key string, n int64) (ok bool, retryAfter time.Duration, err error)
}

// QuotaRefunder is an optional capability of a Quota: Refund gives n bytes back to key's budget
// (within the window they were charged in). The service charges before it reads a body, and refunds
// what a request did not end up writing (refused content, a conflict, a failed store); a Quota
// without it simply keeps those charges.
type QuotaRefunder interface {
	Refund(ctx context.Context, key string, n int64)
}

// UnlimitedQuota is a Quota that allows everything.
type UnlimitedQuota struct{}

// Take implements Quota.
func (UnlimitedQuota) Take(context.Context, string, int64) (bool, time.Duration, error) {
	return true, 0, nil
}

// MemoryQuota gives each key Limit bytes per Window, counted from the key's first charge (a
// fixed window per key, not a rolling one: simple, and a caller gets nothing extra by timing
// it). Idle windows are forgotten, and the number of keys is bounded.
type MemoryQuota struct {
	Limit  int64
	Window time.Duration
	// Now is the clock (time.Now if nil), for tests.
	Now func() time.Time

	mu      sync.Mutex
	keys    map[string]*quotaWindow
	swept   time.Time
	maxKeys int
}

type quotaWindow struct {
	start time.Time
	used  int64
}

// DefaultQuotaKeys bounds the keys a MemoryQuota remembers.
const DefaultQuotaKeys = 100000

// NewMemoryQuota makes a quota of limit bytes per window (a day, if window is 0).
func NewMemoryQuota(limit int64, window time.Duration) *MemoryQuota {
	return &MemoryQuota{Limit: limit, Window: window, keys: map[string]*quotaWindow{}}
}

// Take implements Quota.
func (q *MemoryQuota) Take(_ context.Context, key string, n int64) (bool, time.Duration, error) {
	now := time.Now()
	if q.Now != nil {
		now = q.Now()
	}
	win := q.Window
	if win <= 0 {
		win = 24 * time.Hour
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.keys == nil {
		q.keys = map[string]*quotaWindow{}
	}
	max := q.maxKeys
	if max <= 0 {
		max = DefaultQuotaKeys
	}
	if len(q.keys) >= max || now.Sub(q.swept) > time.Minute {
		q.swept = now
		for k, w := range q.keys {
			if !now.Before(w.start.Add(win)) {
				delete(q.keys, k)
			}
		}
		for k := range q.keys { // still full: drop arbitrary ones
			if len(q.keys) < max {
				break
			}
			delete(q.keys, k)
		}
	}
	w := q.keys[key]
	if w == nil || !now.Before(w.start.Add(win)) {
		w = &quotaWindow{start: now}
		q.keys[key] = w
	}
	if n > q.Limit || w.used+n > q.Limit {
		return false, w.start.Add(win).Sub(now), nil
	}
	w.used += n
	return true, 0, nil
}

// Refund implements QuotaRefunder.
func (q *MemoryQuota) Refund(_ context.Context, key string, n int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if w := q.keys[key]; w != nil {
		w.used = max(0, w.used-n)
	}
}
