// Package sessionstore is an auth.SessionStore kept in a docstore.Adapter, so replicas that share
// the store share their sign-in sessions: a token issued by one is valid on the others, a logout on
// one ends the session on all, and an OpenID Connect login begun on one can finish on another.
package sessionstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

// The keyspaces: "auth/session/<expiry hour>/<hash of the token>" and
// "auth/state/<expiry minute>/<hash of the token>". The token itself is never in a record name or
// body: someone who can list or read the store cannot sign in with what they see. The number in
// the name is the hour (minute, for a state) at which the record is certain to have expired, so the
// sweep deletes by name alone, without reading a record. The document API refuses both prefixes,
// expiry gives them no deadline and IsDocumentKey rejects them.
const (
	SessionPrefix    = docstore.SessionPrefix
	LoginStatePrefix = docstore.LoginStatePrefix
)

// ErrNotConditional is returned by New for a store that cannot write conditionally: without a
// compare-and-set no two replicas can agree on who consumed a login state.
var ErrNotConditional = errors.New("sessionstore: a shared session store needs a store with conditional writes (docstore.ConditionalAdapter)")

const (
	// DefaultMaxSessions bounds the live sessions (the in-memory store's bound).
	DefaultMaxSessions = 100000
	// DefaultMaxStates bounds the pending logins. Smaller than the in-memory bound: every
	// /auth/<provider>/login is an unauthenticated request that here is a write to the store.
	DefaultMaxStates = 10000

	sessionBucket = time.Hour
	stateBucket   = time.Minute
	// sweepGrace is how long past a record's bucket the sweep waits (replicas' clocks differ).
	sweepGrace = time.Minute

	recordVersion = 1

	// maxTTL caps how far ahead a record may expire (a hundred years). Bucket numbers are seconds
	// since the epoch divided by the bucket width, so a huge SessionTTL can neither overflow them
	// nor make a token that is issued but never validates.
	maxTTL = 100 * 365 * 24 * time.Hour
	// maxUnix is the largest second a bucket may stand for (year 36812), far beyond maxTTL: a name
	// with a bigger number is not one this store made.
	maxUnix = 1 << 40

	// sessionListEvery is how often RunSweep lists the sessions when the hour has not changed.
	sessionListEvery = 10 * time.Minute
)

// Store is an auth.SessionStore over a docstore.Adapter.
//
//   - PutSession and PutState create one record with a conditional create (SaveIf with no previous
//     digest); a record that exists is a 128-bit collision: another token is tried, nothing is
//     overwritten.
//   - GetSession loads the record by the hash of the token and checks the stored Expires on this
//     replica's clock. There is no cache: every authenticated request is one read of the store, and
//     a logout (DeleteSession) is therefore seen by the very next request on any replica. A cache
//     would make a logged-out token work for as long as it lives.
//   - Expiry is fixed when the session is made (as in the in-memory store): a request does not
//     extend it, so Config.SessionTTL is also the longest a session can live.
//   - TakeState is single use across processes. It loads the record, then consumes it with one
//     conditional step on the digest it read: DeleteIf when the store is a
//     docstore.ConditionalDeleter, else SaveIf that replaces it with a "used" marker (deleted right
//     after, and by the sweep if that fails). Exactly one of any number of concurrent calls, on any
//     replica, gets ok=true; a replay finds nothing, or the marker, and gets ok=false.
//   - Sweep deletes by name the records whose expiry hour (minute) is past, reading none of them,
//     and resets the counts.
//   - The numbers are bounded: at MaxSessions (MaxStates) a Put returns auth.ErrStoreFull at once,
//     without touching the store; a live record is never evicted to make room. Each replica counts
//     from its last listing (the sweep) plus what it has written and removed, so replicas may
//     overshoot by what they write between two sweeps, never grow without limit.
//
// Under an encrypting adapter the records are encrypted like any document file (they are not in
// encrypt.DefaultSkip: they hold names, e-mail addresses and the PKCE verifier). The adapter has
// no DeleteIf for an encrypted id, so TakeState uses the marker path there.
//
// Clocks: each replica compares Expires with its own clock; a replica whose clock is ahead refuses
// a session a little early, one that is behind accepts it a little late, by the skew.
type Store struct {
	Store docstore.Adapter
	// MaxSessions and MaxStates are the bounds (DefaultMaxSessions and DefaultMaxStates if not
	// positive).
	MaxSessions, MaxStates int
	// Now is the clock (time.Now if nil), for tests.
	Now func() time.Time
	// MissFor is how long a token the store did not know is remembered as unknown by this
	// process (DefaultMissFor if 0; a negative value turns it off). A flood of random bearer tokens
	// then costs one read per token per MissFor instead of one per request. A session made on another
	// replica is therefore seen here at most MissFor after it was made; a session made by this
	// process is seen at once.
	MissFor time.Duration
	// CacheFor, if positive, is how long this process remembers that a token is a live session
	// (off by default: every request reads the store). A logout on this process takes effect here at
	// once, on another replica up to CacheFor later: set it only where that is acceptable, and say so
	// to the people who run the service.
	CacheFor time.Duration

	cacheMu                    sync.Mutex
	hits                       map[string]cachedSession // live sessions seen, by record name (CacheFor)
	misses                     map[string]time.Time     // unknown tokens, by record name, until when (MissFor)
	reads, cacheHits, missHits atomic.Int64

	sessions, states atomic.Int64 // records: the last listing's count plus this process's Puts less its removals
	sessionsNet      atomic.Int64 // Puts less removals of this process, never reset: Sweep keeps what moved during its listing
	statesNet        atomic.Int64
	sweeping         sync.Mutex
}

// New makes an auth.SessionStore over store, which must be a docstore.ConditionalAdapter
// (ErrNotConditional otherwise); use docstore.SupportsConditional first to know the store really
// honours it. Run RunSweep for the expired records.
func New(store docstore.Adapter) (*Store, error) {
	if _, ok := store.(docstore.ConditionalAdapter); !ok {
		return nil, ErrNotConditional
	}
	return &Store{Store: store}, nil
}

var _ auth.SessionStore = (*Store)(nil)

func (s *Store) clock() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) cas() (docstore.ConditionalAdapter, error) {
	c, ok := s.Store.(docstore.ConditionalAdapter)
	if !ok {
		return nil, ErrNotConditional
	}
	return c, nil
}

// kind is one of the two keyspaces.
type kind struct {
	prefix string
	bucket time.Duration
}

var (
	sessionKind = kind{SessionPrefix, sessionBucket}
	stateKind   = kind{LoginStatePrefix, stateBucket}
)

func (s *Store) count(k kind) *atomic.Int64 {
	if k.prefix == SessionPrefix {
		return &s.sessions
	}
	return &s.states
}

func (s *Store) max(k kind) int64 {
	if k.prefix == SessionPrefix {
		if s.MaxSessions > 0 {
			return int64(s.MaxSessions)
		}
		return DefaultMaxSessions
	}
	if s.MaxStates > 0 {
		return int64(s.MaxStates)
	}
	return DefaultMaxStates
}

func (s *Store) net(k kind) *atomic.Int64 {
	if k.prefix == SessionPrefix {
		return &s.sessionsNet
	}
	return &s.statesNet
}

func dec(c *atomic.Int64) {
	if c.Add(-1) < 0 {
		c.Store(0)
	}
}

// bucketOf is the number of the bucket in which expires falls, rounded up: the record is certain
// to have expired at bucketEnd(bucket).
// It counts in seconds (nanoseconds overflow int64 past the year 2262).
func (k kind) bucketOf(expires time.Time) int64 {
	b := int64(k.bucket / time.Second)
	n := expires.Unix()
	if expires.Nanosecond() > 0 {
		n++
	}
	return (n + b - 1) / b
}

func (k kind) bucketEnd(bucket int64) time.Time {
	return time.Unix(bucket*int64(k.bucket/time.Second), 0)
}

// validBucket reports whether n is a bucket number this store could have made.
func (k kind) validBucket(n int64) bool {
	return n >= 0 && n <= maxUnix/int64(k.bucket/time.Second)
}

// expiry is when a record made now with ttl ends, with ttl kept within [0, maxTTL].
func (s *Store) expiry(now time.Time, ttl time.Duration) time.Time {
	return now.Add(min(max(ttl, 0), maxTTL))
}

// newToken is "<256 random bits>.<bucket>". The bucket is only a hint where the record is (and,
// for the sweep, when it is surely dead): the authority on expiry is the record's Expires.
func (k kind) newToken(bucket int64) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b) + "." + strconv.FormatInt(bucket, 10), nil
}

// id is the record name of token, or "" if token is not one this store could have made.
func (k kind) id(token string) string {
	i := strings.LastIndexByte(token, '.')
	// The secret is base64url, which has no ".": a second dot is not a token of ours.
	if i <= 0 || i == len(token)-1 || len(token) > 128 || strings.IndexByte(token, '.') != i {
		return ""
	}
	bucket := token[i+1:]
	if n, err := strconv.ParseInt(bucket, 10, 64); err != nil || !k.validBucket(n) || strconv.FormatInt(n, 10) != bucket {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return k.prefix + bucket + "/" + hex.EncodeToString(sum[:])
}

// idBucket is the bucket in a record name made by id, or false.
func (k kind) idBucket(id string) (int64, bool) {
	rest, ok := strings.CutPrefix(id, k.prefix)
	if !ok {
		return 0, false
	}
	b, hash, ok := strings.Cut(rest, "/")
	if !ok || !isHash(hash) {
		return 0, false
	}
	n, err := strconv.ParseInt(b, 10, 64)
	return n, err == nil && k.validBucket(n) && strconv.FormatInt(n, 10) == b
}

// sessionRecord is what a session record holds: the user and when the session was made and ends.
// No secret: the token is not in it.
type sessionRecord struct {
	V        int       `json:"v"`
	User     auth.User `json:"user"`
	IssuedAt time.Time `json:"issuedAt"`
	Expires  time.Time `json:"expires"`
}

// stateRecord is a login state, or, once consumed by a store without DeleteIf, a marker that it
// was used.
type stateRecord struct {
	V       int             `json:"v"`
	State   auth.LoginState `json:"state"`
	Expires time.Time       `json:"expires"`
	Used    bool            `json:"used,omitempty"`
}

// put creates a record under a fresh token with a conditional create.
func (s *Store) put(ctx context.Context, k kind, expires time.Time, body func() ([]byte, error)) (string, error) {
	cas, err := s.cas()
	if err != nil {
		return "", err
	}
	c := s.count(k)
	if c.Load() >= s.max(k) {
		return "", auth.ErrStoreFull // the sweep (or a removal) makes room; never a scan in the request
	}
	data, err := body()
	if err != nil {
		return "", err
	}
	bucket := k.bucketOf(expires)
	for range 3 { // a name that exists is a collision: try another token, never overwrite
		token, err := k.newToken(bucket)
		if err != nil {
			return "", err
		}
		switch err := cas.SaveIf(ctx, k.id(token), data, ""); {
		case err == nil:
			c.Add(1)
			s.net(k).Add(1)
			return token, nil
		case errors.Is(err, docstore.ErrConflict):
			continue
		default:
			return "", err
		}
	}
	return "", docstore.ErrConflict
}

// PutSession implements auth.SessionStore.
func (s *Store) PutSession(ctx context.Context, u auth.User, ttl time.Duration) (string, error) {
	now := s.clock()
	expires := s.expiry(now, ttl)
	token, err := s.put(ctx, sessionKind, expires, func() ([]byte, error) {
		return json.Marshal(sessionRecord{V: recordVersion, User: u, IssuedAt: now, Expires: expires})
	})
	if err == nil {
		s.forget(sessionKind.id(token)) // a session made here is seen here at once
	}
	return token, err
}

// DefaultMissFor is how long an unknown token is remembered when MissFor is 0.
const DefaultMissFor = time.Second

// maxCached bounds each of the two in-process maps; at the bound the map is emptied (a flood costs
// reads, never memory).
const maxCached = 4096

type cachedSession struct {
	user    auth.User
	expires time.Time // the session's own expiry
	until   time.Time // when this entry stops being used
}

// Stats are the reads this process made of the store for GetSession and what the two caches saved.
type Stats struct{ Reads, CacheHits, MissHits int64 }

// Stats returns the counters, for the operator to judge whether the cost matters.
func (s *Store) Stats() Stats {
	return Stats{Reads: s.reads.Load(), CacheHits: s.cacheHits.Load(), MissHits: s.missHits.Load()}
}

func (s *Store) missFor() time.Duration {
	switch {
	case s.MissFor < 0:
		return 0
	case s.MissFor == 0:
		return DefaultMissFor
	}
	return s.MissFor
}

// cached reports what this process remembers of the token: a live session, or that it is unknown.
func (s *Store) cached(id string, now time.Time) (user auth.User, live, unknown bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if c, ok := s.hits[id]; ok {
		if now.Before(c.until) && now.Before(c.expires) {
			return c.user, true, false
		}
		delete(s.hits, id)
	}
	if until, ok := s.misses[id]; ok {
		if now.Before(until) {
			return auth.User{}, false, true
		}
		delete(s.misses, id)
	}
	return auth.User{}, false, false
}

func (s *Store) remember(id string, c *cachedSession, missUntil time.Time) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if c != nil {
		if s.hits == nil || len(s.hits) >= maxCached {
			s.hits = map[string]cachedSession{}
		}
		s.hits[id] = *c
		return
	}
	if s.misses == nil || len(s.misses) >= maxCached {
		s.misses = map[string]time.Time{}
	}
	s.misses[id] = missUntil
}

func (s *Store) forget(id string) {
	s.cacheMu.Lock()
	delete(s.hits, id)
	delete(s.misses, id)
	s.cacheMu.Unlock()
}

// GetSession implements auth.SessionStore.
func (s *Store) GetSession(ctx context.Context, token string) (auth.User, bool, error) {
	id := sessionKind.id(token)
	if id == "" {
		return auth.User{}, false, nil
	}
	now := s.clock()
	if s.CacheFor > 0 || s.missFor() > 0 {
		switch user, live, unknown := s.cached(id, now); {
		case live:
			s.cacheHits.Add(1)
			return user, true, nil
		case unknown:
			s.missHits.Add(1)
			return auth.User{}, false, nil
		}
	}
	s.reads.Add(1)
	doc, err := s.Store.Load(ctx, id)
	if errors.Is(err, docstore.ErrNotFound) {
		if m := s.missFor(); m > 0 {
			s.remember(id, nil, now.Add(m))
		}
		return auth.User{}, false, nil
	}
	if err != nil {
		return auth.User{}, false, err
	}
	var rec sessionRecord
	if json.Unmarshal(doc.Data, &rec) != nil || rec.V != recordVersion || rec.User.ID == "" {
		return auth.User{}, false, nil // not ours: the sweep removes it
	}
	if !now.Before(rec.Expires) {
		return auth.User{}, false, nil // expired; the sweep deletes it
	}
	if s.CacheFor > 0 {
		s.remember(id, &cachedSession{user: rec.User, expires: rec.Expires, until: now.Add(s.CacheFor)}, time.Time{})
	}
	return rec.User, true, nil
}

// DeleteSession implements auth.SessionStore.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	id := sessionKind.id(token)
	if id == "" {
		return nil
	}
	s.forget(id) // a logout takes effect here at once, whatever CacheFor says
	switch err := s.Store.Delete(ctx, id); {
	case err == nil:
		dec(&s.sessions)
		s.sessionsNet.Add(-1)
		return nil
	case errors.Is(err, docstore.ErrNotFound):
		return nil
	default:
		return err
	}
}

// PutState implements auth.SessionStore.
func (s *Store) PutState(ctx context.Context, st auth.LoginState, ttl time.Duration) (string, error) {
	expires := s.expiry(s.clock(), ttl)
	return s.put(ctx, stateKind, expires, func() ([]byte, error) {
		return json.Marshal(stateRecord{V: recordVersion, State: st, Expires: expires})
	})
}

// TakeState implements auth.SessionStore.
func (s *Store) TakeState(ctx context.Context, token string) (auth.LoginState, bool, error) {
	cas, err := s.cas()
	if err != nil {
		return auth.LoginState{}, false, err
	}
	id := stateKind.id(token)
	if id == "" {
		return auth.LoginState{}, false, nil
	}
	doc, err := s.Store.Load(ctx, id)
	if errors.Is(err, docstore.ErrNotFound) {
		return auth.LoginState{}, false, nil
	}
	if err != nil {
		return auth.LoginState{}, false, err
	}
	var rec stateRecord
	if json.Unmarshal(doc.Data, &rec) != nil || rec.V != recordVersion || rec.Used {
		return auth.LoginState{}, false, nil // consumed by another replica, or not ours: the sweep removes it
	}
	expired := !s.clock().Before(rec.Expires)
	won, err := s.consume(ctx, cas, id, docstore.Digest(doc.Data), rec)
	if err != nil {
		return auth.LoginState{}, false, err
	}
	if !won || expired {
		// An expired record is consumed too (deleted), so it is gone at once, but it is refused.
		return auth.LoginState{}, false, nil
	}
	return rec.State, true, nil
}

// consume removes the record whose content has digest prev with one conditional step and reports
// whether this caller was the one that did it.
func (s *Store) consume(ctx context.Context, cas docstore.ConditionalAdapter, id, prev string, rec stateRecord) (bool, error) {
	if d, ok := s.Store.(docstore.ConditionalDeleter); ok {
		switch err := d.DeleteIf(ctx, id, prev); {
		case err == nil:
			dec(&s.states)
			s.statesNet.Add(-1)
			return true, nil
		case errors.Is(err, docstore.ErrNotFound), errors.Is(err, docstore.ErrConflict):
			return false, nil
		case !errors.Is(err, docstore.ErrConditionalUnsupported):
			return false, err
		}
	}
	// No conditional delete (an encrypting adapter has none for an encrypted id): claim it by
	// replacing it, conditionally, with a used marker that keeps nothing of the state.
	marker, _ := json.Marshal(stateRecord{V: recordVersion, Expires: rec.Expires, Used: true})
	switch err := cas.SaveIf(ctx, id, marker, prev); {
	case err == nil:
	case errors.Is(err, docstore.ErrConflict):
		return false, nil
	default:
		return false, err
	}
	dec(&s.states)
	s.statesNet.Add(-1)
	// Only the winner gets here, and nobody else writes a marker: a plain delete is safe. If it
	// fails the marker stays refusing, and the sweep deletes it by its name.
	_ = s.Store.Delete(ctx, id)
	return true, nil
}

// Sweep deletes the records whose expiry bucket ended more than a minute ago, sessions and login
// states (used markers included: they carry their state's bucket), and sets the counts to what is
// left. It lists the two keyspaces and reads no record; a name that is not one this store made is
// left alone. It returns how many it deleted. Several sweeps at once (replicas) are harmless; in
// one process they run one at a time.
func (s *Store) Sweep(ctx context.Context) (int, error) {
	n, _, err := s.sweep(ctx, true)
	return n, err
}

// sweep is Sweep, listing the sessions only if sessions is true. It reports whether the sessions
// were listed without an error.
func (s *Store) sweep(ctx context.Context, sessions bool) (total int, sessionsListed bool, err error) {
	s.sweeping.Lock()
	defer s.sweeping.Unlock()
	now := s.clock()
	kinds := []kind{stateKind}
	if sessions {
		kinds = []kind{sessionKind, stateKind}
	}
	var errs []error
	for _, k := range kinds {
		metas, err := docstore.ListPrefix(ctx, s.Store, k.prefix)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		left := 0
		before := s.net(k).Load()
		for _, m := range metas {
			if err := ctx.Err(); err != nil {
				return total, false, err
			}
			b, ok := k.idBucket(m.ID)
			if !ok || now.Before(k.bucketEnd(b).Add(sweepGrace)) {
				left++
				continue
			}
			switch err := s.Store.Delete(ctx, m.ID); {
			case err == nil || errors.Is(err, docstore.ErrNotFound):
				total++
			default:
				errs = append(errs, err)
				left++
			}
		}
		// Keep what this process put or removed while the listing ran: the listing may predate it.
		s.count(k).Store(max(int64(left)+s.net(k).Load()-before, 0))
		if k.prefix == SessionPrefix {
			sessionsListed = true
		}
	}
	return total, sessionsListed, errors.Join(errs...)
}

// sweepSchedule decides when the sessions are listed. Their buckets are an hour wide and their
// records are only ever deleted by name once the bucket is over, so listing the whole keyspace
// every minute (up to a hundred LIST calls a minute on an object store, on every replica) buys
// nothing: they are listed at the first sweep, when the hour has changed since the last listing,
// and at least every sessionListEvery. The login states (a minute wide, and the bound on them is
// what an unauthenticated flood presses on) are listed at every sweep.
type sweepSchedule struct {
	listed bool
	hour   int64
	at     time.Time
}

func (c *sweepSchedule) sessionsDue(now time.Time) bool {
	return !c.listed || now.Unix()/int64(sessionBucket/time.Second) != c.hour || now.Sub(c.at) >= sessionListEvery || now.Before(c.at)
}

func (c *sweepSchedule) sessionsListed(now time.Time) {
	c.listed, c.hour, c.at = true, now.Unix()/int64(sessionBucket/time.Second), now
}

// step is one tick of RunSweep: a sweep that lists the sessions only when c says so.
func (s *Store) step(ctx context.Context, c *sweepSchedule) {
	now := s.clock()
	_, listed, err := s.sweep(ctx, c.sessionsDue(now))
	if listed {
		c.sessionsListed(now)
	}
	if err != nil && ctx.Err() == nil {
		log.Printf("sessionstore: sweeping shared sessions: %v", err)
	}
}

// RunSweep sweeps at once and then every interval (a minute when not positive) until ctx ends;
// errors are logged. The login states are listed at every sweep; the sessions at the first, when
// the hour changes and at least every ten minutes (see sweepSchedule). The first sweep is what
// sets the counts from the store at start-up.
func (s *Store) RunSweep(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	var sched sweepSchedule
	s.step(ctx, &sched)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.step(ctx, &sched)
		}
	}
}

// ParseMode parses GOJIBERRY_DOCSTORE_SESSIONS: "" or "memory" (sessions in each process), "store"
// (kept in the store, shared by replicas; on a store without conditional writes the server warns
// and uses memory) or "store-required" (as "store", but the server does not start without
// conditional writes). It returns the canonical name.
func ParseMode(raw string) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(raw)); m {
	case "", "memory":
		return "memory", nil
	case "store", "store-required":
		return m, nil
	}
	return "", errors.New("parsing GOJIBERRY_DOCSTORE_SESSIONS \"" + raw + "\": want \"memory\", \"store\" or \"store-required\"")
}

// isHash says s is a lowercase hex SHA-256: the end of every name this store makes.
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
