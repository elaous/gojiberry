package docstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/elaous/gojiberry/telemetry"
)

// TelemetryKey is where the usage totals are kept when the server persists them (WithTelemetry): one
// small record, written through the same Adapter as the documents. The "telemetry:" keyspace is reserved
// from the /docs API (a client cannot read or write it) and is not a document (it never expires by itself).
const TelemetryKey = "telemetry:totals"

// telemetrySaveDelay is how long a change waits before the totals are written, so a burst of
// batches is one write.
var telemetrySaveDelay = 5 * time.Second

// telemetryStore keeps the totals under TelemetryKey in the server's Adapter.
type telemetryStore struct{ a Adapter }

func (t telemetryStore) Load(ctx context.Context) ([]byte, bool, error) {
	doc, err := t.a.Load(ctx, TelemetryKey)
	if errors.Is(err, ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return doc.Data, true, nil
}

func (t telemetryStore) Save(ctx context.Context, data []byte) error {
	return t.a.Save(ctx, TelemetryKey, data)
}

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
	// The totals live in the store, so a restart does not start the counts again. They hold only what the
	// counters hold (a number per day, name and value; see telemetry.Counters.Snapshot), are bounded by the
	// allowlist and the 90 day retention, and a record that cannot be read is replaced, not trusted.
	// Several server processes on one store each write their own totals (the last write wins).
	if s.adapter != nil {
		if err := c.Persist(context.Background(), telemetryStore{s.adapter}, telemetrySaveDelay, s.clock); err != nil {
			log.Printf("docstore: usage totals could not be loaded from the store (%v); counting from zero and saving over them", err)
		}
	}
	rec := telemetry.NewReceiver(c).WithLimit(func(r *http.Request) (bool, time.Duration) {
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

// WithTelemetryNoise makes the report of /admin/telemetry round each total at random to a multiple of
// step (2 to 1000; 0 or 1 is exact), before the k threshold is applied. See telemetry.Counters.SetNoise
// for what that protects and what it does not. It does nothing without WithTelemetry.
func (s *Server) WithTelemetryNoise(step int) error {
	if s.telemetry == nil {
		return nil
	}
	return s.telemetry.SetNoise(step)
}
