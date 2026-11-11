package docstore

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/observe"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RateLimitPrefix is the keyspace of the budget records.
const RateLimitPrefix = "ratelimit/"

// ConfigPrefix is the keyspace of per-user configuration.
const ConfigPrefix = "config/"

// DeviceUsePrefix is the keyspace of device-use records.
const DeviceUsePrefix = "deviceuse/"

// Limiter decides whether a caller may perform an action now.
type Limiter interface {
	Allow(ctx context.Context, key string) (ok bool, retryAfter time.Duration)
}

// Unlimited is a Limiter that allows everything.
type Unlimited struct{}

// Allow implements Limiter.
func (Unlimited) Allow(context.Context, string) (bool, time.Duration) { return true, 0 }

// LimiterFactory makes the Limiter of one named budget.
type LimiterFactory func(name string, perMinute float64, burst int) Limiter

// MemoryLimiters is the LimiterFactory of in-process budgets.
func MemoryLimiters() LimiterFactory {
	return func(_ string, perMinute float64, burst int) Limiter { return NewRateLimiter(perMinute, burst) }
}

// StoreLimiters is the LimiterFactory of budgets kept in the store.
func StoreLimiters(store Adapter) LimiterFactory {
	return func(name string, perMinute float64, burst int) Limiter {
		return NewStoreLimiter(store, name, perMinute, burst)
	}
}

// ParseLimiterMode parses the limiter mode setting.
func ParseLimiterMode(raw string) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(raw)); m {
	case "", "memory":
		return "memory", nil
	case "store":
		return "store", nil
	}
	return "", fmt.Errorf("parsing GOJIBERRY_DOCSTORE_LIMITER %q: want \"memory\" or \"store\"", raw)
}

// LimiterFactoryFor is the factory a mode selects.
func LimiterFactoryFor(mode string, store Adapter) LimiterFactory {
	if mode == "store" {
		return StoreLimiters(store)
	}
	return MemoryLimiters()
}

// RateLimiter is a token bucket per key, in memory.
type RateLimiter struct {
	PerMinute float64
	Burst     int
	// Now is the clock (time.Now if nil), for tests.
	Now func() time.Time

	mu      sync.Mutex
	buckets map[string]*list.Element
	// value *bucket; ordered by last use, newest first
	order     *list.List
	lastSweep time.Time
}

type bucket struct {
	key    string
	tokens float64
	at     time.Time // last use: the list is in this order
}

// NewRateLimiter makes an in-memory rate limiter.
func NewRateLimiter(perMinute float64, burst int) *RateLimiter {
	return &RateLimiter{PerMinute: perMinute, Burst: burst, buckets: map[string]*list.Element{}, order: list.New()}
}

const maxBuckets = 100000

const sweepEvery = time.Minute

func refill(tokens float64, since time.Duration, perMinute float64, burst int) float64 {
	if since > 0 {
		tokens += since.Minutes() * perMinute
	}
	return math.Min(tokens, float64(burst))
}

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
		l.buckets = map[string]*list.Element{}
	}
	if l.order == nil {
		l.order = list.New()
	}
	if now.Sub(l.lastSweep) >= sweepEvery {
		l.sweepLocked(now)
	}
	var b *bucket
	if el, ok := l.buckets[key]; ok {
		b = el.Value.(*bucket)
		l.order.MoveToFront(el)
	} else {
		for len(l.buckets) >= maxBuckets {
			old := l.order.Back()
			delete(l.buckets, old.Value.(*bucket).key)
			l.order.Remove(old)
		}
		b = &bucket{key: key, tokens: float64(l.Burst), at: now}
		l.buckets[key] = l.order.PushFront(b)
	}
	b.tokens = refill(b.tokens, now.Sub(b.at), l.PerMinute, l.Burst)
	b.at = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, waitFor(b.tokens, l.PerMinute)
}

func (l *RateLimiter) sweepLocked(now time.Time) {
	l.lastSweep = now
	for {
		el := l.order.Back()
		if el == nil {
			return
		}
		b := el.Value.(*bucket)
		if b.tokens+now.Sub(b.at).Minutes()*l.PerMinute < float64(l.Burst) {
			return
		}
		delete(l.buckets, b.key)
		l.order.Remove(el)
	}
}

type StoreLimiter struct {
	// Store holds the records.
	Store Adapter
	// Name tells this limiter's records apart.
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

// NewStoreLimiter makes a store-backed limiter with a name.
func NewStoreLimiter(store Adapter, name string, perMinute float64, burst int) *StoreLimiter {
	return &StoreLimiter{Store: store, Name: name, PerMinute: perMinute, Burst: burst}
}

const maxClockSkew = 5 * time.Second

// lost only under very hot contention
const maxCASRounds = 64

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
		if st.At > now.UnixNano()+int64(maxClockSkew) {
			// a far-ahead replica clock must not lock the others out
			fixed := limitState{Tokens: st.Tokens, At: now.UnixNano(), Full: now.UnixNano()}
			if l.PerMinute > 0 {
				fixed.Full += int64((float64(l.Burst) - st.Tokens) / l.PerMinute * float64(time.Minute))
			}
			data, _ := json.Marshal(fixed)
			switch err := cas.SaveIf(ctx, id, data, prev); {
			case err == nil:
				st, prev = fixed, Digest(data)
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
		// never move a record's time backwards (within the skew)
		at := max(st.At, now.UnixNano())
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

// a record that is not valid is replaced as if new
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

const rateLimitGrace = time.Minute

// MaxRateLimitRecords is how many budget records are kept.
const MaxRateLimitRecords = 100000

// SweepRateLimits deletes idle budget records.
func SweepRateLimits(ctx context.Context, store Adapter, now time.Time) (int, error) {
	return SweepRateLimitsMax(ctx, store, now, MaxRateLimitRecords)
}

// SweepRateLimitsMax is SweepRateLimits with a cap.
func SweepRateLimitsMax(ctx context.Context, store Adapter, now time.Time, maxRecords int) (int, error) {
	st, err := SweepRateLimitsStats(ctx, store, now, maxRecords)
	return st.Deleted, err
}

// RateLimitSweep is what one sweep did.
type RateLimitSweep struct {
	Deleted     int // records deleted in all (idle ones, unreadable ones, and evictions)
	Evicted     int // of those, deleted only to get under the cap
	EvictedLive int
	Kept        int // records left
}

// SweepRateLimitsStats is SweepRateLimitsMax with statistics.
func SweepRateLimitsStats(ctx context.Context, store Adapter, now time.Time, maxRecords int) (RateLimitSweep, error) {
	var out RateLimitSweep
	if maxRecords <= 0 {
		maxRecords = MaxRateLimitRecords
	}
	metas, err := ListPrefix(ctx, store, RateLimitPrefix)
	if err != nil {
		return out, err
	}
	var errs []error
	type keptRec struct {
		meta Meta
		full int64
	}
	kept := make([]keptRec, 0, len(metas))
	for _, m := range metas {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		doc, err := store.Load(ctx, m.ID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			kept = append(kept, keptRec{m, math.MaxInt64})
			continue
		}
		var st limitState
		stale := false
		full := int64(math.MaxInt64)
		if json.Unmarshal(doc.Data, &st) != nil || st.At == 0 {
			stale = now.Sub(doc.UpdatedAt) > time.Hour
		} else {
			stale = now.UnixNano()-st.Full > int64(rateLimitGrace)
			full = st.Full
		}
		if !stale {
			m.UpdatedAt = doc.UpdatedAt
			kept = append(kept, keptRec{m, full})
			continue
		}
		switch gone, err := deleteRateLimit(ctx, store, m.ID, doc.Data); {
		case err != nil:
			errs = append(errs, err)
			kept = append(kept, keptRec{m, full})
		case gone:
			out.Deleted++
		}
	}
	if over := len(kept) - maxRecords; over > 0 {
		sort.SliceStable(kept, func(i, j int) bool {
			if kept[i].full != kept[j].full {
				return kept[i].full < kept[j].full
			}
			return kept[i].meta.UpdatedAt.Before(kept[j].meta.UpdatedAt)
		})
		for _, k := range kept[:over] {
			doc, err := store.Load(ctx, k.meta.ID)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				errs = append(errs, err)
				continue
			}
			switch gone, err := deleteRateLimit(ctx, store, k.meta.ID, doc.Data); {
			case err != nil:
				errs = append(errs, err)
			case gone:
				out.Deleted++
				out.Evicted++
				if k.full != math.MaxInt64 && k.full > now.UnixNano() {
					// an unreadable record's date is unknown: not counted
					out.EvictedLive++
				}
			}
		}
	}
	out.Kept = len(kept) - out.Evicted
	return out, errors.Join(errs...)
}

func deleteRateLimit(ctx context.Context, store Adapter, id string, data []byte) (bool, error) {
	prev := Digest(data)
	if c, ok := store.(ConditionalDeleter); ok {
		switch err := c.DeleteIf(ctx, id, prev); {
		case err == nil || errors.Is(err, ErrNotFound):
			return true, nil
		case errors.Is(err, ErrConflict):
			return false, nil
		case !errors.Is(err, ErrConditionalUnsupported):
			return false, err
		}
	}
	// no conditional delete: claim with a conditional write first
	if c, ok := store.(ConditionalAdapter); ok {
		blank, _ := json.Marshal(limitState{})
		switch err := c.SaveIf(ctx, id, blank, prev); {
		case errors.Is(err, ErrConflict):
			return false, nil
		case err != nil && !errors.Is(err, ErrConditionalUnsupported):
			return false, err
		}
	}
	if err := store.Delete(ctx, id); err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}
	return true, nil
}

// RunRateLimitSweep calls SweepRateLimits periodically.
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
			done := observe.Default().Sweep("ratelimit")
			st, err := SweepRateLimitsStats(ctx, store, time.Now(), MaxRateLimitRecords)
			done(st.Deleted, err)
			if err != nil && ctx.Err() == nil {
				log.Printf("docstore: sweeping rate limit records: %v", err)
			}
			if st.EvictedLive > 0 {
				log.Printf("docstore: the rate limit records hit their cap (%d): %d callers with budget still spent got a fresh bucket (a flood of different callers?)", MaxRateLimitRecords, st.EvictedLive)
			}
		}
	}
}
