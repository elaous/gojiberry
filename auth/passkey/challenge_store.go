package passkey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/elaous/gojiberry/observe"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// ChallengePrefix is the keyspace of the ceremonies a StoreChallenges keeps:
// "passkey/challenge/<expiry minute>/<hash of the token>". It is inside "passkey/", so the document
// API hides it (Server.Hide), expiry never gives it a deadline (it is a system keyspace) and
// IsDocumentKey rejects it. The token itself is never in a record name: someone who can list the
// store cannot finish a ceremony with what they see. The number in the name is the minute (seconds
// since the epoch divided by 60, rounded up) at which the ceremony is certain to have expired, so the
// sweep deletes by name alone, without reading a record, as auth/sessionstore does.
//
// Records of the earlier layout, "passkey/challenge/<hash>" (no minute, the hash 64 lowercase hex
// characters), are not used any more: the sweep removes them once they are older than a day
// (oldChallengeAge), without counting them (see the upgrade note in docs/roadmap.md). Any other name
// under the prefix is foreign and is never touched.
const ChallengePrefix = "passkey/challenge/"

// ErrNotConditional is returned by NewStoreChallenges for a store that cannot write conditionally:
// without a compare-and-set no two replicas can agree on who consumed a challenge.
var ErrNotConditional = errors.New("passkey: a shared challenge store needs a store with conditional writes (docstore.ConditionalAdapter)")

const (
	// storeChallengeGrace is how long past its bucket a record is left before the sweep deletes it
	// (replicas' clocks differ; Take already refuses an expired one on its own clock).
	storeChallengeGrace = time.Minute

	challengeBucket = time.Minute
	// maxChallengeTTL caps how far ahead a ceremony may expire (a hundred years), so a huge
	// Expires can neither overflow a bucket number nor make a token that never validates.
	maxChallengeTTL = 100 * 365 * 24 * time.Hour
	// maxChallengeUnix is the largest second a bucket may stand for (year 36812).
	maxChallengeUnix = 1 << 40
	// maxTokenLen bounds a token before it is hashed or parsed (ours are about 40 bytes).
	maxTokenLen = 128
	// oldChallengeAge is how old (by the store's modification time) a record of the earlier layout
	// must be before the sweep deletes it; a ceremony lives about five minutes, so a day is certain.
	oldChallengeAge = 24 * time.Hour
)

// StoreChallenges is a ChallengeStore kept in a docstore.Adapter, so replicas that share the
// store share their ceremonies: a challenge begun on one finishes on another.
//
//   - Put creates one record with a conditional create (SaveIf with no previous digest) under a
//     fresh token "<192 random bits>.<expiry minute>". The minute is a hint where the record is
//     and when it is surely dead; the authority on expiry is the record's Expires.
//   - Take is single use across processes. A token that is not exactly one this store makes
//     (canonical digits, one dot, at most 128 bytes, a minute in range) is refused without
//     touching the store. Otherwise it reads the record, then consumes it with one conditional step
//     on the digest it read: DeleteIf when the store is a docstore.ConditionalDeleter, else SaveIf
//     that replaces it with a "used" marker (same name, so the same bucket). Exactly one of any
//     number of concurrent Takes (here or on other replicas) gets nil from that step; the others see
//     a conflict or a missing record and report ok=false. The marker is deleted right after, and by
//     the sweep if that fails.
//   - Expiry is the Expires time stored in the record, checked on every Take, plus a sweep (Sweep,
//     StartChallengeSweep) that lists the keyspace and deletes by name what is past its minute and a
//     minute of grace: expired ceremonies, used markers and junk alike, reading none of them.
//   - The number waiting is bounded: at Max (10,000 by default) Put returns ErrBusy at once, without
//     touching the store (no sweep and no Load in the request); room comes back with the background
//     sweep (every minute in the server) or a Take. A live challenge is never evicted to make room:
//     that would let a flood cancel real ceremonies.
//
// # The cap across replicas
//
// Three strategies were weighed for keeping the bound honest with several replicas:
//
//  1. Count names from the prefix listing the sweep already makes, each replica adding what it has
//     written and removing what it has taken since. Cost: one LIST a minute per replica, no reads.
//     Weakness: replicas do not see each other's writes between two listings, so the store can
//     exceed Max by what the other replicas write in one refresh interval.
//  2. Sharded counter records moved by compare-and-set. Exact only up to the contention it creates:
//     every Begin becomes a CAS on a hot record (retries under load, one more write per ceremony and
//     per Take, and a crashed replica leaks its increments), a cost paid on every request to remove
//     an overshoot that is already small.
//  3. Rely on the per-client Limiter on the Begin routes as the real bound and keep the cap as a
//     backstop. A client cannot write faster than its budget, whatever the replica.
//
// This store implements (3) plus (1). The worst-case overshoot of Max is the number of ceremonies
// all replicas can write in one refresh interval: (Limiter rate per client per minute) x (distinct
// clients) x (sweep interval in minutes), and never more than (replicas - 1) x Max. With the default
// limiter (30 a minute on average per client and route group, burst 20) and the one-minute sweep a
// client can write about 50 records in the first interval and 30 in each later one, over the cap;
// with a per-process limiter (GOJIBERRY_DOCSTORE_LIMITER=memory) every replica gives a client its own
// budget, so that is per replica. The growth stops at the next listing, because every replica then
// sees the true count and answers ErrBusy. Records that expire still go on their own: the store
// never holds more than the writes of the last ceremony life (about five minutes) plus the sweep
// lag, whatever happens.
//
// Clocks: each replica compares Expires with its own clock; a replica whose clock is ahead refuses
// a challenge a little early, one that is behind accepts it a little late, by the skew.
type StoreChallenges struct {
	Store docstore.Adapter
	// Max is how many ceremonies may wait (10,000 if not positive).
	Max int
	// Now is the clock (time.Now if nil), for tests.
	Now func() time.Time

	// approx is the waiting records: the last listing's count plus this process's Puts less its Takes. It
	// is a bound, not an exact count: a Put or Take that straddles the reads Sweep makes before and after
	// its listing, or a Put between net.Load and approx.Store, is counted once too many or too few. The
	// drift is at most the operations in that window, and the next listing repairs it.
	approx   atomic.Int64
	net      atomic.Int64 // Puts less Takes of this process, never reset: Sweep keeps what moved during its listing
	sweeping sync.Mutex
}

// NewStoreChallenges makes a ChallengeStore over store, holding at most max ceremonies (10,000 if
// max <= 0). store must be a docstore.ConditionalAdapter (ErrNotConditional otherwise); use
// docstore.SupportsConditional first to know the store really honours it. Run StartChallengeSweep
// (or RunChallengeSweep, which blocks and does not retry) for the expired records and the count.
func NewStoreChallenges(store docstore.Adapter, max int) (*StoreChallenges, error) {
	if _, ok := store.(docstore.ConditionalAdapter); !ok {
		return nil, ErrNotConditional
	}
	return &StoreChallenges{Store: store, Max: max}, nil
}

func (s *StoreChallenges) clock() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *StoreChallenges) max() int {
	if s.Max > 0 {
		return s.Max
	}
	return maxChallenges
}

func (s *StoreChallenges) cas() (docstore.ConditionalAdapter, error) {
	c, ok := s.Store.(docstore.ConditionalAdapter)
	if !ok {
		return nil, ErrNotConditional
	}
	return c, nil
}

// bucketOf is the number of the minute in which expires falls, rounded up: the record is certain to
// have expired at bucketEnd(bucket). It counts in seconds (nanoseconds overflow int64 past 2262).
func bucketOf(expires time.Time) int64 {
	b := int64(challengeBucket / time.Second)
	n := expires.Unix()
	if expires.Nanosecond() > 0 {
		n++
	}
	return (n + b - 1) / b
}

func bucketEnd(bucket int64) time.Time {
	return time.Unix(bucket*int64(challengeBucket/time.Second), 0)
}

func validBucket(n int64) bool {
	return n >= 0 && n <= maxChallengeUnix/int64(challengeBucket/time.Second)
}

// newToken is "<192 random bits>.<bucket>".
func newToken(bucket int64) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b) + "." + strconv.FormatInt(bucket, 10), nil
}

// challengeID is the record name of token, or "" if token is not one this store could have made.
func challengeID(token string) string {
	i := strings.LastIndexByte(token, '.')
	// The secret is base64url, which has no ".": a second dot is not a token of ours.
	if i <= 0 || i == len(token)-1 || len(token) > maxTokenLen || strings.IndexByte(token, '.') != i {
		return ""
	}
	bucket := token[i+1:]
	if n, err := strconv.ParseInt(bucket, 10, 64); err != nil || !validBucket(n) || strconv.FormatInt(n, 10) != bucket {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return ChallengePrefix + bucket + "/" + hex.EncodeToString(sum[:])
}

// isHash reports whether s is exactly 64 lowercase hex characters (a SHA-256 as challengeID writes it).
func isHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// idBucket is the bucket in a record name made by challengeID, or false: the name must be exactly
// "<prefix><canonical bucket>/<64 lowercase hex>", anything else is not ours.
func idBucket(id string) (int64, bool) {
	rest, ok := strings.CutPrefix(id, ChallengePrefix)
	if !ok {
		return 0, false
	}
	b, hash, ok := strings.Cut(rest, "/")
	if !ok || !isHash(hash) {
		return 0, false
	}
	n, err := strconv.ParseInt(b, 10, 64)
	return n, err == nil && validBucket(n) && strconv.FormatInt(n, 10) == b
}

// isOldID reports whether id is a record of the earlier layout: the prefix and exactly a hash.
func isOldID(id string) bool {
	rest, ok := strings.CutPrefix(id, ChallengePrefix)
	return ok && isHash(rest)
}

// storedChallenge is a record: the ceremony, or, once consumed by a store without DeleteIf, a
// marker that it was used.
type storedChallenge struct {
	Challenge
	Used bool `json:"used,omitempty"`
}

// Put implements ChallengeStore.
func (s *StoreChallenges) Put(ctx context.Context, c Challenge) (string, error) {
	cas, err := s.cas()
	if err != nil {
		return "", err
	}
	if s.approx.Load() >= int64(s.max()) {
		return "", ErrBusy // the background sweep (or a Take) makes room; never a scan in the request
	}
	now := s.clock()
	if limit := now.Add(maxChallengeTTL); c.Expires.After(limit) {
		c.Expires = limit
	}
	bucket := bucketOf(c.Expires)
	if c.Expires.Before(now) { // already expired: it still needs a valid name
		bucket = bucketOf(now)
	}
	data, err := json.Marshal(storedChallenge{Challenge: c})
	if err != nil {
		return "", err
	}
	for range 3 { // a name that exists is a collision: try another token, never overwrite
		token, err := newToken(bucket)
		if err != nil {
			return "", err
		}
		switch err := cas.SaveIf(ctx, challengeID(token), data, ""); {
		case err == nil:
			s.net.Add(1)
			s.approx.Add(1)
			return token, nil
		case errors.Is(err, docstore.ErrConflict):
			continue
		default:
			return "", err
		}
	}
	return "", docstore.ErrConflict
}

// Take implements ChallengeStore.
func (s *StoreChallenges) Take(ctx context.Context, token string) (Challenge, bool, error) {
	cas, err := s.cas()
	if err != nil {
		return Challenge{}, false, err
	}
	id := challengeID(token)
	if id == "" {
		return Challenge{}, false, nil // not a token this store made: nothing to look up
	}
	doc, err := s.Store.Load(ctx, id)
	if errors.Is(err, docstore.ErrNotFound) {
		return Challenge{}, false, nil
	}
	if err != nil {
		return Challenge{}, false, err
	}
	prev := docstore.Digest(doc.Data)
	var sc storedChallenge
	if json.Unmarshal(doc.Data, &sc) != nil || sc.Used {
		return Challenge{}, false, nil // consumed by another replica, or not ours: the sweep removes it
	}
	expired := s.clock().After(sc.Expires)
	won, err := s.consume(ctx, cas, id, prev, sc)
	if err != nil {
		return Challenge{}, false, err
	}
	if !won || expired {
		// An expired record is consumed too (deleted), so it is gone at once, but it is refused.
		return Challenge{}, false, nil
	}
	return sc.Challenge, true, nil
}

// consume removes the record whose content has digest prev with one conditional step and reports
// whether this caller was the one that did it.
func (s *StoreChallenges) consume(ctx context.Context, cas docstore.ConditionalAdapter, id, prev string, sc storedChallenge) (bool, error) {
	if d, ok := s.Store.(docstore.ConditionalDeleter); ok {
		switch err := d.DeleteIf(ctx, id, prev); {
		case err == nil:
			s.dec()
			return true, nil
		case errors.Is(err, docstore.ErrNotFound), errors.Is(err, docstore.ErrConflict):
			return false, nil
		case !errors.Is(err, docstore.ErrConditionalUnsupported):
			return false, err
		}
	}
	// No conditional delete: claim it by replacing it, conditionally, with a used marker.
	marker, _ := json.Marshal(storedChallenge{Challenge: Challenge{Expires: sc.Expires}, Used: true})
	switch err := cas.SaveIf(ctx, id, marker, prev); {
	case err == nil:
	case errors.Is(err, docstore.ErrConflict):
		return false, nil
	default:
		return false, err
	}
	s.dec()
	// Only the winner gets here, and nobody writes a marker: a plain delete is safe. If it fails
	// the marker stays refusing, and the sweep deletes it.
	_ = s.Store.Delete(ctx, id)
	return true, nil
}

func (s *StoreChallenges) dec() {
	s.net.Add(-1)
	if s.approx.Add(-1) < 0 {
		s.approx.Store(0)
	}
}

// Sweep deletes by name the records whose expiry minute ended more than a minute ago (expired
// ceremonies and used markers alike), and sets the count of what is waiting to what is left (plus
// the Puts and Takes of this process that happened while it was listing). It lists the keyspace and
// reads no record of the current layout. A record of the earlier layout ("passkey/challenge/<64 hex>")
// is deleted when the store says it was last modified more than a day ago, or, if the store gives no
// modification time, when one Load shows it unparseable or expired; it is never counted. Any other
// name is foreign and left alone. It returns how many it deleted. Several sweeps at once (replicas)
// are harmless; in one process they run one at a time.
func (s *StoreChallenges) Sweep(ctx context.Context) (int, error) {
	done := observe.Default().Sweep("passkey_challenges")
	n, err := s.sweepChallenges(ctx)
	done(n, err)
	return n, err
}

func (s *StoreChallenges) sweepChallenges(ctx context.Context) (int, error) {
	s.sweeping.Lock()
	defer s.sweeping.Unlock()
	now := s.clock()
	before := s.net.Load()
	metas, err := docstore.ListPrefix(ctx, s.Store, ChallengePrefix)
	if err != nil {
		return 0, &sweepListError{err}
	}
	n, left := 0, 0
	var errs []error
	del := func(id string) bool {
		switch err := s.Store.Delete(ctx, id); {
		case err == nil || errors.Is(err, docstore.ErrNotFound):
			n++
			return true
		default:
			errs = append(errs, err)
			return false
		}
	}
	for _, m := range metas {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		b, ok := idBucket(m.ID)
		if !ok {
			if isOldID(m.ID) && s.oldIsDead(ctx, m, now) {
				del(m.ID)
			}
			continue
		}
		if now.Before(bucketEnd(b).Add(storeChallengeGrace)) {
			left++
			continue
		}
		if !del(m.ID) {
			left++
		}
	}
	// Keep what this process put or took while the listing ran: the listing may predate it.
	cur := int64(left) + s.net.Load() - before
	if cur < 0 {
		cur = 0
	}
	s.approx.Store(cur)
	return n, errors.Join(errs...)
}

// oldIsDead reports whether the record m of the earlier layout can be deleted: older than a day by
// the store's time, or, when the store gives none, unparseable or past its Expires and the grace.
func (s *StoreChallenges) oldIsDead(ctx context.Context, m docstore.Meta, now time.Time) bool {
	if !m.UpdatedAt.IsZero() {
		return now.Sub(m.UpdatedAt) > oldChallengeAge
	}
	doc, err := s.Store.Load(ctx, m.ID)
	if err != nil {
		return false // gone already, or unreadable now: the next sweep decides
	}
	var sc storedChallenge
	if json.Unmarshal(doc.Data, &sc) != nil {
		return true
	}
	return now.After(sc.Expires.Add(storeChallengeGrace))
}

// sweepListError is the error of a sweep that could not list the keyspace: nothing was counted, so
// the count is not right yet. A sweep that listed but could not delete a record returns another
// error, and its count is right.
type sweepListError struct{ err error }

func (e *sweepListError) Error() string { return e.err.Error() }
func (e *sweepListError) Unwrap() error { return e.err }

// startSweepAttempts is how many times StartChallengeSweep tries the first sweep when the listing fails.
const startSweepAttempts = 4

// RunChallengeSweep calls Sweep at once (which sets the count from the store at start-up) and then
// every interval (a minute when not positive) until ctx ends; errors are logged. The interval is
// the refresh interval of the cap (see StoreChallenges).
func (s *StoreChallenges) RunChallengeSweep(ctx context.Context, interval time.Duration) {
	if _, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
		log.Printf("passkey: sweeping shared challenges: %v", err)
	}
	s.sweepEvery(ctx, interval)
}

// StartChallengeSweep makes the first sweep before it returns, so the count is right before the
// server answers a Begin (a failed listing is retried a few times with a short backoff and logged,
// after which the count stays at what Puts add until a later sweep succeeds; a record that cannot be
// deleted is logged but not retried, since the count is right and a retry would only delay the start),
// then runs the sweep every interval in a goroutine until ctx ends.
func (s *StoreChallenges) StartChallengeSweep(ctx context.Context, interval time.Duration) {
	backoff := 500 * time.Millisecond
	for attempt := 1; ; attempt++ {
		_, err := s.Sweep(ctx)
		if err == nil || ctx.Err() != nil {
			break
		}
		var le *sweepListError
		if !errors.As(err, &le) {
			log.Printf("passkey: sweeping shared challenges at start: %v", err)
			break
		}
		log.Printf("passkey: sweeping shared challenges at start (attempt %d of %d): %v", attempt, startSweepAttempts, err)
		if attempt == startSweepAttempts {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	go s.sweepEvery(ctx, interval)
}

func (s *StoreChallenges) sweepEvery(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
				log.Printf("passkey: sweeping shared challenges: %v", err)
			}
		}
	}
}

// ParseChallengeMode parses GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES: "" or "memory" (ceremonies in
// each process), "store" (kept in the store, shared by replicas; on a store without conditional
// writes the server warns and uses memory) or "store-required" (as "store", but the server does
// not start without conditional writes). It returns the canonical name.
func ParseChallengeMode(raw string) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(raw)); m {
	case "", "memory":
		return "memory", nil
	case "store", "store-required":
		return m, nil
	}
	return "", errors.New("parsing GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES \"" + raw + "\": want \"memory\", \"store\" or \"store-required\"")
}
