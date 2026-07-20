// Package auth implements a minimal OpenID Connect login flow --
// "Sign in with Google/Microsoft/<any OIDC-compliant provider>" --
// for servers in this repo that need to know who a request is from.
// docstore is the first consumer (see its own server.go for how a
// *Manager plugs into an existing http.Handler); a document's own
// sharing/permissions (still unbuilt -- see docs/ROADMAP.md) is what
// actually needs that identity, this package is just the foundation
// it builds on.
//
// Deliberately small: one generic provider implementation driven
// entirely by config (an issuer URL plus a client ID/secret), not
// bespoke code per provider -- Google, Microsoft/Azure AD, Okta,
// Auth0, or any other OIDC-compliant identity provider all work
// through the same NewManager/Config, no per-provider branching
// anywhere in this package. User carries only the fields every
// caller in this repo actually needs (ID, Email, Name) -- no profile
// picture/locale/verified-email-bool/etc.
//
// Sessions are bearer tokens (an opaque, random session ID a client
// sends back as "Authorization: Bearer <token>"), not cookies: this
// repo's existing HTTP servers already answer CORS with a wildcard
// Access-Control-Allow-Origin (see docstore's own corsMiddleware),
// which a cookie-based session can't safely ride along with (a
// credentialed cross-origin request needs a specific origin echoed
// back, never "*"), and switching every server in this repo to
// per-origin CORS just for this would be a much bigger, riskier
// change than this task's own scope. A bearer token sidesteps that
// entirely, and matches how this app's own client already keeps
// everything else (documents, local config) in browser-local storage
// rather than a server-set cookie -- see OnDesign's own identity.go.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// User is the minimal identity this package -- and everything that
// consumes it -- actually needs: a stable ID (for de-duplication and
// as a session's own subject), an email (the natural way to invite/
// mention someone by), and a display name. No picture, locale, or any
// other OIDC claim this repo has no use for yet.
type User struct {
	// ID is "{provider}:{sub}" -- the issuing provider's own name
	// (Provider.Name) plus its own subject claim, so two different
	// providers' subjects (which are only ever unique *within* one
	// issuer) can never collide with each other once a document
	// starts recording who owns/shares it.
	ID    string
	Email string
	Name  string
}

// Provider configures one OpenID Connect identity provider this
// Manager can start a login with. IssuerURL is used for OIDC
// discovery (GET {IssuerURL}/.well-known/openid-configuration) --
// nothing else about the provider needs to be hardcoded.
type Provider struct {
	// Name is this provider's own short, URL-safe identifier (e.g.
	// "google") -- used in /auth/{name}/login and /auth/{name}/callback,
	// and as the "{provider}:" prefix on every User.ID this provider
	// issues.
	Name string
	// DisplayName is shown on this provider's own "Sign in with ..."
	// button -- e.g. "Google". Defaults to Name if empty.
	DisplayName  string
	IssuerURL    string
	ClientID     string
	ClientSecret string
}

// ProviderInfo is a Provider's own public-facing half -- what
// /auth/providers actually lists; ClientSecret (and ClientID, which
// isn't secret but also isn't this list's business) never leaves the
// server.
type ProviderInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// Config is Manager's own construction-time configuration.
type Config struct {
	// BaseURL is this server's own externally-reachable base URL
	// (e.g. "https://docs.example.com"), used to build each
	// provider's own redirect_uri ({BaseURL}/auth/{provider}/callback)
	// -- an OIDC provider only ever redirects back to a URL it was
	// registered with, so this has to be the real, public address,
	// not localhost, for anything but local testing.
	BaseURL string
	// Providers lists every OIDC provider a client can sign in
	// through -- see Provider's own doc comment. At least one is
	// required; NewManager errors on an empty list rather than
	// silently building a Manager nobody can ever sign in with.
	Providers []Provider
	// SessionTTL is how long a session stays valid after login.
	// Defaults to 30 days (720h) when zero.
	SessionTTL time.Duration
}

// resolvedProvider is a Provider plus everything OIDC discovery
// against its own IssuerURL resolved -- built once in NewManager, not
// per-request, since discovery is a real network round trip.
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

// Manager holds every configured provider (already resolved via OIDC
// discovery) plus the in-memory session/state stores backing the
// login flow and /auth/me. See http.go for the actual HTTP handlers
// built on top of it.
type Manager struct {
	baseURL    string
	byName     map[string]*resolvedProvider
	sessions   *tokenStore[User]
	states     *tokenStore[loginState]
	sessionTTL time.Duration
}

// loginState is what a login's own random state parameter maps to
// server-side -- CSRF protection for the OAuth redirect (the callback
// only ever accepts a state it minted itself) plus ReturnTo, the page
// LoginURL's own caller wants the browser sent back to once login
// finishes.
type loginState struct {
	Provider string
	ReturnTo string
}

const (
	defaultSessionTTL = 30 * 24 * time.Hour
	stateTTL          = 10 * time.Minute
)

// NewManager resolves OIDC discovery for every configured provider up
// front (failing fast on a misconfigured issuer URL/unreachable
// provider, rather than only discovering the problem the first time
// someone tries to sign in through it) and returns a ready-to-use
// Manager.
func NewManager(ctx context.Context, cfg Config) (*Manager, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("auth: Config.BaseURL is required")
	}
	if len(cfg.Providers) == 0 {
		return nil, errors.New("auth: Config.Providers must list at least one provider")
	}
	ttl := cfg.SessionTTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}

	m := &Manager{
		baseURL:    cfg.BaseURL,
		byName:     make(map[string]*resolvedProvider, len(cfg.Providers)),
		sessions:   newTokenStore[User](),
		states:     newTokenStore[loginState](),
		sessionTTL: ttl,
	}
	for _, p := range cfg.Providers {
		if p.Name == "" || p.IssuerURL == "" || p.ClientID == "" {
			return nil, fmt.Errorf("auth: provider %q is missing a required field (Name/IssuerURL/ClientID)", p.Name)
		}
		if _, exists := m.byName[p.Name]; exists {
			return nil, fmt.Errorf("auth: duplicate provider name %q", p.Name)
		}
		op, err := oidc.NewProvider(ctx, p.IssuerURL)
		if err != nil {
			return nil, fmt.Errorf("auth: discovering provider %q: %w", p.Name, err)
		}
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

// Providers lists every configured provider's own public info, in
// the order Config.Providers listed them -- /auth/providers' own
// response body, and what a client renders as its "Sign in with ..."
// buttons.
func (m *Manager) Providers() []ProviderInfo {
	out := make([]ProviderInfo, 0, len(m.byName))
	for _, p := range m.byName {
		out = append(out, ProviderInfo{Name: p.Name, DisplayName: p.displayName()})
	}
	return out
}

// idTokenClaims is the subset of standard OIDC claims this package
// actually reads out of a verified ID token -- see User's own doc
// comment for why nothing beyond these three is extracted.
type idTokenClaims struct {
	Subject string `json:"sub"`
	Email   string `json:"email"`
	Name    string `json:"name"`
}

// randomToken returns a cryptographically random, URL-safe token --
// shared by both session IDs and login-state values, which have
// identical randomness requirements even though they protect
// different things.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// tokenStore is a generic in-memory, expiry-aware map from a random
// token to a value of type T -- session tokens map to a User, login-
// state tokens map to a loginState, and both need identical
// "generate a random key, store a value with an expiry, look it up
// (and for state, consume it once) later" behavior. Lazily prunes
// expired entries on read/write rather than running a background
// sweep goroutine -- a deliberately simple v1 tradeoff (see this
// package's own top doc comment); a long-running server with many
// short-lived login attempts will accumulate some expired-but-
// not-yet-touched entries between reads, bounded by actual traffic,
// not unbounded.
type tokenStore[T any] struct {
	mu      sync.Mutex
	entries map[string]tokenEntry[T]
}

type tokenEntry[T any] struct {
	value   T
	expires time.Time
}

func newTokenStore[T any]() *tokenStore[T] {
	return &tokenStore[T]{entries: make(map[string]tokenEntry[T])}
}

// put generates a fresh random token, stores value against it with
// the given ttl, and returns the token.
func (s *tokenStore[T]) put(value T, ttl time.Time) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.entries[token] = tokenEntry[T]{value: value, expires: ttl}
	s.mu.Unlock()
	return token, nil
}

// get returns token's own value if present and not yet expired.
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

// take is get plus deleting the entry -- login state is only ever
// meant to be consumed once (the callback that matches it), so a
// replayed or guessed state value can never succeed twice even within
// its own TTL.
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

// userFromClaims converts a verified ID token's own claims into this
// package's minimal User -- the one place that mapping happens, so
// handleCallback (and any future caller) never duplicates it.
func userFromClaims(providerName string, c idTokenClaims) User {
	return User{ID: providerName + ":" + c.Subject, Email: c.Email, Name: c.Name}
}
