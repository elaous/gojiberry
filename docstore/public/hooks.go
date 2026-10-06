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

// AdminGuard decides who may call the admin routes (takedown). The default is TokenAdmin; a
// deployment with its own operator sign-in, mTLS or a gateway that authenticates operators
// plugs in its own. A nil AdminGuard allows nobody.
type AdminGuard interface {
	Allow(r *http.Request) bool
}

// AdminHeader is the header TokenAdmin reads (the token alone, not "Bearer ...": the
// Authorization header belongs to sign-in).
const AdminHeader = "X-Gojiberry-Admin-Token"

// TokenAdmin is an AdminGuard that wants the shared secret in AdminHeader, compared in constant
// time (over the SHA-256 of both, so neither the content nor the length leaks). An empty token
// allows nobody: a server that has no admin token has no takedown route to call.
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

// Blocklist refuses content before it is stored. Blocked is asked for every body a session
// writes (create and edit); true refuses it (422). The default is NoBlocklist; HashBlocklist
// refuses known content by its SHA-256; a deployment plugs in a hash-matching service (known
// abusive content, a perceptual hash for images, a scanner) here. An error means "could not
// decide" and fails closed (503), so an outage of the checker does not become a way around it.
type Blocklist interface {
	Blocked(ctx context.Context, content []byte) (bool, error)
}

// NoBlocklist refuses nothing.
type NoBlocklist struct{}

// Blocked implements Blocklist.
func (NoBlocklist) Blocked(context.Context, []byte) (bool, error) { return false, nil }

// HashBlocklist refuses content whose SHA-256 (hex) is in the set. Add is safe for concurrent
// use, so an operator process can grow it while the server runs.
type HashBlocklist struct {
	mu     sync.RWMutex
	hashes map[string]struct{}
}

// NewHashBlocklist makes a blocklist of the given SHA-256 hex digests.
func NewHashBlocklist(hexDigests ...string) *HashBlocklist {
	b := &HashBlocklist{hashes: map[string]struct{}{}}
	for _, h := range hexDigests {
		b.Add(h)
	}
	return b
}

// Add adds a SHA-256 digest in hex (case-insensitive).
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

// Challenger is the hook for a captcha or proof of work: Verify is called before a session is
// issued and before a document is created, with the request (which carries whatever the client
// solved: a header, a field). Returning an error refuses the request with 403 and the error's
// text, so a deployment can ask for a challenge only when a range is busy. The default,
// NoChallenge, accepts everything. This is the interface only; no challenge ships.
type Challenger interface {
	Verify(r *http.Request) error
}

// NoChallenge accepts every request.
type NoChallenge struct{}

// Verify implements Challenger.
func (NoChallenge) Verify(*http.Request) error { return nil }

// ErrChallenge may be returned (or wrapped) by a Challenger to say a challenge is needed.
var ErrChallenge = errors.New("public: a challenge must be solved first")
