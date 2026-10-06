package public

import (
	"container/heap"
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
	// Net is the NetKey of that address (an IPv6 /48, an IPv4 /24): what the second-level cap and
	// the reserve for newcomers count.
	Net string
	// Wide is the WideKey of that address (an IPv6 /32, an IPv4 /16): what the third-level cap
	// counts and the reserve for newcomers looks at (the coarsest level).
	Wide string
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
	// ErrNetSessionsFull is returned by Create when the session's Net already holds its maximum
	// of live sessions.
	ErrNetSessionsFull = errors.New("public: too many live anonymous sessions for this network")
	// ErrWideSessionsFull is returned by Create when the session's Wide already holds its
	// maximum of live sessions.
	ErrWideSessionsFull = errors.New("public: too many live anonymous sessions for this wide network")
	// ErrNoSession is returned by the methods that need a session that is not there (or has expired).
	ErrNoSession = errors.New("public: no such session")
)

// SessionCaps are the figures a SessionStore applies in Create besides its own global cap.
type SessionCaps struct {
	PerClient int // live sessions per Client (<= 0: no cap)
	PerNet    int // live sessions per Net (<= 0: no cap)
	PerWide   int // live sessions per Wide (<= 0: no cap)
	// ReservePercent is the share (0..100) of the global cap kept for sessions whose Wide (the
	// coarsest level) has no live session yet (<= 0: no reserve).
	ReservePercent int
}

// SessionStore keeps the live anonymous sessions. The default is NewMemorySessionStore (one
// process); replicas that must share sessions, or a global cap, plug in a store over Redis or a
// table. key is the hash of the bearer token (see TokenKey). Every method takes now so a store
// needs no clock of its own, and an expired session behaves as absent.
//
// Create must refuse (ErrSessionsFull) when the live sessions are at the cap, counting without
// the expired ones, (ErrClientSessionsFull) when the live sessions with the same Client already
// number caps.PerClient, (ErrNetSessionsFull) when those with the same Net number caps.PerNet and
// (ErrWideSessionsFull) when those with the same Wide number caps.PerWide (a figure <= 0 is no such
// cap), so that one address, or one attacker holding a whole /48 or /32, cannot fill the global cap.
// Once the live sessions reach the cap minus caps.ReservePercent of it, Create refuses
// (ErrSessionsFull) every session whose Wide already has a live session (a session with no Wide:
// whose Net does): the reserve is for visitors from networks that hold none, judged at the
// coarsest level so that moving between the /48s of one /32 does not make an attacker a newcomer. SetDoc must be atomic (a compare-and-set), since it is what makes "one
// document per session" hold under concurrent requests.
type SessionStore interface {
	// Create stores a new session, or returns ErrSessionsFull, ErrClientSessionsFull or
	// ErrNetSessionsFull.
	Create(ctx context.Context, key string, s Session, now time.Time, caps SessionCaps) error
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

// MemorySessionStore is the single-process SessionStore. Expired sessions are dropped through an
// expiry heap (each Create and Reap pops what has expired, so the cost is that of the sessions that
// expired, never a scan of all of them): a flood of sessions that are never used again cannot fill
// it for longer than their lifetime, and a flood of refused Creates does not make each refusal walk
// the whole store under the lock.
type MemorySessionStore struct {
	// Max is the cap on live sessions (0: DefaultMaxSessions).
	Max int

	mu       sync.Mutex
	sessions map[string]*memSession
	byClient map[string]int // live-or-unswept sessions per Client
	byNet    map[string]int // live-or-unswept sessions per Net
	byWide   map[string]int // live-or-unswept sessions per Wide
	expiry   expiryHeap     // every session, earliest Expires first
	popped   int            // sessions the heap has given up for expiry (for tests)
}

// memSession is a session and its place in the expiry heap.
type memSession struct {
	Session
	key string
	idx int
}

// expiryHeap is a container/heap of sessions ordered by Expires.
type expiryHeap []*memSession

func (h expiryHeap) Len() int           { return len(h) }
func (h expiryHeap) Less(i, j int) bool { return h[i].Expires.Before(h[j].Expires) }
func (h expiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx, h[j].idx = i, j
}
func (h *expiryHeap) Push(x any) {
	m := x.(*memSession)
	m.idx = len(*h)
	*h = append(*h, m)
}
func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	m := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	m.idx = -1
	return m
}

// DefaultMaxSessions is the live-session cap when none is set.
const DefaultMaxSessions = 10000

// NewMemorySessionStore makes a store holding at most max live sessions (0: DefaultMaxSessions).
func NewMemorySessionStore(max int) *MemorySessionStore {
	return &MemorySessionStore{Max: max}
}

func (m *MemorySessionStore) cap() int {
	if m.Max > 0 {
		return m.Max
	}
	return DefaultMaxSessions
}

func (m *MemorySessionStore) initLocked() {
	if m.sessions == nil {
		m.sessions = map[string]*memSession{}
	}
	if m.byClient == nil {
		m.byClient = map[string]int{}
	}
	if m.byNet == nil {
		m.byNet = map[string]int{}
	}
	if m.byWide == nil {
		m.byWide = map[string]int{}
	}
}

// reapLocked drops the expired sessions (the heap's head, until it is live) and returns how many.
func (m *MemorySessionStore) reapLocked(now time.Time) int {
	n := 0
	for len(m.expiry) > 0 && !now.Before(m.expiry[0].Expires) {
		m.dropLocked(m.expiry[0].key)
		n++
	}
	m.popped += n
	return n
}

func decr(counts map[string]int, k string) {
	if k == "" {
		return
	}
	if counts[k]--; counts[k] <= 0 {
		delete(counts, k)
	}
}

// dropLocked removes one session and its counts.
func (m *MemorySessionStore) dropLocked(key string) {
	s, ok := m.sessions[key]
	if !ok {
		return
	}
	delete(m.sessions, key)
	if s.idx >= 0 {
		heap.Remove(&m.expiry, s.idx)
	}
	decr(m.byClient, s.Client)
	decr(m.byNet, s.Net)
	decr(m.byWide, s.Wide)
}

// Reap implements SessionReaper.
func (m *MemorySessionStore) Reap(_ context.Context, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reapLocked(now), nil
}

// Create implements SessionStore.
func (m *MemorySessionStore) Create(_ context.Context, key string, s Session, now time.Time, caps SessionCaps) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	m.reapLocked(now) // expired sessions do not count against anyone
	// the coarsest level the session names is the one the reserve looks at
	known := func() bool {
		switch {
		case s.Wide != "":
			return m.byWide[s.Wide] > 0
		case s.Net != "":
			return m.byNet[s.Net] > 0
		}
		return false
	}
	switch {
	case len(m.sessions) >= m.cap():
		return ErrSessionsFull
	case caps.PerClient > 0 && s.Client != "" && m.byClient[s.Client] >= caps.PerClient:
		return ErrClientSessionsFull
	case caps.PerNet > 0 && s.Net != "" && m.byNet[s.Net] >= caps.PerNet:
		return ErrNetSessionsFull
	case caps.PerWide > 0 && s.Wide != "" && m.byWide[s.Wide] >= caps.PerWide:
		return ErrWideSessionsFull
	case caps.ReservePercent > 0 && known() &&
		len(m.sessions) >= m.cap()-m.cap()*min(caps.ReservePercent, 100)/100:
		// In the reserve: only networks with no live session are let in.
		return ErrSessionsFull
	}
	ms := &memSession{Session: s, key: key}
	if old, ok := m.sessions[key]; ok { // the same key again (a custom caller): replace, do not double count
		m.dropLocked(old.key)
	}
	m.sessions[key] = ms
	heap.Push(&m.expiry, ms)
	if s.Client != "" {
		m.byClient[s.Client]++
	}
	if s.Net != "" {
		m.byNet[s.Net]++
	}
	if s.Wide != "" {
		m.byWide[s.Wide]++
	}
	return nil
}

func (m *MemorySessionStore) live(key string, now time.Time) (*memSession, bool) {
	s, ok := m.sessions[key]
	if !ok {
		return nil, false
	}
	if !now.Before(s.Expires) {
		m.dropLocked(key)
		return nil, false
	}
	return s, true
}

// Get implements SessionStore.
func (m *MemorySessionStore) Get(_ context.Context, key string, now time.Time) (Session, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.live(key, now)
	if !ok {
		return Session{}, false, nil
	}
	return s.Session, true, nil
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
		heap.Fix(&m.expiry, s.idx)
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

// Len is the number of sessions held, expired ones not yet dropped included (for tests and metrics).
func (m *MemorySessionStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}
