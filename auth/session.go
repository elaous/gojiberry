package auth

import (
	"context"
	"time"
)

// LoginState is what a login's own random state parameter maps to server-side: CSRF protection for
// the OAuth redirect (the callback only accepts a state it minted itself), the page the browser
// returns to, the PKCE code verifier and the nonce bound into the ID token.
type LoginState struct {
	Provider string
	ReturnTo string
	Verifier string // PKCE code verifier
	Nonce    string // bound into the ID token
}

// SessionStore keeps what a Manager must remember between requests: the bearer sessions and the
// OpenID Connect login states. The default (NewMemorySessionStore) lives in one process; a store
// shared by replicas (auth/sessionstore) makes a token issued by one valid on the others.
//
// The store mints the tokens (at least 128 bits of randomness) so that it may put what it needs
// into them. Implementations are safe for concurrent use. A token that is unknown, expired or
// deleted is (zero, false, nil); an error means the store could not answer, and the Manager then
// signs nobody in (it fails closed).
//
// Expiry is fixed at PutSession: a session does not slide, a request does not extend it.
type SessionStore interface {
	// PutSession stores a session for u that expires after ttl and returns its bearer token. It
	// returns ErrStoreFull at the store's bound, and never evicts a live session to make room.
	PutSession(ctx context.Context, u User, ttl time.Duration) (token string, err error)
	// GetSession returns the user of a live session.
	GetSession(ctx context.Context, token string) (User, bool, error)
	// DeleteSession ends a session (logout). Deleting an unknown token is not an error. Once it
	// returns nil, GetSession of the token is false on every replica that shares the store.
	DeleteSession(ctx context.Context, token string) error
	// PutState stores a login state that expires after ttl and returns its token (the OAuth
	// "state" parameter).
	PutState(ctx context.Context, s LoginState, ttl time.Duration) (token string, err error)
	// TakeState returns the login state of token and consumes it: of any number of concurrent
	// calls (here or on other replicas) with one token, at most one gets ok=true, and a replay
	// never does. An expired state is consumed too, and refused.
	TakeState(ctx context.Context, token string) (LoginState, bool, error)
}

// MemorySessionStore is the SessionStore of one process (the default): sessions and login states
// in two bounded maps, up to 100,000 of each, expired ones swept lazily.
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
