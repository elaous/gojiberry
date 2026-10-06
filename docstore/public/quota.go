package public

import (
	"container/list"
	"context"
	"log"
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

// WindowQuota is an optional capability of a Quota that tells the charge which window it went
// into: TakeIn is Take that also returns an opaque window id, and RefundIn gives n bytes back only
// if that window is still the key's current one. Without it a refund that arrives after the
// window rolled over would credit the new window with the old window's charge.
type WindowQuota interface {
	TakeIn(ctx context.Context, key string, n int64) (ok bool, window int64, retryAfter time.Duration, err error)
	RefundIn(ctx context.Context, key string, n int64, window int64)
}

// UnlimitedQuota is a Quota that allows everything.
type UnlimitedQuota struct{}

// Take implements Quota.
func (UnlimitedQuota) Take(context.Context, string, int64) (bool, time.Duration, error) {
	return true, 0, nil
}

// MemoryQuota gives each key Limit bytes per Window, counted from the key's first charge (a
// fixed window per key, not a rolling one: simple, and a caller gets nothing extra by timing
// it). Idle windows are forgotten, and the number of keys is bounded: at the bound the
// least recently used keys are dropped (their windows start afresh) and the drop is logged.
type MemoryQuota struct {
	Limit  int64
	Window time.Duration
	// Now is the clock (time.Now if nil), for tests.
	Now func() time.Time

	mu      sync.Mutex
	keys    map[string]*quotaWindow
	lru     *list.List // of *quotaWindow, most recently used first
	swept   time.Time
	maxKeys int

	evicted    int // dropped for room since the last log line
	evictedLog time.Time
}

type quotaWindow struct {
	key   string
	start time.Time
	used  int64
	elem  *list.Element
}

// DefaultQuotaKeys bounds the keys a MemoryQuota remembers.
const DefaultQuotaKeys = 100000

// NewMemoryQuota makes a quota of limit bytes per window (a day, if window is 0).
func NewMemoryQuota(limit int64, window time.Duration) *MemoryQuota {
	return &MemoryQuota{Limit: limit, Window: window, keys: map[string]*quotaWindow{}, lru: list.New()}
}

func (q *MemoryQuota) now() time.Time {
	if q.Now != nil {
		return q.Now()
	}
	return time.Now()
}

func (q *MemoryQuota) window() time.Duration {
	if q.Window > 0 {
		return q.Window
	}
	return 24 * time.Hour
}

func (q *MemoryQuota) initLocked() {
	if q.keys == nil {
		q.keys = map[string]*quotaWindow{}
	}
	if q.lru == nil {
		q.lru = list.New()
	}
}

func (q *MemoryQuota) dropLocked(w *quotaWindow) {
	delete(q.keys, w.key)
	q.lru.Remove(w.elem)
}

// Take implements Quota.
func (q *MemoryQuota) Take(ctx context.Context, key string, n int64) (bool, time.Duration, error) {
	ok, _, retry, err := q.TakeIn(ctx, key, n)
	return ok, retry, err
}

// TakeIn implements WindowQuota: the window id is the window's start.
func (q *MemoryQuota) TakeIn(_ context.Context, key string, n int64) (bool, int64, time.Duration, error) {
	now := q.now()
	win := q.window()
	q.mu.Lock()
	defer q.mu.Unlock()
	q.initLocked()
	max := q.maxKeys
	if max <= 0 {
		max = DefaultQuotaKeys
	}
	if len(q.keys) >= max || now.Sub(q.swept) > time.Minute {
		q.swept = now
		for _, w := range q.keys {
			if !now.Before(w.start.Add(win)) {
				q.dropLocked(w)
			}
		}
		// Still full and a new key to hold: the least recently used keys go, not arbitrary ones.
		dropped := 0
		for q.keys[key] == nil && len(q.keys) >= max {
			q.dropLocked(q.lru.Back().Value.(*quotaWindow))
			dropped++
		}
		if dropped > 0 {
			q.evicted += dropped
			if now.Sub(q.evictedLog) >= time.Minute {
				log.Printf("public docstore: quota: %d keys over the bound of %d: the least recently used were dropped and their daily byte windows start afresh (raise the bound or put a shared Quota in front)", q.evicted, max)
				q.evicted, q.evictedLog = 0, now
			}
		}
	}
	w := q.keys[key]
	if w != nil && !now.Before(w.start.Add(win)) {
		q.dropLocked(w)
		w = nil
	}
	if w == nil {
		w = &quotaWindow{key: key, start: now}
		w.elem = q.lru.PushFront(w)
		q.keys[key] = w
	} else {
		q.lru.MoveToFront(w.elem)
	}
	if n > q.Limit || w.used+n > q.Limit {
		return false, w.start.UnixNano(), w.start.Add(win).Sub(now), nil
	}
	w.used += n
	return true, w.start.UnixNano(), 0, nil
}

// Refund implements QuotaRefunder: it credits the key's current window.
func (q *MemoryQuota) Refund(_ context.Context, key string, n int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if w := q.keys[key]; w != nil {
		w.used = max(0, w.used-n)
	}
}

// RefundIn implements WindowQuota: it credits the window the charge was made in, and nothing if
// that window has ended (the key's budget is a new one by now).
func (q *MemoryQuota) RefundIn(_ context.Context, key string, n int64, window int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if w := q.keys[key]; w != nil && w.start.UnixNano() == window {
		w.used = max(0, w.used-n)
	}
}
