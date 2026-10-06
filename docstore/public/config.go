package public

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Config holds the limits and switches of the public offer. A zero field takes its default.
// FromEnv reads the GOJIBERRY_DOCSTORE_PUBLIC_* variables.
type Config struct {
	// Enabled is GOJIBERRY_DOCSTORE_PUBLIC=true: whether the public routes are mounted. Off
	// (the default) the server behaves exactly as without this package.
	Enabled bool

	// MaxDocBytes is the largest document a session may store (413 above it); default 4 MiB.
	MaxDocBytes int64
	// MaxVersions is how many times one document may be saved (create and edits); the next
	// edit is refused (403). It bounds how long a document can be kept alive by editing it.
	// Default 2000.
	MaxVersions int
	// IPDailyBytes is the bytes one client address may write per 24 hours (413-sized writes
	// count, refused ones do not); over it, 429. Default 50 MiB.
	IPDailyBytes int64

	// SessionTTL is how long a session lives from its last write; SessionMaxAge is the longest
	// it lives from issue however much it is used. Defaults 24h and 72h.
	SessionTTL    time.Duration
	SessionMaxAge time.Duration
	// MaxSessions is the global cap on live anonymous sessions (503 above it); default 10000.
	MaxSessions int

	// Per-client request budgets per minute (a token bucket whose burst equals the per-minute
	// figure, so 10 means 10 at once and 10 a minute): sessions issued per address, writes per
	// session, reads of view links per address, reports per address. Defaults 10, 30, 120, 5.
	SessionsPerMinute float64
	WritesPerMinute   float64
	ViewsPerMinute    float64
	ReportsPerMinute  float64

	// ReportTTL and TombstoneTTL are how long abuse reports and takedown tombstones are kept
	// before the sweep deletes them (they do not expire with documents). Defaults 720h (30
	// days) each; a tombstone must outlive the longest life of its document, which the 24 h
	// promise (plus the sweep interval) bounds.
	ReportTTL    time.Duration
	TombstoneTTL time.Duration

	// TermsURL is sent as Link: <url>; rel="terms" on every public response.
	TermsURL string
	// AdminToken is the shared secret of the takedown route (header X-Gojiberry-Admin-Token);
	// empty means the route is not mounted.
	AdminToken string
	// TrustedProxies are the CIDRs of reverse proxies whose X-Forwarded-For is believed when
	// naming a client for the limits (see passkey.TrustedProxies); none means the remote address.
	TrustedProxies []string
}

// Defaults of Config.
const (
	DefaultMaxDocBytes       = 4 << 20
	DefaultMaxVersions       = 2000
	DefaultIPDailyBytes      = 50 << 20
	DefaultSessionTTL        = 24 * time.Hour
	DefaultSessionMaxAge     = 72 * time.Hour
	DefaultSessionsPerMinute = 10
	DefaultWritesPerMinute   = 30
	DefaultViewsPerMinute    = 120
	DefaultReportsPerMinute  = 5
	DefaultRecordTTL         = 30 * 24 * time.Hour
)

// withDefaults returns c with every zero field set to its default.
func (c Config) withDefaults() Config {
	if c.MaxDocBytes <= 0 {
		c.MaxDocBytes = DefaultMaxDocBytes
	}
	if c.MaxVersions <= 0 {
		c.MaxVersions = DefaultMaxVersions
	}
	if c.IPDailyBytes <= 0 {
		c.IPDailyBytes = DefaultIPDailyBytes
	}
	if c.SessionTTL <= 0 {
		c.SessionTTL = DefaultSessionTTL
	}
	if c.SessionMaxAge <= 0 {
		c.SessionMaxAge = DefaultSessionMaxAge
	}
	if c.SessionMaxAge < c.SessionTTL {
		c.SessionMaxAge = c.SessionTTL
	}
	if c.MaxSessions <= 0 {
		c.MaxSessions = DefaultMaxSessions
	}
	if c.SessionsPerMinute <= 0 {
		c.SessionsPerMinute = DefaultSessionsPerMinute
	}
	if c.WritesPerMinute <= 0 {
		c.WritesPerMinute = DefaultWritesPerMinute
	}
	if c.ViewsPerMinute <= 0 {
		c.ViewsPerMinute = DefaultViewsPerMinute
	}
	if c.ReportsPerMinute <= 0 {
		c.ReportsPerMinute = DefaultReportsPerMinute
	}
	if c.ReportTTL <= 0 {
		c.ReportTTL = DefaultRecordTTL
	}
	if c.TombstoneTTL <= 0 {
		c.TombstoneTTL = DefaultRecordTTL
	}
	return c
}

// FromEnv reads the configuration from the environment through getenv (os.Getenv):
//
//	GOJIBERRY_DOCSTORE_PUBLIC                    true to mount the public routes (default off)
//	GOJIBERRY_DOCSTORE_PUBLIC_MAX_DOC_BYTES      bytes per document
//	GOJIBERRY_DOCSTORE_PUBLIC_MAX_VERSIONS       saves per document
//	GOJIBERRY_DOCSTORE_PUBLIC_IP_DAILY_BYTES     bytes per client address per 24 h
//	GOJIBERRY_DOCSTORE_PUBLIC_SESSION_TTL        Go duration, from the last write
//	GOJIBERRY_DOCSTORE_PUBLIC_SESSION_MAX_AGE    Go duration, from issue
//	GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS       live anonymous sessions, all clients
//	GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS_PER_MINUTE, _WRITES_PER_MINUTE, _VIEWS_PER_MINUTE, _REPORTS_PER_MINUTE
//	GOJIBERRY_DOCSTORE_PUBLIC_REPORT_TTL, _TOMBSTONE_TTL
//	GOJIBERRY_DOCSTORE_PUBLIC_TERMS_URL          the Link rel="terms" target
//	GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_TOKEN        secret of DELETE /admin/takedown/<id>
//	GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES    comma separated CIDRs
//
// A value that does not parse is an error naming the variable, never a silent default.
func FromEnv(getenv func(string) string) (Config, error) {
	var c Config
	var errs []string
	get := func(name string) string { return strings.TrimSpace(getenv("GOJIBERRY_DOCSTORE_PUBLIC" + name)) }
	i64 := func(name string, dst *int64) {
		if v := get(name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n <= 0 {
				errs = append(errs, fmt.Sprintf("GOJIBERRY_DOCSTORE_PUBLIC%s %q: want a positive integer", name, v))
				return
			}
			*dst = n
		}
	}
	rate := func(name string, dst *float64) {
		if v := get(name); v != "" {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil || n <= 0 || math.IsInf(n, 0) || math.IsNaN(n) {
				errs = append(errs, fmt.Sprintf("GOJIBERRY_DOCSTORE_PUBLIC%s %q: want a positive number", name, v))
				return
			}
			*dst = n
		}
	}
	dur := func(name string, dst *time.Duration) {
		if v := get(name); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				errs = append(errs, fmt.Sprintf("GOJIBERRY_DOCSTORE_PUBLIC%s %q: want a positive duration such as 24h", name, v))
				return
			}
			*dst = d
		}
	}
	switch strings.ToLower(get("")) {
	case "", "false", "0", "off":
	case "true", "1", "on":
		c.Enabled = true
	default:
		errs = append(errs, fmt.Sprintf("GOJIBERRY_DOCSTORE_PUBLIC %q: want true or false", get("")))
	}
	i64("_MAX_DOC_BYTES", &c.MaxDocBytes)
	i64("_IP_DAILY_BYTES", &c.IPDailyBytes)
	var versions, sessions int64
	i64("_MAX_VERSIONS", &versions)
	i64("_MAX_SESSIONS", &sessions)
	c.MaxVersions, c.MaxSessions = int(versions), int(sessions)
	dur("_SESSION_TTL", &c.SessionTTL)
	dur("_SESSION_MAX_AGE", &c.SessionMaxAge)
	dur("_REPORT_TTL", &c.ReportTTL)
	dur("_TOMBSTONE_TTL", &c.TombstoneTTL)
	rate("_SESSIONS_PER_MINUTE", &c.SessionsPerMinute)
	rate("_WRITES_PER_MINUTE", &c.WritesPerMinute)
	rate("_VIEWS_PER_MINUTE", &c.ViewsPerMinute)
	rate("_REPORTS_PER_MINUTE", &c.ReportsPerMinute)
	c.TermsURL = get("_TERMS_URL")
	c.AdminToken = get("_ADMIN_TOKEN")
	for _, p := range strings.Split(get("_TRUSTED_PROXIES"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.TrustedProxies = append(c.TrustedProxies, p)
		}
	}
	if len(errs) > 0 {
		return Config{}, fmt.Errorf("public: %s", strings.Join(errs, "; "))
	}
	return c, nil
}

// burst is the bucket size for a per-minute budget: the figure itself, at least 1.
func burst(perMinute float64) int {
	return max(1, int(math.Ceil(perMinute)))
}
