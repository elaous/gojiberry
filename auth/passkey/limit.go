package passkey

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Limiter decides whether a caller may start or finish a ceremony now. The default is
// NewRateLimiter (a per-key token bucket in this process); replicas that must share a budget,
// or a deployment with a gateway that already limits, inject their own as Config.Limiter
// (Unlimited turns the check off).
//
// key is "<route group>|<client>" where the client comes from Config.ClientIP (the IP address
// by default). Allow returns ok=false and how long to wait when the caller is over its budget.
type Limiter interface {
	Allow(ctx context.Context, key string) (ok bool, retryAfter time.Duration)
}

// Unlimited is a Limiter that allows everything.
type Unlimited struct{}

// Allow implements Limiter.
func (Unlimited) Allow(context.Context, string) (bool, time.Duration) { return true, 0 }

// RateLimiter is a token bucket per key: Burst requests at once, refilled at PerMinute per
// minute. Idle keys are forgotten, so memory follows the number of active callers.
type RateLimiter struct {
	PerMinute float64
	Burst     int
	// Now is the clock (time.Now if nil), for tests.
	Now func() time.Time

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

// NewRateLimiter makes a limiter allowing burst requests at once and perMinute on average.
func NewRateLimiter(perMinute float64, burst int) *RateLimiter {
	return &RateLimiter{PerMinute: perMinute, Burst: burst, buckets: map[string]*bucket{}}
}

// maxBuckets bounds the keys remembered; past it the oldest idle ones are dropped first.
const maxBuckets = 100000

// Allow implements Limiter.
func (l *RateLimiter) Allow(_ context.Context, key string) (bool, time.Duration) {
	now := time.Now()
	if l.Now != nil {
		now = l.Now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = map[string]*bucket{}
	}
	if len(l.buckets) >= maxBuckets || now.Sub(l.lastSweep) > time.Minute {
		l.sweepLocked(now)
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(l.Burst), at: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.at).Minutes() * l.PerMinute
	if b.tokens > float64(l.Burst) {
		b.tokens = float64(l.Burst)
	}
	b.at = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	if l.PerMinute <= 0 {
		return false, time.Hour
	}
	wait := time.Duration((1 - b.tokens) / l.PerMinute * float64(time.Minute))
	return false, wait
}

// sweepLocked forgets buckets that have refilled completely (idle callers).
func (l *RateLimiter) sweepLocked(now time.Time) {
	l.lastSweep = now
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.at).Minutes()*l.PerMinute >= float64(l.Burst) {
			delete(l.buckets, k)
		}
	}
	for k := range l.buckets { // still too many: drop arbitrary ones (an attacker's keys are as likely as any)
		if len(l.buckets) < maxBuckets {
			break
		}
		delete(l.buckets, k)
	}
}

// Defaults of the limiter a Service makes when Config.Limiter is nil: per client address and
// route group, 20 requests at once and 30 a minute on average.
const (
	defaultPerMinute = 30
	defaultBurst     = 20
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
