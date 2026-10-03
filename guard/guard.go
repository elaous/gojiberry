// Package guard decides who may call one of this repo's HTTP servers from a
// browser or anywhere else: it sets the CORS headers and, depending on the
// Guard chosen, checks the Origin of a request or a shared token.
//
// The servers (docstore, fontserver, syncserver, voiceserver,
// translateserver, brailleserver, the collab hub) all take a Guard in their
// Config. The choice is the operator's:
//
//	guard.Origins("https://app.example.com")   only pages from these origins
//	guard.Token(secret)                        only callers that present the token
//	guard.Chain(guard.Origins(...), guard.Token(secret))   both
//	guard.Open()                               any origin, no check (the default)
//
// or any type that implements Guard (a custom check, a JWT, mTLS, ...).
// FromEnv builds one from environment variables for the command-line
// servers.
package guard

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
)

// Guard wraps a server's handler with its access policy.
type Guard interface {
	Wrap(next http.Handler) http.Handler
}

// Func adapts a function to a Guard.
type Func func(next http.Handler) http.Handler

// Wrap implements Guard.
func (f Func) Wrap(next http.Handler) http.Handler { return f(next) }

// Or returns g, or Open() when g is nil: what a server does with a Config
// that names no Guard.
func Or(g Guard) Guard {
	if g == nil {
		return Open()
	}
	return g
}

// Options are the CORS details every built-in Guard shares.
type Options struct {
	// Methods is Access-Control-Allow-Methods; the default is
	// "GET, POST, PUT, DELETE, OPTIONS".
	Methods string
	// Headers is Access-Control-Allow-Headers; the default is
	// "Content-Type, Authorization".
	Headers string
	// TokenHeader names the header a Token guard reads. Empty means
	// "Authorization: Bearer <token>"; any other name carries the bare token
	// ("X-Gojiberry-Token: <token>"), for a server whose own sign-in already
	// uses the Authorization header (docstore).
	TokenHeader string
	// Public lists paths (exact, e.g. "/health") a Token guard lets through
	// without the token.
	Public []string
}

func (o Options) methods() string {
	if o.Methods != "" {
		return o.Methods
	}
	return "GET, POST, PUT, DELETE, OPTIONS"
}

func (o Options) headers() string {
	h := o.Headers
	if h == "" {
		h = "Content-Type, Authorization"
	}
	if o.TokenHeader != "" && !strings.Contains(strings.ToLower(h), strings.ToLower(o.TokenHeader)) {
		h += ", " + o.TokenHeader
	}
	return h
}

// bearer returns the token a request carries under the Options' header.
func (o Options) bearer(r *http.Request) (string, bool) {
	if o.TokenHeader == "" {
		return strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	v := r.Header.Get(o.TokenHeader)
	return v, v != ""
}

func (o Options) public(path string) bool {
	for _, p := range o.Public {
		if p == path {
			return true
		}
	}
	return false
}

// setIfAbsent sets a response header unless an earlier guard in a Chain has.
func setIfAbsent(h http.Header, k, v string) {
	if h.Get(k) == "" {
		h.Set(k, v)
	}
}

func allowAnyOrigin(w http.ResponseWriter, o Options) {
	h := w.Header()
	setIfAbsent(h, "Access-Control-Allow-Origin", "*")
	setIfAbsent(h, "Access-Control-Allow-Methods", o.methods())
	setIfAbsent(h, "Access-Control-Allow-Headers", o.headers())
}

// Open lets every origin call the server and checks nothing: the
// wildcard-CORS behaviour the servers had before they took a Guard. Fine for
// a server on a private network or a local demo; not for anything holding
// data that matters or running costly work.
func Open(opts ...Options) Guard {
	o := pick(opts)
	return Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowAnyOrigin(w, o)
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
}

// Origins lets only pages from the listed origins ("https://app.example.com",
// scheme and host, no path) call the server. A request that carries another
// Origin is refused with 403 whatever its method, so a page on another site
// cannot even fire a simple POST; the allowed origins get CORS headers that
// name them, and a request without an Origin header (curl, another server)
// passes: this stops other websites, not other programs. "*" means any
// origin, as Open.
func Origins(origins ...string) Guard { return OriginsWith(Options{}, origins...) }

// OriginsWith is Origins with explicit Options.
func OriginsWith(o Options, origins ...string) Guard {
	allowed := make(map[string]bool, len(origins))
	any := false
	for _, origin := range origins {
		origin = strings.TrimRight(strings.TrimSpace(origin), "/")
		switch origin {
		case "":
		case "*":
			any = true
		default:
			allowed[strings.ToLower(origin)] = true
		}
	}
	return Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			switch {
			case any:
				allowAnyOrigin(w, o)
			case origin == "":
				// not a browser cross-origin call
			case allowed[strings.ToLower(origin)]:
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
				setIfAbsent(h, "Access-Control-Allow-Methods", o.methods())
				setIfAbsent(h, "Access-Control-Allow-Headers", o.headers())
			default:
				w.Header().Add("Vary", "Origin")
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
}

// Token requires every request to carry the secret as "Authorization:
// Bearer <secret>" (compared in constant time); a request without it, or
// with another, gets 401. Preflight requests and the paths in
// Options.Public pass without it. Because the credential is the token, not
// the page's origin, CORS is open to any origin (unless an Origins guard
// earlier in a Chain already named one). An empty secret refuses every
// request rather than allowing all.
func Token(secret string, opts ...Options) Guard {
	o := pick(opts)
	return TokenFunc(func(r *http.Request) bool {
		got, ok := o.bearer(r)
		return ok && secret != "" && subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1
	}, o)
}

// TokenFunc is Token with the check supplied by the caller: ok decides, from
// the request, whether it may go on (a JWT verified against a key, a lookup
// in a table of API keys, ...).
func TokenFunc(ok func(*http.Request) bool, opts ...Options) Guard {
	o := pick(opts)
	return Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowAnyOrigin(w, o)
			switch {
			case r.Method == http.MethodOptions:
				w.WriteHeader(http.StatusNoContent)
				return
			case o.public(r.URL.Path):
			case !ok(r):
				w.Header().Set("WWW-Authenticate", `Bearer realm="gojiberry"`)
				http.Error(w, "a valid token is required", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
}

// Chain applies the guards one inside the next: the first listed runs first.
// Put Origins first, so its CORS headers (which name the origin) win over a
// Token guard's open ones.
func Chain(guards ...Guard) Guard {
	return Func(func(next http.Handler) http.Handler {
		h := next
		for i := len(guards) - 1; i >= 0; i-- {
			if guards[i] != nil {
				h = guards[i].Wrap(h)
			}
		}
		return h
	})
}

func pick(opts []Options) Options {
	if len(opts) > 0 {
		return opts[0]
	}
	return Options{}
}

// FromEnv builds a Guard from <prefix>_ALLOW_ORIGINS (comma-separated
// origins, or "*") and <prefix>_TOKEN; with both set, a request must satisfy
// both; with neither, the result is Open(). Public paths are exempt from the
// token. A command-line server passes its own prefix, for example
// "GOJIBERRY_FONTSERVER".
func FromEnv(prefix string, public ...string) Guard {
	return FromEnvWith(prefix, Options{Public: public})
}

// FromEnvWith is FromEnv with explicit Options (a TokenHeader, say).
func FromEnvWith(prefix string, o Options) Guard {
	var gs []Guard
	if raw := os.Getenv(prefix + "_ALLOW_ORIGINS"); raw != "" {
		gs = append(gs, OriginsWith(o, strings.Split(raw, ",")...))
	}
	if tok := os.Getenv(prefix + "_TOKEN"); tok != "" {
		gs = append(gs, Token(tok, o))
	}
	switch len(gs) {
	case 0:
		return Open(o)
	case 1:
		return gs[0]
	}
	return Chain(gs...)
}
