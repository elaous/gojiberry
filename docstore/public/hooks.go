package public

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
)

// AdminGuard decides who may call the admin routes.
type AdminGuard interface {
	Allow(r *http.Request) bool
}

// AdminHeader is the header TokenAdmin reads.
const AdminHeader = "X-Gojiberry-Admin-Token"

// TokenAdmin is an AdminGuard that wants the shared secret.
type TokenAdmin string

// Allow implements AdminGuard.
func (t TokenAdmin) Allow(r *http.Request) bool {
	if t == "" {
		return false
	}
	got := r.Header.Get(AdminHeader)
	if got == "" {
		return false
	}
	a, b := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(string(t)))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// Blocklist refuses content before it is stored.
type Blocklist interface {
	Blocked(ctx context.Context, content []byte) (bool, error)
}

// NoBlocklist refuses nothing.
type NoBlocklist struct{}

// Blocked implements Blocklist.
func (NoBlocklist) Blocked(context.Context, []byte) (bool, error) { return false, nil }

// HashBlocklist refuses content whose SHA-256 is listed.
type HashBlocklist struct {
	mu     sync.RWMutex
	hashes map[string]struct{}
}

// NewHashBlocklist makes a blocklist of the given SHA-256 hashes.
func NewHashBlocklist(hexDigests ...string) *HashBlocklist {
	b := &HashBlocklist{hashes: map[string]struct{}{}}
	for _, h := range hexDigests {
		b.Add(h)
	}
	return b
}

// Add adds a SHA-256 digest in hex, case-insensitive.
func (b *HashBlocklist) Add(hexDigest string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.hashes == nil {
		b.hashes = map[string]struct{}{}
	}
	b.hashes[strings.ToLower(strings.TrimSpace(hexDigest))] = struct{}{}
}

// Blocked implements Blocklist.
func (b *HashBlocklist) Blocked(_ context.Context, content []byte) (bool, error) {
	h := sha256.Sum256(content)
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, bad := b.hashes[hex.EncodeToString(h[:])]
	return bad, nil
}

type Challenger interface {
	Verify(r *http.Request) error
}

// NoChallenge accepts every request.
type NoChallenge struct{}

// Verify implements Challenger.
func (NoChallenge) Verify(*http.Request) error { return nil }

// ErrChallenge may be returned by a Challenger.
var ErrChallenge = errors.New("public: a challenge must be solved first")
