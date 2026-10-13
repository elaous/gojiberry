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

// WithObserver turns on operator observability (package observe, docs/observability.md): the HTTP
// middleware records one span and the request metrics per route template, the document handlers
// time their store operations, and GET /admin/observe (the guard of WithDirectoryAdmin) shows what
// the allowlist lets out and which sinks are on. A nil observer leaves the server as it was. The
// usage counts of WithTelemetry are routed to the observer's usage sinks as well.
func (s *Server) WithObserver(o *observe.Observer) *Server {
	s.obs = o
	s.syncUsageAllowlist()
	return s
}

// syncUsageAllowlist hands the observer the telemetry allowlist, so that the usage facts it accepts
// are exactly the receiver's.
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

// routeUsage is the receiver's hook: every accepted batch (already validated, and only sent by a
// browser whose person opted in) is counted into the usage sinks.
func (s *Server) routeUsage(b telemetry.Batch) {
	for _, c := range b.Counts {
		s.obs.Usage(string(c.Name), c.Value, c.N)
	}
}

// handleObserve serves GET /admin/observe: the allowlist, the sinks that are on and the shape of
// what they sent last. It answers even when observability is off (that is how an operator looks
// before turning it on).
func (s *Server) handleObserve(w http.ResponseWriter, r *http.Request) {
	if !s.operatorOnly(w, r) {
		return
	}
	observe.WriteAdmin(w, s.obs)
}

// opStart times one store operation (a span under the request's, a counter and a duration). Call the
// returned function with the operation's error; count is the size of a listing, -1 otherwise.
func (s *Server) opStart(ctx context.Context, op string) func(err error, count int) {
	done := s.obs.StoreOp(ctx, op)
	return func(err error, count int) {
		done(storeResult(ctx, err), count)
	}
}

// storeResult sorts a store error into one of the fixed results.
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

// ObserveLimiters makes every limiter of f count its decisions (allowed or limited, by the kind of
// limiter, never by key or client) in o. With a nil observer it returns f.
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

// limiterKind maps a limiter's name to the fixed list of kinds.
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

// observeUsage reports the size of the store as gauges (counts and bytes, nothing about a document).
func (s *Server) observeUsage(u usageResponse) {
	s.obs.Set(observe.StoreDocuments, float64(u.Documents))
	s.obs.Set(observe.StoreRecords, float64(u.Records))
	s.obs.Set(observe.StoreBytes, float64(u.Bytes))
}
