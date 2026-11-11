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

// TelemetryKey is where the usage totals are kept.
const TelemetryKey = "telemetry:totals"

var telemetrySaveDelay = 5 * time.Second

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

type telemetryShared struct {
	telemetryStore
	c ConditionalAdapter
}

func (t telemetryShared) LoadVersion(ctx context.Context) ([]byte, string, bool, error) {
	data, found, err := t.Load(ctx)
	if err != nil || !found {
		return nil, "", false, err
	}
	return data, Digest(data), true, nil
}

func (t telemetryShared) SaveIf(ctx context.Context, data []byte, version string) error {
	err := t.c.SaveIf(ctx, TelemetryKey, data, version)
	if errors.Is(err, ErrConflict) {
		return telemetry.ErrConflict
	}
	return err
}

const telemetryPerMinute = 6

// WithTelemetry mounts the usage receiver.
func (s *Server) WithTelemetry(c *telemetry.Counters) *Server {
	if c == nil {
		return s
	}
	s.telemetry = c
	if s.adapter != nil {
		var err error
		if ca, ok := s.adapter.(ConditionalAdapter); ok {
			err = c.PersistShared(context.Background(), telemetryShared{telemetryStore{s.adapter}, ca}, telemetrySaveDelay, s.clock)
		} else {
			log.Printf("docstore: the store has no conditional writes, so usage totals are kept by one process only: run one counting process or use a store that has them")
			err = c.Persist(context.Background(), telemetryStore{s.adapter}, telemetrySaveDelay, s.clock)
		}
		if err != nil {
			log.Printf("docstore: usage totals could not be loaded from the store (%v); counting from zero and saving over them", err)
		}
	}
	s.syncUsageAllowlist()
	rec := telemetry.NewReceiver(c).WithBatchHook(s.routeUsage).WithLimit(func(r *http.Request) (bool, time.Duration) {
		sum := sha256.Sum256([]byte(s.clientAddr(r)))
		return s.limits().telemetry.Allow(r.Context(), "tm:"+hex.EncodeToString(sum[:8]))
	})
	s.WithRoutes("/telemetry", rec)
	return s
}

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
	if s.telemetry.Shared() { // the other processes' counts too
		_ = s.telemetry.Sync(r.Context(), s.clock)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"k": k, "totals": s.telemetry.Report(k)})
}

// WithTelemetryNoise makes the report round each total.
func (s *Server) WithTelemetryNoise(step int) error {
	if s.telemetry == nil {
		return nil
	}
	return s.telemetry.SetNoise(step)
}
