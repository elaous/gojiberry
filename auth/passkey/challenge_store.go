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

// system keyspace: hidden from document API, never given a deadline
const ChallengePrefix = "passkey/challenge/"

var ErrNotConditional = errors.New("passkey: a shared challenge store needs a store with conditional writes (docstore.ConditionalAdapter)")

const (
	// replicas' clocks differ
	storeChallengeGrace = time.Minute

	challengeBucket = time.Minute
	// bounds Expires: a huge value would overflow bucket numbers
	maxChallengeTTL  = 100 * 365 * 24 * time.Hour
	maxChallengeUnix = 1 << 40
	// bounds a token before it is hashed or parsed (ours are about 40 bytes)
	maxTokenLen     = 128
	oldChallengeAge = 24 * time.Hour
)

type StoreChallenges struct {
	Store docstore.Adapter
	Max   int
	// Now is the clock (time.Now if nil), for tests.
	Now func() time.Time

	// a bound, not exact: Put and Take race Sweep
	approx   atomic.Int64
	net      atomic.Int64
	sweeping sync.Mutex
}

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

// seconds, not Duration: nanoseconds overflow int64 past 2262
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

func newToken(bucket int64) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b) + "." + strconv.FormatInt(bucket, 10), nil
}

func challengeID(token string) string {
	i := strings.LastIndexByte(token, '.')
	// base64url has no ".": a second dot is not a token of ours
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

func isOldID(id string) bool {
	rest, ok := strings.CutPrefix(id, ChallengePrefix)
	return ok && isHash(rest)
}

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
		return "", ErrBusy
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
	for range 3 {
		// an existing name is a collision: never overwrite
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
		return Challenge{}, false, nil
	}
	expired := s.clock().After(sc.Expires)
	won, err := s.consume(ctx, cas, id, prev, sc)
	if err != nil {
		return Challenge{}, false, err
	}
	if !won || expired {
		return Challenge{}, false, nil
	}
	return sc.Challenge, true, nil
}

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
	// no conditional delete: claim by conditionally replacing with a used marker
	marker, _ := json.Marshal(storedChallenge{Challenge: Challenge{Expires: sc.Expires}, Used: true})
	switch err := cas.SaveIf(ctx, id, marker, prev); {
	case err == nil:
	case errors.Is(err, docstore.ErrConflict):
		return false, nil
	default:
		return false, err
	}
	s.dec()
	_ = s.Store.Delete(ctx, id)
	return true, nil
}

func (s *StoreChallenges) dec() {
	s.net.Add(-1)
	if s.approx.Add(-1) < 0 {
		s.approx.Store(0)
	}
}

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
	// keep what this process did while listing: the listing may predate it
	cur := int64(left) + s.net.Load() - before
	if cur < 0 {
		cur = 0
	}
	s.approx.Store(cur)
	return n, errors.Join(errs...)
}

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

type sweepListError struct{ err error }

func (e *sweepListError) Error() string { return e.err.Error() }
func (e *sweepListError) Unwrap() error { return e.err }

const startSweepAttempts = 4

func (s *StoreChallenges) RunChallengeSweep(ctx context.Context, interval time.Duration) {
	if _, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
		log.Printf("passkey: sweeping shared challenges: %v", err)
	}
	s.sweepEvery(ctx, interval)
}

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

func ParseChallengeMode(raw string) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(raw)); m {
	case "", "memory":
		return "memory", nil
	case "store", "store-required":
		return m, nil
	}
	return "", errors.New("parsing GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES \"" + raw + "\": want \"memory\", \"store\" or \"store-required\"")
}
