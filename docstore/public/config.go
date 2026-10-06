package public

import (
	"fmt"
	"math"
	"net/netip"
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
	// IPDailyBytes is the bytes one client (see ClientKey) may write per 24 hours, charged before
	// the body is read and refunded for what is not stored; over it, 429. Default 50 MiB.
	IPDailyBytes int64

	// SessionTTL is how long a session lives from its last write; SessionMaxAge is the longest
	// it lives from issue however much it is used. Defaults 24h and 72h. SessionIdleTTL is how
	// long a session that has not stored a document yet lives (the first write extends it to
	// SessionTTL); default 1h, so sessions that are issued and never used free their place fast.
	SessionTTL     time.Duration
	SessionMaxAge  time.Duration
	SessionIdleTTL time.Duration
	// MaxSessions is the global cap on live anonymous sessions (503 above it); default 10000.
	// MaxSessionsPerAddress caps the live sessions of one client address (an IPv4 address, or
	// an IPv6 /64: see ClientKey), 429 above it; default 10, so no one client can fill the
	// global cap.
	MaxSessions           int
	MaxSessionsPerAddress int

	// Per-client request budgets (a token bucket whose burst equals the figure, so 10 a minute
	// means 10 at once and 10 a minute): writes per session and per address, reads of view links
	// per address, reports per address per minute; sessions issued per address per hour.
	// Defaults 30 and 120 (writes per session, per address), 120, 5 a minute and 6 an hour.
	SessionsPerHour           float64
	WritesPerMinute           float64
	WritesPerMinutePerAddress float64
	ViewsPerMinute            float64
	ReportsPerMinute          float64

	// MaxTotalBytes is the global budget of live public document bytes (documents and any
	// version snapshots kept for them); a write that would exceed it is refused with 503 before
	// it is stored. Default 1 GiB. The figure is a counter kept by the service and reconciled
	// against the store by Sweep.
	MaxTotalBytes int64
	// MaxReports and MaxReportBytes bound the stored abuse reports as a whole; beyond either,
	// the oldest reports are evicted. Defaults 5000 and 8 MiB.
	MaxReports     int
	MaxReportBytes int64

	// RequestTimeout bounds how long a client may take to send a request body (and to receive
	// the answer, twice as long): a slow-body client is answered 408 and cut off. Default 30s.
	RequestTimeout time.Duration
	// FullSweepInterval bounds the cost of Sweep over a store that cannot list by prefix (every
	// listing is then the whole store): the listing sweep runs at most this often there.
	// Default 1h. With a PrefixLister it is not used.
	FullSweepInterval time.Duration

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
	// NoProxy says the server really is reached directly (GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES=none):
	// it silences the startup warning that, behind a proxy, every client would share the proxy's
	// address and so its limits.
	NoProxy bool
	// AllowOpenDocs (GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_OPEN_DOCS=true) keeps the generic /docs API open
	// to anonymous readers and writers in public mode. Without it, public mode closes /docs unless
	// sign-in with required ACLs protects it. Read by the docstore command, not by this package.
	AllowOpenDocs bool
}

// Defaults of Config.
const (
	DefaultMaxDocBytes       = 4 << 20
	DefaultMaxVersions       = 2000
	DefaultIPDailyBytes      = 50 << 20
	DefaultSessionTTL        = 24 * time.Hour
	DefaultSessionMaxAge     = 72 * time.Hour
	DefaultSessionIdleTTL    = time.Hour
	DefaultSessionsPerHour   = 6
	DefaultWritesPerMinute   = 30
	DefaultViewsPerMinute    = 120
	DefaultReportsPerMinute  = 5
	DefaultRecordTTL         = 30 * 24 * time.Hour
	DefaultMaxPerAddress     = 10
	DefaultAddrWritesMinute  = 120
	DefaultMaxTotalBytes     = 1 << 30
	DefaultMaxReports        = 5000
	DefaultMaxReportBytes    = 8 << 20
	DefaultRequestTimeout    = 30 * time.Second
	DefaultFullSweepInterval = time.Hour
	// TombstoneMargin is how much longer than SessionMaxAge a tombstone must live: a document
	// lives up to 24 hours after its last edit, and edits need a session.
	TombstoneMargin = 24 * time.Hour
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
	if c.SessionIdleTTL <= 0 {
		c.SessionIdleTTL = DefaultSessionIdleTTL
	}
	if c.SessionIdleTTL > c.SessionTTL {
		c.SessionIdleTTL = c.SessionTTL
	}
	if c.MaxSessions <= 0 {
		c.MaxSessions = DefaultMaxSessions
	}
	if c.MaxSessionsPerAddress <= 0 {
		c.MaxSessionsPerAddress = DefaultMaxPerAddress
	}
	if c.MaxSessionsPerAddress > c.MaxSessions {
		c.MaxSessionsPerAddress = c.MaxSessions
	}
	if c.SessionsPerHour <= 0 {
		c.SessionsPerHour = DefaultSessionsPerHour
	}
	if c.WritesPerMinute <= 0 {
		c.WritesPerMinute = DefaultWritesPerMinute
	}
	if c.WritesPerMinutePerAddress <= 0 {
		c.WritesPerMinutePerAddress = DefaultAddrWritesMinute
	}
	if c.MaxTotalBytes <= 0 {
		c.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if c.MaxReports <= 0 {
		c.MaxReports = DefaultMaxReports
	}
	if c.MaxReportBytes <= 0 {
		c.MaxReportBytes = DefaultMaxReportBytes
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = DefaultRequestTimeout
	}
	if c.FullSweepInterval <= 0 {
		c.FullSweepInterval = DefaultFullSweepInterval
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

// Validate checks the figures that must agree, on a Config with its defaults applied. A tombstone
// is what keeps a taken-down session from starting another document, so it must outlive the
// longest the session can live (SessionMaxAge) plus the 24 hours a document outlives its last
// edit: a shorter TombstoneTTL would let the ban lapse while the session still works.
func (c Config) Validate() error {
	if min := c.SessionMaxAge + TombstoneMargin; c.TombstoneTTL < min {
		return fmt.Errorf("public: TombstoneTTL %s is shorter than SessionMaxAge + 24h (%s): a takedown would stop banning its session while the session is alive", c.TombstoneTTL, min)
	}
	return nil
}

// Warnings lists what is legal but probably a mistake in a public deployment, for the operator's
// log at startup.
func (c Config) Warnings() []string {
	var w []string
	if len(c.TrustedProxies) == 0 && !c.NoProxy {
		w = append(w, "GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES is empty: clients are told apart by the connection's remote address, so behind a reverse proxy every client shares the proxy's address and its limits (one client can lock everyone out). Set it to the proxy's CIDRs, or to \"none\" if this server is reached directly")
	}
	return w
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
//	GOJIBERRY_DOCSTORE_PUBLIC_SESSION_IDLE_TTL   Go duration a session without a document lives
//	GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS_PER_ADDRESS  live sessions per IPv4 address or IPv6 /64
//	GOJIBERRY_DOCSTORE_PUBLIC_MAX_TOTAL_BYTES    global budget of live document bytes (503 above)
//	GOJIBERRY_DOCSTORE_PUBLIC_MAX_REPORTS, _MAX_REPORT_BYTES  global bounds of stored reports
//	GOJIBERRY_DOCSTORE_PUBLIC_REQUEST_TIMEOUT    Go duration to send a request body
//	GOJIBERRY_DOCSTORE_PUBLIC_FULL_SWEEP_INTERVAL  Go duration, listing sweeps over stores without prefix listing
//	GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS_PER_HOUR, _WRITES_PER_MINUTE, _ADDRESS_WRITES_PER_MINUTE, _VIEWS_PER_MINUTE, _REPORTS_PER_MINUTE
//	GOJIBERRY_DOCSTORE_PUBLIC_REPORT_TTL, _TOMBSTONE_TTL
//	GOJIBERRY_DOCSTORE_PUBLIC_TERMS_URL          the Link rel="terms" target
//	GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_TOKEN        secret of DELETE /admin/takedown/<id>
//	GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES    comma separated CIDRs, or "none" (reached directly)
//	GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_OPEN_DOCS    true keeps the generic /docs API open (see docstorecmd)
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
	i64("_MAX_TOTAL_BYTES", &c.MaxTotalBytes)
	i64("_MAX_REPORT_BYTES", &c.MaxReportBytes)
	var versions, sessions, perAddr, reports int64
	i64("_MAX_VERSIONS", &versions)
	i64("_MAX_SESSIONS", &sessions)
	i64("_MAX_SESSIONS_PER_ADDRESS", &perAddr)
	i64("_MAX_REPORTS", &reports)
	c.MaxVersions, c.MaxSessions, c.MaxSessionsPerAddress, c.MaxReports = int(versions), int(sessions), int(perAddr), int(reports)
	dur("_SESSION_TTL", &c.SessionTTL)
	dur("_SESSION_MAX_AGE", &c.SessionMaxAge)
	dur("_SESSION_IDLE_TTL", &c.SessionIdleTTL)
	dur("_REPORT_TTL", &c.ReportTTL)
	dur("_TOMBSTONE_TTL", &c.TombstoneTTL)
	dur("_REQUEST_TIMEOUT", &c.RequestTimeout)
	dur("_FULL_SWEEP_INTERVAL", &c.FullSweepInterval)
	rate("_SESSIONS_PER_HOUR", &c.SessionsPerHour)
	rate("_WRITES_PER_MINUTE", &c.WritesPerMinute)
	rate("_ADDRESS_WRITES_PER_MINUTE", &c.WritesPerMinutePerAddress)
	rate("_VIEWS_PER_MINUTE", &c.ViewsPerMinute)
	rate("_REPORTS_PER_MINUTE", &c.ReportsPerMinute)
	c.TermsURL = get("_TERMS_URL")
	c.AdminToken = get("_ADMIN_TOKEN")
	for _, p := range strings.Split(get("_TRUSTED_PROXIES"), ",") {
		switch p = strings.TrimSpace(p); {
		case p == "":
		case strings.EqualFold(p, "none"):
			c.NoProxy = true
		default:
			c.TrustedProxies = append(c.TrustedProxies, p)
		}
	}
	if c.NoProxy && len(c.TrustedProxies) > 0 {
		errs = append(errs, `GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES: "none" cannot be combined with addresses`)
	}
	switch strings.ToLower(get("_ALLOW_OPEN_DOCS")) {
	case "", "false", "0", "off":
	case "true", "1", "on":
		c.AllowOpenDocs = true
	default:
		errs = append(errs, fmt.Sprintf("GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_OPEN_DOCS %q: want true or false", get("_ALLOW_OPEN_DOCS")))
	}
	if len(errs) == 0 {
		if err := c.withDefaults().Validate(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return Config{}, fmt.Errorf("public: %s", strings.Join(errs, "; "))
	}
	return c, nil
}

// burst is the bucket size for a budget: the figure itself, at least 1.
func burst(figure float64) int {
	return max(1, int(math.Ceil(figure)))
}

// ClientKey names the client behind addr for every limit and quota: an IPv4 address as it is, an
// IPv6 address by its /64 (one subscriber is usually given a whole /64, so rotating through its
// addresses buys nothing). Anything that is not an address is lowercased and cut to 64 bytes, so
// it cannot make a long key either.
func ClientKey(addr string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(addr))
	if err != nil {
		s := strings.ToLower(strings.TrimSpace(addr))
		if len(s) > 64 {
			s = s[:64]
		}
		return s
	}
	a = a.WithZone("").Unmap()
	if a.Is4() {
		return a.String()
	}
	p, err := a.Prefix(64)
	if err != nil {
		return a.String()
	}
	return p.Masked().String()
}
