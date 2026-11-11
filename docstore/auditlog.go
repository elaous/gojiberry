package docstore

import (
	"context"
	"net/http"
	"time"

	"github.com/elaous/gojiberry/audit"
	"github.com/elaous/gojiberry/observe"
)

// WithAudit turns on the operator's audit log.
func (s *Server) WithAudit(l *audit.Logger, key []byte) *Server {
	s.auditLog, s.auditKey = l, key
	if e, ok := s.adapter.(*ExpiringAdapter); ok && l != nil {
		e.OnExpired = func(n int) { s.audit(audit.DocumentExpired, audit.OK, audit.System, audit.NoReason, n, "") }
	}
	return s
}

// audit writes one event; subjects are pseudonymised.
func (s *Server) audit(kind audit.Kind, outcome audit.Outcome, actor audit.Actor, reason audit.Reason, count int, subject string) {
	if s.auditLog == nil {
		return
	}
	e := audit.Event{Kind: kind, Outcome: outcome, Actor: actor, Reason: reason, Count: count}
	if subject != "" && len(s.auditKey) > 0 {
		e.Ref = audit.Pseudonym(s.auditKey, subject)
	}
	_ = s.auditLog.Log(e)
}

// auditActor is the class of the actor making the request.
func (s *Server) auditActor(r *http.Request, id string) audit.Actor {
	if s.auth == nil {
		return audit.Anonymous
	}
	me, signedIn := s.auth.CurrentUser(r)
	if !signedIn {
		return audit.Anonymous
	}
	acl, err := s.loadACL(r.Context(), id)
	switch {
	case err != nil:
		return audit.Anonymous
	case acl.owns(personOf(me)):
		return audit.Owner
	case acl.canWrite(personOf(me)):
		return audit.Editor
	}
	return audit.Viewer
}

// auditGrants logs the difference between two ACLs.
func (s *Server) auditGrants(r *http.Request, id string, before, after map[string]Role) {
	var granted, revoked int
	for who, role := range after {
		if before[who] != role {
			granted++
		}
	}
	for who := range before {
		if _, ok := after[who]; !ok {
			revoked++
		}
	}
	actor := s.auditActor(r, id)
	if granted > 0 {
		s.audit(audit.AccessGranted, audit.OK, actor, audit.NoReason, granted, id)
	}
	if revoked > 0 {
		s.audit(audit.AccessRevoked, audit.OK, actor, audit.NoReason, revoked, id)
	}
}

// AuditLimiters makes every limiter log its refusals.
func AuditLimiters(f LimiterFactory, l *audit.Logger) LimiterFactory {
	if f == nil || l == nil {
		return f
	}
	return func(name string, perMinute float64, burst int) Limiter {
		return auditedLimiter{Limiter: f(name, perMinute, burst), l: l}
	}
}

type auditedLimiter struct {
	Limiter
	l *audit.Logger
}

func (a auditedLimiter) Allow(ctx context.Context, key string) (bool, time.Duration) {
	ok, wait := a.Limiter.Allow(ctx, key)
	if !ok {
		_ = a.l.Log(audit.Event{Kind: audit.RateLimited, Outcome: audit.Denied, Actor: audit.Anonymous, Reason: audit.OverRate, Count: 1})
	}
	return ok, wait
}

// auditSignIns logs a failed sign-in.
func (s *Server) auditSignIns(next http.Handler) http.Handler {
	if s.auditLog == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := observe.RouteOf(r.URL.Path)
		if route != "/auth/{provider}/callback" && (route != "/auth/passkey/*" || r.Method != http.MethodPost) {
			next.ServeHTTP(w, r)
			return
		}
		rec := &codeWriter{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		switch c := rec.code; {
		case c == http.StatusTooManyRequests || c < 400 || c >= 500:
		case c == http.StatusUnauthorized || c == http.StatusForbidden:
			s.audit(audit.SignInFailed, audit.Denied, audit.Anonymous, audit.NotAllowed, 0, "")
		default:
			s.audit(audit.SignInFailed, audit.Denied, audit.Anonymous, audit.BadSignature, 0, "")
		}
	})
}

// codeWriter remembers the status an answer wrote.
type codeWriter struct {
	http.ResponseWriter
	code int
}

func (c *codeWriter) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *codeWriter) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	return c.ResponseWriter.Write(b)
}

// Flush and Unwrap keep streaming and http.ResponseController working.
func (c *codeWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *codeWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
