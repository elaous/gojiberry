package public

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/internal/clientkey"
	"math"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Config holds the limits and switches of the public offer.
type Config struct {
	legacyEnv []string

	// Enabled is GOJIBERRY_DOCSTORE_PUBLIC=true.
	Enabled bool

	// MaxDocBytes is the largest document a session may store.
	MaxDocBytes int64
	// MaxVersions is how many times one document may be saved.
	MaxVersions int
	// IPDailyBytes is the bytes one client may upload per day.
	IPDailyBytes int64

	// SessionTTL is how long a session lasts.
	SessionTTL     time.Duration
	SessionMaxAge  time.Duration
	SessionIdleTTL time.Duration
	// MaxSessions is the global cap on live anonymous sessions.
	MaxSessions           int
	MaxSessionsPerAddress int
	// MaxSessionsPerNet is the cap on live sessions per network.
	MaxSessionsPerNet int
	// MaxSessionsPerWide is the cap on live sessions per wide network.
	MaxSessionsPerWide int
	// SessionReservePercent is the share of MaxSessions kept in reserve.
	SessionReservePercent int

	// Per-client request budgets.
	SessionsPerHour           float64
	WritesPerMinute           float64
	WritesPerMinutePerAddress float64
	ViewsPerMinute            float64
	ReportsPerMinute          float64

	// MaxTotalBytes is the global budget of live public bytes.
	MaxTotalBytes int64
	// MaxReports and MaxReportBytes bound the stored abuse reports.
	MaxReports     int
	MaxReportBytes int64

	// MaxInflightBytes is the global cap on bytes being uploaded.
	MaxInflightBytes int64
	// MaxInflightPerClient and MaxInflightPerNet cap in-flight uploads.
	MaxInflightPerClient int64
	MaxInflightPerNet    int64
	// MinUploadRate is the slowest allowed upload rate, in bytes per second.
	MinUploadRate int64
	UploadGrace   time.Duration

	// ReportReservedPerDoc is how many of a document's reports are reserved.
	ReportReservedPerDoc          int
	ReportReservePercent          int
	ReportPriorityCategories      []string
	MaxPriorityReportsPerReporter int
	PriorityBurstDocs             int
	// ReportKey is the secret that keys the reporter hashes.
	ReportKey []byte

	// RequestTimeout bounds how long a request may take.
	RequestTimeout time.Duration
	// FullSweepInterval bounds the cost of Sweep over the whole store.
	FullSweepInterval time.Duration

	// ReportTTL and TombstoneTTL are how long reports and tombstones last.
	ReportTTL    time.Duration
	TombstoneTTL time.Duration

	// TermsURL is sent as a Link header on public responses.
	TermsURL string
	// AdminToken is the shared secret of the takedown API.
	AdminToken string
	// AllowShortAdminToken lets a short admin token be used.
	AllowShortAdminToken bool
	// AdminFailuresPerMinute caps failed admin attempts.
	AdminFailuresPerMinute       float64
	AdminFailuresPerNetPerMinute float64
	AdminFailuresGlobalPerMinute float64
	// TrustedProxies are the CIDRs of reverse proxies.
	TrustedProxies []string
	// NoProxy says the server is reached directly, with no proxy.
	NoProxy       bool
	AllowOpenDocs bool
}

// Defaults of Config.
const (
	DefaultMaxDocBytes      = 4 << 20
	DefaultMaxVersions      = 2000
	DefaultIPDailyBytes     = 50 << 20
	DefaultSessionTTL       = 24 * time.Hour
	DefaultSessionMaxAge    = 72 * time.Hour
	DefaultSessionIdleTTL   = time.Hour
	DefaultSessionsPerHour  = 6
	DefaultWritesPerMinute  = 30
	DefaultViewsPerMinute   = 120
	DefaultReportsPerMinute = 5
	DefaultRecordTTL        = 30 * 24 * time.Hour
	DefaultMaxPerAddress    = 10
	DefaultMaxPerNet        = 100
	DefaultMaxPerWide       = 500
	DefaultMinUploadRate    = 32 << 10
	// MinUploadRateFloor is the lowest minimum upload rate allowed.
	MinUploadRateFloor       = 1 << 10
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
	// TombstoneMargin is how much longer tombstones outlast sessions.
	TombstoneMargin = 24 * time.Hour
	// MinAdminTokenLength is the shortest admin token allowed.
	MinAdminTokenLength = 16
	// AdminCooldown is how long the admin route refuses after failures.
	AdminCooldown = 30 * time.Second
	// MinRateBodyBytes is the body size from which minimum rate applies.
	MinRateBodyBytes = 64 << 10
)

// withDefaults returns c with every zero field set.
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
	if c.MinUploadRate > 0 && c.MinUploadRate < MinUploadRateFloor {
		c.MinUploadRate = MinUploadRateFloor
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

// Validate checks the figures that must agree.
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

// Warnings lists what is legal but probably a mistake.
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
	if c.MinUploadRate > 0 && c.MinUploadRate < MinUploadRateFloor {
		w = append(w, fmt.Sprintf("GOJIBERRY_DOCSTORE_PUBLIC_MIN_UPLOAD_RATE %d is below the floor of %d bytes per second and is raised to it (a slower minimum lets a trickling client hold a slot for the whole request timeout); use a negative value to turn the minimum off", c.MinUploadRate, MinUploadRateFloor))
	}
	d := c.withDefaults()
	if c.MaxSessionsPerWide > 0 && d.MaxSessionsPerWide != c.MaxSessionsPerWide {
		w = append(w, fmt.Sprintf("GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS_PER_WIDE %d is not usable next to the other session caps and is changed to %d (it is at least _MAX_SESSIONS_PER_NET and at most _MAX_SESSIONS)", c.MaxSessionsPerWide, d.MaxSessionsPerWide))
	} else if c.MaxSessionsPerWide <= 0 && d.MaxSessionsPerWide != DefaultMaxPerWide {
		w = append(w, fmt.Sprintf("GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS_PER_WIDE is not set and is %d, not the default %d, because _MAX_SESSIONS_PER_NET or _MAX_SESSIONS moved it; set it to say what is meant (the per-wide cap was added after the per-network one, so a configuration written before it has none)", d.MaxSessionsPerWide, DefaultMaxPerWide))
	}
	for _, name := range c.legacyEnv {
		w = append(w, name+" is no longer read; use GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS_PER_HOUR (a rate per hour, not per minute)")
	}
	return w
}

// FromEnv reads the configuration from the environment.
func FromEnv(getenv func(string) string) (Config, error) {
	var c Config
	var errs []string
	for _, old := range []string{"GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS_PER_MINUTE"} {
		if strings.TrimSpace(getenv(old)) != "" {
			c.legacyEnv = append(c.legacyEnv, old)
		}
	}
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
	// share reads a percentage, 0 meaning none.
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
		// The directory's signing seed is read from hex.
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

// burst is the bucket size for a budget.
func burst(figure float64) int {
	return max(1, int(math.Ceil(figure)))
}

// ClientKey names the client behind addr for the limits.
func ClientKey(addr string) string { return clientkey.Of(addr) }

// NetKey names the network prefix behind addr.
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

// WideKey names the widest prefix behind addr.
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

// ErrAdminTokenTooShort is what Validate returns for a short token.
var ErrAdminTokenTooShort = errors.New("public: GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_TOKEN is shorter than 16 characters: the admin route can be guessed online; use a long random token, or set GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_SHORT_ADMIN_TOKEN=true to accept the risk")

// Report categories: the fixed set the report API accepts.
const (
	CategoryCSAM    = "csam"
	CategoryIllegal = "illegal"
	CategorySpam    = "spam"
	CategoryOther   = "other"
)

// ReportCategories lists the categories in order.
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

// categoryOf is the category a report names.
func categoryOf(c string) string {
	if c = normalizeCategory(c); validCategory(c) {
		return c
	}
	return CategoryOther
}

// deriveReportKey turns key material into the report key.
func deriveReportKey(material []byte) []byte {
	m := hmac.New(sha256.New, material)
	_, _ = m.Write([]byte("gojiberry/public/report-key/v1"))
	return m.Sum(nil)
}
