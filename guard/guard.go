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

func Or(g Guard) Guard {
	if g == nil {
		return Open()
	}
	return g
}

// Options are the CORS details every built-in Guard shares.
type Options struct {
	Methods string
	// Headers is Access-Control-Allow-Headers; the default is "Content-Type, Authorization".
	Headers string
	// TokenHeader names the header a Token guard reads.
	TokenHeader string
	// only for "/ws" paths: browsers cannot set WebSocket headers
	TokenQuery string
	// Public lists paths that need no token.
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

func (o Options) bearer(r *http.Request) (string, bool) {
	var v string
	var ok bool
	if o.TokenHeader == "" {
		v, ok = strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	} else {
		v = r.Header.Get(o.TokenHeader)
		ok = v != ""
	}
	if !ok && o.TokenQuery != "" && strings.HasSuffix(r.URL.Path, "/ws") {
		v = r.URL.Query().Get(o.TokenQuery)
		ok = v != ""
	}
	return v, ok
}

func (o Options) public(path string) bool {
	for _, p := range o.Public {
		if p == path {
			return true
		}
	}
	return false
}

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

// checks nothing: not for data that matters or costly work
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

// another Origin gets 403 whatever the method; no Origin passes
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
			// no Origin: not a browser cross-origin call
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

// constant-time compare; preflight requests pass
// constant-time compare; an empty secret refuses every request
func Token(secret string, opts ...Options) Guard {
	o := pick(opts)
	return TokenFunc(func(r *http.Request) bool {
		got, ok := o.bearer(r)
		return ok && secret != "" && subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1
	}, o)
}

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

// put Origins first: its CORS headers must win
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
