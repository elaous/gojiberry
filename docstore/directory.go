package docstore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/elaous/gojiberry/auth"
)

// DirectoryPrefix is the keyspace of the key directory. Its records are written only by the
// server's publish route (POST /directory/publish): the document API treats the prefix as
// reserved, like "acl:" and "collab:", so no session can read, write, list or delete them
// there.
const DirectoryPrefix = "directory/"

// Errors a DirectoryService returns, so the routes can say what went wrong.
var (
	// ErrDirectoryRefused: the user may not publish (no verified e-mail, a passkey session, too
	// many keys for the address). Answered 403.
	ErrDirectoryRefused = errors.New("docstore: the directory refuses this publisher")
	// ErrDirectoryBadKey: the key text is not a public key. Answered 400.
	ErrDirectoryBadKey = errors.New("docstore: not a public key")
)

// DirectoryKey is a key found in the directory, as the lookup route answers it.
type DirectoryKey struct {
	Key         string `json:"key"`
	Label       string `json:"label,omitempty"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	Verified    bool   `json:"verified"`
}

// DirectoryService is the key directory the server fronts (encrypt.DirectoryService makes one
// over an adapter; it lives in docstore/encrypt, which imports this package). Publish records
// key as a key of the user's own address; Lookup lists the keys published for an address.
// Both are called only after the routes checked the session.
type DirectoryService interface {
	Publish(ctx context.Context, user auth.User, key, label string) (DirectoryKey, error)
	Lookup(ctx context.Context, email string) ([]DirectoryKey, error)
}

// WithDirectory turns on the directory routes, mounted next to /docs when an auth.Manager is
// present (without sign-in there is nobody to vouch for an address, so they are not mounted):
//
//	POST /directory/publish        {"key": "<public key>", "label": "..."}  -> the key as published
//	GET  /directory/lookup?email=  -> {"keys": [{key, type, fingerprint, verified}]}
//
// Both need a signed-in identity-provider session (a passkey session gets 403 on both).
// Lookup reveals the public keys published for any address to every signed-in provider user,
// by design: a key is public, and finding a person's key by address is what the directory is
// for. It is limited per user and per client IP (the connection's address: behind a proxy
// every client shares it, so the real limiting belongs to the gateway).
// Publishing needs an explicitly verified e-mail (User.VerifiedEmail: the provider sent
// email_verified true, or is configured with auth.Provider.AssumeEmailVerified) and publishes
// under that address only: an "email" in the body that is not the session's own is refused.
// Bodies are small.
func (s *Server) WithDirectory(d DirectoryService) *Server {
	s.directory = d
	return s
}

const (
	maxDirectoryBody   = 4 << 10
	maxLookupKeys      = 50
	publishesPerMinute = 10
	lookupsPerMinute   = 60
	lookupsPerIP       = 120
)

// windowLimiter is a fixed one-minute window per key. It is small on purpose: a gateway in
// front of the server does the real limiting; this stops one session from hammering the store.
type windowLimiter struct {
	mu    sync.Mutex
	win   time.Time
	count map[string]int
	now   func() time.Time
}

func (l *windowLimiter) allow(key string, limit int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now
	if l.now != nil {
		now = l.now
	}
	if t := now(); l.count == nil || t.Sub(l.win) >= time.Minute {
		l.win, l.count = t, map[string]int{}
	}
	l.count[key]++
	return l.count[key] <= limit
}

func (s *Server) directoryUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	u, ok := s.auth.CurrentUser(r)
	if !ok {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return auth.User{}, false
	}
	return u, true
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, ok := s.directoryUser(w, r)
	if !ok {
		return
	}
	// Only a provider session with a verified e-mail can vouch for an address.
	if !u.VerifiedEmail || strings.TrimSpace(u.Email) == "" || u.ID == "" || strings.HasPrefix(u.ID, "passkey:") {
		http.Error(w, "only a signed-in account with a verified e-mail can publish a key", http.StatusForbidden)
		return
	}
	if !s.limiter.allow("p:"+u.ID, publishesPerMinute) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	var body struct {
		Key   string `json:"key"`
		Label string `json:"label"`
		Email string `json:"email"`
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDirectoryBody))
	if err != nil {
		http.Error(w, "request too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	if json.Unmarshal(data, &body) != nil || strings.TrimSpace(body.Key) == "" {
		http.Error(w, "expected {\"key\": \"<public key>\"}", http.StatusBadRequest)
		return
	}
	if body.Email != "" && NormalizeAddress(body.Email) != NormalizeAddress(u.Email) {
		http.Error(w, "a key can be published only for your own address", http.StatusForbidden)
		return
	}
	k, err := s.directory.Publish(r.Context(), u, strings.TrimSpace(body.Key), body.Label)
	switch {
	case errors.Is(err, ErrDirectoryRefused):
		http.Error(w, "the directory refuses this publisher", http.StatusForbidden)
	case errors.Is(err, ErrDirectoryBadKey):
		http.Error(w, "not a public key", http.StatusBadRequest)
	case err != nil:
		serverError(w, err)
	default:
		writeJSONStatus(w, http.StatusOK, k)
	}
}

func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, ok := s.directoryUser(w, r)
	if !ok {
		return
	}
	// Only a provider session may look up: a passkey account's address was typed by whoever
	// made it, and nothing vouches for the account, so it is not a directory reader.
	if strings.HasPrefix(u.ID, "passkey:") {
		http.Error(w, "only a signed-in provider account can look up keys", http.StatusForbidden)
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if !s.limiter.allow("li:"+ip, lookupsPerIP) || !s.limiter.allow("l:"+u.ID, lookupsPerMinute) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	if email == "" || len(email) > 320 {
		http.Error(w, "email is required", http.StatusBadRequest)
		return
	}
	keys, err := s.directory.Lookup(r.Context(), email)
	if err != nil {
		serverError(w, err)
		return
	}
	if len(keys) > maxLookupKeys {
		keys = keys[:maxLookupKeys]
	}
	if keys == nil {
		keys = []DirectoryKey{}
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"keys": keys})
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // a write failure here means the client disconnected
}
