package docstore

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
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
}

// ConfigFromEnv reads GOJIBERRY_DOCSTORE_ADDR, GOJIBERRY_DOCSTORE_BACKEND,
// and GOJIBERRY_DOCSTORE_BACKEND_CONFIG (that backend's own config, as a
// raw JSON object -- e.g. GOJIBERRY_DOCSTORE_BACKEND_CONFIG='{"dsn":"..."}'
// for the postgres backend). Leaving BACKEND_CONFIG unset is fine for
// backends with no required fields (memory needs none; localfs
// defaults its dir).
func ConfigFromEnv() Config {
	return Config{
		Addr:          envOr("GOJIBERRY_DOCSTORE_ADDR", ":8090"),
		Backend:       envOr("GOJIBERRY_DOCSTORE_BACKEND", "memory"),
		BackendConfig: json.RawMessage(os.Getenv("GOJIBERRY_DOCSTORE_BACKEND_CONFIG")),
	}
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
type Server struct {
	adapter Adapter
	backend string
}

// NewServer wraps an already-constructed Adapter. Use this directly
// when embedding docstore in another program that builds its own
// Adapter; use NewServerFromConfig to select a backend by Config.
func NewServer(adapter Adapter, backendName string) *Server {
	return &Server{adapter: adapter, backend: backendName}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/docs", s.handleList)
	mux.HandleFunc("/docs/", s.handleDoc)
	return corsMiddleware(mux)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "backend": s.backend})
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
	json.NewEncoder(w).Encode(metas)
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
		w.Write(doc.Data)
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
