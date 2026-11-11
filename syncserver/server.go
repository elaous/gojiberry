package syncserver

import (
	"encoding/json"
	"github.com/elaous/gojiberry/guard"
	"github.com/elaous/gojiberry/observe"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/elaous/gojiberry/crdt"
)

type Config struct {
	Guard  guard.Guard
	Addr   string // listen address, e.g. ":8080"
	Region string // operator-chosen label for where this instance is hosted
	// Strategy selects the crdt.MergeStrategy this relay's own Doc merges with.
	Strategy crdt.MergeStrategy
}

func ConfigFromEnv() Config {
	strategy := crdt.MergeLWW
	if envOr("GOJIBERRY_SYNC_STRATEGY", "lww") == "char-crdt" {
		strategy = crdt.MergeCharCRDT
	}
	return Config{
		Guard:    guard.FromEnv("GOJIBERRY_SYNC", "/health"),
		Addr:     envOr("GOJIBERRY_SYNC_ADDR", ":8080"),
		Region:   envOr("GOJIBERRY_SYNC_REGION", "unspecified"),
		Strategy: strategy,
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type Presence struct {
	Replica string `json:"replica"`
	Name    string `json:"name"`
	Color   string `json:"color"`
	BlockID string `json:"blockId"`
	Col     int    `json:"col"`
}

type presenceEntry struct {
	Presence
	lastSeen time.Time
}

const presenceTTL = 10 * time.Second

// Server relays Ops between clients.
type Server struct {
	cfg      Config
	mu       sync.Mutex
	doc      *crdt.Doc
	presence map[string]presenceEntry // by Replica
	now      func() time.Time
}

// NewServer creates a relay for the given config.
func NewServer(cfg Config) *Server {
	return &Server{
		cfg:      cfg,
		doc:      crdt.NewDocWithStrategy("server:"+cfg.Region, cfg.Strategy),
		presence: make(map[string]presenceEntry),
		now:      time.Now,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ops", s.handleOps)
	mux.HandleFunc("/presence", s.handlePresence)
	return guard.Or(s.cfg.Guard).Wrap(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"region": s.cfg.Region})
}

const (
	maxBody     = 8 << 20 // bytes in one request
	maxPresence = 256     // cursors kept
)

// deferred unlock: a panic in Apply must not leave it locked
func (s *Server) applyOps(ops []crdt.Op) {
	done := observe.Default().Work("sync_merge")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.doc.Apply(ops...)
	done("ok")
}

func (s *Server) handleOps(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var ops []crdt.Op
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&ops); err != nil {
			http.Error(w, "invalid or oversized request body", http.StatusBadRequest)
			return
		}
		s.applyOps(ops)
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

func (s *Server) handlePresence(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var p Presence
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&p); err != nil {
			http.Error(w, "invalid or oversized request body", http.StatusBadRequest)
			return
		}
		if p.Replica == "" || len(p.Replica) > 128 || len(p.Name) > 256 || len(p.Color) > 64 {
			http.Error(w, "replica is required (at most 128 characters)", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		now := s.now()
		for id, e := range s.presence {
			// a poster that never polls must not grow the map
			if now.Sub(e.lastSeen) > presenceTTL {
				delete(s.presence, id)
			}
		}
		if _, known := s.presence[p.Replica]; !known && len(s.presence) >= maxPresence {
			s.mu.Unlock()
			http.Error(w, "too many people", http.StatusTooManyRequests)
			return
		}
		s.presence[p.Replica] = presenceEntry{Presence: p, lastSeen: now}
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		self := r.URL.Query().Get("replica")
		cutoff := s.now().Add(-presenceTTL)
		s.mu.Lock()
		peers := make([]Presence, 0, len(s.presence))
		for id, entry := range s.presence {
			if entry.lastSeen.Before(cutoff) {
				delete(s.presence, id)
				continue
			}
			if id == self {
				continue
			}
			peers = append(peers, entry.Presence)
		}
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, peers)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
