package public

import (
	"context"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/docstore"
	"log"
	"net/http"
	"time"
)

// Health is what /admin/health reports.
type Health struct {
	OK       bool     `json:"ok"`
	Problems []string `json:"problems,omitempty"`
	// LastSweep is when Sweep last finished.
	LastSweep time.Time `json:"lastSweep"`
	// StorageBytes and StorageMax are the bytes stored and the budget.
	StorageBytes int64 `json:"storageBytes"`
	StorageMax   int64 `json:"storageMax"`
	// PriorityBursts is how many networks sent bursts of priority reports.
	PriorityBursts int `json:"priorityBursts"`
}

// Problem names reported in Health.Problems.
const (
	ProblemExpiryDisabled = "expiry-disabled" // the store's policy gives public documents no deadline
	ProblemNoDeadline     = "no-deadline"     // a public document was seen without a deadline (and removed)
	ProblemSweeperStale   = "sweeper-stale"   // Sweep has not run for twice its interval
	ProblemSweepFailing   = "sweep-failing"   // the last Sweep ended in an error
	ProblemStorageFull    = "storage-full"    // the byte budget is used up: writes are refused
)

// DefaultSweepInterval is how often Run sweeps.
const DefaultSweepInterval = 5 * time.Minute

// expiryDisabled asks the store's policy whether expiry is off.
func (s *Service) expiryDisabled() bool {
	ea, ok := s.store.(*docstore.ExpiringAdapter)
	if !ok || ea.Policy == nil {
		return false
	}
	ttl, has := ea.Policy.TTL(docKey("health-probe"), s.now())
	return !has || ttl <= 0
}

// Health reports on the offer's own promises.
func (s *Service) Health() Health {
	now := s.now()
	s.healthMu.Lock()
	last, failing, every, born := s.lastSweep, s.sweepFailing, s.sweepEvery, s.born
	s.healthMu.Unlock()
	if every <= 0 {
		every = DefaultSweepInterval
	}
	h := Health{LastSweep: last.UTC(), StorageMax: s.cfg.MaxTotalBytes, PriorityBursts: s.priorityBursts(now)}
	h.StorageBytes, _ = s.bytes.get()
	if s.expiryDisabled() {
		h.Problems = append(h.Problems, ProblemExpiryDisabled)
	}
	if s.noDeadline.Load() {
		h.Problems = append(h.Problems, ProblemNoDeadline)
	}
	base := last
	if base.IsZero() {
		base = born
	}
	if now.Sub(base) > 2*every {
		h.Problems = append(h.Problems, ProblemSweeperStale)
	}
	if failing {
		h.Problems = append(h.Problems, ProblemSweepFailing)
	}
	if h.StorageBytes >= h.StorageMax {
		h.Problems = append(h.Problems, ProblemStorageFull)
	}
	h.OK = len(h.Problems) == 0
	return h
}

// handleHealth serves GET /public/health.
func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	if s.Health().OK {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]bool{"ok": false})
}

// handleAdminHealth serves GET /admin/health.
func (s *Service) handleAdminHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	if !s.adminAllowed(w, r) {
		return
	}
	h := s.Health()
	status := http.StatusOK
	if !h.OK {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, h)
}

var errNoDeadline = errors.New("a public document got no expiry; check the expiry policy")

func (s *Service) alarmNoDeadline() {
	s.noDeadline.Store(true)
	log.Print("public docstore: ALARM: a public document was stored without a deadline and was removed: the store's expiry policy does not cover " + fmt.Sprintf("%q", docKeyPrefix) + " keys, so the 24 hour promise is not kept; see /public/health")
}

// priorityBursts is how many networks are over the burst limit.
func (s *Service) priorityBursts(now time.Time) int {
	if s.burst != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if n, err := s.burst.bursts(ctx, now, s.cfg.PriorityBurstDocs); err == nil {
			return n
		}
	}
	return s.prio.bursts(now, s.cfg.PriorityBurstDocs)
}
