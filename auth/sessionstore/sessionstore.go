package sessionstore

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

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

const (
	// the token is never in a record name or body
	SessionPrefix    = docstore.SessionPrefix
	LoginStatePrefix = docstore.LoginStatePrefix
)

var ErrNotConditional = errors.New("sessionstore: a shared session store needs a store with conditional writes (docstore.ConditionalAdapter)")

const (
	// DefaultMaxSessions bounds the live sessions (the in-memory store's bound).
	DefaultMaxSessions = 100000
	// DefaultMaxStates bounds the pending logins.
	DefaultMaxStates = 10000

	sessionBucket = time.Hour
	stateBucket   = time.Minute
	// replicas' clocks differ
	sweepGrace = time.Minute

	recordVersion = 1

	// bounds expiry: a huge TTL would overflow bucket numbers
	maxTTL  = 100 * 365 * 24 * time.Hour
	maxUnix = 1 << 40

	sessionListEvery = 10 * time.Minute
)

// Store is an auth.SessionStore over a docstore.Adapter.
type Store struct {
	Store                  docstore.Adapter
	MaxSessions, MaxStates int
	// Now is the clock (time.Now if nil), for tests.
	Now      func() time.Time
	MissFor  time.Duration
	CacheFor time.Duration

	cacheMu                    sync.Mutex
	hits                       map[string]cachedSession // live sessions seen, by record name (CacheFor)
	misses                     map[string]time.Time     // unknown tokens, by record name, until when (MissFor)
	reads, cacheHits, missHits atomic.Int64

	sessions, states atomic.Int64
	sessionsNet      atomic.Int64
	// never reset: Sweep keeps what moved during its listing
	statesNet atomic.Int64
	sweeping  sync.Mutex
}

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

// seconds, not Duration: nanoseconds overflow int64 past 2262
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

func (k kind) validBucket(n int64) bool {
	return n >= 0 && n <= maxUnix/int64(k.bucket/time.Second)
}

func (s *Store) expiry(now time.Time, ttl time.Duration) time.Time {
	return now.Add(min(max(ttl, 0), maxTTL))
}

func (k kind) newToken(bucket int64) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b) + "." + strconv.FormatInt(bucket, 10), nil
}

func (k kind) id(token string) string {
	i := strings.LastIndexByte(token, '.')
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

type sessionRecord struct {
	V        int       `json:"v"`
	User     auth.User `json:"user"`
	IssuedAt time.Time `json:"issuedAt"`
	Expires  time.Time `json:"expires"`
}

type stateRecord struct {
	V       int             `json:"v"`
	State   auth.LoginState `json:"state"`
	Expires time.Time       `json:"expires"`
	Used    bool            `json:"used,omitempty"`
}

func (s *Store) put(ctx context.Context, k kind, expires time.Time, body func() ([]byte, error)) (string, error) {
	cas, err := s.cas()
	if err != nil {
		return "", err
	}
	c := s.count(k)
	if c.Load() >= s.max(k) {
		return "", auth.ErrStoreFull
	}
	data, err := body()
	if err != nil {
		return "", err
	}
	bucket := k.bucketOf(expires)
	for range 3 {
		// an existing name is a collision: never overwrite
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

const DefaultMissFor = time.Second

// at the bound the map is emptied: a flood costs reads
const maxCached = 4096

type cachedSession struct {
	user    auth.User
	expires time.Time // the session's own expiry
	until   time.Time // when this entry stops being used
}

type Stats struct{ Reads, CacheHits, MissHits int64 }

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
		return auth.LoginState{}, false, nil
	}
	expired := !s.clock().Before(rec.Expires)
	won, err := s.consume(ctx, cas, id, docstore.Digest(doc.Data), rec)
	if err != nil {
		return auth.LoginState{}, false, err
	}
	if !won || expired {
		return auth.LoginState{}, false, nil
	}
	return rec.State, true, nil
}

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
	_ = s.Store.Delete(ctx, id)
	return true, nil
}

func (s *Store) Sweep(ctx context.Context) (int, error) {
	n, _, err := s.sweep(ctx, true)
	return n, err
}

func (s *Store) sweep(ctx context.Context, sessions bool) (total int, sessionsListed bool, err error) {
	done := observe.Default().Sweep("sessions")
	defer func() { done(total, err) }()
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
		s.count(k).Store(max(int64(left)+s.net(k).Load()-before, 0))
		if k.prefix == SessionPrefix {
			sessionsListed = true
		}
	}
	return total, sessionsListed, errors.Join(errs...)
}

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

func ParseMode(raw string) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(raw)); m {
	case "", "memory":
		return "memory", nil
	case "store", "store-required":
		return m, nil
	}
	return "", errors.New("parsing GOJIBERRY_DOCSTORE_SESSIONS \"" + raw + "\": want \"memory\", \"store\" or \"store-required\"")
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
