// Package syncserver is the decoupled backend half of the sync
// engine: a thin relay that clients push their offline Ops to and
// pull everyone else's from. It holds no client-specific logic and no
// hardcoded address — every deployment detail comes from the
// environment, so the exact same binary can sit behind a domestic
// Brazilian data center for the reference deployment, or any other
// operator's own infrastructure, by changing environment variables
// alone.
package syncserver

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"

	"github.com/elaous/gojiberry/crdt"
)

// Config is loaded entirely from the environment; nothing here is
// compiled in, so cloning this repo for a different country or
// operator only ever requires new environment variables, never a
// code change or a fork of this package.
type Config struct {
	Addr   string // listen address, e.g. ":8080"
	Region string // operator-chosen label for where this instance is hosted
}

// ConfigFromEnv reads GOJIBERRY_SYNC_ADDR and GOJIBERRY_SYNC_REGION,
// falling back to sane local defaults when unset.
func ConfigFromEnv() Config {
	return Config{
		Addr:   envOr("GOJIBERRY_SYNC_ADDR", ":8080"),
		Region: envOr("GOJIBERRY_SYNC_REGION", "unspecified"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Server relays Ops between clients. It is itself just another CRDT
// replica — conflicts are resolved by the same last-writer-wins rule
// every client uses, so the server never needs bespoke merge logic
// clients must trust.
type Server struct {
	cfg Config
	mu  sync.Mutex
	doc *crdt.Doc
}

// NewServer creates a relay for the given config. Each Server owns one
// document; a real deployment would shard by document/tenant ID, but
// the sync protocol below is unaffected by that.
func NewServer(cfg Config) *Server {
	return &Server{cfg: cfg, doc: crdt.NewDoc("server:" + cfg.Region)}
}

// Handler returns the HTTP routes: POST /ops to push a client's
// pending delta, GET /ops for a full snapshot (fresh-device
// onboarding), GET /health to confirm which region this instance is
// configured for.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ops", s.handleOps)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"region": s.cfg.Region})
}

func (s *Server) handleOps(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var ops []crdt.Op
		if err := json.NewDecoder(r.Body).Decode(&ops); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.doc.Apply(ops...)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		s.mu.Lock()
		snapshot := s.doc.Snapshot()
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, snapshot)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
