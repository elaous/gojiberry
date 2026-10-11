package docstore

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
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

// ConfigPrefix is the keyspace of per-user configuration (package userconfig): preferences, styles,
// templates. The document API refuses it and expiry leaves it alone; it is not in the encrypting
// adapter's skip list, so it is encrypted with the owner's keys like any document.
const ConfigPrefix = "config/"

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
//
// The table holds at most maxBuckets keys, in order of last use: past that the least recently
// used key is dropped, in constant time (a new key never triggers a scan of the table, so a flood
// of distinct keys costs the limiter O(1) per request, not O(table) under its mutex). Idle keys,
// those whose bucket has refilled completely, are also dropped from the least recently used end,
// at most once every sweepEvery. The flood-reset effect: a caller who floods with more than
// maxBuckets distinct keys (an attacker rotating IPv6 addresses, say) pushes the keys of busy
// legitimate callers out of the table; a caller dropped that way returns with a full bucket. The
// same holds for a StoreLimiter in its in-process fallback (store down, or no conditional writes).
// Per-caller limits are therefore a fair-use control, not a defence against an attacker with
// unlimited identities: that belongs to the gateway.
type RateLimiter struct {
	PerMinute float64
	Burst     int
	// Now is the clock (time.Now if nil), for tests.
	Now func() time.Time

	mu        sync.Mutex
	buckets   map[string]*list.Element // value: *bucket; the list is ordered by last use, newest first
	order     *list.List
	lastSweep time.Time
}

type bucket struct {
	key    string
	tokens float64
	at     time.Time // last use: the list is in this order
}

// NewRateLimiter makes a limiter allowing burst requests at once and perMinute on average.
func NewRateLimiter(perMinute float64, burst int) *RateLimiter {
	return &RateLimiter{PerMinute: perMinute, Burst: burst, buckets: map[string]*list.Element{}, order: list.New()}
}

// maxBuckets bounds the keys remembered; past it the least recently used one is dropped.
const maxBuckets = 100000

// sweepEvery is the shortest time between two sweeps of idle keys.
const sweepEvery = time.Minute

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
		for len(l.buckets) >= maxBuckets { // least recently used goes: O(1), no scan
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

// sweepLocked forgets buckets that have refilled completely (idle callers), starting from the
// least recently used end and stopping at the first one that has not: the cost is what it removes.
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
// this replica's clock refills nothing until that time has passed, but a record dated more than
// maxClockSkew ahead of this replica's clock (a replica whose clock is hours ahead) is re-dated to
// it on first sight, with its tokens, so it cannot lock the others out: skew changes the refill by
// seconds, never the burst.
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

// maxClockSkew is how far into the future a StoreLimiter believes a record's time: replicas'
// clocks differ by seconds, not by hours, and a record written by one that is far ahead would
// otherwise refill nothing for everybody else until their clocks caught up.
const maxClockSkew = 5 * time.Second

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
		if st.At > now.UnixNano()+int64(maxClockSkew) {
			// A record from a replica whose clock is far ahead must not lock the others out
			// (nothing would refill until their clocks caught up): it is re-dated to this
			// replica's clock, keeping its tokens, and the request goes on from there.
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
		at := max(st.At, now.UnixNano()) // never move a record's time backwards (within the skew)
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

// MaxRateLimitRecords is how many budget records SweepRateLimits leaves in the store: past it the
// cheapest to lose are deleted first (see SweepRateLimitsStats): records whose bucket is already full again
// carry no information, then the ones with the least spent budget. A record whose bucket is not yet full is
// a caller who gets a fresh bucket when it goes, which is the cost of bounding what a flood of different
// callers, say unauthenticated clients behind rotating addresses, can make the store hold; the sweep counts
// those evictions so an operator can see a flood reaching live callers.
const MaxRateLimitRecords = 100000

// SweepRateLimits deletes the budget records in store of callers that have been idle: whose
// bucket has been full again for a minute (a full bucket and a missing record mean the same, so
// nothing is lost), and records that cannot be read and have not been written for an hour; then,
// if more than MaxRateLimitRecords remain, the oldest. It returns how many it deleted.
//
// A record that fails to load or delete is skipped and the sweep goes on; the errors are returned
// together at the end (with the count), so one bad record cannot stop the rest from being swept.
// The delete is conditional on the record being the one that was read (ConditionalDeleter): a
// caller who came back in between keeps their update. A store without that gets a two step
// delete (the record is first replaced, conditionally, by a full bucket, which means the same as
// no record, and then deleted), which narrows the race to one token at most.
func SweepRateLimits(ctx context.Context, store Adapter, now time.Time) (int, error) {
	return SweepRateLimitsMax(ctx, store, now, MaxRateLimitRecords)
}

// SweepRateLimitsMax is SweepRateLimits with the number of records to keep chosen by the caller
// (zero or less: MaxRateLimitRecords).
func SweepRateLimitsMax(ctx context.Context, store Adapter, now time.Time, maxRecords int) (int, error) {
	st, err := SweepRateLimitsStats(ctx, store, now, maxRecords)
	return st.Deleted, err
}

// RateLimitSweep is what one sweep did.
type RateLimitSweep struct {
	Deleted     int // records deleted in all (idle ones, unreadable ones, and evictions)
	Evicted     int // of those, deleted only to get under the cap
	EvictedLive int // of the evicted, records whose bucket was not full yet: their callers got a fresh bucket
	Kept        int // records left
}

// SweepRateLimitsStats is SweepRateLimitsMax that also says what it did. Over the cap it deletes the records
// that are cheapest to lose first: the ones whose bucket is already full (nothing is lost), then the ones
// that will be full soonest (the least spent budget), so a flood of one-off callers evicts other idle or
// nearly idle records before it evicts a slow, live caller. Records that cannot be read go last.
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
		full int64 // when its bucket is full again (unix nanoseconds); the largest value if unknown
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
				if k.full != math.MaxInt64 && k.full > now.UnixNano() { // an unreadable record's date is unknown: not counted
					out.EvictedLive++
				}
			}
		}
	}
	out.Kept = len(kept) - out.Evicted
	return out, errors.Join(errs...)
}

// deleteRateLimit deletes the record id if it still holds data (a conditional delete when the
// store has one). It reports whether the record is gone; a record that changed meanwhile is kept
// (false, nil).
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
	// No conditional delete: claim the record with a conditional write first (a full bucket, the
	// same as no record), so a caller that changed it in between makes the claim conflict.
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
			st, err := SweepRateLimitsStats(ctx, store, time.Now(), MaxRateLimitRecords)
			if err != nil && ctx.Err() == nil {
				log.Printf("docstore: sweeping rate limit records: %v", err)
			}
			if st.EvictedLive > 0 {
				log.Printf("docstore: the rate limit records hit their cap (%d): %d callers with budget still spent got a fresh bucket (a flood of different callers?)", MaxRateLimitRecords, st.EvictedLive)
			}
		}
	}
}
