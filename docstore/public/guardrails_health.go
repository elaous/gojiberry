package public

import (
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/docstore"
	"log"
	"net/http"
	"time"
)

// ---- health

// Health is what /admin/health reports (and /public/health reduces to its OK): whether the
// promises the offer rests on are being kept. It holds no secrets and no per-document data, but
// it says how full the offer is, so it is for the operator.
type Health struct {
	OK       bool     `json:"ok"`
	Problems []string `json:"problems,omitempty"`
	// LastSweep is when Sweep last finished (the zero time: never).
	LastSweep time.Time `json:"lastSweep"`
	// StorageBytes and StorageMax are the byte counter and its budget.
	StorageBytes int64 `json:"storageBytes"`
	StorageMax   int64 `json:"storageMax"`
	// PriorityBursts is how many networks sent priority-category reports for more than
	// Config.PriorityBurstDocs different documents within the last hour: a flood of the path that
	// outranks other reports, or a real wave of abuse, either way for the operator to look at.
	// It is not a Problem (it does not turn /public/health to 503: that is for the offer's own
	// promises).
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

// DefaultSweepInterval is how often Run sweeps when it is given no interval, and the interval
// Health assumes for a Service whose Run was never started.
const DefaultSweepInterval = 5 * time.Minute

// expiryDisabled asks the store's policy (when it is an ExpiringAdapter, the only one that can be
// asked) whether a public document would get a deadline.
func (s *Service) expiryDisabled() bool {
	ea, ok := s.store.(*docstore.ExpiringAdapter)
	if !ok || ea.Policy == nil {
		return false
	}
	ttl, has := ea.Policy.TTL(docKey("health-probe"), s.now())
	return !has || ttl <= 0
}

// Health reports on the offer's own promises: documents get deadlines, the sweeper runs, the
// storage budget has room.
func (s *Service) Health() Health {
	now := s.now()
	s.healthMu.Lock()
	last, failing, every, born := s.lastSweep, s.sweepFailing, s.sweepEvery, s.born
	s.healthMu.Unlock()
	if every <= 0 {
		every = DefaultSweepInterval
	}
	h := Health{LastSweep: last.UTC(), StorageMax: s.cfg.MaxTotalBytes, PriorityBursts: s.prio.bursts(now, s.cfg.PriorityBurstDocs)}
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

// handleHealth serves GET /public/health, which anyone may call: 200 when everything holds and 503
// when not, with a body that says no more than that. The storage figures, the names of the problems
// and the time of the last sweep tell an attacker when the offer is full or its sweeper is down, so
// they are in the detailed report behind the admin guard (GET /admin/health) only.
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

// handleAdminHealth serves GET /admin/health: the detailed Health report, for the operator.
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
