package docstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/elaous/gojiberry/telemetry"
)

// telemetryPerMinute is how many batches one client may send a minute (a page sends one every
// minute at most).
const telemetryPerMinute = 6

// WithTelemetry mounts the usage receiver (package telemetry): POST /telemetry takes a batch of
// counts from the allowlist and only increments counters, and GET /admin/telemetry (the guard of
// WithDirectoryAdmin) reports the totals of at least k, and /admin/usage the request totals. The
// route reads no cookie or token and keeps no address: the per-client budget is keyed by a hash of
// the address that lives in memory only and is never written to the store or to a log.
// Without this call the route does not exist.
func (s *Server) WithTelemetry(c *telemetry.Counters) *Server {
	if c == nil {
		return s
	}
	s.telemetry = c
	s.syncUsageAllowlist()
	rec := telemetry.NewReceiver(c).WithBatchHook(s.routeUsage).WithLimit(func(r *http.Request) (bool, time.Duration) {
		sum := sha256.Sum256([]byte(s.clientAddr(r)))
		return s.limits().telemetry.Allow(r.Context(), "tm:"+hex.EncodeToString(sum[:8]))
	})
	s.WithRoutes("/telemetry", rec)
	return s
}

// handleTelemetryReport serves GET /admin/telemetry: the totals of at least k (?k=, default 5,
// never below the default), by day, name and value.
func (s *Server) handleTelemetryReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.dirAdmin(r) {
		lim := s.limits().adminFail
		if ok, wait := lim.Allow(r.Context(), "ad:"+s.clientAddr(r)); !ok {
			tooMany(w, wait)
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	k := telemetry.DefaultK
	if v, err := strconv.Atoi(r.URL.Query().Get("k")); err == nil && v > k {
		k = v // a larger threshold is allowed, a smaller one is not
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"k": k, "totals": s.telemetry.Report(k)})
}
