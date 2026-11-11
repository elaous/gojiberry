package docstore

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/elaous/gojiberry/observe"
	"github.com/elaous/gojiberry/telemetry"
)

// WithObserver turns on operator observability.
func (s *Server) WithObserver(o *observe.Observer) *Server {
	s.obs = o
	s.syncUsageAllowlist()
	return s
}

func (s *Server) syncUsageAllowlist() {
	if s.obs == nil || s.telemetry == nil {
		return
	}
	allow := map[string][]string{}
	for _, row := range telemetry.Allowlist() {
		allow[string(row.Name)] = row.Values
	}
	s.obs.SetUsageAllowlist(allow)
}

func (s *Server) routeUsage(b telemetry.Batch) {
	for _, c := range b.Counts {
		s.obs.Usage(string(c.Name), c.Value, c.N)
	}
}

func (s *Server) handleObserve(w http.ResponseWriter, r *http.Request) {
	if !s.operatorOnly(w, r) {
		return
	}
	observe.WriteAdmin(w, s.obs)
}

func (s *Server) opStart(ctx context.Context, op string) func(err error, count int) {
	done := s.obs.StoreOp(ctx, op)
	return func(err error, count int) {
		done(storeResult(ctx, err), count)
	}
}

func storeResult(ctx context.Context, err error) string {
	switch {
	case err == nil:
		return "ok"
	case ctx.Err() != nil:
		return "canceled"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrConflict):
		return "conflict"
	}
	return "error"
}

// ObserveLimiters makes every limiter of a factory observed.
func ObserveLimiters(f LimiterFactory, o *observe.Observer) LimiterFactory {
	if f == nil || !o.Enabled() {
		return f
	}
	return func(name string, perMinute float64, burst int) Limiter {
		return observedLimiter{Limiter: f(name, perMinute, burst), o: o, kind: limiterKind(name)}
	}
}

type observedLimiter struct {
	Limiter
	o    *observe.Observer
	kind string
}

func (l observedLimiter) Allow(ctx context.Context, key string) (bool, time.Duration) {
	ok, wait := l.Limiter.Allow(ctx, key)
	outcome := "allowed"
	if !ok {
		outcome = "limited"
	}
	l.o.Add(observe.LimiterResults, 1, "limiter", l.kind, "outcome", outcome)
	return ok, wait
}

func limiterKind(name string) string {
	switch {
	case strings.HasPrefix(name, "dir-admin"):
		return "admin"
	case strings.HasPrefix(name, "dir-"):
		return "directory"
	case name == "passkey", name == "login", name == "telemetry":
		return name
	case name == "auth-miss":
		return "auth_miss"
	}
	return "other"
}

func (s *Server) observeUsage(u usageResponse) {
	s.obs.Set(observe.StoreDocuments, float64(u.Documents))
	s.obs.Set(observe.StoreRecords, float64(u.Records))
	s.obs.Set(observe.StoreBytes, float64(u.Bytes))
}
