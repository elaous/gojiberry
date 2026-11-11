package auth

import (
	"context"
	"time"
)

// LoginState is the server-side record of an OAuth login.
type LoginState struct {
	Provider string
	ReturnTo string
	Verifier string // PKCE code verifier
	Nonce    string // bound into the ID token
}

// SessionStore keeps bearer sessions and login states between requests.
type SessionStore interface {
	// PutSession stores a session and returns its token; never evicts live ones.
	PutSession(ctx context.Context, u User, ttl time.Duration) (token string, err error)
	// GetSession returns the user of a live session.
	GetSession(ctx context.Context, token string) (User, bool, error)
	// DeleteSession ends a session; deleting an unknown token is not an error.
	DeleteSession(ctx context.Context, token string) error
	// PutState stores a login state for ttl and returns its OAuth state token.
	PutState(ctx context.Context, s LoginState, ttl time.Duration) (token string, err error)
	// TakeState consumes a login state; concurrent takes yield at most one hit.
	TakeState(ctx context.Context, token string) (LoginState, bool, error)
}

// MemorySessionStore is the default, in-process SessionStore.
type MemorySessionStore struct {
	sessions *tokenStore[User]
	states   *tokenStore[LoginState]
}

// NewMemorySessionStore makes an empty MemorySessionStore.
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{sessions: newTokenStore[User](), states: newTokenStore[LoginState]()}
}

// PutSession implements SessionStore.
func (s *MemorySessionStore) PutSession(_ context.Context, u User, ttl time.Duration) (string, error) {
	return s.sessions.put(u, time.Now().Add(ttl))
}

// GetSession implements SessionStore.
func (s *MemorySessionStore) GetSession(_ context.Context, token string) (User, bool, error) {
	u, ok := s.sessions.get(token)
	return u, ok, nil
}

// DeleteSession implements SessionStore.
func (s *MemorySessionStore) DeleteSession(_ context.Context, token string) error {
	s.sessions.delete(token)
	return nil
}

// PutState implements SessionStore.
func (s *MemorySessionStore) PutState(_ context.Context, st LoginState, ttl time.Duration) (string, error) {
	return s.states.put(st, time.Now().Add(ttl))
}

// TakeState implements SessionStore.
func (s *MemorySessionStore) TakeState(_ context.Context, token string) (LoginState, bool, error) {
	st, ok := s.states.take(token)
	return st, ok, nil
}

var _ SessionStore = (*MemorySessionStore)(nil)
