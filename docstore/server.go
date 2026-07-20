package docstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// "mongo"/"s3" -- see each backend's own Config type, e.g.
// PostgresConfig, for what BackendConfig needs to hold). This package
// never hardcodes that list itself -- each backend registers its own
// name via RegisterAdapter, so BuildAdapter/Config never need a code
// change to support a new one.
//
// BackendConfig is the chosen backend's own config, JSON-encoded --
// e.g. `{"dsn":"postgres://..."}` for "postgres", `{"bucket":"docs"}`
// for "s3". Passed through unparsed; only the backend itself
// understands its own shape.
type Config struct {
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
// default when unset.
func ConfigFromEnv() Config {
	cfg := Config{
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
	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Server exposes an Adapter over HTTP:
//
//	PUT    /docs/{id}  -- save (request body is the raw document bytes)
//	GET    /docs/{id}  -- load (response body is the raw document bytes)
//	DELETE /docs/{id}  -- delete
//	GET    /docs       -- list metadata, as a JSON array of Meta
//	GET    /health     -- liveness + configured backend name
//
// Plus, when auth is configured (see AuthConfig/WithAuth), every
// /auth/* route auth.Manager.Routes documents -- /auth/providers,
// /auth/{provider}/login, /auth/{provider}/callback, /auth/me,
// /auth/logout. Identity only: this Server doesn't itself check a
// request's signed-in User against anything before serving /docs/* --
// that's document ownership/permissions, a separate layer not built
// yet (see docs/ROADMAP.md's Share entry in the OnDesign repo).
type Server struct {
	adapter Adapter
	backend string
	auth    *auth.Manager
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
		BaseURL:    cfg.BaseURL,
		Providers:  cfg.Providers,
		SessionTTL: cfg.SessionTTL,
	})
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/docs", s.handleList)
	mux.HandleFunc("/docs/", s.handleDoc)
	if s.auth != nil {
		mux.Handle("/auth/", s.auth.Routes())
	}
	return corsMiddleware(mux)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "backend": s.backend}) // a write failure here means the client disconnected -- nothing actionable to do about it
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	metas, err := s.adapter.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metas) // a write failure here means the client disconnected -- nothing actionable to do about it
}

func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/docs/")
	if id == "" {
		http.Error(w, "missing document id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPut:
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.adapter.Save(r.Context(), id, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		doc, err := s.adapter.Load(r.Context(), id)
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(doc.Data) // a write failure here means the client disconnected -- nothing actionable to do about it
	case http.MethodDelete:
		err := s.adapter.Delete(r.Context(), id)
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
