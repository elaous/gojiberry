package public

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/elaous/gojiberry/auth"
)

// Session is one anonymous visitor: a random user ID ("anon:<random>"), no e-mail, no profile,
// and the one document it may have. It is found by the hash of its bearer token (the token
// itself is never stored), so a copy of the store does not hold a usable token.
type Session struct {
	// ID is the random part of the user ID; User().ID is "anon:"+ID.
	ID string
	// DocID is the internal ID of the session's one document, "" until it has made one. It is
	// kept even after the document is gone, taken down or expired, until the session ends
	// (see Service: a taken-down session cannot start another).
	DocID string
	// Client is the ClientKey of the address that was given the session: what the per-address
	// cap of live sessions counts.
	Client string
	// Created is when the session was issued; it never extends the session beyond MaxAge.
	Created time.Time
	// Expires is the instant the session stops working.
	Expires time.Time
}

// User is the session as the rest of the program knows people (see package auth): the ID
// "anon:<random>" and nothing else.
func (s Session) User() auth.User { return auth.AnonymousUser(s.ID) }

// Errors of a SessionStore.
var (
	// ErrSessionsFull is returned by Create when the store holds its maximum of live sessions.
	ErrSessionsFull = errors.New("public: too many live anonymous sessions")
	// ErrClientSessionsFull is returned by Create when the session's Client already holds its
	// maximum of live sessions.
	ErrClientSessionsFull = errors.New("public: too many live anonymous sessions for this address")
	// ErrNoSession is returned by the methods that need a session that is not there (or has expired).
	ErrNoSession = errors.New("public: no such session")
)

// SessionStore keeps the live anonymous sessions. The default is NewMemorySessionStore (one
// process); replicas that must share sessions, or a global cap, plug in a store over Redis or a
// table. key is the hash of the bearer token (see TokenKey). Every method takes now so a store
// needs no clock of its own, and an expired session behaves as absent.
//
// Create must refuse (ErrSessionsFull) when the live sessions are at the cap, counting without
// the expired ones, and (ErrClientSessionsFull) when the live sessions with the same Client
// already number perClient (perClient <= 0: no per-client cap), so that one address cannot fill
// the global cap; SetDoc must be atomic (a compare-and-set), since it is what makes "one
// document per session" hold under concurrent requests.
type SessionStore interface {
	// Create stores a new session, or returns ErrSessionsFull or ErrClientSessionsFull.
	Create(ctx context.Context, key string, s Session, now time.Time, perClient int) error
	// Get returns the live session.
	Get(ctx context.Context, key string, now time.Time) (Session, bool, error)
	// SetDoc sets the session's DocID to newDoc if it is currently old (use "" for the first),
	// and reports whether it did. ErrNoSession if the session is gone.
	SetDoc(ctx context.Context, key, old, newDoc string, now time.Time) (bool, error)
	// Extend moves the session's expiry to expires (never earlier than now; the caller
	// has already applied MaxAge).
	Extend(ctx context.Context, key string, expires, now time.Time) error
	// Delete ends the session; deleting a missing one is not an error.
	Delete(ctx context.Context, key string) error
}

// SessionReaper is an optional capability of a SessionStore: Reap deletes the sessions that have
// expired (idle ones included: a session that never wrote lives only Config.SessionIdleTTL) and
// returns how many. Service.Sweep calls it, so expired sessions do not wait for the store to be full.
type SessionReaper interface {
	Reap(ctx context.Context, now time.Time) (int, error)
}

// TokenKey is the store key of a bearer token: its SHA-256 in hex.
func TokenKey(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// randomString returns n random bytes, base64url without padding.
func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// MemorySessionStore is the single-process SessionStore. Expired sessions are swept lazily when
// the store is busy and by Create at the cap, so a flood of sessions that are never used again
// cannot fill it for longer than their lifetime.
type MemorySessionStore struct {
	// Max is the cap on live sessions (0: DefaultMaxSessions).
	Max int

	mu       sync.Mutex
	sessions map[string]Session
	byClient map[string]int // live-or-unswept sessions per Client
}

// DefaultMaxSessions is the live-session cap when none is set.
const DefaultMaxSessions = 10000

// NewMemorySessionStore makes a store holding at most max live sessions (0: DefaultMaxSessions).
func NewMemorySessionStore(max int) *MemorySessionStore {
	return &MemorySessionStore{Max: max, sessions: map[string]Session{}, byClient: map[string]int{}}
}

func (m *MemorySessionStore) cap() int {
	if m.Max > 0 {
		return m.Max
	}
	return DefaultMaxSessions
}

// sweepLocked drops the expired sessions and returns how many.
func (m *MemorySessionStore) sweepLocked(now time.Time) int {
	n := 0
	for k, s := range m.sessions {
		if !now.Before(s.Expires) {
			m.dropLocked(k)
			n++
		}
	}
	return n
}

// dropLocked removes one session and its count.
func (m *MemorySessionStore) dropLocked(key string) {
	s, ok := m.sessions[key]
	if !ok {
		return
	}
	delete(m.sessions, key)
	if s.Client != "" {
		if m.byClient[s.Client]--; m.byClient[s.Client] <= 0 {
			delete(m.byClient, s.Client)
		}
	}
}

// Reap implements SessionReaper.
func (m *MemorySessionStore) Reap(_ context.Context, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sweepLocked(now), nil
}

// Create implements SessionStore.
func (m *MemorySessionStore) Create(_ context.Context, key string, s Session, now time.Time, perClient int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions == nil {
		m.sessions = map[string]Session{}
	}
	if m.byClient == nil {
		m.byClient = map[string]int{}
	}
	if len(m.sessions) >= m.cap() || (perClient > 0 && s.Client != "" && m.byClient[s.Client] >= perClient) {
		m.sweepLocked(now)
	}
	if len(m.sessions) >= m.cap() {
		return ErrSessionsFull
	}
	if perClient > 0 && s.Client != "" && m.byClient[s.Client] >= perClient {
		return ErrClientSessionsFull
	}
	m.sessions[key] = s
	if s.Client != "" {
		m.byClient[s.Client]++
	}
	return nil
}

func (m *MemorySessionStore) live(key string, now time.Time) (Session, bool) {
	s, ok := m.sessions[key]
	if !ok {
		return Session{}, false
	}
	if !now.Before(s.Expires) {
		m.dropLocked(key)
		return Session{}, false
	}
	return s, true
}

// Get implements SessionStore.
func (m *MemorySessionStore) Get(_ context.Context, key string, now time.Time) (Session, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.live(key, now)
	return s, ok, nil
}

// SetDoc implements SessionStore.
func (m *MemorySessionStore) SetDoc(_ context.Context, key, old, newDoc string, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.live(key, now)
	if !ok {
		return false, ErrNoSession
	}
	if s.DocID != old {
		return false, nil
	}
	s.DocID = newDoc
	m.sessions[key] = s
	return true, nil
}

// Extend implements SessionStore.
func (m *MemorySessionStore) Extend(_ context.Context, key string, expires, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.live(key, now)
	if !ok {
		return ErrNoSession
	}
	if expires.After(s.Expires) {
		s.Expires = expires
		m.sessions[key] = s
	}
	return nil
}

// Delete implements SessionStore.
func (m *MemorySessionStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	m.dropLocked(key)
	m.mu.Unlock()
	return nil
}

// Len is the number of sessions held, expired ones not yet swept included (for tests and metrics).
func (m *MemorySessionStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}
