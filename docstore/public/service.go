package public

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/auth/passkey"
	"github.com/elaous/gojiberry/docstore"
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
	Quota          Quota                      // NewMemoryQuota(Config.IPDailyBytes, 24h)
	Admin          AdminGuard                 // TokenAdmin(Config.AdminToken)
	Blocklist      Blocklist                  // NoBlocklist
	Challenger     Challenger                 // NoChallenge
	ClientIP       func(*http.Request) string // passkey.TrustedProxies(Config.TrustedProxies...), else the remote address
	Now            func() time.Time           // time.Now; use the clock of the ExpiringAdapter in tests
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
		sessions: o.Sessions, lim: o.Limiters, quota: o.Quota, admin: o.Admin,
		blocklist: o.Blocklist, challenge: o.Challenger, clientIP: o.ClientIP, now: o.Now,
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
	// Version snapshots written through the store (the saver's layout) are counted when they are
	// written. This sets a hook on the caller's adapter: build the Service before serving.
	if ea, ok := o.Store.(*docstore.ExpiringAdapter); ok {
		prev := ea.OnSave
		ea.OnSave = func(id string, size int) {
			if prev != nil {
				prev(id, size)
			}
			s.NoteVersionWrite(id, int64(size))
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

// AdminHandler serves DELETE /admin/takedown/<id> and GET /admin/health.
func (s *Service) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/takedown/", s.handleTakedown)
	mux.HandleFunc("/admin/health", s.handleAdminHealth)
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

// reportRef is the part of a stored report its document's record keeps.
type reportRef struct {
	ID       string    `json:"id"`
	Reporter string    `json:"by"`  // the reporter's hash (reporterHash)
	Key      string    `json:"key"` // hash of (reporter, normalized reason): the dedupe key
	Priority bool      `json:"pri,omitempty"`
	Size     int       `json:"size"`
	At       time.Time `json:"at"`
}

type viewRecord struct {
	Doc string `json:"doc"`
}

type tombstone struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason,omitempty"`
}

// Report is a stored abuse report, "report/<ID>". It outlives the document (Config.ReportTTL);
// the content is not kept, so what the operator can act on is the view ID (takedown by ID).
type Report struct {
	ID     string    `json:"id"`
	ViewID string    `json:"view"`
	DocID  string    `json:"doc"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
	// Category is one of ReportCategories ("other" when the reporter named none or an unknown
	// one); Priority says the report was kept as a priority one: its category is on the priority
	// list and the reporter's and the document's priority caps had room.
	Category string `json:"category,omitempty"`
	Priority bool   `json:"priority,omitempty"`
	// Reporter is a keyed hash of the reporting client (never the address; see reporterHash), so
	// the same client's reports can be told from other clients' without holding an address.
	Reporter string `json:"reporter,omitempty"`
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

// ---- documents

// docResponse is the body of POST /public/doc.
type docResponse struct {
	// ID is the document's ID, for the owner (and collaborators holding the edit link); report
	// and takedown accept it as they accept the view ID.
	ID string `json:"id"`
	// ViewID names the read-only link: GET /public/view/<viewId>.
	ViewID   string    `json:"viewId"`
	Expires  time.Time `json:"expires"`
	Versions int       `json:"versions"`
}

// errTooSlow is what the minimum-rate reader fails with.
var errTooSlow = errors.New("public: the request body arrives slower than the minimum rate")

// rateReader reads a request body and holds the sender to a minimum average rate: after grace, at
// least rate bytes per second must have arrived, or the next read fails with errTooSlow. Before
// every read it sets the connection's read deadline to the moment the rate would be missed if
// nothing more came (grace + bytes read / rate, never later than limit after the start), so a
// client that sends nothing is cut off then, not at the request's far deadline: the declared length
// of a large body is held in flight, and a trickle must not hold it for the whole timeout.
type rateReader struct {
	r     io.Reader
	rc    *http.ResponseController
	start time.Time
	read  int64
	grace time.Duration
	rate  int64         // bytes per second
	limit time.Duration // the whole body, from the start
}

func (m *rateReader) Read(p []byte) (int, error) {
	allowed := min(m.grace+time.Duration(m.read*int64(time.Second)/m.rate), m.limit)
	if time.Since(m.start) >= allowed {
		return 0, errTooSlow
	}
	_ = m.rc.SetReadDeadline(m.start.Add(allowed)) // not every writer supports it (tests)
	n, err := m.r.Read(p)
	m.read += int64(n)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		err = errTooSlow
	}
	return n, err
}

// readBody reads the document body, refusing more than MaxDocBytes (413), an empty one (400) and
// one that arrives too slowly (408: below the minimum rate, see rateReader, or past the request's
// read deadline, see headers). A body that fails to arrive, whatever the reason, was paid for by
// the client: its daily bytes stay charged (tk.keep) and only the in-flight claim ends.
func (s *Service) readBody(w http.ResponseWriter, r *http.Request, tk *ticket) ([]byte, bool) {
	if r.ContentLength > s.cfg.MaxDocBytes {
		http.Error(w, "document too large", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	var src io.Reader = r.Body
	if s.cfg.MinUploadRate > 0 && (r.ContentLength < 0 || r.ContentLength >= MinRateBodyBytes) {
		src = &rateReader{r: r.Body, rc: http.NewResponseController(w), start: time.Now(), grace: s.cfg.UploadGrace, rate: s.cfg.MinUploadRate, limit: s.cfg.RequestTimeout}
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, struct {
		io.Reader
		io.Closer
	}{src, r.Body}, s.cfg.MaxDocBytes))
	if err != nil {
		tk.keep()
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "document too large", http.StatusRequestEntityTooLarge)
			return nil, false
		}
		if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, errTooSlow) {
			w.Header().Set("Connection", "close")
			http.Error(w, "the request body took too long to arrive", http.StatusRequestTimeout)
			return nil, false
		}
		http.Error(w, "reading the request body failed", http.StatusBadRequest)
		return nil, false
	}
	if len(data) == 0 {
		http.Error(w, "an empty document", http.StatusBadRequest)
		return nil, false
	}
	return data, true
}

// screen runs the content checks every write goes through. A body refused for its content (422)
// stays charged to the client's daily bytes: it was sent, and giving the bytes back would let one
// client send refused content for free; an outage of the checker (503) is the server's and is
// refunded.
func (s *Service) screen(w http.ResponseWriter, r *http.Request, data []byte, tk *ticket) bool {
	bad, err := s.blocklist.Blocked(r.Context(), data)
	if err != nil {
		log.Printf("public docstore: blocklist: %v", err)
		http.Error(w, "content check unavailable", http.StatusServiceUnavailable)
		return false
	}
	if bad {
		tk.keep()
		http.Error(w, "this content is not accepted", http.StatusUnprocessableEntity)
		return false
	}
	return true
}

// handleDoc serves the session's own document:
//
//	POST   /public/doc   create it (body: the document bytes); 409 if the session already has one
//	PUT    /public/doc   save an edit (renews the 24 hours)
//	GET    /public/doc   download it (never renews; always available until it is deleted)
//	DELETE /public/doc   discard it, freeing the session for another
func (s *Service) handleDoc(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.createDoc(w, r)
	case http.MethodPut:
		s.editDoc(w, r)
	case http.MethodGet, http.MethodHead:
		s.downloadDoc(w, r)
	case http.MethodDelete:
		s.discardDoc(w, r)
	default:
		methodNotAllowed(w, "POST, PUT, GET, DELETE")
	}
}

func (s *Service) createDoc(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key, sess, ok := s.session(w, r)
	if !ok {
		return
	}
	if err := s.challenge.Verify(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if !s.writeAllowed(w, r, sess) {
		return
	}
	// One document per session. A document that is gone (expired) frees the session; one that
	// was taken down does not: that session is done. Checked before the body is read.
	old := sess.DocID
	if old != "" {
		if gone, err := s.removed(ctx, old); err != nil {
			s.fail(w, err)
			return
		} else if gone {
			http.Error(w, "this session's document was removed; the session cannot create another", http.StatusForbidden)
			return
		}
		if live, err := s.live(ctx, old); err != nil {
			s.fail(w, err)
			return
		} else if live {
			http.Error(w, "this session already has a document: download or discard it first", http.StatusConflict)
			return
		}
	}
	// Size, storage and the daily bytes are settled before the body is read.
	tk, ok := s.admit(w, r)
	if !ok {
		return
	}
	defer tk.release()
	data, ok := s.readBody(w, r, tk)
	if !ok {
		return
	}
	tk.settle(int64(len(data)))
	if !s.screen(w, r, data, tk) {
		return
	}
	n := int64(len(data))
	docID, err := randomString(16)
	if err != nil {
		s.fail(w, err)
		return
	}
	res, fits := s.bytes.reserve(docID, n, n, s.cfg.MaxTotalBytes)
	if !fits {
		storeFull(w)
		return
	}
	defer res.cancel() // a no-op once committed
	switch claimed, err := s.sessions.SetDoc(ctx, key, old, docID, s.now()); {
	case errors.Is(err, ErrNoSession):
		http.Error(w, "the session ended", http.StatusUnauthorized)
		return
	case err != nil:
		s.fail(w, err)
		return
	case !claimed:
		http.Error(w, "this session already has a document: download or discard it first", http.StatusConflict)
		return
	}
	release := func() { _, _ = s.sessions.SetDoc(context.WithoutCancel(ctx), key, docID, old, s.now()) }
	unlock := s.lock(docID)
	defer unlock()
	view := viewIDOf(docID)
	meta := docMeta{Owner: sess.User().ID, ViewID: view, Created: s.now().UTC(), Versions: 1, Bytes: len(data)}
	// Records first, the document last: a failure leaves records without a document, which the
	// sweep removes, never a document nobody can find or expire.
	rollback := func() {
		bg := context.WithoutCancel(ctx)
		_ = s.deleteKey(bg, docKey(docID))
		_ = s.dropRecords(bg, docID)
		s.bytes.release(docID) // a reconcile may have listed the document meanwhile
		release()
	}
	if err := s.putJSON(ctx, docRecordPrefix+docID, meta); err != nil {
		rollback()
		s.fail(w, err)
		return
	}
	if err := s.putJSON(ctx, viewRecordPrefix+view, viewRecord{Doc: docID}); err != nil {
		rollback()
		s.fail(w, err)
		return
	}
	if err := s.store.Save(ctx, docKey(docID), data); err != nil {
		rollback()
		s.fail(w, err)
		return
	}
	exp, has, err := s.expiresOf(ctx, docID)
	if err != nil {
		rollback()
		s.fail(w, err)
		return
	}
	if !has {
		// The store did not give the document a deadline (a policy that skips it): never keep a
		// public document that would live for ever.
		rollback()
		s.alarmNoDeadline()
		s.fail(w, errNoDeadline)
		return
	}
	// A takedown that arrived while this create was in flight (it tombstones an ID it cannot
	// resolve yet, see handleTakedown) wins: look again now that the document is stored, so
	// either this sees the tombstone or the takedown sees the records and deletes them.
	if gone, err := s.removed(ctx, docID); err != nil {
		rollback()
		s.fail(w, err)
		return
	} else if gone {
		// Removed, and the session keeps the claim on the dead document ID: it stays banned.
		bg := context.WithoutCancel(ctx)
		_ = s.deleteKey(bg, docKey(docID))
		_ = s.dropRecords(bg, docID)
		s.bytes.release(docID)
		http.Error(w, "this document was removed", http.StatusGone)
		return
	}
	res.commit()
	tk.keep()
	s.renewSession(ctx, key, sess)
	w.Header().Set("X-Gojiberry-Expires", exp.Format(time.RFC3339))
	writeJSON(w, http.StatusCreated, docResponse{ID: docID, ViewID: view, Expires: exp, Versions: 1})
}

// ownDoc finds the session's document and its record, answering 404 (none, or expired) or 410
// (taken down).
func (s *Service) ownDoc(w http.ResponseWriter, r *http.Request, sess Session) (docID string, ok bool) {
	ctx := r.Context()
	if sess.DocID == "" {
		http.Error(w, "this session has no document", http.StatusNotFound)
		return "", false
	}
	if gone, err := s.removed(ctx, sess.DocID); err != nil {
		s.fail(w, err)
		return "", false
	} else if gone {
		http.Error(w, "this document was removed", http.StatusGone)
		return "", false
	}
	return sess.DocID, true
}

func (s *Service) editDoc(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key, sess, ok := s.session(w, r)
	if !ok {
		return
	}
	docID, ok := s.ownDoc(w, r, sess)
	if !ok {
		return
	}
	if !s.writeAllowed(w, r, sess) {
		return
	}
	// The save limit is checked on the record as it is now, before the body is read, and again
	// under the lock below.
	var early docMeta
	if found, err := s.getJSON(ctx, docRecordPrefix+docID, &early); err != nil {
		s.fail(w, err)
		return
	} else if found && early.Versions >= s.cfg.MaxVersions {
		http.Error(w, "this document has reached its limit of saves; download it and start a new one", http.StatusForbidden)
		return
	}
	tk, ok := s.admit(w, r)
	if !ok {
		return
	}
	defer tk.release()
	data, ok := s.readBody(w, r, tk)
	if !ok {
		return
	}
	tk.settle(int64(len(data)))
	if !s.screen(w, r, data, tk) {
		return
	}
	unlock := s.lock(docID)
	defer unlock()
	var meta docMeta
	if found, err := s.getJSON(ctx, docRecordPrefix+docID, &meta); err != nil {
		s.fail(w, err)
		return
	} else if !found || meta.Owner != sess.User().ID {
		http.Error(w, "this session has no document", http.StatusNotFound)
		return
	}
	if live, err := s.live(ctx, docID); err != nil {
		s.fail(w, err)
		return
	} else if !live {
		// Expired: an edit must not bring it back (the view link and the document are gone).
		http.Error(w, "this document expired; create a new one", http.StatusNotFound)
		return
	}
	if meta.Versions >= s.cfg.MaxVersions {
		http.Error(w, "this document has reached its limit of saves; download it and start a new one", http.StatusForbidden)
		return
	}
	delta := int64(len(data)) - int64(meta.Bytes)
	res, fits := s.bytes.reserve(docID, int64(len(data)), delta, s.cfg.MaxTotalBytes)
	if !fits {
		storeFull(w)
		return
	}
	defer res.cancel() // a no-op once committed
	// The count goes up before the save and comes back down if the save fails: a crash between
	// the two can only leave the count one too high (the safe side), never an uncounted edit.
	next := meta
	next.Versions++
	next.Bytes = len(data)
	if err := s.putJSON(ctx, docRecordPrefix+docID, next); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.store.Save(ctx, docKey(docID), data); err != nil {
		_ = s.putJSON(context.WithoutCancel(ctx), docRecordPrefix+docID, meta)
		s.fail(w, err)
		return
	}
	// The save renewed the deadline; make sure there is one. Without it the document would
	// live for ever: remove it (the edit cannot be taken back) and raise the alarm.
	exp, has, err := s.expiresOf(ctx, docID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !has {
		bg := context.WithoutCancel(ctx)
		_ = s.deleteKey(bg, docKey(docID))
		_ = s.dropRecords(bg, docID)
		s.bytes.release(docID) // the document is gone: its bytes are not counted until the next sweep
		s.alarmNoDeadline()
		s.fail(w, errNoDeadline)
		return
	}
	res.commit()
	tk.keep()
	s.renewSession(ctx, key, sess)
	w.Header().Set("X-Gojiberry-Expires", exp.Format(time.RFC3339))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) downloadDoc(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, sess, ok := s.session(w, r)
	if !ok {
		return
	}
	docID, ok := s.ownDoc(w, r, sess)
	if !ok {
		return
	}
	doc, err := s.store.Load(ctx, docKey(docID))
	if errors.Is(err, docstore.ErrNotFound) {
		http.Error(w, "this document expired or was deleted", http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="document"`)
	s.setExpires(ctx, w, docID)
	_, _ = w.Write(doc.Data)
}

func (s *Service) discardDoc(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key, sess, ok := s.session(w, r)
	if !ok {
		return
	}
	docID, ok := s.ownDoc(w, r, sess)
	if !ok {
		return
	}
	unlock := s.lock(docID)
	defer unlock()
	if err := s.deleteKey(ctx, docKey(docID)); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.dropRecords(ctx, docID); err != nil {
		s.fail(w, err)
		return
	}
	s.bytes.release(docID)
	if _, err := s.sessions.SetDoc(ctx, key, docID, "", s.now()); err != nil && !errors.Is(err, ErrNoSession) {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- view links

// handleView serves GET /public/view/<viewId>: the document, read-only. It never renews the
// expiry, and shows nothing but the content and the deadline: not the edit token, the owner
// session or the document ID. A taken-down view ID answers 410.
func (s *Service) handleView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	if !s.allow(w, r, s.lim.View, "view", "") {
		return
	}
	ctx := r.Context()
	view := strings.TrimPrefix(r.URL.Path, "/public/view/")
	if !validID(view) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var t tombstone
	if gone, err := s.getJSON(ctx, tombPrefix+view, &t); err != nil {
		s.fail(w, err)
		return
	} else if gone {
		http.Error(w, "this document was removed", http.StatusGone)
		return
	}
	var rec viewRecord
	if found, err := s.getJSON(ctx, viewRecordPrefix+view, &rec); err != nil {
		s.fail(w, err)
		return
	} else if !found || !validID(rec.Doc) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	doc, err := s.store.Load(ctx, docKey(rec.Doc))
	if errors.Is(err, docstore.ErrNotFound) {
		_ = s.dropRecords(context.WithoutCancel(ctx), rec.Doc) // expired: its records go with it
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	s.setExpires(ctx, w, rec.Doc)
	_, _ = w.Write(doc.Data)
}

// resolve finds the document an ID names: a view ID or a document ID. ok is false for an
// unknown one.
func (s *Service) resolve(ctx context.Context, id string) (docID, view string, ok bool, err error) {
	if !validID(id) {
		return "", "", false, nil
	}
	var rec viewRecord
	if found, err := s.getJSON(ctx, viewRecordPrefix+id, &rec); err != nil {
		return "", "", false, err
	} else if found && validID(rec.Doc) {
		return rec.Doc, id, true, nil
	}
	var meta docMeta
	if found, err := s.getJSON(ctx, docRecordPrefix+id, &meta); err != nil {
		return "", "", false, err
	} else if found {
		return id, viewIDOf(id), true, nil
	}
	return "", "", false, nil
}

// ---- reports

const (
	maxReportBody   = 8 << 10
	maxReasonBytes  = 2000
	maxReportsOfDoc = 20
	// maxPriorityOfDoc is how many of a document's slots priority reports may hold: half, so a
	// priority flood cannot pin a document's slots and the ordinary reports of everyone else
	// still find room.
	maxPriorityOfDoc = maxReportsOfDoc / 2
)

// handleReport serves POST /public/report {"id": "<view id or document id>", "reason": "...",
// "category": "csam|illegal|spam|other"}. The category is one of the fixed set ReportCategories (an
// unknown or missing one is "other"); only the category, never the free text of the reason, can
// make a report a priority one. The reports are kept (report/<id>) for the operator, who takes a
// document down by ID. A document stops collecting reports after a few, so one link cannot fill the
// store.
func (s *Service) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	if !s.allow(w, r, s.lim.Report, "report", "") {
		return
	}
	ctx := r.Context()
	var body struct {
		ID       string `json:"id"`
		Reason   string `json:"reason"`
		Category string `json:"category"`
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxReportBody))
	if err != nil {
		http.Error(w, "request too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	if json.Unmarshal(data, &body) != nil || strings.TrimSpace(body.Reason) == "" || strings.TrimSpace(body.ID) == "" {
		http.Error(w, `expected {"id": "<view id>", "reason": "<why>", "category": "csam|illegal|spam|other"}`, http.StatusBadRequest)
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if len(reason) > maxReasonBytes {
		reason = strings.ToValidUTF8(reason[:maxReasonBytes], "")
	}
	category := categoryOf(body.Category)
	id := strings.TrimSpace(body.ID)
	if validID(id) {
		var t tombstone
		if gone, err := s.getJSON(ctx, tombPrefix+id, &t); err != nil {
			s.fail(w, err)
			return
		} else if gone {
			http.Error(w, "this document was already removed", http.StatusGone)
			return
		}
	}
	docID, view, found, err := s.resolve(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	addr := s.clientIP(r)
	reporter, err := s.reporterHash(ctx, ClientKey(addr))
	if err != nil {
		// Without the salt no reporter can be told from another (and a made-up one would split a
		// reporter's reports across restarts): the report is refused, not taken half-way.
		w.Header().Set("Retry-After", "10")
		http.Error(w, "reports cannot be taken right now; try again shortly", http.StatusServiceUnavailable)
		return
	}
	if s.isPriority(category) {
		s.noteBurst(NetKey(addr), docID)
	}
	reportID, err := randomString(16)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.ensureCounters(ctx) // the report counters are only for eviction: a report is taken even if they are unknown
	needEvict, stored, err := func() (bool, string, error) {
		unlock := s.lock(docID)
		defer unlock()
		var meta docMeta
		if found, err := s.getJSON(ctx, docRecordPrefix+docID, &meta); err != nil || !found {
			return false, "", err
		}
		id, _, over, err := s.storeReport(ctx, &meta, Report{ID: reportID, ViewID: view, DocID: docID, Reason: reason, Category: category, At: s.now().UTC(), Reporter: reporter})
		return over, id, err
	}()
	if err != nil {
		s.fail(w, err)
		return
	}
	if stored != "" {
		reportID = stored
	}
	if needEvict {
		// Over the global bound: the oldest ordinary reports go, the new one stays. It runs after
		// the document's lock is let go: evicting updates the records of other documents, each
		// under its own lock.
		if metas, info, err := s.listReports(context.WithoutCancel(ctx)); err == nil {
			s.evictReports(context.WithoutCancel(ctx), metas, info)
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"reportId": reportID})
}

// reportKey is the dedupe key of a report: the reporter, the category and the reason as compared
// (trimmed, lowercase, spaces collapsed), so the same client saying the same thing twice about a
// document counts once.
func reportKey(reporter, category, reason string) string {
	h := sha256.Sum256([]byte(reporter + "\x00" + categoryOf(category) + "\x00" + normalizeReason(reason)))
	return base64.RawURLEncoding.EncodeToString(h[:9])
}

// saltRetryMax caps the backoff between attempts to get the reporter salt.
const saltRetryMax = 30 * time.Second

// reporterSalt returns the stored salt of the reporter hashes, creating it if the store has none.
// The salt is never made up for this process when the store fails: a failure is an error, retried
// after a backoff (1s, 2s, ... up to 30s; nothing is tried before it ends), and the caller refuses
// the report until a salt exists. It is created with a create-if-absent write (SaveIf with no
// previous content), so replicas starting together converge on one value: whoever loses the race
// reads the winner's. A store that cannot write conditionally falls back to save and read back.
func (s *Service) reporterSalt(ctx context.Context) ([]byte, error) {
	s.saltMu.Lock()
	defer s.saltMu.Unlock()
	if s.salt != nil {
		return s.salt, nil
	}
	now := s.now()
	if now.Before(s.saltRetry) {
		return nil, errSaltUnavailable
	}
	// The store may be slow or the client may leave: neither may decide for everyone, so the
	// attempt has a context of its own.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	salt, err := s.loadOrCreateSalt(ctx)
	if err != nil {
		s.saltFails++
		backoff := min(time.Second<<min(s.saltFails-1, 5), saltRetryMax)
		s.saltRetry = now.Add(backoff)
		log.Printf("public docstore: the reporter salt cannot be read or created: %s (reports are refused; next attempt in %s)", redact(err), backoff)
		return nil, errSaltUnavailable
	}
	s.salt, s.saltFails = salt, 0
	return salt, nil
}

var errSaltUnavailable = errors.New("public: the reporter salt is not available")

func (s *Service) loadOrCreateSalt(ctx context.Context) ([]byte, error) {
	valid := func(b []byte) ([]byte, bool) {
		var rec struct {
			Salt []byte `json:"salt"`
		}
		return rec.Salt, json.Unmarshal(b, &rec) == nil && len(rec.Salt) >= 16
	}
	for range 3 {
		d, err := s.store.Load(ctx, saltKey)
		prev := ""
		switch {
		case err == nil:
			if salt, ok := valid(d.Data); ok {
				return salt, nil
			}
			prev = docstore.Digest(d.Data) // damaged: replace exactly that
		case errors.Is(err, docstore.ErrNotFound):
		default:
			return nil, err
		}
		fresh := make([]byte, 32)
		if _, err := rand.Read(fresh); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(map[string][]byte{"salt": fresh})
		c, ok := s.store.(docstore.ConditionalAdapter)
		if !ok {
			return s.saveSaltUnconditionally(ctx, raw)
		}
		switch err := c.SaveIf(ctx, saltKey, raw, prev); {
		case err == nil:
			return fresh, nil
		case errors.Is(err, docstore.ErrConflict):
			continue // somebody else created it first: read theirs
		case errors.Is(err, docstore.ErrConditionalUnsupported):
			return s.saveSaltUnconditionally(ctx, raw)
		default:
			return nil, err
		}
	}
	return nil, errors.New("the reporter salt kept changing while it was being created")
}

// saveSaltUnconditionally is the fallback for a store without a conditional write: save, then read
// back what is stored and use that, so replicas that raced end up with the last writer's value
// (after a short window in which they may have used their own).
func (s *Service) saveSaltUnconditionally(ctx context.Context, raw []byte) ([]byte, error) {
	if err := s.store.Save(ctx, saltKey, raw); err != nil {
		return nil, err
	}
	d, err := s.store.Load(ctx, saltKey)
	if err != nil {
		return nil, err
	}
	var rec struct {
		Salt []byte `json:"salt"`
	}
	if json.Unmarshal(d.Data, &rec) != nil || len(rec.Salt) < 16 {
		return nil, errors.New("the stored reporter salt is unreadable")
	}
	return rec.Salt, nil
}

// reporterHash names a reporting client without holding its address: HMAC-SHA-256 under the
// report key (Config.ReportKey, which is never stored) over the stored salt and the client, cut to
// 96 bits. The salt alone would not do: it is in the store, and with it a dump could be tested
// against all four billion IPv4 addresses; the key is not. So "the report holds no address"
// holds as long as the key is kept out of the store. It is the same across restarts and replicas
// that share the key and the store, and an error (no salt yet) means the report cannot be taken.
func (s *Service) reporterHash(ctx context.Context, client string) (string, error) {
	salt, err := s.reporterSalt(ctx)
	if err != nil {
		return "", err
	}
	m := hmac.New(sha256.New, s.reportKey)
	_, _ = m.Write([]byte("gojiberry/public/reporter/v1\x00"))
	_, _ = m.Write(salt)
	_, _ = m.Write([]byte{0})
	_, _ = m.Write([]byte(client))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:12]), nil
}

// refLive reports whether the report a reference names is still stored.
func (s *Service) refLive(ctx context.Context, ref reportRef) (bool, error) {
	_, err := s.store.Load(ctx, docstore.ReportPrefix+ref.ID)
	if errors.Is(err, docstore.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// pruneRefs drops the references whose reports are gone (evicted or deleted by a path that did not
// update the record, a crash between the two) and reports whether it changed meta. The count
// follows the live references.
func (s *Service) pruneRefs(ctx context.Context, meta *docMeta) (bool, error) {
	kept := meta.ReportRefs[:0:0]
	for _, ref := range meta.ReportRefs {
		live, err := s.refLive(ctx, ref)
		if err != nil {
			return false, err
		}
		if live {
			kept = append(kept, ref)
		}
	}
	if len(kept) == len(meta.ReportRefs) {
		return false, nil
	}
	meta.ReportRefs = kept
	meta.Reports = len(kept)
	return true, nil
}

// storeReport decides whether rep is kept for the document whose record is meta (the caller holds
// the document's lock) and keeps it. The rules, in order:
//
//   - the same reporter with the same category and reason again is a duplicate and counts once:
//     nothing is stored, and the id of the first report is returned (if that report is gone, the
//     reference is dropped and this is a new report);
//   - a report counts as a priority one only if its category is on the priority list, its reporter
//     has made fewer than Config.MaxPriorityReportsPerReporter priority reports in the last day, and
//     the document holds fewer than maxPriorityOfDoc priority reports; otherwise it is an ordinary
//     one (kept, or dropped, by the rules below);
//   - a document has at most maxReportsOfDoc reports; a priority one that finds the document full
//     evicts the document's oldest ordinary report to make room (or is dropped if every report
//     there is a priority one, which the half cap makes impossible), any other is dropped;
//   - the last Config.ReportReservedPerDoc slots of a document are for reporters with no report on
//     it yet, and, once the stored reports pass the global bound minus Config.ReportReservePercent
//     of it, reports are taken only from reporters with no stored report at all (priority reports
//     are exempt from both reserves: they are not the flood the reserves keep out).
//
// The slots are counted from the document's live reports: references to reports that no longer
// exist are dropped before they are counted. A dropped report is answered 202 like a kept one, so a
// flooder learns nothing. id is the report's id if it was stored or is a duplicate, "" if it was
// dropped; over says the stored reports now pass the global bound, which the caller (not holding
// the lock) answers with evictReports.
func (s *Service) storeReport(ctx context.Context, meta *docMeta, rep Report) (id string, stored, over bool, err error) {
	pruned := false
	defer func() {
		// A prune is worth keeping even when the report itself is dropped.
		if pruned && !stored {
			if e := s.putJSON(ctx, docRecordPrefix+rep.DocID, *meta); e != nil {
				log.Printf("public docstore: updating a document's report list: %s", redact(e))
			}
		}
	}()
	key := reportKey(rep.Reporter, rep.Category, rep.Reason)
	for i, ref := range meta.ReportRefs {
		if ref.Key == key {
			live, err := s.refLive(ctx, ref)
			if err != nil {
				return "", false, false, err
			}
			if live {
				return ref.ID, false, false, nil
			}
			meta.ReportRefs = append(meta.ReportRefs[:i:i], meta.ReportRefs[i+1:]...)
			meta.Reports = len(meta.ReportRefs)
			pruned = true
			break
		}
	}
	if len(meta.ReportRefs) >= maxReportsOfDoc-max(s.cfg.ReportReservedPerDoc, 0) {
		changed, err := s.pruneRefs(ctx, meta)
		if err != nil {
			return "", false, false, err
		}
		pruned = pruned || changed
	}
	priority := false
	if s.isPriority(rep.Category) {
		held := 0
		for _, ref := range meta.ReportRefs {
			if ref.Priority {
				held++
			}
		}
		priority = held < maxPriorityOfDoc && s.prio.allow(rep.Reporter, s.now(), s.cfg.MaxPriorityReportsPerReporter)
	}
	rep.Priority = priority
	n := max(len(meta.ReportRefs), meta.Reports)
	if n >= maxReportsOfDoc {
		if !priority || !s.evictOrdinaryRef(ctx, meta) {
			return "", false, false, nil
		}
		pruned = true // the eviction changed the record; keep it even if the save below fails
	} else if !priority {
		if reserved := s.cfg.ReportReservedPerDoc; reserved > 0 && n >= maxReportsOfDoc-reserved {
			for _, ref := range meta.ReportRefs {
				if ref.Reporter == rep.Reporter {
					return "", false, false, nil // the slots left are for new reporters
				}
			}
		}
		if pct := s.cfg.ReportReservePercent; pct > 0 {
			all, mine := s.reports.held(rep.Reporter)
			if all >= s.cfg.MaxReports-s.cfg.MaxReports*pct/100 && mine > 0 {
				return "", false, false, nil
			}
		}
	}
	raw, _ := json.Marshal(rep)
	if err := s.store.Save(ctx, docstore.ReportPrefix+rep.ID, raw); err != nil {
		return "", false, false, err
	}
	s.reports.add(1, int64(len(raw)), rep.Reporter)
	if priority {
		s.prio.record(rep.Reporter, s.now())
	}
	over = s.reports.over(s.cfg.MaxReports, s.cfg.MaxReportBytes)
	meta.ReportRefs = append(meta.ReportRefs, reportRef{ID: rep.ID, Reporter: rep.Reporter, Key: key, Priority: priority, Size: len(raw), At: rep.At})
	meta.Reports = len(meta.ReportRefs)
	if err := s.putJSON(ctx, docRecordPrefix+rep.DocID, *meta); err != nil {
		return "", false, over, err
	}
	return rep.ID, true, over, nil
}

// evictOrdinaryRef deletes the oldest non-priority report of the document and reports whether
// there was one.
func (s *Service) evictOrdinaryRef(ctx context.Context, meta *docMeta) bool {
	oldest := -1
	for i, ref := range meta.ReportRefs {
		if !ref.Priority && (oldest < 0 || ref.At.Before(meta.ReportRefs[oldest].At)) {
			oldest = i
		}
	}
	if oldest < 0 {
		return false
	}
	ref := meta.ReportRefs[oldest]
	if err := s.deleteKey(ctx, docstore.ReportPrefix+ref.ID); err != nil {
		log.Printf("public docstore: evicting a report: %s", redact(err))
		return false
	}
	s.reports.add(-1, -int64(ref.Size), ref.Reporter)
	meta.ReportRefs = append(meta.ReportRefs[:oldest:oldest], meta.ReportRefs[oldest+1:]...)
	meta.Reports = len(meta.ReportRefs)
	return true
}

// ---- takedown

// adminAllowed is the admin guard in front of the admin routes. The token is checked first (in
// constant time); a request that passes is never counted and never held back, so wrong guesses
// from an address, a network or everyone behind one shared address cannot lock the operator out.
// Only failures are counted: per client (a /64, Limiters.Admin), per network (a /48 or /24,
// Limiters.AdminNet) and in all (Limiters.AdminGlobal); past the first two a failure answers 429
// with Retry-After instead of 403, and once the global budget is used up every wrong token gets 429
// for AdminCooldown. A burst of failures is logged (adminFailed).
//
// What the limits are for: they slow a guesser down, bound the work it can ask for and make a
// burst visible. They cannot make guessing safe by themselves, because a right guess is answered
// at once however many wrong ones came before it, and wrong guesses cost the guesser nothing but
// a request: the safety is the token's length, which is why the server will not start with fewer
// than MinAdminTokenLength characters (Config.AllowShortAdminToken is the explicit override).
func (s *Service) adminAllowed(w http.ResponseWriter, r *http.Request) bool {
	if s.admin != nil && s.admin.Allow(r) {
		return true
	}
	now := s.now()
	s.adminFailed(now)
	s.adminMu.Lock()
	cool := s.adminCool
	s.adminMu.Unlock()
	if now.Before(cool) {
		tooMany(w, cool.Sub(now))
		return false
	}
	addr := s.clientIP(r)
	if !s.allow(w, r, s.lim.Admin, "admin", ClientKey(addr)) || !s.allow(w, r, s.lim.AdminNet, "adminnet", NetKey(addr)) {
		return false
	}
	if ok, _ := s.lim.AdminGlobal.Allow(r.Context(), "admin-all"); !ok {
		s.adminMu.Lock()
		first := !now.Before(s.adminCool)
		s.adminCool = now.Add(AdminCooldown)
		s.adminMu.Unlock()
		if first {
			log.Printf("public docstore: ALARM: failed admin authentications passed %v a minute in all: wrong tokens are refused for %s (the admin token is being guessed, or a client of the admin route is misconfigured)", s.cfg.AdminFailuresGlobalPerMinute, AdminCooldown)
		}
		tooMany(w, AdminCooldown)
		return false
	}
	http.Error(w, "forbidden", http.StatusForbidden)
	return false
}

// adminBurst is how many failed admin authentications in a minute are worth a log line.
const adminBurst = 10

// adminFailed counts a failed admin authentication and logs once per minute in which there were
// adminBurst or more.
func (s *Service) adminFailed(now time.Time) {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	if now.Sub(s.adminMin) >= time.Minute {
		s.adminMin, s.adminFails = now, 0
	}
	s.adminFails++
	if s.adminFails == adminBurst {
		log.Printf("public docstore: WARNING: %d failed admin authentications within a minute (the admin token may be being guessed); see the limits in section 7.2 item 18", s.adminFails)
	}
}

// handleTakedown serves DELETE /admin/takedown/<id> (a view ID or a document ID): the document,
// its view ID and its records are deleted and a tombstone is written, so the view ID answers 410
// from then on and the owner's session cannot create another document. The call is idempotent
// (a second one on a removed ID is 204 too). ?reason= is kept on the tombstone.
func (s *Service) handleTakedown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		methodNotAllowed(w, "DELETE")
		return
	}
	if !s.adminAllowed(w, r) {
		return
	}
	ctx := r.Context()
	id := strings.TrimPrefix(r.URL.Path, "/admin/takedown/")
	if !validID(id) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var existing tombstone
	if gone, err := s.getJSON(ctx, tombPrefix+id, &existing); err != nil {
		s.fail(w, err)
		return
	} else if gone {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	reason := strings.ToValidUTF8(r.URL.Query().Get("reason"), "")
	if len(reason) > 200 {
		reason = strings.ToValidUTF8(reason[:200], "")
	}
	tomb := func(view string) error {
		return s.putJSON(ctx, tombPrefix+view, tombstone{At: s.now().UTC(), Reason: reason})
	}
	docID, view, found, err := s.resolve(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !found {
		// No record names this ID: the document expired and its records were swept (its session,
		// which keeps the document ID, must still be banned), or it is being created right now.
		// The ban is the tombstone of the view ID, which the session's check derives from its
		// document ID, so it is written for both readings of the ID (as a view ID, and as a
		// document ID whose view ID follows) before anything else, and then looked up once more:
		// a create in flight sees the tombstone when it finishes (createDoc), or has left records
		// this finds now.
		if err := tomb(id); err != nil {
			s.fail(w, err)
			return
		}
		if err := tomb(viewIDOf(id)); err != nil {
			s.fail(w, err)
			return
		}
		if docID, view, found, err = s.resolve(ctx, id); err != nil {
			s.fail(w, err)
			return
		}
		if !found {
			log.Print("public docstore: takedown of an ID with no records: tombstoned (the document is gone or not yet stored)")
			w.WriteHeader(http.StatusAccepted)
			return
		}
	}
	unlock := s.lock(docID)
	defer unlock()
	// The tombstone first: from this write on the view ID answers 410, whatever fails below
	// (a retry finishes the deletion).
	if err := tomb(view); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.deleteKey(ctx, docKey(docID)); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.dropRecords(ctx, docID); err != nil {
		s.fail(w, err)
		return
	}
	s.bytes.release(docID)
	w.WriteHeader(http.StatusNoContent)
}

// NoteVersionWrite tells the byte budget that a version snapshot of a public document was written:
// key is the snapshot's store key ("versions/pubdoc/<docID>/<name>") and size its length; any other
// key is ignored. A Service over an *docstore.ExpiringAdapter wires this to its OnSave hook itself;
// a deployment whose snapshots are written some other way calls it from there. Without it the
// snapshots are counted only when Sweep lists the store.
func (s *Service) NoteVersionWrite(key string, size int64) {
	rest, ok := strings.CutPrefix(key, versionsKeyPrefix)
	if !ok {
		return
	}
	id, _, _ := strings.Cut(rest, "/")
	if validID(id) {
		s.bytes.addExtra(id, size)
	}
}

// ---- sweep

// recordGrace is how long a document record may exist without its document before the sweep
// calls it an orphan: creation writes the records first.
const recordGrace = time.Minute

// Sweep removes what the ExpiringAdapter does not: the records of documents that expired or are
// gone (metadata and view-ID records, which are in a system keyspace and so never expire by
// themselves), the reports and tombstones past their own TTL, and the reports over the global
// bound (oldest first); it reaps expired sessions, and reconciles the byte and report counters
// with the store. It returns how many records it deleted. Run calls it on an interval.
//
// Cost. Its listings are by prefix, so with a PrefixLister they cost what the public data costs
// (live documents, records, reports, tombstones: all bounded by the caps). Over a store without
// prefix listing every listing is the whole store, so the listing part runs at most every
// Config.FullSweepInterval there; the other calls only reap sessions.
func (s *Service) Sweep(ctx context.Context) (removed int, err error) {
	var first error
	defer func() {
		s.healthMu.Lock()
		s.lastSweep = s.now()
		s.sweepFailing = first != nil || err != nil
		s.healthMu.Unlock()
	}()
	note := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	del := func(key string) bool {
		if err := s.deleteKey(ctx, key); err != nil {
			note(err)
			return false
		}
		removed++
		return true
	}
	now := s.now()
	if r, ok := s.sessions.(SessionReaper); ok {
		if _, err := r.Reap(ctx, now); err != nil {
			note(err)
		}
	}
	if !s.prefixOK {
		s.healthMu.Lock()
		skip := !s.lastFull.IsZero() && now.Sub(s.lastFull) < s.cfg.FullSweepInterval
		if !skip {
			s.lastFull = now
		}
		s.healthMu.Unlock()
		if skip {
			return 0, first
		}
	}

	repWin := s.reports.begin()
	repDone := false
	defer func() {
		if !repDone {
			s.reports.abort(repWin)
		}
	}()
	bytesWin := s.bytes.begin()
	listed, err := s.listBudget(ctx)
	if err != nil {
		s.bytes.abort(bytesWin)
		return 0, err
	}
	s.bytes.set(bytesWin, listed) // version snapshots count with their documents
	live := make(map[string]bool, len(listed))
	for id, e := range listed {
		if e.doc > 0 {
			live[id] = true
		}
	}
	docs, err := docstore.ListPrefix(ctx, s.store, docRecordPrefix)
	if err != nil {
		return 0, err
	}
	known := map[string]bool{} // views of document records that stay
	for _, m := range docs {
		docID := strings.TrimPrefix(m.ID, docRecordPrefix)
		if live[docID] {
			known[viewIDOf(docID)] = true
			continue
		}
		var meta docMeta
		found, err := s.getJSON(ctx, m.ID, &meta)
		if err != nil {
			note(err)
			continue
		}
		if !found || now.Sub(meta.Created) < recordGrace {
			known[viewIDOf(docID)] = true
			continue
		}
		unlock := s.lock(docID)
		if ok, err := s.live(ctx, docID); err != nil {
			note(err)
			known[viewIDOf(docID)] = true
		} else if ok {
			known[viewIDOf(docID)] = true
		} else {
			del(viewRecordPrefix + viewIDOf(docID))
			del(m.ID)
		}
		unlock()
	}
	views, err := docstore.ListPrefix(ctx, s.store, viewRecordPrefix)
	if err != nil {
		note(err)
	}
	for _, m := range views {
		view := strings.TrimPrefix(m.ID, viewRecordPrefix)
		if known[view] {
			continue
		}
		var rec viewRecord
		if found, err := s.getJSON(ctx, m.ID, &rec); err != nil {
			note(err)
			continue
		} else if found && validID(rec.Doc) {
			if found, err := s.getJSON(ctx, docRecordPrefix+rec.Doc, &docMeta{}); err != nil {
				note(err)
				continue
			} else if found {
				continue
			}
		}
		del(m.ID)
	}
	s.sweepOld(ctx, tombPrefix, s.cfg.TombstoneTTL, func(raw []byte) (time.Time, reportInfo, bool) {
		var t tombstone
		ok := json.Unmarshal(raw, &t) == nil
		return t.At, reportInfo{}, ok
	}, del, note, nil)
	kept := s.sweepOld(ctx, docstore.ReportPrefix, s.cfg.ReportTTL, func(raw []byte) (time.Time, reportInfo, bool) {
		var r Report
		ok := json.Unmarshal(raw, &r) == nil
		return r.At, reportInfo{reporter: r.Reporter, doc: r.DocID, priority: r.Priority, at: r.At}, ok
	}, del, note, func(key string, info reportInfo) {
		// a report past its TTL leaves its document's record too
		s.dropRef(ctx, info.doc, strings.TrimPrefix(key, docstore.ReportPrefix))
	})
	if kept != nil {
		removed += s.evictReports(ctx, kept.metas, kept.info)
	}
	s.prio.prune(now)
	if err := s.recountReportsIn(ctx, repWin); err != nil {
		note(err)
	}
	repDone = true
	return removed, first
}

// swept is what sweepOld leaves.
type swept struct {
	metas []docstore.Meta
	info  map[string]reportInfo
}

// sweepOld deletes the records under prefix whose own timestamp is older than ttl; one that
// cannot be read as a record is deleted too once it is, by its adapter timestamp, that old. It
// returns the records that stay, with what at read from them (nil if the listing failed).
func (s *Service) sweepOld(ctx context.Context, prefix string, ttl time.Duration, at func([]byte) (time.Time, reportInfo, bool), del func(string) bool, note func(error), onDeleted func(key string, info reportInfo)) *swept {
	metas, err := docstore.ListPrefix(ctx, s.store, prefix)
	if err != nil {
		note(err)
		return nil
	}
	now := s.now()
	out := &swept{info: map[string]reportInfo{}}
	for _, m := range metas {
		d, err := s.store.Load(ctx, m.ID)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			note(err)
			out.metas = append(out.metas, m)
			continue
		}
		t, info, ok := at(d.Data)
		if !ok {
			t = d.UpdatedAt
		}
		if now.Sub(t) >= ttl {
			if del(m.ID) && onDeleted != nil {
				onDeleted(m.ID, info)
			}
			continue
		}
		out.metas = append(out.metas, m)
		out.info[m.ID] = info
	}
	return out
}

// Run sweeps now and then every interval until ctx is done (the first sweep reconciles the
// counters, so the byte budget is right from the start); errors go to the log.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	s.healthMu.Lock()
	s.sweepEvery = interval
	s.healthMu.Unlock()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
			log.Printf("public docstore: sweep: %s", redact(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
