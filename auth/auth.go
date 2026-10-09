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

// User is the minimal identity this package -- and everything that
// consumes it -- actually needs: a stable ID (for de-duplication and
// as a session's own subject), an email (the natural way to invite/
// mention someone by), a display name, and the username and picture for
// showing them. No locale or any other OIDC claim this repo has no use for yet.
type User struct {
	// ID is "{provider}:{sub}" -- the issuing provider's own name
	// (Provider.Name) plus its own subject claim, so two different
	// providers' subjects (which are only ever unique *within* one
	// issuer) can never collide with each other once a document
	// starts recording who owns/shares it.
	ID string
	// Email is set only when the provider vouched for it: an explicit
	// `email_verified: true` claim, or Provider.AssumeEmailVerified. An address the provider
	// did not vouch for (claim false or absent) is left empty, so an access list never matches
	// a self-asserted one.
	Email string
	// VerifiedEmail is true only when the provider sent `email_verified: true` explicitly, or
	// the provider is configured with AssumeEmailVerified. The key directory publishes only
	// under an address with this set.
	VerifiedEmail bool
	Name          string
	// Username is the provider's preferred_username, and Picture its picture (a
	// URL), when it gives them; both are for display and for finding people.
	Username, Picture string
}

// Person is the user as the rest of the library describes people, with a role
// (user.Owner, user.Editor or user.Viewer) or "" for none.
func (u User) Person(role string) user.Person {
	return user.Person{ID: u.ID, Email: u.Email, Username: u.Username, Name: u.Name, Picture: u.Picture, Role: role}
}

// AnonProvider is the reserved provider name of anonymous users: an anonymous session's User.ID is
// AnonProvider + ":" + a random string, with no e-mail and no profile (see docstore/public).
// NewManager refuses a Provider with this name so a real provider's subject can never be mistaken
// for an anonymous one.
const AnonProvider = "anon"

// AnonymousUser returns the User of an anonymous session whose random part is id.
func AnonymousUser(id string) User { return User{ID: AnonProvider + ":" + id} }

// IsAnonymous reports whether u is an anonymous user (ID "anon:<random>").
func (u User) IsAnonymous() bool { return strings.HasPrefix(u.ID, AnonProvider+":") }

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
	// AssumeEmailVerified treats the provider's e-mail as verified even though its ID token has
	// no `email_verified: true` claim. Default false: a token without the claim yields no
	// User.Email and no VerifiedEmail. Set it only for a provider you know verifies every
	// address it issues but omits the claim (an explicit `email_verified: false` still wins).
	AssumeEmailVerified bool
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
	// ReturnOrigins lists the origins (e.g. "https://app.example.com")
	// a login may send the browser back to, besides BaseURL's own. A
	// return_to anywhere else is ignored: the session token travels in the
	// redirect, so an unchecked target would hand it to any site.
	ReturnOrigins []string
	// Sessions keeps the bearer sessions and the OpenID Connect login states. Nil is
	// NewMemorySessionStore: they live in this process, so a token issued by one replica is unknown
	// to another. Pass a shared store (auth/sessionstore) for replicas that serve the same people.
	Sessions SessionStore
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
	store      SessionStore // sessions and login states: in this process by default, see Config.Sessions
	sessionTTL time.Duration
	order      []string            // provider names, as configured
	origins    map[string]struct{} // origins a login may return to

	loginLimiter Limiter                    // nil: /auth/{provider}/login is not limited
	clientIP     func(*http.Request) string // the client's address for loginLimiter (nil: the remote address)
}

// Limiter is a request budget per key: Allow reports whether the caller of key may go on now and,
// if not, how long to wait. docstore.Limiter (and so docstore.RateLimiter and docstore.StoreLimiter)
// satisfies it; the interface is declared here because this package cannot import docstore.
type Limiter interface {
	Allow(ctx context.Context, key string) (bool, time.Duration)
}

// SetLoginLimiter limits /auth/{provider}/login, which anyone can call without signing in and
// which writes a login state to the session store: without a budget per client, a flood of logins
// fills the store's bound on pending logins and every real login is refused until the sweep
// clears it. Each request spends from l under the key "login|<client>", where clientIP gives the
// client's address (nil: the host part of the connection's remote address). Over budget the route
// answers 429 with Retry-After and writes nothing. A nil l (the default) turns the limit off.
// Behind a reverse proxy clientIP must read the forwarded address from the proxy only
// (passkey.TrustedProxies), or every client shares one budget. Call it before the Manager serves.
func (m *Manager) SetLoginLimiter(l Limiter, clientIP func(*http.Request) string) {
	m.loginLimiter, m.clientIP = l, clientIP
}

// allowLogin spends one login from the client's budget and reports whether it may go on and, if
// not, the whole seconds (at least 1) it should wait.
func (m *Manager) allowLogin(r *http.Request) (bool, int) {
	if m.loginLimiter == nil {
		return true, 0
	}
	var ip string
	if m.clientIP != nil {
		ip = m.clientIP(r)
	} else if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		ip = host
	} else {
		ip = r.RemoteAddr
	}
	ok, wait := m.loginLimiter.Allow(r.Context(), "login|"+ip)
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

// NewManager resolves OIDC discovery for every configured provider up
// front (failing fast on a misconfigured issuer URL/unreachable
// provider, rather than only discovering the problem the first time
// someone tries to sign in through it) and returns a ready-to-use
// Manager.
func NewManager(ctx context.Context, cfg Config) (*Manager, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("auth: Config.BaseURL is required")
	}
	// Providers may be empty: a server that signs people in with passkeys only (auth/passkey)
	// still needs a Manager for its sessions (StartSession, CurrentUser).
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

// StartSession makes a signed-in session for u and returns its bearer token, as a
// finished OIDC login does: how another way of signing in (a passkey, auth/passkey) gives the
// person the one session format every handler already understands.
func (m *Manager) StartSession(u User) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), sessionStoreTimeout)
	defer cancel()
	return m.store.PutSession(ctx, u, m.sessionTTL)
}

// SetSessionStore replaces the store of sessions and login states (see Config.Sessions) for a
// Manager built elsewhere (docstore.NewAuthManager). Call it before the Manager serves: sessions
// already issued stay in the store they were issued in.
func (m *Manager) SetSessionStore(s SessionStore) {
	if s != nil {
		m.store = s
	}
}

// sessionStoreTimeout bounds one call to the session store made without a request (StartSession).
const sessionStoreTimeout = 10 * time.Second

// Providers lists every configured provider's own public info, in
// the order Config.Providers listed them -- /auth/providers' own
// response body, and what a client renders as its "Sign in with ..."
// buttons.
func (m *Manager) Providers() []ProviderInfo {
	out := make([]ProviderInfo, 0, len(m.byName))
	for _, name := range m.order {
		p := m.byName[name]
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
	// EmailVerified is a pointer so absent can be told from false; neither counts as verified
	// unless the provider sets AssumeEmailVerified (absent only).
	EmailVerified     *bool  `json:"email_verified"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	Picture           string `json:"picture"`
}

// safeReturnTo returns target if a login may send the browser there: a
// path on this server, or an absolute URL on an allowed origin. Anything
// else is "" (the caller then uses BaseURL).
func (m *Manager) safeReturnTo(target string) string {
	if target == "" {
		return ""
	}
	u, err := url.Parse(target)
	if err != nil {
		return ""
	}
	if u.Scheme == "" && u.Host == "" {
		// A bare path ("/doc/1"), but not "//host" or "/\\host".
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
// state tokens map to a LoginState, and both need identical
// "generate a random key, store a value with an expiry, look it up
// (and for state, consume it once) later" behavior. Lazily prunes
// expired entries on read/write rather than running a background
// sweep goroutine -- a deliberately simple v1 tradeoff (see this
// package's own top doc comment); a long-running server with many
// short-lived login attempts will accumulate some expired-but-
// not-yet-touched entries between reads; put sweeps them out once the
// store passes pruneAt entries and refuses new ones at maxEntries.
type tokenStore[T any] struct {
	mu      sync.Mutex
	entries map[string]tokenEntry[T]
	swept   time.Time // the last sweep of expired entries
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
	defer s.mu.Unlock()
	if len(s.entries) >= pruneAt {
		// Sweep at most once a second: a flood of puts must not make each
		// one walk the whole map.
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
	// pruneAt is the size from which put sweeps out expired entries.
	pruneAt = 1024
	// maxEntries bounds a store: logins that never finish cannot grow it.
	maxEntries = 100000
)

// ErrStoreFull is returned by a SessionStore that holds its maximum of sessions or pending logins
// (the HTTP routes answer 503). A live record is never evicted to make room.
var ErrStoreFull = errors.New("auth: too many pending logins or sessions")

var errStoreFull = ErrStoreFull

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
// assumeVerified is Provider.AssumeEmailVerified.
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
