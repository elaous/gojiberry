package docstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/guard"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/elaous/gojiberry/auth"
)

// Config is loaded entirely from the environment, mirroring
// syncserver/fontserver's convention: cloning this repo for a
// different operator or backend never requires a code change.
//
// Backend selects which registered AdapterFactory BuildAdapter calls
// ("memory" the zero-dependency default, or "localfs"/"postgres"/
// "mongo"/"s3"/"minio" -- see each backend's own Config type, e.g.
// postgres.Config (docstore/backends), for what BackendConfig needs to hold; "minio" is
// s3.Config too, just defaulted for a MinIO endpoint instead of real
// AWS, see s3.go's own doc comment). This package never hardcodes
// that list itself -- each backend registers its own name via
// RegisterAdapter, so BuildAdapter/Config never need a code change to
// support a new one.
//
// BackendConfig is the chosen backend's own config, JSON-encoded --
// e.g. `{"dsn":"postgres://..."}` for "postgres", `{"bucket":"docs"}`
// for "s3", `{"bucket":"docs","endpoint":"http://localhost:9000"}`
// for "minio". Passed through unparsed; only the backend itself
// understands its own shape.
type Config struct {
	// Guard decides who may call the server (see package guard); nil means
	// guard.Open(). ConfigFromEnv reads GOJIBERRY_DOCSTORE_ALLOW_ORIGINS and
	// GOJIBERRY_DOCSTORE_TOKEN (sent as X-Gojiberry-Token, since Authorization
	// carries the sign-in session).
	Guard guard.Guard
	// RequireACL closes documents that have no ACL (see Server.WithRequireACL);
	// ConfigFromEnv reads GOJIBERRY_DOCSTORE_REQUIRE_ACL=true.
	RequireACL bool
	// ExpiryTTL, when positive, makes every document expire that long after its last write
	// (see ExpiringAdapter); ExpirySweep is how often expired ones are deleted (they are
	// invisible at once either way). ConfigFromEnv reads GOJIBERRY_DOCSTORE_EXPIRY_TTL and
	// GOJIBERRY_DOCSTORE_EXPIRY_SWEEP.
	ExpiryTTL   time.Duration
	ExpirySweep time.Duration
	// ExpiryAdoptUpdated (GOJIBERRY_DOCSTORE_EXPIRY_ADOPT=updated) gives documents that existed
	// before expiry was switched on the deadline UpdatedAt + TTL, so old ones are deleted by the
	// first sweep. The default is now + TTL, a full grace period.
	ExpiryAdoptUpdated bool
	// Limiter chooses where rate limits (the directory's, and the passkey routes') are kept:
	// "memory" (the default: a budget per process) or "store" (a budget shared through the
	// store by every replica on it, see StoreLimiter). ConfigFromEnv reads
	// GOJIBERRY_DOCSTORE_LIMITER.
	Limiter string
	// ExpiryOrphans (GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS=collab) makes the sweeper also delete the
	// shared-editing state ("collab:<id>") of documents that do not exist, once it is older than
	// the TTL plus ExpiryOrphanGrace (GOJIBERRY_DOCSTORE_EXPIRY_ORPHAN_GRACE, default 1h): without
	// it a document that lives only in the collaboration hub is never swept. It is on by default
	// when GOJIBERRY_DOCSTORE_PUBLIC is on (GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS=off disables it).
	ExpiryOrphans     bool
	ExpiryOrphanGrace time.Duration
	// ExpiryMaxAge (GOJIBERRY_DOCSTORE_EXPIRY_MAX_AGE, default off) caps a document's life from
	// its creation, however often it is edited (ExpiringAdapter.MaxAge).
	ExpiryMaxAge  time.Duration
	Addr          string
	Backend       string
	BackendConfig json.RawMessage
	Auth          AuthConfig
}

// AuthConfig configures the optional Sign-in-with-OIDC endpoints
// docstore mounts under /auth/* -- see the gojiberry/auth package
// itself for what those endpoints do and why a bearer token, not a
// cookie, carries the session. Entirely optional: leaving Providers
// empty (the default) mounts no /auth/* routes at all and docstore
// behaves exactly as it did before this package existed. This is
// identity only -- resolving a request's own bearer token to a User --
// not document ownership/permissions, which is a separate, still-
// unbuilt layer on top (see docs/ROADMAP.md's Share/sharing entries in
// the OnDesign repo).
type AuthConfig struct {
	BaseURL    string
	Providers  []auth.Provider
	SessionTTL time.Duration
	// ReturnOrigins are the extra origins a login may redirect back to
	// (see auth.Config.ReturnOrigins).
	ReturnOrigins []string
}

// ConfigFromEnv reads GOJIBERRY_DOCSTORE_ADDR, GOJIBERRY_DOCSTORE_BACKEND,
// GOJIBERRY_DOCSTORE_BACKEND_CONFIG (that backend's own config, as a
// raw JSON object -- e.g. GOJIBERRY_DOCSTORE_BACKEND_CONFIG='{"dsn":"..."}'
// for the postgres backend), and the optional GOJIBERRY_DOCSTORE_AUTH_*
// vars below. Leaving BACKEND_CONFIG unset is fine for backends with no
// required fields (memory needs none; localfs defaults its dir).
//
// GOJIBERRY_DOCSTORE_AUTH_PROVIDERS, if set, is a JSON array of
// auth.Provider (e.g. '[{"name":"google","issuerURL":"https://accounts.google.com","clientID":"...","clientSecret":"..."}]')
// and turns on the /auth/* endpoints -- see AuthConfig. Leaving it
// unset (the default) leaves auth off entirely. A provider's e-mail counts
// (User.Email, User.VerifiedEmail) only when its ID token says email_verified
// true; add "assumeEmailVerified": true to a provider that verifies every
// address but omits the claim.
// GOJIBERRY_DOCSTORE_AUTH_BASE_URL is this server's own externally-
// reachable base URL (see auth.Config.BaseURL); required when
// AUTH_PROVIDERS is set. GOJIBERRY_DOCSTORE_AUTH_SESSION_TTL is a
// Go duration string (e.g. "720h"); defaults to auth's own 30-day
// default when unset. GOJIBERRY_DOCSTORE_AUTH_RETURN_ORIGINS is a comma-
// separated list of extra origins a login may send the browser back to
// (besides AUTH_BASE_URL's own).
func ConfigFromEnv() Config {
	cfg := Config{
		Guard:         guard.FromEnvWith("GOJIBERRY_DOCSTORE", guard.Options{Public: []string{"/health"}, TokenHeader: "X-Gojiberry-Token"}),
		Addr:          envOr("GOJIBERRY_DOCSTORE_ADDR", ":8090"),
		Backend:       envOr("GOJIBERRY_DOCSTORE_BACKEND", "memory"),
		BackendConfig: json.RawMessage(os.Getenv("GOJIBERRY_DOCSTORE_BACKEND_CONFIG")),
		Auth: AuthConfig{
			BaseURL: os.Getenv("GOJIBERRY_DOCSTORE_AUTH_BASE_URL"),
		},
	}
	if raw := os.Getenv("GOJIBERRY_DOCSTORE_AUTH_PROVIDERS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.Auth.Providers); err != nil {
			log.Fatalf("docstore: parsing GOJIBERRY_DOCSTORE_AUTH_PROVIDERS: %v", err)
		}
	}
	if raw := os.Getenv("GOJIBERRY_DOCSTORE_AUTH_SESSION_TTL"); raw != "" {
		ttl, err := time.ParseDuration(raw)
		if err != nil {
			log.Fatalf("docstore: parsing GOJIBERRY_DOCSTORE_AUTH_SESSION_TTL: %v", err)
		}
		cfg.Auth.SessionTTL = ttl
	}
	cfg.RequireACL = os.Getenv("GOJIBERRY_DOCSTORE_REQUIRE_ACL") == "true"
	if raw := os.Getenv("GOJIBERRY_DOCSTORE_AUTH_RETURN_ORIGINS"); raw != "" {
		for _, o := range strings.Split(raw, ",") {
			if o = strings.TrimSpace(o); o != "" {
				cfg.Auth.ReturnOrigins = append(cfg.Auth.ReturnOrigins, o)
			}
		}
	}
	var err error
	if cfg.ExpiryTTL, cfg.ExpirySweep, err = ParseExpiryEnv(os.Getenv("GOJIBERRY_DOCSTORE_EXPIRY_TTL"), os.Getenv("GOJIBERRY_DOCSTORE_EXPIRY_SWEEP")); err != nil {
		log.Fatalf("docstore: %v", err)
	}
	if cfg.ExpiryAdoptUpdated, err = ParseExpiryAdopt(os.Getenv("GOJIBERRY_DOCSTORE_EXPIRY_ADOPT")); err != nil {
		log.Fatalf("docstore: %v", err)
	}
	if cfg.Limiter, err = ParseLimiterMode(os.Getenv("GOJIBERRY_DOCSTORE_LIMITER")); err != nil {
		log.Fatalf("docstore: %v", err)
	}
	// In public mode the sweep of orphaned shared-editing state is on unless switched off with
	// GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS=off: the offer promises that nothing outlives its
	// deadline, and hub state of a document that only ever lived in the hub has none of its own.
	publicOn := publicOfferOn(os.Getenv("GOJIBERRY_DOCSTORE_PUBLIC"))
	if cfg.ExpiryOrphans, cfg.ExpiryOrphanGrace, err = ParseExpiryOrphansDefault(os.Getenv("GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS"), os.Getenv("GOJIBERRY_DOCSTORE_EXPIRY_ORPHAN_GRACE"), publicOn); err != nil {
		log.Fatalf("docstore: %v", err)
	}
	if publicOn && !cfg.ExpiryOrphans {
		log.Print("docstore: WARNING: public mode with GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS=off: hub-only shared-editing state ('collab:<id>') of documents that never reached the store is never deleted, against the retention promise")
	}
	if raw := strings.TrimSpace(os.Getenv("GOJIBERRY_DOCSTORE_EXPIRY_MAX_AGE")); raw != "" {
		if cfg.ExpiryMaxAge, err = time.ParseDuration(raw); err != nil || cfg.ExpiryMaxAge <= 0 {
			log.Fatalf("docstore: parsing GOJIBERRY_DOCSTORE_EXPIRY_MAX_AGE %q: want a positive duration such as 168h", raw)
		}
	}
	return cfg
}

// ParseExpiryOrphans parses GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS ("collab" turns the sweep of
// orphaned shared-editing state on; empty is off) and GOJIBERRY_DOCSTORE_EXPIRY_ORPHAN_GRACE (a Go
// duration; empty is DefaultOrphanGrace). A grace without the policy is an error.
func ParseExpiryOrphans(raw, graceRaw string) (on bool, grace time.Duration, err error) {
	return ParseExpiryOrphansDefault(raw, graceRaw, false)
}

// ParseExpiryOrphansDefault is ParseExpiryOrphans with the answer for an empty setting: defaultOn
// is what the public offer uses (the sweep is on unless GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS=off).
// "off" (or "none", "false") always turns it off; a grace beside it is an error.
func ParseExpiryOrphansDefault(raw, graceRaw string, defaultOn bool) (on bool, grace time.Duration, err error) {
	raw, graceRaw = strings.ToLower(strings.TrimSpace(raw)), strings.TrimSpace(graceRaw)
	switch raw {
	case "":
		if !defaultOn {
			if graceRaw != "" {
				return false, 0, errors.New("GOJIBERRY_DOCSTORE_EXPIRY_ORPHAN_GRACE needs GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS=collab")
			}
			return false, 0, nil
		}
	case "off", "none", "false":
		if graceRaw != "" {
			return false, 0, errors.New("GOJIBERRY_DOCSTORE_EXPIRY_ORPHAN_GRACE needs GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS=collab")
		}
		return false, 0, nil
	case "collab":
	default:
		return false, 0, fmt.Errorf("parsing GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS %q: want \"collab\" or \"off\" (or empty: off, on in public mode)", raw)
	}
	if graceRaw == "" {
		return true, DefaultOrphanGrace, nil
	}
	if grace, err = time.ParseDuration(graceRaw); err != nil || grace <= 0 {
		return false, 0, fmt.Errorf("parsing GOJIBERRY_DOCSTORE_EXPIRY_ORPHAN_GRACE %q: want a positive duration such as 1h", graceRaw)
	}
	return true, grace, nil
}

// publicOfferOn reports whether GOJIBERRY_DOCSTORE_PUBLIC turns the public offer on (the same
// values public.FromEnv accepts; a test in internal/docstorecmd keeps the two together).
func publicOfferOn(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "on":
		return true
	}
	return false
}

// ParseExpiryAdopt parses GOJIBERRY_DOCSTORE_EXPIRY_ADOPT: empty (or "grace") gives documents that
// predate expiry a full lifetime from the first sweep; "updated" gives them UpdatedAt + TTL, which
// deletes the old ones at once. It reports whether "updated" was asked for.
func ParseExpiryAdopt(raw string) (updated bool, err error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "grace":
		return false, nil
	case "updated":
		return true, nil
	}
	return false, fmt.Errorf("parsing GOJIBERRY_DOCSTORE_EXPIRY_ADOPT %q: want \"updated\" (or empty for a full grace period)", raw)
}

// DefaultExpirySweep is how often expired documents are deleted when only the TTL is set.
const DefaultExpirySweep = 5 * time.Minute

// ParseExpiryEnv parses GOJIBERRY_DOCSTORE_EXPIRY_TTL and GOJIBERRY_DOCSTORE_EXPIRY_SWEEP, Go
// durations such as "24h". Both empty means no expiry. A sweep interval without a TTL is an
// error; with a TTL and no interval it is DefaultExpirySweep, or the TTL if that is shorter.
func ParseExpiryEnv(ttlRaw, sweepRaw string) (ttl, sweep time.Duration, err error) {
	ttlRaw, sweepRaw = strings.TrimSpace(ttlRaw), strings.TrimSpace(sweepRaw)
	if ttlRaw == "" {
		if sweepRaw != "" {
			return 0, 0, errors.New("GOJIBERRY_DOCSTORE_EXPIRY_SWEEP needs GOJIBERRY_DOCSTORE_EXPIRY_TTL")
		}
		return 0, 0, nil
	}
	if ttl, err = time.ParseDuration(ttlRaw); err != nil || ttl <= 0 {
		return 0, 0, fmt.Errorf("parsing GOJIBERRY_DOCSTORE_EXPIRY_TTL %q: want a positive duration such as 24h", ttlRaw)
	}
	if sweepRaw == "" {
		return ttl, min(DefaultExpirySweep, ttl), nil
	}
	if sweep, err = time.ParseDuration(sweepRaw); err != nil || sweep <= 0 {
		return 0, 0, fmt.Errorf("parsing GOJIBERRY_DOCSTORE_EXPIRY_SWEEP %q: want a positive duration such as 5m", sweepRaw)
	}
	return ttl, sweep, nil
}

// maxDocBytes is the largest document a PUT may carry.
const maxDocBytes = 64 << 20

// serverError answers a backend failure without handing its text (which can
// hold a DSN, a bucket name or a path) to the caller.
func serverError(w http.ResponseWriter, err error) {
	log.Printf("docstore: %v", err)
	http.Error(w, "storage error", http.StatusInternalServerError)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Server exposes an Adapter over HTTP:
//
//	PUT    /docs/{id}      -- save (request body is the raw document bytes)
//	GET    /docs/{id}      -- load (response body is the raw document bytes)
//	DELETE /docs/{id}      -- delete
//	GET    /docs           -- list metadata, as a JSON array of Meta (plus "expires" when the
//	                          store expires documents, see WithExpiry; reads and writes then
//	                          carry X-Gojiberry-Expires)
//	POST   /docs/{id}/renew -- an edit kept elsewhere (the collaboration hub): move the deadline
//	                          of a live document, needs write permission (see handleRenew)
//	GET    /docs/{id}/acl  -- this document's own ACL (see acl.go), Owner-only
//	PUT    /docs/{id}/acl  -- replace it (bootstraps ownership on first use)
//	GET    /health         -- liveness + configured backend name
//
// Plus, when auth is configured (see AuthConfig/WithAuth), every
// /auth/* route auth.Manager.Routes documents -- /auth/providers,
// /auth/{provider}/login, /auth/{provider}/callback, /auth/me,
// /auth/logout.
//
// Plus, with WithDirectory and an auth.Manager, the /directory/* routes (challenge, publish,
// revoke, lookup, log, owner enrollment: see WithDirectory); the "directory/", "directory-log/", "directory-enroll/" and "ratelimit/"
// ids are reserved from /docs.
//
// GET/PUT/DELETE on /docs/{id} enforce that document's own ACL, if any
// (checkPermission, acl.go) -- a document nobody has ever shared via
// PUT /docs/{id}/acl is unaffected, exactly as before this file
// existed, so this is entirely opt-in. Without an auth.Manager
// configured at all, no request can ever resolve to a real identity,
// so ACL enforcement (and /docs/{id}/acl itself) is a no-op/501 --
// this Server behaves exactly as it did before either feature
// existed.
type Server struct {
	adapter Adapter
	backend string
	auth    *auth.Manager
	guard   guard.Guard
	// requireACL closes documents that have no ACL (see WithRequireACL).
	requireACL bool
	// extra are more routes mounted beside /docs and /auth (WithRoutes); hidden are the
	// prefixes of ids the server keeps for itself (Hide).
	extra  []extraRoute
	hidden []string // lowercased (Hide)
	// docsClosed answers 404 to the generic API (see CloseDocs).
	docsClosed bool
	// directory is the key directory behind /directory/* (WithDirectory); its routes are limited
	// by the limiters the factory makes (WithDirectoryLimits) and its lookups narrowed by
	// dirPolicy (WithDirectoryPolicy).
	directory       DirectoryService
	dirLimitFactory LimiterFactory
	dirLimitOnce    sync.Once
	dirLimits       *directoryLimits
	dirPolicy       DirectoryPolicy
	// dirDomainBudget makes the whole-server lookup budget one per e-mail domain
	// (WithDirectoryGlobalPerDomain).
	dirDomainBudget bool
}

type extraRoute struct {
	pattern string
	h       http.Handler
}

// NewServer wraps an already-constructed Adapter. Use this directly
// when embedding docstore in another program that builds its own
// Adapter; use NewServerFromConfig to select a backend by Config.
func NewServer(adapter Adapter, backendName string) *Server {
	return &Server{adapter: adapter, backend: backendName}
}

// WithAuth attaches an already-built auth.Manager, mounting its
// /auth/* routes alongside /docs/* the next time Handler is called.
// Returns s so it can chain off NewServer at the call site.
func (s *Server) WithAuth(m *auth.Manager) *Server {
	s.auth = m
	return s
}

// WithGuard sets the access policy (CORS, origin or token checks; see package
// guard). Without one the server is open to every origin.
func (s *Server) WithGuard(g guard.Guard) *Server {
	s.guard = g
	return s
}

// WithRequireACL makes a document without an ACL closed instead of open to
// everyone: with sign-in configured, only the first signed-in writer of a new
// document can create it, and becomes its owner (the ACL is written for
// them). Without sign-in it has no effect.
func (s *Server) WithRequireACL(on bool) *Server {
	s.requireACL = on
	return s
}

// WithExpiry serves e (an ExpiringAdapter over the store) instead of the adapter the server was
// made with, the same as passing e to NewServer: document reads then carry X-Gojiberry-Expires,
// listings an "expires" field, and every write renews. Viewing (GET) never does. The caller
// runs e.Run to delete what has expired.
func (s *Server) WithExpiry(e *ExpiringAdapter) *Server {
	s.adapter = e
	return s
}

// setExpires adds the X-Gojiberry-Expires header (RFC 3339, UTC) if id has a deadline.
func (s *Server) setExpires(w http.ResponseWriter, r *http.Request, id string) {
	if x, ok := s.adapter.(Expirer); ok {
		if t, has, err := x.Expiry(r.Context(), id); err == nil && has {
			w.Header().Set("X-Gojiberry-Expires", t.UTC().Format(time.RFC3339))
		}
	}
}

// listItem is one entry of the GET /docs listing: a Meta, plus when it expires if it does.
type listItem struct {
	Meta
	Expires *time.Time `json:"expires,omitempty"`
}

// WithRoutes mounts another handler on the server (the passkey routes of auth/passkey, say),
// next to /docs and /auth, under the same guard.
func (s *Server) WithRoutes(pattern string, h http.Handler) *Server {
	s.extra = append(s.extra, extraRoute{pattern, h})
	return s
}

// Hide keeps ids that start with prefix away from people: they are not listed and cannot be
// read, written or deleted over HTTP. For records a part of the program keeps in the same
// store ("passkey/" holds accounts and their credentials) that only that part should touch.
// The match ignores case ("PubDoc/x" is hidden by "pubdoc/"), since a backend that folds case
// would alias the two.
func (s *Server) Hide(prefix string) *Server {
	s.hidden = append(s.hidden, strings.ToLower(prefix))
	return s
}

// CloseDocs shuts the generic document API: /docs, /docs/{id}, /docs/{id}/acl and /quota answer 404
// to everybody, whatever the guard or the ACLs say. For a server whose store is shared with a
// public (anonymous) offer, where the generic API would otherwise let anyone read and write every
// document that has no ACL. /health, /auth/*, /directory/* and the routes added with WithRoutes
// stay.
func (s *Server) CloseDocs() *Server {
	s.docsClosed = true
	return s
}

// DocsClosed reports whether the generic document API is closed to anonymous callers: shut by
// CloseDocs, or every document without an ACL is closed (WithRequireACL) and sign-in is configured
// (WithAuth), so that nobody can write or read without being signed in and listed.
func (s *Server) DocsClosed() bool {
	return s.docsClosed || (s.requireACL && s.auth != nil)
}

// reserved says an id is not for people to use: this package's own keys and the hidden ones.
func (s *Server) reserved(id string) bool {
	if isReservedDocID(id) {
		return true
	}
	lower := strings.ToLower(id)
	for _, p := range s.hidden {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// NewAuthManager builds an auth.Manager from cfg, or returns (nil,
// nil) when cfg has no providers configured -- the "auth is entirely
// optional" case ConfigFromEnv's own doc comment describes. Exists
// mainly so cmd/docstore's main can build a Manager without importing
// the auth package itself.
func NewAuthManager(ctx context.Context, cfg AuthConfig) (*auth.Manager, error) {
	if len(cfg.Providers) == 0 {
		return nil, nil
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("docstore: AuthConfig.BaseURL is required when Providers is set")
	}
	return auth.NewManager(ctx, auth.Config{
		BaseURL:       cfg.BaseURL,
		Providers:     cfg.Providers,
		SessionTTL:    cfg.SessionTTL,
		ReturnOrigins: cfg.ReturnOrigins,
	})
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	if s.docsClosed {
		closed := http.NotFoundHandler()
		mux.Handle("/quota", closed)
		mux.Handle("/docs", closed)
		mux.Handle("/docs/", closed)
	} else {
		mux.HandleFunc("/quota", s.handleQuota)
		mux.HandleFunc("/docs", s.handleList)
		mux.HandleFunc("/docs/", s.handleDoc)
	}
	if s.auth != nil {
		mux.Handle("/auth/", s.auth.Routes())
	}
	if s.auth != nil && s.directory != nil {
		mux.HandleFunc("/directory/challenge", s.handleChallenge)
		mux.HandleFunc("/directory/publish", s.handlePublish)
		mux.HandleFunc("/directory/keys/", s.handleRevoke)
		mux.HandleFunc("/directory/lookup", s.handleLookup)
		mux.HandleFunc("/directory/log", s.handleLog)
		mux.HandleFunc("/directory/log/head", s.handleLogHead)
		mux.HandleFunc("/directory/log/address", s.handleLogAddress)
		mux.HandleFunc("/directory/enroll", s.handleEnrollRoot)
		mux.HandleFunc("/directory/enroll/", s.handleEnrollSub)
	}
	for _, e := range s.extra {
		mux.Handle(e.pattern, e.h)
	}
	return guard.Or(s.guard).Wrap(exposeHeaders(mux))
}

// ExposedHeaders is the Access-Control-Expose-Headers value of the document routes: the response
// headers a script on another origin needs (the deadline of an expiring document, how long to wait
// after a 429 or 503, and the terms link of a public offer), which a browser hides unless they are
// listed.
const ExposedHeaders = "X-Gojiberry-Expires, Retry-After, Link"

func exposeHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Expose-Headers", ExposedHeaders)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	out := map[string]any{"status": "ok", "backend": s.backend}
	if h, ok := s.directory.(DirectoryHealther); ok {
		out["directory"] = h.DirectoryHealth()
	}
	_ = json.NewEncoder(w).Encode(out) // a write failure here means the client disconnected -- nothing actionable to do about it
}

// quotaResponse is handleQuota's own JSON shape -- Available false
// means exactly what QuotaReporter's own doc comment describes: this
// backend has no real answer to "how much space is left" (it doesn't
// implement QuotaReporter at all, or its own Usage call failed), not
// that the answer is zero.
type quotaResponse struct {
	Available bool  `json:"available"`
	Used      int64 `json:"used,omitempty"`
	Total     int64 `json:"total,omitempty"`
}

// handleQuota reports s's own configured Adapter's storage quota, if
// it has one -- see QuotaReporter's own doc comment (docstore.go) for
// which backends do. Always 200, never an error status: "this backend
// can't answer that" is a normal, expected response shape here, not a
// server failure.
func (s *Server) handleQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	reporter, ok := s.adapter.(QuotaReporter)
	if !ok {
		_ = json.NewEncoder(w).Encode(quotaResponse{Available: false})
		return
	}
	q, err := reporter.Usage(r.Context())
	if err != nil {
		_ = json.NewEncoder(w).Encode(quotaResponse{Available: false})
		return
	}
	_ = json.NewEncoder(w).Encode(quotaResponse{Available: true, Used: q.Used, Total: q.Total})
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	metas, err := s.adapter.List(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	// Filter out the reserved ACL keyspace (acl.go) -- each one is
	// this package's own bookkeeping, not a real document, and should
	// never appear in a document listing.
	var deadlines map[string]time.Time
	if x, ok := s.adapter.(Expirer); ok {
		deadlines, _ = x.Expiries(r.Context()) // best effort: a listing without deadlines still lists
	}
	visible := make([]listItem, 0, len(metas))
	for _, m := range metas {
		if s.reserved(m.ID) {
			continue
		}
		// With sign-in on, only list what the caller may read.
		if allowed, _ := s.checkPermission(r, m.ID, http.MethodGet); !allowed {
			continue
		}
		item := listItem{Meta: m}
		if t, ok := deadlines[m.ID]; ok {
			t = t.UTC()
			item.Expires = &t
		}
		visible = append(visible, item)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(visible) // a write failure here means the client disconnected -- nothing actionable to do about it
}

func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	if idEndsInRoute(r) {
		http.Error(w, "a document id may not end in /renew or /acl (those paths are routes)", http.StatusBadRequest)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/docs/")
	if id, ok := strings.CutSuffix(path, "/renew"); ok {
		s.handleRenew(w, r, id)
		return
	}
	if id, ok := strings.CutSuffix(path, "/acl"); ok {
		if id == "" || s.reserved(id) {
			http.Error(w, "missing or invalid document id", http.StatusBadRequest)
			return
		}
		s.handleACL(w, r, id)
		return
	}
	id := path
	if id == "" || s.reserved(id) {
		http.Error(w, "missing or invalid document id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPut:
		if allowed, status := s.checkPermission(r, id, http.MethodPut); !allowed {
			writePermissionError(w, status)
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDocBytes))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				http.Error(w, "document too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "reading the request body failed", http.StatusBadRequest)
			return
		}
		var saveErr error
		if match, none := r.Header.Get("If-Match"), r.Header.Get("If-None-Match"); match != "" || none != "" {
			cond, ok := s.adapter.(ConditionalAdapter)
			if !ok {
				http.Error(w, "conditional save is not supported", http.StatusNotImplemented)
				return
			}
			// If-None-Match: * means "only if there is none"; If-Match carries the digest
			// of what was read, in quotes. Anything else (another If-None-Match value,
			// If-Match: *) is not something this store means by those headers.
			prev := strings.Trim(match, `"`)
			if none != "" {
				if strings.TrimSpace(none) != "*" || match != "" {
					http.Error(w, "unsupported precondition", http.StatusBadRequest)
					return
				}
				prev = ""
			} else if prev == "" || prev == "*" {
				http.Error(w, "unsupported precondition", http.StatusBadRequest)
				return
			}
			saveErr = cond.SaveIf(r.Context(), id, data, prev)
		} else {
			saveErr = s.adapter.Save(r.Context(), id, data)
		}
		if errors.Is(saveErr, ErrConflict) {
			http.Error(w, "the document changed", http.StatusPreconditionFailed)
			return
		}
		if errors.Is(saveErr, ErrConditionalUnsupported) {
			http.Error(w, "conditional save is not supported", http.StatusNotImplemented)
			return
		}
		if saveErr != nil {
			serverError(w, saveErr)
			return
		}
		s.setExpires(w, r, id)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		if allowed, status := s.checkPermission(r, id, http.MethodGet); !allowed {
			writePermissionError(w, status)
			return
		}
		doc, err := s.adapter.Load(r.Context(), id)
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		s.setExpires(w, r, id)
		_, _ = w.Write(doc.Data) // a write failure here means the client disconnected -- nothing actionable to do about it
	case http.MethodDelete:
		if allowed, status := s.checkPermission(r, id, http.MethodDelete); !allowed {
			writePermissionError(w, status)
			return
		}
		err := s.adapter.Delete(r.Context(), id)
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// idEndsInRoute reports whether the request names a document whose id ends in "/renew" or
// "/acl" with the slash percent-encoded ("/docs/a%2Frenew", what HTTPAdapter sends for the id
// "a/renew"). The router decodes the path, so such an id would be taken for the renew or ACL
// route of document "a": PUT, POST and every other method on it are refused with 400 instead
// (the same rule is what keeps such an id from being created: ids ending in these segments are
// reserved, and a real route is always sent with its slash plain).
func idEndsInRoute(r *http.Request) bool {
	ep := strings.ToLower(r.URL.EscapedPath())
	return strings.HasSuffix(ep, "%2frenew") || strings.HasSuffix(ep, "%2facl")
}

// handleRenew serves POST /docs/{id}/renew: the caller says document id was edited somewhere
// else (the collaboration hub, which keeps its state apart from the document), and the store
// moves its deadline to now + TTL without writing it. The permission is the one a PUT needs
// (checkPermission): the owner or an editor, never a viewer, never an anonymous caller of a
// document with an access list; a document nobody has shared is open, as for a PUT, and with
// RequireACL it is closed (a renewal never creates the access list the way a first PUT does).
// 204 with X-Gojiberry-Expires; 404 for a document that does not exist or has expired (it is
// never revived), marked with GoneHeader and {"error":"gone"} so a client can tell it from any other 404; 501 when the store does not expire documents.
func (s *Server) handleRenew(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" || s.reserved(id) {
		http.Error(w, "missing or invalid document id", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if allowed, status := s.checkPermission(r, id, methodRenew); !allowed {
		writePermissionError(w, status)
		return
	}
	rn, ok := s.adapter.(Renewer)
	if !ok {
		http.Error(w, "this store does not expire documents", http.StatusNotImplemented)
		return
	}
	switch err := rn.Renew(r.Context(), id); {
	case errors.Is(err, ErrNotFound):
		// The marker tells a real "document gone" from any other 404 (a proxy, a wrong route,
		// an older server): HTTPAdapter.Renew returns ErrNotFound only for this.
		w.Header().Set(GoneHeader, "1")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"gone"}`))
	case err != nil:
		serverError(w, err)
	default:
		s.setExpires(w, r, id)
		w.WriteHeader(http.StatusNoContent)
	}
}
