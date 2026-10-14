package docstore

import (
	"context"
	"net/http"
	"time"

	"github.com/elaous/gojiberry/audit"
	"github.com/elaous/gojiberry/observe"
)

// WithAudit turns on the operator's audit log (package audit, docs/audit.md): one line for each document
// created or deleted, each batch of grants or revocations in an access list, each sweep that expired
// documents, each directory key enrolled, revoked or reset, and (AuditLimiters) each request a rate limit
// refused. An event has no free text: it names what happened, how it ended and the class of the one who did it,
// never a name, an address, a document id or content. A subject (a document, an account) is followed through the
// log by a Ref, the keyed hash of its id under key; with no key there is no Ref. A nil logger turns it off.
func (s *Server) WithAudit(l *audit.Logger, key []byte) *Server {
	s.auditLog, s.auditKey = l, key
	if e, ok := s.adapter.(*ExpiringAdapter); ok && l != nil {
		e.OnExpired = func(n int) { s.audit(audit.DocumentExpired, audit.OK, audit.System, audit.NoReason, n, "") }
	}
	return s
}

// audit writes one event (see WithAudit); subject is what the Ref stands for ("" for none).
func (s *Server) audit(kind audit.Kind, outcome audit.Outcome, actor audit.Actor, reason audit.Reason, count int, subject string) {
	if s.auditLog == nil {
		return
	}
	e := audit.Event{Kind: kind, Outcome: outcome, Actor: actor, Reason: reason, Count: count}
	if subject != "" && len(s.auditKey) > 0 {
		e.Ref = audit.Pseudonym(s.auditKey, subject)
	}
	_ = s.auditLog.Log(e) // a refused or failed write is counted by the logger (Rejected); the request goes on
}

// auditActor is the class of the one making the request on document id: its owner, an editor, or nobody known.
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

// auditGrants logs the difference between two access lists: grants added (access.granted) and removed
// (access.revoked), by count.
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

// AuditLimiters makes every limiter of f log the requests it refuses (ratelimit.hit, denied, over_rate), by
// the count of one and no key or client. With a nil logger it returns f.
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

// auditSignIns logs a failed sign-in: a provider callback or a passkey request that ended in a client error other than
// "too many requests" (those are ratelimit.hit). The event says that it was refused and the class of the reason (a
// refusal, or a signature or state that did not check), and nothing else: not the provider, the account, the
// address or anything the person typed. It reads the status of the answer and the route template, never the request.
func (s *Server) auditSignIns(next http.Handler) http.Handler {
	if s.auditLog == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := observe.RouteOf(r.URL.Path)
		if route != "/auth/{provider}/callback" && !(route == "/auth/passkey/*" && r.Method == http.MethodPost) {
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

// codeWriter remembers the status an answer was given.
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

// Flush and Unwrap keep streaming and http.ResponseController working through the wrapper.
func (c *codeWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *codeWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
