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
	// ErrNoSession is returned by the methods that need a session that is not there (or has expired).
	ErrNoSession = errors.New("public: no such session")
)

// SessionStore keeps the live anonymous sessions. The default is NewMemorySessionStore (one
// process); replicas that must share sessions, or a global cap, plug in a store over Redis or a
// table. key is the hash of the bearer token (see TokenKey). Every method takes now so a store
// needs no clock of its own, and an expired session behaves as absent.
//
// Create must refuse (ErrSessionsFull) when the live sessions are at the cap, counting without
// the expired ones; SetDoc must be atomic (a compare-and-set), since it is what makes "one
// document per session" hold under concurrent requests.
type SessionStore interface {
	// Create stores a new session, or returns ErrSessionsFull.
	Create(ctx context.Context, key string, s Session, now time.Time) error
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
}

// DefaultMaxSessions is the live-session cap when none is set.
const DefaultMaxSessions = 10000

// NewMemorySessionStore makes a store holding at most max live sessions (0: DefaultMaxSessions).
func NewMemorySessionStore(max int) *MemorySessionStore {
	return &MemorySessionStore{Max: max, sessions: map[string]Session{}}
}

func (m *MemorySessionStore) cap() int {
	if m.Max > 0 {
		return m.Max
	}
	return DefaultMaxSessions
}

// sweepLocked drops the expired sessions.
func (m *MemorySessionStore) sweepLocked(now time.Time) {
	for k, s := range m.sessions {
		if !now.Before(s.Expires) {
			delete(m.sessions, k)
		}
	}
}

// Create implements SessionStore.
func (m *MemorySessionStore) Create(_ context.Context, key string, s Session, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions == nil {
		m.sessions = map[string]Session{}
	}
	if len(m.sessions) >= m.cap() {
		m.sweepLocked(now)
		if len(m.sessions) >= m.cap() {
			return ErrSessionsFull
		}
	}
	m.sessions[key] = s
	return nil
}

func (m *MemorySessionStore) live(key string, now time.Time) (Session, bool) {
	s, ok := m.sessions[key]
	if !ok {
		return Session{}, false
	}
	if !now.Before(s.Expires) {
		delete(m.sessions, key)
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
	delete(m.sessions, key)
	m.mu.Unlock()
	return nil
}

// Len is the number of sessions held, expired ones not yet swept included (for tests and metrics).
func (m *MemorySessionStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}
