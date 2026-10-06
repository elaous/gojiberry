// Package passkey signs people in with WebAuthn passkeys (https://www.w3.org/TR/webauthn-3/),
// with no password: the server keeps each credential's public key and a signature counter and
// checks a signed one-time challenge. Verification is done by github.com/go-webauthn/webauthn,
// not by code here. A successful login gives the same bearer session as an OpenID Connect one
// (auth.Manager.StartSession), so every handler that knows a signed-in person works unchanged.
//
// A passkey is two factors in itself, something you have (the authenticator) and something you
// are or know (the biometric or PIN that unlocks it): Config.UserVerification defaults to
// "required", which is what lets a passkey sign-in stand without a second step.
package passkey

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/elaous/gojiberry/docstore"
)

// ErrNotFound is returned by a Store for an account or credential it does not have.
var ErrNotFound = errors.New("passkey: not found")

// Credential is one passkey of an account: what the server verifies with, and what the person
// sees in their list.
type Credential struct {
	Cred     webauthn.Credential `json:"cred"`
	Label    string              `json:"label,omitempty"`
	Created  time.Time           `json:"created"`
	LastUsed time.Time           `json:"lastUsed,omitempty"`
}

// ID is the credential's ID as text (base64url), what a list and a removal use.
func (c Credential) ID() string { return base64.RawURLEncoding.EncodeToString(c.Cred.ID) }

// Account is a person who can sign in with passkeys. ID is the person's user ID in the rest of
// the library ("passkey:<random>" for an account made by a passkey, or the ID of an OpenID
// Connect user the passkey was linked to while signed in as them). Handle is the opaque user
// handle the authenticator stores (random, never the ID or the e-mail).
type Account struct {
	ID          string       `json:"id"`
	Handle      []byte       `json:"handle"`
	Email       string       `json:"email,omitempty"`
	Name        string       `json:"name,omitempty"`
	Credentials []Credential `json:"credentials"`
	Created     time.Time    `json:"created"`
}

// WebAuthnID implements webauthn.User.
func (a *Account) WebAuthnID() []byte { return a.Handle }

// WebAuthnName implements webauthn.User.
func (a *Account) WebAuthnName() string {
	if a.Email != "" {
		return a.Email
	}
	return a.ID
}

// WebAuthnDisplayName implements webauthn.User.
func (a *Account) WebAuthnDisplayName() string {
	if a.Name != "" {
		return a.Name
	}
	return a.WebAuthnName()
}

// WebAuthnCredentials implements webauthn.User.
func (a *Account) WebAuthnCredentials() []webauthn.Credential {
	out := make([]webauthn.Credential, len(a.Credentials))
	for i, c := range a.Credentials {
		out[i] = c.Cred
	}
	return out
}

// Store keeps accounts and their credentials. Implementations must be safe for concurrent use.
type Store interface {
	// Get finds an account by its ID, or ErrNotFound.
	Get(ctx context.Context, id string) (*Account, error)
	// ByHandle finds an account by the user handle an authenticator returned.
	ByHandle(ctx context.Context, handle []byte) (*Account, error)
	// Put saves an account (creating it, or replacing the credentials and details it had).
	Put(ctx context.Context, a *Account) error
	// Delete removes an account and everything of it.
	Delete(ctx context.Context, id string) error
}

func newHandle() ([]byte, error) {
	h := make([]byte, 32)
	_, err := rand.Read(h)
	return h, err
}

func clone(a *Account) *Account {
	b := *a
	b.Handle = append([]byte(nil), a.Handle...)
	b.Credentials = append([]Credential(nil), a.Credentials...)
	return &b
}

// MemoryStore is a Store in memory, for tests and one-process servers.
type MemoryStore struct {
	mu       sync.Mutex
	accounts map[string]*Account
}

// NewMemoryStore makes an empty MemoryStore.
func NewMemoryStore() *MemoryStore { return &MemoryStore{accounts: map[string]*Account{}} }

// Get implements Store.
func (m *MemoryStore) Get(_ context.Context, id string) (*Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.accounts[id]; ok {
		return clone(a), nil
	}
	return nil, ErrNotFound
}

// ByHandle implements Store.
func (m *MemoryStore) ByHandle(_ context.Context, handle []byte) (*Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.accounts {
		if string(a.Handle) == string(handle) {
			return clone(a), nil
		}
	}
	return nil, ErrNotFound
}

// Put implements Store.
func (m *MemoryStore) Put(_ context.Context, a *Account) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accounts[a.ID] = clone(a)
	return nil
}

// Delete implements Store.
func (m *MemoryStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[id]; !ok {
		return ErrNotFound
	}
	delete(m.accounts, id)
	return nil
}

// AdapterStore keeps accounts in a docstore.Adapter: a record per account
// ("passkey/account/<ID>") and one per user handle ("passkey/handle/<handle>") that names its
// account. The records hold public keys and e-mail addresses, not secrets, but they are
// personal data: use an adapter that is not served to people by a docstore server (or one whose
// ACL hides these keys), and the server's own store only through this type.
type AdapterStore struct {
	Adapter docstore.Adapter
	mu      sync.Mutex // one change at a time within this process
}

func accountKey(id string) string {
	return "passkey/account/" + base64.RawURLEncoding.EncodeToString([]byte(id))
}
func handleKey(h []byte) string { return "passkey/handle/" + base64.RawURLEncoding.EncodeToString(h) }

// Get implements Store.
func (s *AdapterStore) Get(ctx context.Context, id string) (*Account, error) {
	d, err := s.Adapter.Load(ctx, accountKey(id))
	if errors.Is(err, docstore.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var a Account
	if err := json.Unmarshal(d.Data, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// ByHandle implements Store.
func (s *AdapterStore) ByHandle(ctx context.Context, handle []byte) (*Account, error) {
	d, err := s.Adapter.Load(ctx, handleKey(handle))
	if errors.Is(err, docstore.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, string(d.Data))
}

// Put implements Store.
func (s *AdapterStore) Put(ctx context.Context, a *Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	if err := s.Adapter.Save(ctx, handleKey(a.Handle), []byte(a.ID)); err != nil {
		return err
	}
	return s.Adapter.Save(ctx, accountKey(a.ID), data)
}

// Delete implements Store.
func (s *AdapterStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	_ = s.Adapter.Delete(ctx, handleKey(a.Handle))
	return s.Adapter.Delete(ctx, accountKey(id))
}
