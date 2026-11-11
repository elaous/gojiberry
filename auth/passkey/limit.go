package passkey

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/elaous/gojiberry/docstore"
)

type Limiter = docstore.Limiter

// Unlimited is a Limiter that allows everything.
type Unlimited = docstore.Unlimited

type RateLimiter = docstore.RateLimiter

func NewRateLimiter(perMinute float64, burst int) *RateLimiter {
	return docstore.NewRateLimiter(perMinute, burst)
}

const (
	defaultPerMinute = 30
	defaultBurst     = 20
	DefaultPerMinute = defaultPerMinute
	DefaultBurst     = defaultBurst
)

const (
	DefaultAccountPerMinute = 20.0 / 60
	DefaultAccountBurst     = 10
)

func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

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
