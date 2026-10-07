package passkey

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/elaous/gojiberry/docstore"
)

// Limiter decides whether a caller may start or finish a ceremony now. It is docstore.Limiter:
// the default is NewRateLimiter (a per-key token bucket in this process); replicas that must
// share a budget inject docstore.StoreLimiter (or any other) as Config.Limiter, a deployment
// with a gateway that already limits can inject Unlimited (which turns the check off).
//
// key is "<route group>|<client>" where the client comes from Config.ClientIP (the IP address
// by default). Allow returns ok=false and how long to wait when the caller is over its budget.
type Limiter = docstore.Limiter

// Unlimited is a Limiter that allows everything.
type Unlimited = docstore.Unlimited

// RateLimiter is a token bucket per key held in this process (docstore.RateLimiter).
type RateLimiter = docstore.RateLimiter

// NewRateLimiter makes a limiter allowing burst requests at once and perMinute on average.
func NewRateLimiter(perMinute float64, burst int) *RateLimiter {
	return docstore.NewRateLimiter(perMinute, burst)
}

// Defaults of the limiter a Service makes when Config.Limiter is nil: per client address and
// route group, 20 requests at once and 30 a minute on average.
const (
	defaultPerMinute = 30
	defaultBurst     = 20
	// DefaultPerMinute and DefaultBurst are those numbers, for a host that builds the Limiter
	// itself (docstore.StoreLimiters) and wants the same budget.
	DefaultPerMinute = defaultPerMinute
	DefaultBurst     = defaultBurst
)

// ClientIP is the address of the client of r: the connection's remote address.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// TrustedProxies returns a Config.ClientIP that reads the client's address from the
// X-Forwarded-For header, but only when the connection comes from one of the given proxies
// (CIDR ranges or single addresses, "10.0.0.0/8", "127.0.0.1"). Any other connection is taken
// at its remote address, so a caller cannot choose its own key by sending the header. The
// address used is the rightmost one that is not itself a trusted proxy.
func TrustedProxies(proxies ...string) func(*http.Request) string {
	var nets []netip.Prefix
	for _, p := range proxies {
		if pf, err := netip.ParsePrefix(p); err == nil {
			nets = append(nets, pf)
		} else if a, err := netip.ParseAddr(p); err == nil {
			nets = append(nets, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	trusted := func(s string) bool {
		a, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil {
			return false
		}
		for _, n := range nets {
			if n.Contains(a.Unmap()) {
				return true
			}
		}
		return false
	}
	return func(r *http.Request) string {
		remote := ClientIP(r)
		if !trusted(remote) {
			return remote
		}
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(parts) - 1; i >= 0; i-- {
			p := strings.TrimSpace(parts[i])
			if p == "" {
				continue
			}
			if !trusted(p) {
				return p
			}
		}
		return remote
	}
}
