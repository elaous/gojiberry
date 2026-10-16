package public

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/auth/passkey"
	"github.com/elaous/gojiberry/docstore"
	"hash/fnv"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Limiter limits requests per key ("<group>|<client>"); it is the passkey one, so the same
// implementations (a shared Redis budget, a gateway's decision, passkey.Unlimited) plug in here.
type Limiter = passkey.Limiter

// Limiters are the per-group request budgets. A nil one is a passkey.RateLimiter made from the
// Config's figures. Every "<client>" is the ClientKey of the address (an IPv4 address, an IPv6
// /64). Keys: Session "session|<client>", Write "write|<session id>" and WriteAddr
// "writeaddr|<client>" (a write must pass both, so rotating sessions buys no write budget),
// View "view|<client>", Report "report|<client>", Admin "admin|<client>": the budget of FAILED
// admin authentications only (20 a minute by default), so a client that guesses wrong is slowed
// down and one that presents the token is never counted (see adminAllowed). AdminNet
// ("adminnet|<NetKey>", 60 a minute) and AdminGlobal ("admin-all", 300 a minute) count the same
// failures per network (a /48 or /24) and in all, so guesses spread over the /64s of one network,
// or over many networks, meet a limit too; when AdminGlobal is used up the admin route answers
// wrong tokens 429 for AdminCooldown.
type Limiters struct {
	Session, Write, WriteAddr, View, Report, Admin, AdminNet, AdminGlobal Limiter
}

// Options builds a Service. Only Store is required.
type Options struct {
	// Store holds documents and records. It must expire documents (be an *docstore.ExpiringAdapter,
	// or anything that implements docstore.Expirer): New refuses a store that does not, since the
	// offer's promise is deletion 24 hours after the last edit. Public documents are stored under
	// "pubdoc/<id>", records under docstore.PublicPrefix and reports under docstore.ReportPrefix.
	Store  docstore.Adapter
	Config Config

	// The injectable pieces, each with a single-process default (see doc.go):
	Sessions SessionStore // NewMemorySessionStore(Config.MaxSessions)
	Limiters Limiters     // passkey.RateLimiter from the Config
	// LimiterFactory, when set, makes the budgets that Limiters leaves nil: docstore.StoreLimiters
	// (replicas on one store share them) or any factory, instead of a per-process RateLimiter.
	// The names are "public-session", "public-write", "public-writeaddr", "public-view",
	// "public-report" and "public-admin"; with docstore.StoreLimiters they are records under
	// docstore.RateLimitPrefix, which the document API refuses and expiry leaves alone.
	LimiterFactory docstore.LimiterFactory
	Quota          Quota // NewMemoryQuota(Config.IPDailyBytes, 24h)
	// PriorityQuota, when set, counts each reporter's priority reports (Config.MaxPriorityReportsPerReporter
	// a day) instead of the per-process count, so replicas on one store share the allowance: give it a
	// NewStoreQuota(store, int64(Config.MaxPriorityReportsPerReporter), 24*time.Hour). It is charged one unit
	// per priority report, under the key "prio|<reporter>", with the fixed calendar days of a StoreQuota.
	PriorityQuota Quota
	// SharedBurstWatch keeps the burst watch of the priority reports (networks that name many different
	// documents within an hour, which alarms the operator and shows in /admin/health) in the store, one
	// record per network under BurstKeyPrefix named by a keyed hash, so replicas count together and the
	// alarm is logged once an hour, not once per replica. It needs a store with conditional writes
	// (New refuses it otherwise).
	SharedBurstWatch bool
	Admin            AdminGuard                 // TokenAdmin(Config.AdminToken)
	Blocklist        Blocklist                  // NoBlocklist
	Challenger       Challenger                 // NoChallenge
	ClientIP         func(*http.Request) string // passkey.TrustedProxies(Config.TrustedProxies...), else the remote address
	Now              func() time.Time           // time.Now; use the clock of the ExpiringAdapter in tests
}

// Service is the public (anonymous) offer: session, document, view link, report and takedown
// routes over a Store. Build it with New, mount it with Mount (or Handler/AdminHandler), and
// run Run so records of vanished documents and old reports are swept.
type Service struct {
	store     docstore.Adapter
	cfg       Config
	sessions  SessionStore
	lim       Limiters
	quota     Quota
	prioQuota Quota       // shared allowance of priority reports, or nil: the per-process count (prio)
	burst     *burstStore // the shared burst watch, or nil: the per-process one (prio)
	admin     AdminGuard
	blocklist Blocklist
	challenge Challenger
	clientIP  func(*http.Request) string
	now       func() time.Time
	terms     string
	locks     [64]sync.Mutex // per document (by hash): serializes the read-modify-write of its record

	bytes    budget        // live bytes of public documents (Config.MaxTotalBytes)
	reports  reportStats   // stored abuse reports
	flight   inflight      // bytes of request bodies being read (Config.MaxInflightBytes)
	recSem   chan struct{} // one holder at a time runs the first reconcile (ensureCounters)
	recRetry time.Time     // no new first reconcile before this (writes are refused until then)

	saltMu    sync.Mutex
	salt      []byte    // stored salt of the reporter hashes (see reporterHash); nil until it is known
	saltRetry time.Time // no new attempt to get it before this
	saltFails int
	reportKey []byte // keys the reporter hashes; never stored
	prio      prioTracker

	adminMu    sync.Mutex
	adminCool  time.Time // wrong admin tokens are answered 429 until this
	adminFails int       // failures in the current minute (for the burst log)
	adminMin   time.Time // start of that minute

	prefixOK bool // the store lists by prefix (else Sweep's listings are throttled)

	healthMu     sync.Mutex
	born         time.Time
	lastSweep    time.Time
	lastFull     time.Time
	sweepFailing bool
	sweepEvery   time.Duration
	noDeadline   atomic.Bool
}

// ErrAdminNeedsProxyConfig is returned by New when the admin route is enabled and nothing says how
// clients are told apart.
var ErrAdminNeedsProxyConfig = errors.New("public: the admin route is enabled but GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES is unset: behind a reverse proxy every caller would share the proxy's address, and wrong guesses of one would lock the operator out; name the proxy's CIDRs, or set it to \"none\" if the server is reached directly")

// ErrExpiryRequired is returned by New for a store that does not expire documents.
var ErrExpiryRequired = errors.New("public: the store must expire documents (the 24 hour promise): wrap it in docstore.ExpiringAdapter and set GOJIBERRY_DOCSTORE_EXPIRY_TTL")

// docKeyPrefix is where public documents live in the store. They are plain, expiring
// documents (docstore.IsDocumentKey accepts them), so it is not a system keyspace: HiddenPrefixes
// keeps it away from the generic /docs routes instead.
const docKeyPrefix = "pubdoc/"

// HiddenPrefixes are the ids the generic /docs routes must not reach besides the reserved
// docstore.PublicPrefix and docstore.ReportPrefix: the public documents themselves. Mount hides them.
func HiddenPrefixes() []string { return []string{docKeyPrefix} }

const (
	docRecordPrefix  = docstore.PublicPrefix + "doc/"  // docMeta of a document
	viewRecordPrefix = docstore.PublicPrefix + "view/" // view id -> document
	tombPrefix       = docstore.PublicPrefix + "tomb/" // tombstones of taken-down view ids
	saltKey          = docstore.PublicPrefix + "salt"  // the salt of the reporter hashes
	// TombstonePrefix is where takedown tombstones are kept: inside docstore.PublicPrefix, so
	// they never expire with documents; the sweep deletes them after Config.TombstoneTTL.
	TombstonePrefix = tombPrefix
)

// New builds a Service. It fails when the store does not expire documents or the terms URL is
// not an http(s) URL.
func New(o Options) (*Service, error) {
	if o.Store == nil {
		return nil, errors.New("public: Options.Store is required")
	}
	if _, ok := o.Store.(docstore.Expirer); !ok {
		return nil, ErrExpiryRequired
	}
	cfg := o.Config.withDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	// The admin route counts failed authentications per client address. Behind a proxy that is
	// the proxy's address unless the proxy is named, and then one client's wrong guesses would
	// lock the operator out: refuse to start the route that way, unless the operator says there is
	// no proxy ("none") or supplies its own way to name a client.
	if (cfg.AdminToken != "" || o.Admin != nil) && o.ClientIP == nil && len(cfg.TrustedProxies) == 0 && !cfg.NoProxy {
		return nil, ErrAdminNeedsProxyConfig
	}
	s := &Service{
		store: o.Store, cfg: cfg,
		sessions: o.Sessions, lim: o.Limiters, quota: o.Quota, prioQuota: o.PriorityQuota, admin: o.Admin,
		blocklist: o.Blocklist, challenge: o.Challenger, clientIP: o.ClientIP, now: o.Now,
	}
	if o.SharedBurstWatch {
		ca, ok := o.Store.(docstore.ConditionalAdapter)
		if !ok {
			return nil, errors.New("public: SharedBurstWatch needs a store with conditional writes")
		}
		s.burst = &burstStore{store: ca}
	}
	s.flight.max, s.flight.perClient, s.flight.perNet = cfg.MaxInflightBytes, cfg.MaxInflightPerClient, cfg.MaxInflightPerNet
	s.recSem = make(chan struct{}, 1)
	s.reportKey = cfg.ReportKey
	if len(s.reportKey) == 0 {
		s.reportKey = make([]byte, 32)
		if _, err := rand.Read(s.reportKey); err != nil {
			return nil, fmt.Errorf("public: making the report key: %w", err)
		}
		log.Print("public docstore: no report key (GOJIBERRY_DOCSTORE_PUBLIC_REPORT_KEY): using one made for this process, so the detection of duplicate reports starts afresh at every restart")
	}
	if cfg.TermsURL != "" {
		u, err := url.Parse(cfg.TermsURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.ContainsAny(cfg.TermsURL, "<>\"\r\n ") {
			return nil, fmt.Errorf("public: the terms URL %q must be an http(s) URL", cfg.TermsURL)
		}
		s.terms = cfg.TermsURL
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.born = s.now()
	s.prefixOK = canListPrefix(o.Store)
	if s.sessions == nil {
		s.sessions = NewMemorySessionStore(cfg.MaxSessions)
	}
	// rl makes a token bucket of burst `figure` refilled at figure per `per`.
	rl := func(l Limiter, name string, figure float64, per time.Duration) Limiter {
		if l != nil {
			return l
		}
		perMinute := figure * float64(time.Minute) / float64(per)
		if o.LimiterFactory != nil {
			return o.LimiterFactory(name, perMinute, burst(figure))
		}
		r := passkey.NewRateLimiter(perMinute, burst(figure))
		r.Now = s.now
		return r
	}
	s.lim.Session = rl(s.lim.Session, "public-session", cfg.SessionsPerHour, time.Hour)
	s.lim.Write = rl(s.lim.Write, "public-write", cfg.WritesPerMinute, time.Minute)
	s.lim.WriteAddr = rl(s.lim.WriteAddr, "public-writeaddr", cfg.WritesPerMinutePerAddress, time.Minute)
	s.lim.View = rl(s.lim.View, "public-view", cfg.ViewsPerMinute, time.Minute)
	s.lim.Report = rl(s.lim.Report, "public-report", cfg.ReportsPerMinute, time.Minute)
	s.lim.Admin = rl(s.lim.Admin, "public-admin", cfg.AdminFailuresPerMinute, time.Minute)
	s.lim.AdminNet = rl(s.lim.AdminNet, "public-admin-net", cfg.AdminFailuresPerNetPerMinute, time.Minute)
	s.lim.AdminGlobal = rl(s.lim.AdminGlobal, "public-admin-global", cfg.AdminFailuresGlobalPerMinute, time.Minute)
	if s.quota == nil {
		q := NewMemoryQuota(cfg.IPDailyBytes, 24*time.Hour)
		q.Now = s.now
		s.quota = q
	}
	if s.admin == nil {
		s.admin = TokenAdmin(cfg.AdminToken)
	}
	if s.blocklist == nil {
		s.blocklist = NoBlocklist{}
	}
	if s.challenge == nil {
		s.challenge = NoChallenge{}
	}
	if s.clientIP == nil {
		s.clientIP = passkey.ClientIP
		if len(cfg.TrustedProxies) > 0 {
			s.clientIP = passkey.TrustedProxies(cfg.TrustedProxies...)
		}
	}
	// Version snapshots written through the store (the saver's layout) are asked for before they
	// are written (refused with docstore.ErrStorageFull when they would pass the byte budget) and
	// counted when they are stored, and subtracted when they are deleted. This sets hooks on the
	// caller's adapter: build the Service before serving.
	if ea := expiringOf(o.Store); ea != nil {
		prev := ea.Guard
		ea.Guard = func(id string, size int) (func(bool), error) {
			var prevDone func(bool)
			if prev != nil {
				d, err := prev(id, size)
				if err != nil {
					return nil, err
				}
				prevDone = d
			}
			doc, isVersion := versionDoc(id)
			if !isVersion {
				return prevDone, nil
			}
			if !s.bytes.holdExtra(int64(size), s.cfg.MaxTotalBytes) {
				if prevDone != nil {
					prevDone(false)
				}
				return nil, fmt.Errorf("%w: the public offer's byte budget is full, a version snapshot is refused", docstore.ErrStorageFull)
			}
			return func(stored bool) {
				s.bytes.releaseExtra(int64(size))
				if stored {
					s.bytes.addExtra(doc, int64(size))
				}
				if prevDone != nil {
					prevDone(stored)
				}
			}, nil
		}
		prevDel := ea.OnDelete
		ea.OnDelete = func(id string, size int) {
			if prevDel != nil {
				prevDel(id, size)
			}
			if doc, ok := versionDoc(id); ok {
				s.bytes.subExtra(doc, int64(size))
			}
		}
	}
	if s.expiryDisabled() {
		log.Printf("public docstore: ALARM: the store's expiry policy gives public documents no deadline: every create will be refused (500) until it does; see /public/health")
	}
	if !s.prefixOK {
		log.Printf("public docstore: warning: the store cannot list by prefix, so the sweep lists the whole store; it does so at most every %s (Config.FullSweepInterval)", cfg.FullSweepInterval)
	}
	return s, nil
}

// Mount serves the public routes on srv (under its guard) and keeps the public documents away
// from its generic /docs routes. The admin route is mounted only when an AdminGuard that can
// allow somebody is configured (a non-empty token, or a custom guard).
func (s *Service) Mount(srv *docstore.Server) *docstore.Server {
	for _, p := range HiddenPrefixes() {
		srv.Hide(p)
	}
	srv.WithRoutes("/public/", s.Handler())
	if t, isToken := s.admin.(TokenAdmin); !isToken || t != "" {
		srv.WithRoutes("/admin/takedown/", s.AdminHandler())
		srv.WithRoutes("/admin/health", s.AdminHandler())
	}
	return srv
}

// Handler serves /public/session, /public/doc, /public/view/<id>, /public/report and /public/health.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/public/health", s.handleHealth)
	mux.HandleFunc("/public/session", s.handleSession)
	mux.HandleFunc("/public/doc", s.handleDoc)
	mux.HandleFunc("/public/view/", s.handleView)
	mux.HandleFunc("/public/report", s.handleReport)
	return s.headers(mux)
}

// headers adds what every public response carries: no indexing, no caching, no sniffing, no
// referrer (a view link must not leak through the Referer of a click on a link in the
// document), the terms link, the headers a browser client on another origin may read, and the
// deadlines of the request (a slow body is cut off).
func (s *Service) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d := s.cfg.RequestTimeout; d > 0 {
			rc := http.NewResponseController(w)
			now := time.Now()
			_ = rc.SetReadDeadline(now.Add(d)) // not supported by every writer (tests): then the server's own timeouts apply
			_ = rc.SetWriteDeadline(now.Add(2 * d))
		}
		h := w.Header()
		h.Set("Access-Control-Expose-Headers", ExposedHeaders)
		h.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if s.terms != "" {
			h.Add("Link", "<"+s.terms+`>; rel="terms"`)
			h.Add("Link", "<"+s.terms+`>; rel="terms-of-service"`)
		}
		next.ServeHTTP(w, r)
	})
}

// ExposedHeaders is Access-Control-Expose-Headers of the public routes (and of /docs, see
// docstore.ExposedHeaders): the response headers a script on another origin needs to read, which a
// browser otherwise hides.
const ExposedHeaders = docstore.ExposedHeaders

// ---- identifiers and records

// docKey is the store key of the document docID.
func docKey(docID string) string { return docKeyPrefix + docID }

// viewIDOf derives the view-only ID of a document: the first 128 bits of SHA-256 over a domain
// tag and the document ID. The document ID is random and never shown to a viewer, so the view
// ID is unguessable and does not reveal the document ID (or the edit token, which is a
// separate random value); deriving it makes the view record findable from the document alone.
func viewIDOf(docID string) string {
	h := sha256.Sum256([]byte("gojiberry/public/view/v1\x00" + docID))
	return base64.RawURLEncoding.EncodeToString(h[:16])
}

// validID reports whether id looks like an ID this service makes (22 base64url characters), so
// nothing else is ever put into a store key.
func validID(id string) bool {
	if len(id) != 22 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// docMeta is the record of one public document, "public/doc/<docID>". It holds no content and
// no token: the owner is the user ID of the session ("anon:<random>"), not a bearer.
type docMeta struct {
	Owner    string    `json:"owner"`
	ViewID   string    `json:"view"`
	Created  time.Time `json:"created"`
	Versions int       `json:"versions"`
	Bytes    int       `json:"bytes"`
	Reports  int       `json:"reports,omitempty"`
	// ReportRefs are the reports stored for this document (at most maxReportsOfDoc), what the
	// dedupe, the reserved slots and the priority eviction of handleReport work from.
	ReportRefs []reportRef `json:"reportRefs,omitempty"`
}

type viewRecord struct {
	Doc string `json:"doc"`
}

type tombstone struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason,omitempty"`
}

func (s *Service) putJSON(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.store.Save(ctx, key, b)
}

// getJSON loads key into v; found is false for a missing record.
func (s *Service) getJSON(ctx context.Context, key string, v any) (found bool, err error) {
	d, err := s.store.Load(ctx, key)
	if errors.Is(err, docstore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(d.Data, v)
}

func (s *Service) deleteKey(ctx context.Context, key string) error {
	if err := s.store.Delete(ctx, key); err != nil && !errors.Is(err, docstore.ErrNotFound) {
		return err
	}
	return nil
}

func (s *Service) lock(docID string) func() {
	h := fnv.New32a()
	_, _ = h.Write([]byte(docID))
	m := &s.locks[h.Sum32()%uint32(len(s.locks))]
	m.Lock()
	return m.Unlock
}

// removed reports whether the document's view ID has a tombstone: it was taken down.
func (s *Service) removed(ctx context.Context, docID string) (bool, error) {
	var t tombstone
	return s.getJSON(ctx, tombPrefix+viewIDOf(docID), &t)
}

// live reports whether the document is stored and not expired (an expired one is invisible to
// the ExpiringAdapter at once, before its sweep).
//
// It asks for the one document's deadline (a single small read), never for a listing; a store
// that has no deadline for it falls back to loading it.
func (s *Service) live(ctx context.Context, docID string) (bool, error) {
	key := docKey(docID)
	if x, ok := s.store.(docstore.Expirer); ok {
		t, has, err := x.Expiry(ctx, key)
		if err != nil {
			return false, err
		}
		if has {
			return s.now().Before(t), nil
		}
	}
	_, err := s.store.Load(ctx, key)
	if errors.Is(err, docstore.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// dropRecords deletes the document's own records (not the document, not the tombstone).
func (s *Service) dropRecords(ctx context.Context, docID string) error {
	if err := s.deleteKey(ctx, viewRecordPrefix+viewIDOf(docID)); err != nil {
		return err
	}
	return s.deleteKey(ctx, docRecordPrefix+docID)
}

// ---- responses

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // a write failure means the client went away
}

// fail answers 500 without the error's text, and logs it with the store keys (which hold document
// IDs) taken out.
func (s *Service) fail(w http.ResponseWriter, err error) {
	log.Printf("public docstore: %s", redact(err))
	http.Error(w, "storage error", http.StatusInternalServerError)
}

func tooMany(w http.ResponseWriter, retry time.Duration) {
	secs := int(retry/time.Second) + 1
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	http.Error(w, "too many requests", http.StatusTooManyRequests)
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// clientKey is the ClientKey of the address the request comes from (the remote address, or what
// Options.ClientIP names).
func (s *Service) clientKey(r *http.Request) string { return ClientKey(s.clientIP(r)) }

// allow asks the limiter for group's budget of the client of r (or of key, if not "").
func (s *Service) allow(w http.ResponseWriter, r *http.Request, l Limiter, group, who string) bool {
	if who == "" {
		who = s.clientKey(r)
	}
	ok, retry := l.Allow(r.Context(), group+"|"+who)
	if !ok {
		tooMany(w, retry)
	}
	return ok
}

// setExpires adds X-Gojiberry-Expires for the document, as docstore's /docs routes do.
func (s *Service) setExpires(ctx context.Context, w http.ResponseWriter, docID string) {
	if x, ok := s.store.(docstore.Expirer); ok {
		if t, has, err := x.Expiry(ctx, docKey(docID)); err == nil && has {
			w.Header().Set("X-Gojiberry-Expires", t.UTC().Format(time.RFC3339))
		}
	}
}

// expiresOf is the document's deadline; has is false when it has none (err nil) and err says the
// store could not be asked.
func (s *Service) expiresOf(ctx context.Context, docID string) (t time.Time, has bool, err error) {
	x, ok := s.store.(docstore.Expirer)
	if !ok {
		return time.Time{}, false, nil
	}
	t, has, err = x.Expiry(ctx, docKey(docID))
	return t.UTC(), has, err
}

// writeAllowed charges a write to the session's budget and to its address's.
func (s *Service) writeAllowed(w http.ResponseWriter, r *http.Request, sess Session) bool {
	return s.allow(w, r, s.lim.Write, "write", sess.ID) && s.allow(w, r, s.lim.WriteAddr, "writeaddr", "")
}

// ---- sessions

// sessionResponse is the body of POST /public/session.
type sessionResponse struct {
	// Token is the bearer token of the session: the edit capability. Keep it secret; the edit
	// link carries it in its fragment.
	Token   string    `json:"token"`
	UserID  string    `json:"userId"`
	Expires time.Time `json:"expires"`
	Limits  limits    `json:"limits"`
}

type limits struct {
	MaxDocBytes int64 `json:"maxDocBytes"`
	MaxVersions int   `json:"maxVersions"`
}

// handleSession serves POST /public/session: a new anonymous session. One document per session
// is the rule; clearing the browser's storage makes a new session, which is why the per-address
// budgets (sessions issued, bytes a day) are the real limit.
func (s *Service) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	if err := s.challenge.Verify(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if !s.allow(w, r, s.lim.Session, "session", "") {
		return
	}
	token, err := randomString(32)
	if err != nil {
		s.fail(w, err)
		return
	}
	id, err := randomString(16)
	if err != nil {
		s.fail(w, err)
		return
	}
	now := s.now()
	// A session that has not stored a document yet is short-lived: the first write extends it.
	addr := s.clientIP(r)
	sess := Session{ID: id, Client: ClientKey(addr), Net: NetKey(addr), Wide: WideKey(addr), Created: now, Expires: now.Add(s.cfg.SessionIdleTTL)}
	caps := SessionCaps{PerClient: s.cfg.MaxSessionsPerAddress, PerNet: s.cfg.MaxSessionsPerNet, PerWide: s.cfg.MaxSessionsPerWide, ReservePercent: max(0, s.cfg.SessionReservePercent)}
	switch err := s.sessions.Create(r.Context(), TokenKey(token), sess, now, caps); {
	case errors.Is(err, ErrClientSessionsFull):
		w.Header().Set("Retry-After", "900")
		http.Error(w, "too many live sessions from your address; finish or wait for the others to expire", http.StatusTooManyRequests)
		return
	case errors.Is(err, ErrNetSessionsFull):
		w.Header().Set("Retry-After", "900")
		http.Error(w, "too many live sessions from your network; finish or wait for the others to expire", http.StatusTooManyRequests)
		return
	case errors.Is(err, ErrWideSessionsFull):
		w.Header().Set("Retry-After", "900")
		http.Error(w, "too many live sessions from your network provider; finish or wait for the others to expire", http.StatusTooManyRequests)
		return
	case errors.Is(err, ErrSessionsFull):
		w.Header().Set("Retry-After", "60")
		http.Error(w, "the public offer is full right now; try again later", http.StatusServiceUnavailable)
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sessionResponse{
		Token: token, UserID: sess.User().ID, Expires: sess.Expires.UTC(),
		Limits: limits{MaxDocBytes: s.cfg.MaxDocBytes, MaxVersions: s.cfg.MaxVersions},
	})
}

// CurrentUser resolves the request's bearer token to its anonymous user (ID "anon:<random>"),
// the way auth.Manager.CurrentUser does for a signed-in one.
func (s *Service) CurrentUser(r *http.Request) (auth.User, bool) {
	_, sess, ok := s.lookup(r)
	if !ok {
		return auth.User{}, false
	}
	return sess.User(), true
}

func (s *Service) lookup(r *http.Request) (key string, sess Session, ok bool) {
	tok, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found || tok == "" || len(tok) > 256 {
		return "", Session{}, false
	}
	key = TokenKey(tok)
	sess, ok, err := s.sessions.Get(r.Context(), key, s.now())
	if err != nil || !ok {
		return "", Session{}, false
	}
	return key, sess, true
}

// session answers 401 unless the request carries a live session's token.
func (s *Service) session(w http.ResponseWriter, r *http.Request) (string, Session, bool) {
	key, sess, ok := s.lookup(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="public"`)
		http.Error(w, "start a session first (POST /public/session)", http.StatusUnauthorized)
	}
	return key, sess, ok
}

// renewSession extends the session after a write, within SessionMaxAge.
func (s *Service) renewSession(ctx context.Context, key string, sess Session) {
	now := s.now()
	exp := now.Add(s.cfg.SessionTTL)
	if limit := sess.Created.Add(s.cfg.SessionMaxAge); exp.After(limit) {
		exp = limit
	}
	_ = s.sessions.Extend(ctx, key, exp, now) // best effort: a failure shortens the session, not the document
}
