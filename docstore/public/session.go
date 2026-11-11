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

// Session is one anonymous visitor.
type Session struct {
	// ID is the random part of the user ID.
	ID string
	// DocID is the internal ID of the session's one document.
	DocID string
	// Client is the ClientKey of the session's address.
	Client string
	// Net is the NetKey of that address.
	Net string
	// Wide is the WideKey of that address.
	Wide string
	// Created is when the session was issued.
	Created time.Time
	// Expires is the instant the session stops working.
	Expires time.Time
}

func (s Session) User() auth.User { return auth.AnonymousUser(s.ID) }

// Errors of a SessionStore.
var (
	// ErrSessionsFull is returned by Create when the store is full.
	ErrSessionsFull = errors.New("public: too many live anonymous sessions")
	// ErrClientSessionsFull is returned by Create when a client is full.
	ErrClientSessionsFull = errors.New("public: too many live anonymous sessions for this address")
	// ErrNetSessionsFull is returned by Create when a network is full.
	ErrNetSessionsFull  = errors.New("public: too many live anonymous sessions for this network")
	ErrWideSessionsFull = errors.New("public: too many live anonymous sessions for this wide network")
	// ErrNoSession is returned by methods that need a session.
	ErrNoSession = errors.New("public: no such session")
)

// SessionCaps are the figures a SessionStore applies in Create.
type SessionCaps struct {
	PerClient      int // live sessions per Client (<= 0: no cap)
	PerNet         int // live sessions per Net (<= 0: no cap)
	PerWide        int // live sessions per Wide (<= 0: no cap)
	ReservePercent int
}

// SessionStore keeps the live anonymous sessions.
type SessionStore interface {
	Create(ctx context.Context, key string, s Session, now time.Time, caps SessionCaps) error
	// Get returns the live session.
	Get(ctx context.Context, key string, now time.Time) (Session, bool, error)
	// SetDoc sets the session's DocID if it is live.
	SetDoc(ctx context.Context, key, old, newDoc string, now time.Time) (bool, error)
	// Extend moves the session's expiry to expires.
	Extend(ctx context.Context, key string, expires, now time.Time) error
	// Delete ends a session.
	Delete(ctx context.Context, key string) error
}

// SessionReaper is an optional capability of a SessionStore.
type SessionReaper interface {
	Reap(ctx context.Context, now time.Time) (int, error)
}

func TokenKey(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// MemorySessionStore is the single-process SessionStore.
type MemorySessionStore struct {
	Max int

	mu       sync.Mutex
	sessions map[string]*memSession
	byClient map[string]int // live-or-unswept sessions per Client
	byNet    map[string]int // live-or-unswept sessions per Net
	byWide   map[string]int // live-or-unswept sessions per Wide
	expiry   expiryHeap     // every session, earliest Expires first
	popped   int            // sessions the heap has given up for expiry (for tests)
}

type memSession struct {
	Session
	key string
	idx int
}

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

// NewMemorySessionStore makes a store holding at most max sessions.
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
		return ErrSessionsFull
	}
	ms := &memSession{Session: s, key: key}
	if old, ok := m.sessions[key]; ok {
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

// Len is the number of sessions held.
func (m *MemorySessionStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}
