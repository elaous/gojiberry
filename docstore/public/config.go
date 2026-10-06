package public

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	// MaxSessionsPerNet is the second-level cap: live sessions per network prefix (an IPv6 /48 or
	// an IPv4 /24, see NetKey), 429 above it; default 100 (never below MaxSessionsPerAddress).
	// Per-/64 caps alone leave one attacker holding a /48 (65,536 of them) able to fill the
	// global cap.
	MaxSessionsPerNet int
	// MaxSessionsPerWide is the third-level cap: live sessions per wide prefix (an IPv6 /32 or an
	// IPv4 /16, see WideKey), 429 above it; default 500 (never below MaxSessionsPerNet, never above
	// MaxSessions). A /48 cap alone leaves an attacker holding a /32 (65,536 /48s) able to fill the
	// global cap and, by moving between /48s, to look like a new network every time.
	MaxSessionsPerWide int
	// SessionReservePercent is the part of MaxSessions kept for visitors whose wide prefix (the
	// coarsest level, see WideKey) has no live session yet: once the live sessions reach
	// MaxSessions minus this share, only such newcomers are admitted, so a flood from prefixes that
	// already hold sessions cannot starve legitimate new visitors. Default 20; negative: no
	// reserve (the environment variable's 0). The residual is a botnet spread over many wide
	// prefixes: each of its members is a newcomer, and only the per-prefix caps and the issue rate
	// limit it.
	SessionReservePercent int

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

	// MaxInflightBytes is the global cap on the bytes of request bodies being read at once (the
	// declared length, or MaxDocBytes for a body of unknown length, is held from admission until
	// the write ends); a write that would pass it is answered 503. Default 256 MiB; at least
	// MaxDocBytes.
	MaxInflightBytes int64
	// MaxInflightPerClient and MaxInflightPerNet are the shares of MaxInflightBytes one client (an
	// IPv4 address or an IPv6 /64) and one network (a /48 or a /24) may hold at once; over them a
	// write is answered 429, so a few addresses cannot use up the global cap and make every write
	// of everyone else 503. Defaults MaxInflightBytes/8 and MaxInflightBytes/4, never below
	// MaxDocBytes (a client must be able to write one document).
	MaxInflightPerClient int64
	MaxInflightPerNet    int64
	// MinUploadRate is the average rate in bytes per second a request body of MinRateBodyBytes or
	// more (or of unknown length) must keep up with once UploadGrace has passed: below it the
	// request is answered 408 and cut off (the daily bytes stay charged), so a client cannot hold
	// a large declared length for the whole RequestTimeout by trickling bytes. Default 32 KiB/s;
	// negative: no minimum rate (only RequestTimeout). UploadGrace is the time before the minimum
	// applies, default 5s (never above RequestTimeout).
	MinUploadRate int64
	UploadGrace   time.Duration

	// ReportReservedPerDoc is how many of a document's 20 report slots are kept for reporters
	// who have no report on it yet (default 5; negative: none), and ReportReservePercent the
	// share of MaxReports kept for reporters with no stored report at all (default 20; negative:
	// none). ReportPriorityCategories are the categories (of the fixed set ReportCategories) whose
	// reports are never evicted before others and may take a slot of a full document, evicting its
	// oldest ordinary report; nil is the default list, "csam" and "illegal", and an empty non-nil
	// slice means none. The free-text reason never decides priority: only the category the report
	// API accepts does, and an unknown one is "other". Three caps keep the priority path from
	// being flooded or used to pin a document's slots: MaxPriorityReportsPerReporter priority
	// reports per reporter per 24 hours (default 3; negative: no cap; one beyond it is kept as an
	// ordinary report), at most half of a document's slots held by priority reports (one beyond it
	// is ordinary), and PriorityBurstDocs, the number of different documents priority reports from
	// one network (see NetKey) may name within an hour before the operator is told (a log line and
	// Health.PriorityBursts); default 10, negative: no flag.
	ReportReservedPerDoc          int
	ReportReservePercent          int
	ReportPriorityCategories      []string
	MaxPriorityReportsPerReporter int
	PriorityBurstDocs             int
	// ReportKey is the secret that keys the reporter hashes (HMAC; see Service.reporterHash):
	// without it a store dump holding the salt could be tested against every IPv4 address. Keep it
	// OUT of the store the documents are in (FromEnv: GOJIBERRY_DOCSTORE_PUBLIC_REPORT_KEY, else
	// derived from GOJIBERRY_DOCSTORE_DIRECTORY_KEY). Empty: New makes one for this process, so the
	// duplicate detection of reports starts afresh at every restart (Warnings says so).
	ReportKey []byte

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
	// empty means the route is not mounted. It must be at least MinAdminTokenLength characters
	// (Validate refuses a shorter one) unless AllowShortAdminToken says the operator accepts that.
	AdminToken string
	// AllowShortAdminToken (GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_SHORT_ADMIN_TOKEN=true) lets the
	// server start with an admin token shorter than MinAdminTokenLength. Warnings then says so
	// loudly: a short token can be guessed online however the failures are limited.
	AllowShortAdminToken bool
	// AdminFailuresPerMinute, AdminFailuresPerNetPerMinute and AdminFailuresGlobalPerMinute are the
	// budgets of FAILED admin authentications per client (/64), per network (/48 or /24) and in
	// all; defaults 20, 60 and 300. When the global one is used up the admin route answers every
	// request with a wrong token 429 for AdminCooldown; a request with the right token is never
	// held back by any of them.
	AdminFailuresPerMinute       float64
	AdminFailuresPerNetPerMinute float64
	AdminFailuresGlobalPerMinute float64
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
	DefaultMaxPerNet         = 100
	DefaultMaxPerWide        = 500
	DefaultMinUploadRate     = 32 << 10
	DefaultUploadGrace       = 5 * time.Second
	DefaultPriorityPerDay    = 3
	DefaultPriorityBurst     = 10
	DefaultAdminFailures     = 20
	DefaultAdminFailuresNet  = 60
	DefaultAdminFailuresAll  = 300
	DefaultSessionReserve    = 20
	DefaultMaxInflightBytes  = 256 << 20
	DefaultReportReserved    = 5
	DefaultReportReservePct  = 20
	DefaultAddrWritesMinute  = 120
	DefaultMaxTotalBytes     = 1 << 30
	DefaultMaxReports        = 5000
	DefaultMaxReportBytes    = 8 << 20
	DefaultRequestTimeout    = 30 * time.Second
	DefaultFullSweepInterval = time.Hour
	// TombstoneMargin is how much longer than SessionMaxAge a tombstone must live: a document
	// lives up to 24 hours after its last edit, and edits need a session.
	TombstoneMargin = 24 * time.Hour
	// MinAdminTokenLength is the shortest admin token the server starts with by default.
	MinAdminTokenLength = 16
	// AdminCooldown is how long the admin route answers wrong tokens 429 after the global failure
	// budget was used up.
	AdminCooldown = 30 * time.Second
	// MinRateBodyBytes is the declared body size from which MinUploadRate applies.
	MinRateBodyBytes = 64 << 10
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
	if c.MaxSessionsPerNet <= 0 {
		c.MaxSessionsPerNet = DefaultMaxPerNet
	}
	if c.MaxSessionsPerNet < c.MaxSessionsPerAddress {
		c.MaxSessionsPerNet = c.MaxSessionsPerAddress
	}
	if c.MaxSessionsPerNet > c.MaxSessions {
		c.MaxSessionsPerNet = c.MaxSessions
	}
	if c.MaxSessionsPerWide <= 0 {
		c.MaxSessionsPerWide = DefaultMaxPerWide
	}
	if c.MaxSessionsPerWide < c.MaxSessionsPerNet {
		c.MaxSessionsPerWide = c.MaxSessionsPerNet
	}
	if c.MaxSessionsPerWide > c.MaxSessions {
		c.MaxSessionsPerWide = c.MaxSessions
	}
	if c.SessionReservePercent == 0 {
		c.SessionReservePercent = DefaultSessionReserve
	}
	if c.SessionReservePercent > 90 {
		c.SessionReservePercent = 90
	}
	if c.MaxInflightBytes <= 0 {
		c.MaxInflightBytes = DefaultMaxInflightBytes
	}
	if c.MaxInflightPerClient <= 0 {
		c.MaxInflightPerClient = max(c.MaxInflightBytes/8, c.MaxDocBytes)
	}
	if c.MaxInflightPerClient > c.MaxInflightBytes {
		c.MaxInflightPerClient = c.MaxInflightBytes
	}
	if c.MaxInflightPerNet <= 0 {
		c.MaxInflightPerNet = max(c.MaxInflightBytes/4, c.MaxInflightPerClient)
	}
	if c.MaxInflightPerNet < c.MaxInflightPerClient {
		c.MaxInflightPerNet = c.MaxInflightPerClient
	}
	if c.MaxInflightPerNet > c.MaxInflightBytes {
		c.MaxInflightPerNet = c.MaxInflightBytes
	}
	if c.MinUploadRate == 0 {
		c.MinUploadRate = DefaultMinUploadRate
	}
	if c.UploadGrace <= 0 {
		c.UploadGrace = DefaultUploadGrace
	}
	if c.ReportReservedPerDoc == 0 {
		c.ReportReservedPerDoc = DefaultReportReserved
	}
	if c.ReportReservedPerDoc >= maxReportsOfDoc {
		c.ReportReservedPerDoc = maxReportsOfDoc - 1
	}
	if c.ReportReservePercent == 0 {
		c.ReportReservePercent = DefaultReportReservePct
	}
	if c.ReportReservePercent > 90 {
		c.ReportReservePercent = 90
	}
	if c.ReportPriorityCategories == nil {
		c.ReportPriorityCategories = []string{CategoryCSAM, CategoryIllegal}
	}
	norm := make([]string, 0, len(c.ReportPriorityCategories))
	for _, r := range c.ReportPriorityCategories {
		if r = normalizeCategory(r); r != "" && r != CategoryOther {
			norm = append(norm, r)
		}
	}
	c.ReportPriorityCategories = norm
	if c.MaxPriorityReportsPerReporter == 0 {
		c.MaxPriorityReportsPerReporter = DefaultPriorityPerDay
	}
	if c.PriorityBurstDocs == 0 {
		c.PriorityBurstDocs = DefaultPriorityBurst
	}
	if c.AdminFailuresPerMinute <= 0 {
		c.AdminFailuresPerMinute = DefaultAdminFailures
	}
	if c.AdminFailuresPerNetPerMinute <= 0 {
		c.AdminFailuresPerNetPerMinute = DefaultAdminFailuresNet
	}
	if c.AdminFailuresGlobalPerMinute <= 0 {
		c.AdminFailuresGlobalPerMinute = DefaultAdminFailuresAll
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
	if c.UploadGrace > c.RequestTimeout {
		c.UploadGrace = c.RequestTimeout
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
	if c.MaxInflightBytes < c.MaxDocBytes {
		return fmt.Errorf("public: MaxInflightBytes %d is smaller than MaxDocBytes %d: no document could ever be written", c.MaxInflightBytes, c.MaxDocBytes)
	}
	if c.MaxInflightPerClient < c.MaxDocBytes {
		return fmt.Errorf("public: MaxInflightPerClient %d is smaller than MaxDocBytes %d: no client could ever write a full document", c.MaxInflightPerClient, c.MaxDocBytes)
	}
	if c.AdminToken != "" && len(c.AdminToken) < MinAdminTokenLength && !c.AllowShortAdminToken {
		return ErrAdminTokenTooShort
	}
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
	if c.AdminToken != "" && len(c.AdminToken) < MinAdminTokenLength {
		w = append(w, "GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_SHORT_ADMIN_TOKEN=true: the admin token is shorter than 16 characters. THE ADMIN ROUTE CAN BE GUESSED ONLINE: failed attempts are rate limited, but a right guess is never held back, and the limits only slow down one client or one network. Use a long random token (at least 16 characters) and remove this override")
	}
	if len(c.ReportKey) == 0 {
		w = append(w, "GOJIBERRY_DOCSTORE_PUBLIC_REPORT_KEY (or GOJIBERRY_DOCSTORE_DIRECTORY_KEY) is not set: the reporter hashes of abuse reports are keyed with a key made for this process, so the detection of duplicate reports starts afresh at every restart. Set a secret that is kept out of the store the documents are in")
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
//	GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS_PER_NET  live sessions per IPv6 /48 or IPv4 /24
//	GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS_PER_WIDE  live sessions per IPv6 /32 or IPv4 /16
//	GOJIBERRY_DOCSTORE_PUBLIC_SESSION_RESERVE_PERCENT  share of MAX_SESSIONS kept for wide prefixes with no session (0: none)
//	GOJIBERRY_DOCSTORE_PUBLIC_MAX_INFLIGHT_BYTES  global cap on request bodies being read at once
//	GOJIBERRY_DOCSTORE_PUBLIC_MAX_INFLIGHT_PER_CLIENT, _MAX_INFLIGHT_PER_NET  shares of it per /64 (or IPv4 address) and per /48 (or /24)
//	GOJIBERRY_DOCSTORE_PUBLIC_MIN_UPLOAD_RATE, _UPLOAD_GRACE  minimum bytes per second of a large body after the grace (Go duration)
//	GOJIBERRY_DOCSTORE_PUBLIC_REPORT_RESERVED_PER_DOC, _REPORT_RESERVE_PERCENT  report slots kept for new reporters (0: none)
//	GOJIBERRY_DOCSTORE_PUBLIC_REPORT_PRIORITY_CATEGORIES  comma separated categories (csam, illegal, spam, other) never evicted first, or "none" (default csam,illegal)
//	GOJIBERRY_DOCSTORE_PUBLIC_REPORT_PRIORITY_PER_REPORTER  priority reports per reporter per 24 h (default 3)
//	GOJIBERRY_DOCSTORE_PUBLIC_REPORT_PRIORITY_BURST  different documents with priority reports from one network in an hour before the operator is told (default 10)
//	GOJIBERRY_DOCSTORE_PUBLIC_REPORT_KEY         secret keying the reporter hashes (else derived from GOJIBERRY_DOCSTORE_DIRECTORY_KEY, else per process)
//	GOJIBERRY_DOCSTORE_PUBLIC_MAX_TOTAL_BYTES    global budget of live document bytes (503 above)
//	GOJIBERRY_DOCSTORE_PUBLIC_MAX_REPORTS, _MAX_REPORT_BYTES  global bounds of stored reports
//	GOJIBERRY_DOCSTORE_PUBLIC_REQUEST_TIMEOUT    Go duration to send a request body
//	GOJIBERRY_DOCSTORE_PUBLIC_FULL_SWEEP_INTERVAL  Go duration, listing sweeps over stores without prefix listing
//	GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS_PER_HOUR, _WRITES_PER_MINUTE, _ADDRESS_WRITES_PER_MINUTE, _VIEWS_PER_MINUTE, _REPORTS_PER_MINUTE
//	GOJIBERRY_DOCSTORE_PUBLIC_REPORT_TTL, _TOMBSTONE_TTL
//	GOJIBERRY_DOCSTORE_PUBLIC_TERMS_URL          the Link rel="terms" target
//	GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_TOKEN        secret of DELETE /admin/takedown/<id> (at least 16 characters)
//	GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_SHORT_ADMIN_TOKEN  true starts the server with a shorter token (loud warning)
//	GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_FAILURES_PER_MINUTE, _ADMIN_FAILURES_PER_NET_PER_MINUTE, _ADMIN_FAILURES_GLOBAL_PER_MINUTE  budgets of failed admin attempts
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
	i64("_MAX_INFLIGHT_BYTES", &c.MaxInflightBytes)
	i64("_MAX_INFLIGHT_PER_CLIENT", &c.MaxInflightPerClient)
	i64("_MAX_INFLIGHT_PER_NET", &c.MaxInflightPerNet)
	i64("_MIN_UPLOAD_RATE", &c.MinUploadRate)
	// share reads a percentage 0..90 where 0 means "none" (stored as -1; Config's 0 is the default).
	share := func(name string, dst *int, top int) {
		if v := get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > top {
				errs = append(errs, fmt.Sprintf("GOJIBERRY_DOCSTORE_PUBLIC%s %q: want a whole number from 0 to %d", name, v, top))
				return
			}
			if n == 0 {
				n = -1
			}
			*dst = n
		}
	}
	share("_SESSION_RESERVE_PERCENT", &c.SessionReservePercent, 90)
	share("_REPORT_RESERVE_PERCENT", &c.ReportReservePercent, 90)
	share("_REPORT_RESERVED_PER_DOC", &c.ReportReservedPerDoc, maxReportsOfDoc-1)
	if v := get("_REPORT_PRIORITY_CATEGORIES"); v != "" {
		c.ReportPriorityCategories = []string{}
		if !strings.EqualFold(v, "none") {
			for _, r := range strings.Split(v, ",") {
				switch r = normalizeCategory(r); {
				case r == "":
				case !validCategory(r):
					errs = append(errs, fmt.Sprintf("GOJIBERRY_DOCSTORE_PUBLIC_REPORT_PRIORITY_CATEGORIES %q: want some of %s", r, strings.Join(ReportCategories, ", ")))
				default:
					c.ReportPriorityCategories = append(c.ReportPriorityCategories, r)
				}
			}
		}
	}
	var versions, sessions, perAddr, perNet, perWide, reports, prioPer, prioBurst int64
	i64("_MAX_SESSIONS_PER_WIDE", &perWide)
	c.MaxSessionsPerWide = int(perWide)
	i64("_REPORT_PRIORITY_PER_REPORTER", &prioPer)
	i64("_REPORT_PRIORITY_BURST", &prioBurst)
	c.MaxPriorityReportsPerReporter, c.PriorityBurstDocs = int(prioPer), int(prioBurst)
	i64("_MAX_VERSIONS", &versions)
	i64("_MAX_SESSIONS", &sessions)
	i64("_MAX_SESSIONS_PER_ADDRESS", &perAddr)
	i64("_MAX_SESSIONS_PER_NET", &perNet)
	c.MaxSessionsPerNet = int(perNet)
	i64("_MAX_REPORTS", &reports)
	c.MaxVersions, c.MaxSessions, c.MaxSessionsPerAddress, c.MaxReports = int(versions), int(sessions), int(perAddr), int(reports)
	dur("_SESSION_TTL", &c.SessionTTL)
	dur("_SESSION_MAX_AGE", &c.SessionMaxAge)
	dur("_SESSION_IDLE_TTL", &c.SessionIdleTTL)
	dur("_REPORT_TTL", &c.ReportTTL)
	dur("_TOMBSTONE_TTL", &c.TombstoneTTL)
	dur("_REQUEST_TIMEOUT", &c.RequestTimeout)
	dur("_FULL_SWEEP_INTERVAL", &c.FullSweepInterval)
	dur("_UPLOAD_GRACE", &c.UploadGrace)
	rate("_SESSIONS_PER_HOUR", &c.SessionsPerHour)
	rate("_WRITES_PER_MINUTE", &c.WritesPerMinute)
	rate("_ADDRESS_WRITES_PER_MINUTE", &c.WritesPerMinutePerAddress)
	rate("_VIEWS_PER_MINUTE", &c.ViewsPerMinute)
	rate("_REPORTS_PER_MINUTE", &c.ReportsPerMinute)
	rate("_ADMIN_FAILURES_PER_MINUTE", &c.AdminFailuresPerMinute)
	rate("_ADMIN_FAILURES_PER_NET_PER_MINUTE", &c.AdminFailuresPerNetPerMinute)
	rate("_ADMIN_FAILURES_GLOBAL_PER_MINUTE", &c.AdminFailuresGlobalPerMinute)
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
	switch strings.ToLower(get("_ALLOW_SHORT_ADMIN_TOKEN")) {
	case "", "false", "0", "off":
	case "true", "1", "on":
		c.AllowShortAdminToken = true
	default:
		errs = append(errs, fmt.Sprintf("GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_SHORT_ADMIN_TOKEN %q: want true or false", get("_ALLOW_SHORT_ADMIN_TOKEN")))
	}
	if v := get("_REPORT_KEY"); v != "" {
		if len(v) < 16 {
			errs = append(errs, "GOJIBERRY_DOCSTORE_PUBLIC_REPORT_KEY: want a secret of at least 16 characters")
		} else {
			c.ReportKey = deriveReportKey([]byte(v))
		}
	} else if v := strings.TrimSpace(getenv("GOJIBERRY_DOCSTORE_DIRECTORY_KEY")); v != "" {
		// The directory's signing seed (32 bytes in hex) is key material the operator already keeps
		// out of the store; the reporter key is derived from it under its own label. An unreadable
		// seed is the directory's own error to report, not this one's.
		if seed, err := hex.DecodeString(v); err == nil && len(seed) >= 16 {
			c.ReportKey = deriveReportKey(seed)
		}
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

// NetKey names the network prefix behind addr for the second-level session cap: an IPv4 address by
// its /24, an IPv6 address by its /48 (the allocation a site or a subscriber usually holds, so an
// attacker cannot multiply its cap by moving between the /64s inside it). Anything that is not an
// address is treated as ClientKey treats it.
func NetKey(addr string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(addr))
	if err != nil {
		return ClientKey(addr)
	}
	a = a.WithZone("").Unmap()
	bits := 48
	if a.Is4() {
		bits = 24
	}
	p, err := a.Prefix(bits)
	if err != nil {
		return a.String()
	}
	return p.Masked().String()
}

// WideKey names the widest prefix behind addr for the third-level session cap and the reserve for
// newcomers: an IPv4 address by its /16, an IPv6 address by its /32 (an ISP's allocation or a large
// site's, so an attacker moving between the /48s inside it is still one wide network). Anything that
// is not an address is treated as ClientKey treats it.
func WideKey(addr string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(addr))
	if err != nil {
		return ClientKey(addr)
	}
	a = a.WithZone("").Unmap()
	bits := 32
	if a.Is4() {
		bits = 16
	}
	p, err := a.Prefix(bits)
	if err != nil {
		return a.String()
	}
	return p.Masked().String()
}

// ErrAdminTokenTooShort is what Validate (and so New and FromEnv) returns for an admin token
// shorter than MinAdminTokenLength without Config.AllowShortAdminToken.
var ErrAdminTokenTooShort = errors.New("public: GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_TOKEN is shorter than 16 characters: the admin route can be guessed online; use a long random token, or set GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_SHORT_ADMIN_TOKEN=true to accept the risk")

// Report categories: the fixed set the report API accepts besides the free-text reason. Only the
// category can make a report a priority one.
const (
	CategoryCSAM    = "csam"
	CategoryIllegal = "illegal"
	CategorySpam    = "spam"
	CategoryOther   = "other"
)

// ReportCategories lists the categories, in the order the documentation names them.
var ReportCategories = []string{CategoryCSAM, CategoryIllegal, CategorySpam, CategoryOther}

func normalizeCategory(c string) string { return strings.ToLower(strings.TrimSpace(c)) }

func validCategory(c string) bool {
	for _, k := range ReportCategories {
		if c == k {
			return true
		}
	}
	return false
}

// categoryOf is the category a report names: one of ReportCategories, "other" for anything else.
func categoryOf(c string) string {
	if c = normalizeCategory(c); validCategory(c) {
		return c
	}
	return CategoryOther
}

// deriveReportKey turns secret key material into the reporter-hash key, under its own label so the
// same material used elsewhere (the directory seed) yields an unrelated key.
func deriveReportKey(material []byte) []byte {
	m := hmac.New(sha256.New, material)
	_, _ = m.Write([]byte("gojiberry/public/report-key/v1"))
	return m.Sum(nil)
}

// normalizeReason is a report reason as it is compared: trimmed, lowercased, inner whitespace
// collapsed.
func normalizeReason(r string) string {
	return strings.ToLower(strings.Join(strings.Fields(r), " "))
}
