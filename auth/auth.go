// Package auth implements a minimal OpenID Connect login flow and bearer sessions.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/internal/clientkey"
	"github.com/elaous/gojiberry/user"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// User is the minimal identity: a stable ID, optional email, and display fields.
type User struct {
	// ID is "{provider}:{sub}", unique across providers.
	ID string
	// Email is set only when the provider verified it.
	Email string
	// VerifiedEmail is true only when the provider verified the address.
	VerifiedEmail bool
	Name          string
	// Username and Picture are for display, when the provider gives them.
	Username, Picture string
}

// Person returns u as a user.Person with the given role.
func (u User) Person(role string) user.Person {
	return user.Person{ID: u.ID, Email: u.Email, Username: u.Username, Name: u.Name, Picture: u.Picture, Role: role}
}

// AnonProvider is the reserved provider name for anonymous users.
const AnonProvider = "anon"

// AnonymousUser returns the User for an anonymous session id.
func AnonymousUser(id string) User { return User{ID: AnonProvider + ":" + id} }

// IsAnonymous reports whether u is an anonymous user (ID "anon:<random>").
func (u User) IsAnonymous() bool { return strings.HasPrefix(u.ID, AnonProvider+":") }

// Provider configures one OIDC identity provider; IssuerURL drives discovery.
type Provider struct {
	// Name is the provider's URL-safe identifier, used in routes and User.ID.
	Name string
	// DisplayName labels the sign-in button; defaults to Name.
	DisplayName  string
	IssuerURL    string
	ClientID     string
	ClientSecret string
	// AssumeEmailVerified trusts emails from tokens that lack email_verified.
	AssumeEmailVerified bool
}

// ProviderInfo is the public part of a Provider; it omits secrets.
type ProviderInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// Config is Manager's own construction-time configuration.
type Config struct {
	// BaseURL is this server's public base URL, used for redirect_uri.
	BaseURL string
	// Providers lists the OIDC providers a client can sign in through.
	Providers []Provider
	// SessionTTL is the session lifetime; zero means 720h.
	SessionTTL time.Duration
	// ReturnOrigins lists extra origins a login may redirect to.
	ReturnOrigins []string
	// Sessions stores bearer sessions and login states; nil means in-memory.
	Sessions SessionStore
}

// resolvedProvider is a Provider after OIDC discovery.
type resolvedProvider struct {
	Provider
	oauth2Config oauth2.Config
	verifier     *oidc.IDTokenVerifier
}

func (p resolvedProvider) displayName() string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	return p.Name
}

// Manager serves the login flow; HTTP handlers are in http.go.
type Manager struct {
	baseURL    string
	byName     map[string]*resolvedProvider
	store      SessionStore // in-process by default; see Config.Sessions
	sessionTTL time.Duration
	order      []string            // provider names, as configured
	origins    map[string]struct{} // origins a login may return to

	loginLimiter Limiter                    // nil: /auth/{provider}/login is not limited
	clientIP     func(*http.Request) string // the client's address for loginLimiter (nil: the remote address)

	missLimiter Limiter                    // nil: a token that is not a session is not counted
	missIP      func(*http.Request) string // the client's address for missLimiter
	missMu      sync.Mutex
	missBlocked map[string]time.Time
}

// Limiter spends a per-key request budget.
type Limiter interface {
	Allow(ctx context.Context, key string) (bool, time.Duration)
}

// SetLoginLimiter limits login starts per client; nil leaves them unlimited.
func (m *Manager) SetLoginLimiter(l Limiter, clientIP func(*http.Request) string) {
	m.loginLimiter, m.clientIP = l, clientIP
}

// maxMissBlocked bounds the blocked-client map.
const maxMissBlocked = 4096

// SetMissLimiter limits bearer tokens that are not sessions, per client.
func (m *Manager) SetMissLimiter(l Limiter, clientIP func(*http.Request) string) {
	m.missLimiter, m.missIP = l, clientIP
}

func (m *Manager) missKey(r *http.Request) string {
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		ip = host
	}
	if m.missIP != nil {
		if v := m.missIP(r); v != "" {
			ip = v
		}
	}
	return ip
}

// missBlockedNow reports whether the client is blocked for misses.
func (m *Manager) missBlockedNow(ip string) bool {
	m.missMu.Lock()
	defer m.missMu.Unlock()
	until, ok := m.missBlocked[ip]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(m.missBlocked, ip)
		return false
	}
	return true
}

// countMiss spends one miss; an exhausted client is blocked until the limiter allows.
func (m *Manager) countMiss(ctx context.Context, ip string) {
	ok, wait := m.missLimiter.Allow(ctx, "miss|"+clientkey.Of(ip))
	if ok {
		return
	}
	m.missMu.Lock()
	defer m.missMu.Unlock()
	if m.missBlocked == nil || len(m.missBlocked) >= maxMissBlocked {
		m.missBlocked = map[string]time.Time{}
	}
	m.missBlocked[ip] = time.Now().Add(max(wait, time.Second))
}

// allowLogin spends one login and returns the wait in seconds if refused.
func (m *Manager) allowLogin(r *http.Request) (bool, int) {
	if m.loginLimiter == nil {
		return true, 0
	}
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		remote = host
	}
	ip := remote
	if m.clientIP != nil {
		// Empty clientIP answers fall back to the remote address.
		if v := m.clientIP(r); v != "" {
			ip = v
		}
	}
	ok, wait := m.loginLimiter.Allow(r.Context(), "login|"+clientkey.Of(ip))
	if ok {
		return true, 0
	}
	secs := int(math.Ceil(wait.Seconds()))
	if secs < 1 {
		secs = 1
	}
	return false, secs
}

const (
	defaultSessionTTL = 30 * 24 * time.Hour
	stateTTL          = 10 * time.Minute
)

// NewManager resolves OIDC discovery for every provider, failing fast.
func NewManager(ctx context.Context, cfg Config) (*Manager, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("auth: Config.BaseURL is required")
	}
	// Empty Providers is valid for passkey-only servers.
	ttl := cfg.SessionTTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}

	if cfg.Sessions == nil {
		cfg.Sessions = NewMemorySessionStore()
	}
	m := &Manager{
		baseURL:    cfg.BaseURL,
		byName:     make(map[string]*resolvedProvider, len(cfg.Providers)),
		store:      cfg.Sessions,
		sessionTTL: ttl,
		origins:    map[string]struct{}{},
	}
	for _, o := range append([]string{cfg.BaseURL}, cfg.ReturnOrigins...) {
		if u, err := url.Parse(o); err == nil && u.Scheme != "" && u.Host != "" {
			m.origins[strings.ToLower(u.Scheme+"://"+u.Host)] = struct{}{}
		}
	}
	for _, p := range cfg.Providers {
		if p.Name == "" || p.IssuerURL == "" || p.ClientID == "" {
			return nil, fmt.Errorf("auth: provider %q is missing a required field (Name/IssuerURL/ClientID)", p.Name)
		}
		if p.Name == "passkey" {
			return nil, errors.New(`auth: the provider name "passkey" is reserved for accounts made by passkeys (auth/passkey)`)
		}
		if p.Name == AnonProvider {
			return nil, errors.New(`auth: the provider name "anon" is reserved for anonymous sessions (docstore/public): its user IDs ("anon:<random>") must not collide with a provider's`)
		}
		if _, exists := m.byName[p.Name]; exists {
			return nil, fmt.Errorf("auth: duplicate provider name %q", p.Name)
		}
		op, err := oidc.NewProvider(ctx, p.IssuerURL)
		if err != nil {
			return nil, fmt.Errorf("auth: discovering provider %q: %w", p.Name, err)
		}
		m.order = append(m.order, p.Name)
		m.byName[p.Name] = &resolvedProvider{
			Provider: p,
			oauth2Config: oauth2.Config{
				ClientID:     p.ClientID,
				ClientSecret: p.ClientSecret,
				RedirectURL:  cfg.BaseURL + "/auth/" + p.Name + "/callback",
				Endpoint:     op.Endpoint(),
				Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
			},
			verifier: op.Verifier(&oidc.Config{ClientID: p.ClientID}),
		}
	}
	return m, nil
}

// StartSession signs u in and returns a bearer token for it.
func (m *Manager) StartSession(u User) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), sessionStoreTimeout)
	defer cancel()
	return m.store.PutSession(ctx, u, m.sessionTTL)
}

// SetSessionStore replaces the session store; call it before serving.
func (m *Manager) SetSessionStore(s SessionStore) {
	if s != nil {
		m.store = s
	}
}

// sessionStoreTimeout bounds store calls made without a request.
const sessionStoreTimeout = 10 * time.Second

// Providers returns the public info of each configured provider.
func (m *Manager) Providers() []ProviderInfo {
	out := make([]ProviderInfo, 0, len(m.byName))
	for _, name := range m.order {
		p := m.byName[name]
		out = append(out, ProviderInfo{Name: p.Name, DisplayName: p.displayName()})
	}
	return out
}

// idTokenClaims holds the standard OIDC claims this package reads.
type idTokenClaims struct {
	Subject string `json:"sub"`
	Email   string `json:"email"`
	// EmailVerified is a pointer: absent differs from false.
	EmailVerified     *bool  `json:"email_verified"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	Picture           string `json:"picture"`
}

// safeReturnTo returns target when a login may redirect there, else empty.
func (m *Manager) safeReturnTo(target string) string {
	if target == "" {
		return ""
	}
	u, err := url.Parse(target)
	if err != nil {
		return ""
	}
	if u.Scheme == "" && u.Host == "" {
		// Bare paths only; reject "//host" and "/\\host".
		if strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "//") && !strings.HasPrefix(target, "/\\") {
			return strings.TrimRight(m.baseURL, "/") + target
		}
		return ""
	}
	if _, ok := m.origins[strings.ToLower(u.Scheme+"://"+u.Host)]; ok && u.User == nil {
		return target
	}
	return ""
}

// randomToken returns a cryptographically random URL-safe token.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// tokenStore is an expiring in-memory map from random tokens to values.
type tokenStore[T any] struct {
	mu      sync.Mutex
	entries map[string]tokenEntry[T]
	swept   time.Time
}

type tokenEntry[T any] struct {
	value   T
	expires time.Time
}

func newTokenStore[T any]() *tokenStore[T] {
	return &tokenStore[T]{entries: make(map[string]tokenEntry[T])}
}

// put stores value under a fresh random token until ttl.
func (s *tokenStore[T]) put(value T, ttl time.Time) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) >= pruneAt {
		// Sweep at most once a second, so floods of puts stay cheap.
		if now := time.Now(); now.Sub(s.swept) >= time.Second {
			s.swept = now
			for k, e := range s.entries {
				if now.After(e.expires) {
					delete(s.entries, k)
				}
			}
		}
		if len(s.entries) >= maxEntries {
			return "", errStoreFull
		}
	}
	s.entries[token] = tokenEntry[T]{value: value, expires: ttl}
	return token, nil
}

const (
	// pruneAt is the size at which put sweeps expired entries.
	pruneAt = 1024
	// maxEntries bounds a store: logins that never finish cannot grow it.
	maxEntries = 100000
)

// ErrStoreFull: the store is at its bound; live entries are never evicted.
var ErrStoreFull = errors.New("auth: too many pending logins or sessions")

var errStoreFull = ErrStoreFull

// get returns the value of token if present and unexpired.
func (s *tokenStore[T]) get(token string) (T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[token]
	if !ok || time.Now().After(e.expires) {
		var zero T
		return zero, false
	}
	return e.value, true
}

// take is get plus delete: a login state is consumed once.
func (s *tokenStore[T]) take(token string) (T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[token]
	delete(s.entries, token)
	if !ok || time.Now().After(e.expires) {
		var zero T
		return zero, false
	}
	return e.value, true
}

func (s *tokenStore[T]) delete(token string) {
	s.mu.Lock()
	delete(s.entries, token)
	s.mu.Unlock()
}

// userFromClaims maps verified ID token claims to a User.
func userFromClaims(providerName string, c idTokenClaims, assumeVerified bool) User {
	verified := c.EmailVerified != nil && *c.EmailVerified
	if c.EmailVerified == nil && assumeVerified {
		verified = true
	}
	email := c.Email
	if !verified {
		email = "" // an address the provider has not vouched for identifies no one
	}
	return User{ID: providerName + ":" + c.Subject, Email: email, VerifiedEmail: verified && email != "", Name: c.Name, Username: c.PreferredUsername, Picture: c.Picture}
}
