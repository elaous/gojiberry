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
// PostgresConfig, for what BackendConfig needs to hold; "minio" is
// S3Config too, just defaulted for a MinIO endpoint instead of real
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
	RequireACL    bool
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
// unset (the default) leaves auth off entirely.
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
	return cfg
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
//	GET    /docs           -- list metadata, as a JSON array of Meta
//	GET    /docs/{id}/acl  -- this document's own ACL (see acl.go), Owner-only
//	PUT    /docs/{id}/acl  -- replace it (bootstraps ownership on first use)
//	GET    /health         -- liveness + configured backend name
//
// Plus, when auth is configured (see AuthConfig/WithAuth), every
// /auth/* route auth.Manager.Routes documents -- /auth/providers,
// /auth/{provider}/login, /auth/{provider}/callback, /auth/me,
// /auth/logout.
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
	mux.HandleFunc("/quota", s.handleQuota)
	mux.HandleFunc("/docs", s.handleList)
	mux.HandleFunc("/docs/", s.handleDoc)
	if s.auth != nil {
		mux.Handle("/auth/", s.auth.Routes())
	}
	return guard.Or(s.guard).Wrap(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "backend": s.backend}) // a write failure here means the client disconnected -- nothing actionable to do about it
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
	visible := make([]Meta, 0, len(metas))
	for _, m := range metas {
		if isReservedDocID(m.ID) {
			continue
		}
		// With sign-in on, only list what the caller may read.
		if allowed, _ := s.checkPermission(r, m.ID, http.MethodGet); !allowed {
			continue
		}
		visible = append(visible, m)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(visible) // a write failure here means the client disconnected -- nothing actionable to do about it
}

func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/docs/")
	if id, ok := strings.CutSuffix(path, "/acl"); ok {
		if id == "" || isReservedDocID(id) {
			http.Error(w, "missing or invalid document id", http.StatusBadRequest)
			return
		}
		s.handleACL(w, r, id)
		return
	}
	id := path
	if id == "" || isReservedDocID(id) {
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
			prev := strings.Trim(match, `"`)
			if none != "" {
				prev = ""
			}
			saveErr = cond.SaveIf(r.Context(), id, data, prev)
		} else {
			saveErr = s.adapter.Save(r.Context(), id, data)
		}
		if errors.Is(saveErr, ErrConflict) {
			http.Error(w, "the document changed", http.StatusPreconditionFailed)
			return
		}
		if saveErr != nil {
			serverError(w, saveErr)
			return
		}
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
