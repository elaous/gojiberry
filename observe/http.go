package observe

import (
	"net/http"
	"strings"
	"sync/atomic"
)

// a pure function of path shape: no person-chosen segment appears
func RouteOf(path string) string {
	p := strings.ToLower(path)
	switch {
	case p == "/health" || p == "/healthz":
		return "/health"
	case p == "/ops" || p == "/presence" || p == "/translate" || p == "/speak" || p == "/transcribe":
		return p
	case strings.HasPrefix(p, "/local/"):
		return "/local/*"
	case strings.HasPrefix(p, "/google/"):
		return "/google/*"
	case p == "/ready":
		return "/ready"
	case p == "/quota":
		return "/quota"
	case p == "/telemetry":
		return "/telemetry"
	case p == "/docs" || p == "/docs/":
		return "/docs"
	case strings.HasPrefix(p, "/docs/"):
		switch {
		case strings.HasSuffix(p, "/acl"):
			return "/docs/{id}/acl"
		case strings.HasSuffix(p, "/renew"):
			return "/docs/{id}/renew"
		case strings.HasSuffix(p, "/events"):
			return "/docs/{id}/events"
		case strings.HasSuffix(p, "/snapshot"):
			return "/docs/{id}/snapshot"
		case strings.HasSuffix(p, "/ws"):
			return "/docs/{id}/ws"
		case strings.HasSuffix(p, "/ops"):
			return "/docs/{id}/ops"
		case strings.HasSuffix(p, "/presence"):
			return "/docs/{id}/presence"
		case strings.HasSuffix(p, "/role"):
			return "/docs/{id}/role"
		}
		return "/docs/{id}"
	case p == "/me/config" || strings.HasPrefix(p, "/me/config/"):
		return "/me/config"
	case p == "/auth/me":
		return "/auth/me"
	case p == "/auth/logout":
		return "/auth/logout"
	case strings.HasPrefix(p, "/auth/passkey/") || p == "/auth/passkey":
		return "/auth/passkey/*"
	case strings.HasPrefix(p, "/auth/"):
		switch {
		case strings.HasSuffix(p, "/login"):
			return "/auth/{provider}/login"
		case strings.HasSuffix(p, "/callback"):
			return "/auth/{provider}/callback"
		}
		return "/auth/other"
	case strings.HasPrefix(p, "/directory/"):
		return "/directory/*"
	case strings.HasPrefix(p, "/admin/"):
		return "/admin/*"
	case strings.HasPrefix(p, "/public/"):
		return "/public/*"
	}
	return "other"
}

func methodOf(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return m
	}
	return "other"
}

func statusClass(code int) string {
	switch {
	case code >= 100 && code < 200:
		return "1xx"
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500 && code < 600:
		return "5xx"
	}
	return "other"
}

func flowOf(route string) string {
	switch route {
	case "/auth/{provider}/login", "/auth/{provider}/callback", "/auth/me", "/auth/logout", "/auth/other":
		return "signin"
	case "/auth/passkey/*":
		return "passkey"
	case "/directory/*":
		return "directory"
	case "/public/*":
		return "public"
	case "/admin/*":
		return "admin"
	}
	return ""
}

func outcomeOf(code int) string {
	switch {
	case code == http.StatusTooManyRequests:
		return "limited"
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return "denied"
	case code >= 500:
		return "error"
	case code >= 400:
		return "rejected"
	}
	return "ok"
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.code == 0 && (code >= 200 || code == http.StatusSwitchingProtocols) {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Flush lets streaming handlers keep working through the wrapper.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the real writer (hijack, deadlines).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func Middleware(o *Observer, next http.Handler) http.Handler {
	if !o.Enabled() {
		return next
	}
	var inflight atomic.Int64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, method := RouteOf(r.URL.Path), methodOf(r.Method)
		start := o.now()
		ctx, sp := o.Start(r.Context(), SpanHTTP, S("route", route), S("method", method))
		o.Set(HTTPInflight, float64(inflight.Add(1)))
		sw := &statusWriter{ResponseWriter: w}
		finished := false
		defer func() {
			o.Set(HTTPInflight, float64(inflight.Add(-1)))
			code := sw.code
			switch {
			case !finished: // the handler panicked
				o.Error("panic")
				code = http.StatusInternalServerError
			case code == 0:
				code = http.StatusOK
			case code >= 500:
				o.Error("server_error")
			}
			class := statusClass(code)
			o.Add(HTTPRequests, 1, "route", route, "method", method, "status_class", class)
			o.Observe(HTTPDuration, o.now().Sub(start).Seconds(), "route", route, "method", method)
			if flow := flowOf(route); flow != "" {
				o.Add(AuthOutcomes, 1, "flow", flow, "outcome", outcomeOf(code))
			}
			result := "ok"
			if code >= 500 {
				result = "error"
			}
			sp.Set(S("status_class", class), N("status_code", float64(code)), S("result", result))
			sp.End()
		}()
		next.ServeHTTP(sw, r.WithContext(ctx))
		finished = true
	})
}
