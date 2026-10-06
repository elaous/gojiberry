package docstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RateLimitPrefix is the keyspace of the budgets a StoreLimiter keeps: one record per limiter
// name and caller, "ratelimit/<name>/<hash of the caller's key>". Like "directory/" it is not a
// document: the document API refuses it, expiry never gives it a deadline, and encryption leaves
// it alone. SweepRateLimits deletes the records of callers that have been idle.
const RateLimitPrefix = "ratelimit/"

// Limiter decides whether a caller may do something now (start a ceremony, publish a key, look
// one up). key names the caller ("<route group>|<client>", "u:<user id>"); Allow returns
// ok=false and how long to wait when the caller is over its budget. Implementations are safe
// for concurrent use. RateLimiter is a per-process one; StoreLimiter shares its state through a
// store so that replicas of a server share one budget; Unlimited turns limiting off.
type Limiter interface {
	Allow(ctx context.Context, key string) (ok bool, retryAfter time.Duration)
}

// Unlimited is a Limiter that allows everything.
type Unlimited struct{}

// Allow implements Limiter.
func (Unlimited) Allow(context.Context, string) (bool, time.Duration) { return true, 0 }

// LimiterFactory makes the Limiter of one named budget: perMinute requests a minute on average,
// burst at once. The name tells budgets apart (StoreLimiters keeps them under different
// records), so it must be the same on every replica that is to share the budget.
type LimiterFactory func(name string, perMinute float64, burst int) Limiter

// MemoryLimiters is the LimiterFactory of per-process budgets (the default).
func MemoryLimiters() LimiterFactory {
	return func(_ string, perMinute float64, burst int) Limiter { return NewRateLimiter(perMinute, burst) }
}

// StoreLimiters is the LimiterFactory of budgets kept in store: replicas on one store share them.
// With a store that is not a ConditionalAdapter it is MemoryLimiters (see StoreLimiter).
func StoreLimiters(store Adapter) LimiterFactory {
	return func(name string, perMinute float64, burst int) Limiter {
		return NewStoreLimiter(store, name, perMinute, burst)
	}
}

// ParseLimiterMode parses GOJIBERRY_DOCSTORE_LIMITER: "" or "memory" (a budget per process) or
// "store" (a budget shared through the store). It returns the canonical name.
func ParseLimiterMode(raw string) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(raw)); m {
	case "", "memory":
		return "memory", nil
	case "store":
		return "store", nil
	}
	return "", fmt.Errorf("parsing GOJIBERRY_DOCSTORE_LIMITER %q: want \"memory\" or \"store\"", raw)
}

// LimiterFactoryFor is the factory a mode from ParseLimiterMode names.
func LimiterFactoryFor(mode string, store Adapter) LimiterFactory {
	if mode == "store" {
		return StoreLimiters(store)
	}
	return MemoryLimiters()
}

// RateLimiter is a token bucket per key: Burst requests at once, refilled at PerMinute per
// minute. Idle keys are forgotten, so memory follows the number of active callers. State is in
// this process only.
type RateLimiter struct {
	PerMinute float64
	Burst     int
	// Now is the clock (time.Now if nil), for tests.
	Now func() time.Time

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

// NewRateLimiter makes a limiter allowing burst requests at once and perMinute on average.
func NewRateLimiter(perMinute float64, burst int) *RateLimiter {
	return &RateLimiter{PerMinute: perMinute, Burst: burst, buckets: map[string]*bucket{}}
}

// maxBuckets bounds the keys remembered; past it the oldest idle ones are dropped first.
const maxBuckets = 100000

// refill is the token count after the time since at, capped at burst.
func refill(tokens float64, since time.Duration, perMinute float64, burst int) float64 {
	if since > 0 {
		tokens += since.Minutes() * perMinute
	}
	return math.Min(tokens, float64(burst))
}

// waitFor is how long until a bucket holding tokens has one to give.
func waitFor(tokens, perMinute float64) time.Duration {
	if perMinute <= 0 {
		return time.Hour
	}
	return time.Duration((1 - tokens) / perMinute * float64(time.Minute))
}

// Allow implements Limiter.
func (l *RateLimiter) Allow(_ context.Context, key string) (bool, time.Duration) {
	now := time.Now()
	if l.Now != nil {
		now = l.Now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = map[string]*bucket{}
	}
	if len(l.buckets) >= maxBuckets || now.Sub(l.lastSweep) > time.Minute {
		l.sweepLocked(now)
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(l.Burst), at: now}
		l.buckets[key] = b
	}
	b.tokens = refill(b.tokens, now.Sub(b.at), l.PerMinute, l.Burst)
	b.at = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, waitFor(b.tokens, l.PerMinute)
}

// sweepLocked forgets buckets that have refilled completely (idle callers).
func (l *RateLimiter) sweepLocked(now time.Time) {
	l.lastSweep = now
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.at).Minutes()*l.PerMinute >= float64(l.Burst) {
			delete(l.buckets, k)
		}
	}
	for k := range l.buckets { // still too many: drop arbitrary ones (an attacker's keys are as likely as any)
		if len(l.buckets) < maxBuckets {
			break
		}
		delete(l.buckets, k)
	}
}

// StoreLimiter is a token bucket per key whose state lives in the store, so every replica
// sharing the store shares the budget. A key's bucket is one record,
// "ratelimit/<Name>/<hash of the key>" (the caller's address or user ID is not in the record
// name), changed with a compare-and-set loop: read the record, refill and take a token,
// SaveIf with the digest that was read, and start over if somebody else saved in between (so
// concurrent callers, in one process or many, never spend the same token twice). A denied
// request writes nothing.
//
// Degraded modes. When the store is not a ConditionalAdapter (or says so with
// ErrConditionalUnsupported) the limiter is an in-process RateLimiter with the same numbers:
// the budget is then per process, which is the limit this type exists to remove. When the
// store fails (an I/O error), that one decision is made by the in-process limiter and the
// failure is logged at most once a minute: a broken store must neither lock everyone out nor
// switch the limit off. When the loop loses the race too many times (a hot key) the request is
// refused for a second. Clocks: each replica uses its own clock; a record "from the future" of
// this replica's clock refills nothing until that time has passed, so skew of a few seconds
// changes the refill, never the burst.
//
// Records of callers that stopped calling are deleted by SweepRateLimits.
type StoreLimiter struct {
	// Store holds the records.
	Store Adapter
	// Name tells this limiter's records from another's (letters, digits, '-' and '_'; anything
	// else becomes '_').
	Name      string
	PerMinute float64
	Burst     int
	// Now is the clock (time.Now if nil), for tests.
	Now func() time.Time

	once     sync.Once
	mem      *RateLimiter
	noCAS    atomic.Bool
	lastWarn atomic.Int64
}

// NewStoreLimiter makes the limiter named name over store: burst requests at once and perMinute
// on average, per key.
func NewStoreLimiter(store Adapter, name string, perMinute float64, burst int) *StoreLimiter {
	return &StoreLimiter{Store: store, Name: name, PerMinute: perMinute, Burst: burst}
}

// maxCASRounds bounds the compare-and-set loop. Every lost round means another caller won one,
// so a loop of this length is lost only under very hot contention on one key.
const maxCASRounds = 64

// limitState is what a record holds: the tokens at time At, and when the bucket is full again
// (Full), which is what the sweeper reads. Times are Unix nanoseconds.
type limitState struct {
	Tokens float64 `json:"t"`
	At     int64   `json:"a"`
	Full   int64   `json:"f"`
}

func (l *StoreLimiter) clock() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *StoreLimiter) memory() *RateLimiter {
	l.once.Do(func() {
		l.mem = NewRateLimiter(l.PerMinute, l.Burst)
		l.mem.Now = l.Now
	})
	return l.mem
}

func (l *StoreLimiter) warn(err error) {
	now := l.clock().Unix()
	if last := l.lastWarn.Load(); now-last >= 60 && l.lastWarn.CompareAndSwap(last, now) {
		log.Printf("docstore: shared rate limiter %q: %v (deciding in this process)", l.Name, err)
	}
}

func limitName(name string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)
}

func (l *StoreLimiter) recordID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return RateLimitPrefix + limitName(l.Name) + "/" + hex.EncodeToString(sum[:16])
}

// Allow implements Limiter.
func (l *StoreLimiter) Allow(ctx context.Context, key string) (bool, time.Duration) {
	cas, ok := l.Store.(ConditionalAdapter)
	if !ok || l.noCAS.Load() {
		return l.memory().Allow(ctx, key)
	}
	id := l.recordID(key)
	for range maxCASRounds {
		now := l.clock()
		st, prev, err := l.read(ctx, cas, id)
		if err != nil {
			l.warn(err)
			return l.memory().Allow(ctx, key)
		}
		if st.At == 0 {
			st = limitState{Tokens: float64(l.Burst), At: now.UnixNano()}
		}
		at := max(st.At, now.UnixNano()) // never move a record's time backwards
		tokens := refill(st.Tokens, time.Duration(now.UnixNano()-st.At), l.PerMinute, l.Burst)
		if tokens < 1 {
			return false, waitFor(tokens, l.PerMinute)
		}
		tokens--
		next := limitState{Tokens: tokens, At: at, Full: at}
		if l.PerMinute > 0 {
			next.Full = at + int64((float64(l.Burst)-tokens)/l.PerMinute*float64(time.Minute))
		}
		data, _ := json.Marshal(next)
		switch err := cas.SaveIf(ctx, id, data, prev); {
		case err == nil:
			return true, 0
		case errors.Is(err, ErrConflict):
			continue
		case errors.Is(err, ErrConditionalUnsupported):
			l.noCAS.Store(true)
			return l.memory().Allow(ctx, key)
		default:
			l.warn(err)
			return l.memory().Allow(ctx, key)
		}
	}
	return false, time.Second
}

// read loads the state and the digest SaveIf must be given ("" when there is no record or it
// cannot be read: a record that is not valid is replaced as if it were new).
func (l *StoreLimiter) read(ctx context.Context, a Adapter, id string) (limitState, string, error) {
	doc, err := a.Load(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return limitState{}, "", nil
	}
	if err != nil {
		return limitState{}, "", err
	}
	var st limitState
	if json.Unmarshal(doc.Data, &st) != nil || st.At == 0 || st.Tokens < 0 || math.IsNaN(st.Tokens) {
		return limitState{}, Digest(doc.Data), nil
	}
	return st, Digest(doc.Data), nil
}

// rateLimitGrace is how long a bucket must have been full before its record is deleted.
const rateLimitGrace = time.Minute

// SweepRateLimits deletes the budget records in store of callers that have been idle: whose
// bucket has been full again for a minute (a full bucket and a missing record mean the same, so
// nothing is lost), and records that cannot be read and have not been written for an hour. It
// returns how many it deleted. A caller that comes back between the read and the delete loses
// one token at most; the next SaveIf of a replica that read the deleted record conflicts and
// starts from the empty record, which is the same as a full bucket.
func SweepRateLimits(ctx context.Context, store Adapter, now time.Time) (int, error) {
	metas, err := ListPrefix(ctx, store, RateLimitPrefix)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range metas {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		doc, err := store.Load(ctx, m.ID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return n, err
		}
		var st limitState
		stale := false
		if json.Unmarshal(doc.Data, &st) != nil || st.At == 0 {
			stale = now.Sub(doc.UpdatedAt) > time.Hour
		} else {
			stale = now.UnixNano()-st.Full > int64(rateLimitGrace)
		}
		if !stale {
			continue
		}
		if err := store.Delete(ctx, m.ID); err != nil && !errors.Is(err, ErrNotFound) {
			return n, err
		}
		n++
	}
	return n, nil
}

// RunRateLimitSweep calls SweepRateLimits every interval (ten minutes when not positive) until
// ctx ends; errors are logged.
func RunRateLimitSweep(ctx context.Context, store Adapter, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := SweepRateLimits(ctx, store, time.Now()); err != nil && ctx.Err() == nil {
				log.Printf("docstore: sweeping rate limit records: %v", err)
			}
		}
	}
}
