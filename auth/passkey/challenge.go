package passkey

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// Challenge is a started ceremony: what the server remembers between "begin" and "finish".
type Challenge struct {
	Session webauthn.SessionData `json:"session"`
	Account string               `json:"account,omitempty"` // the account a registration is for, "" for a sign-in
	BegunBy string               `json:"begunBy,omitempty"` // user ID of the session that began a registration
	Email   string               `json:"email,omitempty"`   // for a new account
	Name    string               `json:"name,omitempty"`
	Create  bool                 `json:"create,omitempty"` // the registration makes the account
	Expires time.Time            `json:"expires"`
}

// ChallengeStore keeps started ceremonies. The default (NewMemoryChallenges) is a map in this
// process, which is right for one server. Several replicas behind a load balancer need a shared
// one (Redis, a database): implement this interface and pass it as Config.Challenges.
//
// The contract: Put stores c (its Expires is already set) and returns an unguessable token (at
// least 128 bits of randomness); it returns ErrBusy when too many are waiting. Take returns
// the challenge for token and removes it, atomically, so a challenge is good for one finish
// (two concurrent Takes must not both succeed); ok is false if there is none or it expired.
type ChallengeStore interface {
	Put(ctx context.Context, c Challenge) (token string, err error)
	Take(ctx context.Context, token string) (c Challenge, ok bool, err error)
}

// maxChallenges bounds the ceremonies waiting to be finished (memory held per anonymous
// request), and sweepEvery how often expired ones are looked for.
const (
	maxChallenges = 10000
	sweepEvery    = 5 * time.Second
)

// MemoryChallenges is a ChallengeStore in this process's memory.
type MemoryChallenges struct {
	mu        sync.Mutex
	items     map[string]Challenge
	lastSweep time.Time
	max       int
}

// NewMemoryChallenges makes one holding at most max ceremonies (10,000 if max <= 0).
func NewMemoryChallenges(max int) *MemoryChallenges {
	if max <= 0 {
		max = maxChallenges
	}
	return &MemoryChallenges{items: map[string]Challenge{}, max: max}
}

// Put implements ChallengeStore.
func (m *MemoryChallenges) Put(_ context.Context, c Challenge) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.items) >= m.max || now.Sub(m.lastSweep) >= sweepEvery {
		m.sweepLocked(now) // drop what has expired: not on every start, but when it matters
		if len(m.items) >= m.max {
			return "", ErrBusy
		}
	}
	m.items[token] = c
	return token, nil
}

func (m *MemoryChallenges) sweepLocked(now time.Time) {
	m.lastSweep = now
	for k, v := range m.items {
		if now.After(v.Expires) {
			delete(m.items, k)
		}
	}
}

// Take implements ChallengeStore.
func (m *MemoryChallenges) Take(_ context.Context, token string) (Challenge, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.items[token]
	delete(m.items, token)
	if !ok || time.Now().After(c.Expires) {
		return Challenge{}, false, nil
	}
	return c, true, nil
}
