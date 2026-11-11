package passkey

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	mrand "math/rand/v2"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/elaous/gojiberry/docstore"
)

var ErrNotFound = errors.New("passkey: not found")

type Credential struct {
	Cred     webauthn.Credential `json:"cred"`
	Label    string              `json:"label,omitempty"`
	Created  time.Time           `json:"created"`
	LastUsed time.Time           `json:"lastUsed,omitempty"`
}

func (c Credential) ID() string { return base64.RawURLEncoding.EncodeToString(c.Cred.ID) }

// Account is a person who can sign in with passkeys.
type Account struct {
	ID     string `json:"id"`
	Handle []byte `json:"handle"`
	// a label only: never the session's identity
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

// Store keeps accounts and their credentials.
type Store interface {
	// Get finds an account by its ID, or ErrNotFound.
	Get(ctx context.Context, id string) (*Account, error)
	ByHandle(ctx context.Context, handle []byte) (*Account, error)
	Put(ctx context.Context, a *Account) error
	// Delete removes an account and everything of it.
	Delete(ctx context.Context, id string) error
}

type Updater interface {
	Update(ctx context.Context, id string, fn func(*Account) error) error
}

type Creator interface {
	Create(ctx context.Context, a *Account) error
}

// ErrExists is returned by Creator.Create when the account already exists.
var ErrExists = errors.New("passkey: the account already exists")

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

// Update implements Updater.
func (m *MemoryStore) Update(_ context.Context, id string, fn func(*Account) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	c := clone(a)
	if err := fn(c); err != nil {
		return err
	}
	m.accounts[id] = clone(c)
	return nil
}

// Create implements Creator.
func (m *MemoryStore) Create(_ context.Context, a *Account) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[a.ID]; ok {
		return ErrExists
	}
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
	return s.putLocked(ctx, a)
}

func (s *AdapterStore) putLocked(ctx context.Context, a *Account) error {
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	// account first, then handle: failure never leaves a dangling handle
	if err := s.Adapter.Save(ctx, accountKey(a.ID), data); err != nil {
		return err
	}
	return s.Adapter.Save(ctx, handleKey(a.Handle), []byte(a.ID))
}

const maxUpdateTries = 32

// Update implements Updater.
func (s *AdapterStore) Update(ctx context.Context, id string, fn func(*Account) error) error {
	ca, ok := s.Adapter.(docstore.ConditionalAdapter)
	if !ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		a, err := s.Get(ctx, id)
		if err != nil {
			return err
		}
		if err := fn(a); err != nil {
			return err
		}
		return s.putLocked(ctx, a)
	}
	for try := 0; try < maxUpdateTries; try++ {
		d, err := s.Adapter.Load(ctx, accountKey(id))
		if errors.Is(err, docstore.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var a Account
		if err := json.Unmarshal(d.Data, &a); err != nil {
			return err
		}
		if err := fn(&a); err != nil {
			return err
		}
		data, err := json.Marshal(&a)
		if err != nil {
			return err
		}
		if err := ca.SaveIf(ctx, accountKey(id), data, docstore.Digest(d.Data)); err != nil {
			if errors.Is(err, docstore.ErrConflict) {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(mrand.IntN(1+try)) * time.Millisecond):
				}
				continue
			}
			return err
		}
		return s.Adapter.Save(ctx, handleKey(a.Handle), []byte(a.ID))
	}
	return docstore.ErrConflict
}

// Create implements Creator (atomic over a docstore.ConditionalAdapter).
func (s *AdapterStore) Create(ctx context.Context, a *Account) error {
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	if ca, ok := s.Adapter.(docstore.ConditionalAdapter); ok {
		if err := ca.SaveIf(ctx, accountKey(a.ID), data, ""); err != nil {
			if errors.Is(err, docstore.ErrConflict) {
				return ErrExists
			}
			return err
		}
		return s.Adapter.Save(ctx, handleKey(a.Handle), []byte(a.ID))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.Get(ctx, a.ID); err == nil {
		return ErrExists
	}
	return s.putLocked(ctx, a)
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
