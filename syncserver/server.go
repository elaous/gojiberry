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
	"github.com/elaous/gojiberry/guard"
	"github.com/elaous/gojiberry/observe"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/elaous/gojiberry/crdt"
)

// Config is loaded entirely from the environment; nothing here is
// compiled in, so cloning this repo for a different country or
// operator only ever requires new environment variables, never a
// code change or a fork of this package.
type Config struct {
	// Guard decides who may call the server: guard.Origins(...) for pages from
	// listed origins, guard.Token(...) for callers holding a token, both with
	// guard.Chain, or your own. Nil means guard.Open() (any origin, no check).
	// ConfigFromEnv reads GOJIBERRY_SYNC_ALLOW_ORIGINS and GOJIBERRY_SYNC_TOKEN.
	Guard  guard.Guard
	Addr   string // listen address, e.g. ":8080"
	Region string // operator-chosen label for where this instance is hosted
	// Strategy selects the crdt.MergeStrategy this relay's own Doc
	// merges with. The zero value, crdt.MergeLWW, is this package's
	// original behavior. Every client syncing through this server must
	// use the *same* strategy the server does -- a MergeCharCRDT client
	// relayed through a MergeLWW server doesn't just merge coarsely,
	// it loses data outright: the server's own Snapshot only carries
	// CharOps when its Doc is MergeCharCRDT, so a MergeCharCRDT client
	// receiving a CharOps-less op ends up reconstructing an *empty*
	// Text for that block (see Doc.applyCharCRDT) instead of merely
	// missing the fine-grained merge. Set Strategy: crdt.MergeCharCRDT
	// here whenever any client does.
	Strategy crdt.MergeStrategy
}

// ConfigFromEnv reads GOJIBERRY_SYNC_ADDR, GOJIBERRY_SYNC_REGION, and
// GOJIBERRY_SYNC_STRATEGY ("lww", the default, or "char-crdt"),
// falling back to sane local defaults when unset.
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

// Presence is one replica's last-broadcast cursor position and
// display identity -- ephemeral, never merged into doc and never
// persisted, unlike an Op. The client-side shape this mirrors is
// OnDesign's own model.PresencePeer; kept as this package's own
// independent type (not imported from any one client's package) for
// the same reason crdt.Op is -- a generic relay has no business
// depending on any one client's own package. BlockID/Col name a
// position the same way OnDesign's document does (a stable block ID
// plus an offset within it), but this package has no idea what either
// actually means -- it only ever stores and relays them verbatim.
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

// presenceTTL is how long a replica's last-broadcast position stays
// visible to peers after its own client stops POSTing updates (tab
// closed, network dropped) -- several sync ticks' worth of grace
// (OnDesign's own wireSync posts every 1.2s) so one slow round trip
// doesn't flicker a peer's cursor away, but short enough that a
// genuinely gone collaborator disappears within a few seconds rather
// than lingering indefinitely.
const presenceTTL = 10 * time.Second

// Server relays Ops between clients. It is itself just another CRDT
// replica — conflicts are resolved by the same last-writer-wins rule
// every client uses, so the server never needs bespoke merge logic
// clients must trust.
type Server struct {
	cfg      Config
	mu       sync.Mutex
	doc      *crdt.Doc
	presence map[string]presenceEntry // by Replica
	// now is a seam for TestPresenceExpiresAfterTTL to fast-forward
	// time deterministically instead of a real sleep -- time.Now in
	// every other caller.
	now func() time.Time
}

// NewServer creates a relay for the given config. Each Server owns one
// document; a real deployment would shard by document/tenant ID, but
// the sync protocol below is unaffected by that.
func NewServer(cfg Config) *Server {
	return &Server{
		cfg:      cfg,
		doc:      crdt.NewDocWithStrategy("server:"+cfg.Region, cfg.Strategy),
		presence: make(map[string]presenceEntry),
		now:      time.Now,
	}
}

// Handler returns the HTTP routes: POST /ops to push a client's
// pending delta, GET /ops for a full snapshot (fresh-device
// onboarding), POST /presence to broadcast this replica's own cursor
// position, GET /presence?replica=X for every *other* replica's own
// last-broadcast position, GET /health to confirm which region this
// instance is configured for. Every response carries permissive CORS
// headers and OPTIONS preflight requests short-circuit with no
// further routing -- this relay's entire reason to exist is being
// reachable from a client on a different origin (a different host,
// port, or both from wherever the client's own static assets are
// served), so without this a browser's own fetch() would refuse every
// request outright before it ever reached handleOps.
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

// applyOps merges ops into the shared document; the deferred unlock keeps a
// panic in Apply from leaving the server locked for good.
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

// handlePresence: POST records/refreshes the caller's own Presence
// (their own Replica is the entry's key, so a repeat POST from the
// same replica overwrites rather than accumulating). GET returns
// every *other* replica's own still-live Presence -- ?replica= names
// the caller's own so it never sees its own cursor echoed back --
// pruning any entry whose lastSeen has fallen outside presenceTTL as a
// side effect of every GET, rather than running a separate janitor
// goroutine: a relay this small doesn't need a background sweep to
// keep memory bounded when every read already walks the whole map.
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
		for id, e := range s.presence { // prune here too: a poster that never polls must not grow the map
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
