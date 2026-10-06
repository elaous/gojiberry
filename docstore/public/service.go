package public

import (
	"context"
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
// View "view|<client>", Report "report|<client>", Admin "admin|<client>" (a generous default
// of 20 a minute: it is what slows down guessing the admin token).
type Limiters struct {
	Session, Write, WriteAddr, View, Report, Admin Limiter
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

	bytes   budget      // live bytes of public documents (Config.MaxTotalBytes)
	reports reportStats // stored abuse reports
	recMu   sync.Mutex  // serializes reconcile

	prefixOK bool // the store lists by prefix (else Sweep's listings are throttled)

	healthMu     sync.Mutex
	born         time.Time
	lastSweep    time.Time
	lastFull     time.Time
	sweepFailing bool
	sweepEvery   time.Duration
	noDeadline   atomic.Bool
}

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
	s := &Service{
		store: o.Store, cfg: cfg,
		sessions: o.Sessions, lim: o.Limiters, quota: o.Quota, admin: o.Admin,
		blocklist: o.Blocklist, challenge: o.Challenger, clientIP: o.ClientIP, now: o.Now,
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
	s.lim.Admin = rl(s.lim.Admin, "public-admin", 20, time.Minute)
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

// AdminHandler serves DELETE /admin/takedown/<id>.
func (s *Service) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/takedown/", s.handleTakedown)
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

// recordedBytes is the size the document's record says it has (0 if there is no record).
func (s *Service) recordedBytes(ctx context.Context, docID string) int64 {
	var meta docMeta
	if found, err := s.getJSON(ctx, docRecordPrefix+docID, &meta); err != nil || !found {
		return 0
	}
	return int64(meta.Bytes)
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
	sess := Session{ID: id, Client: s.clientKey(r), Created: now, Expires: now.Add(s.cfg.SessionIdleTTL)}
	switch err := s.sessions.Create(r.Context(), TokenKey(token), sess, now, s.cfg.MaxSessionsPerAddress); {
	case errors.Is(err, ErrClientSessionsFull):
		w.Header().Set("Retry-After", "900")
		http.Error(w, "too many live sessions from your address; finish or wait for the others to expire", http.StatusTooManyRequests)
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

// readBody reads the document body, refusing more than MaxDocBytes (413), an empty one (400) and
// one that arrives too slowly (408: the request's read deadline, see headers).
func (s *Service) readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.ContentLength > s.cfg.MaxDocBytes {
		http.Error(w, "document too large", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxDocBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "document too large", http.StatusRequestEntityTooLarge)
			return nil, false
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
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

// screen runs the content checks every write goes through.
func (s *Service) screen(w http.ResponseWriter, r *http.Request, data []byte) bool {
	bad, err := s.blocklist.Blocked(r.Context(), data)
	if err != nil {
		log.Printf("public docstore: blocklist: %v", err)
		http.Error(w, "content check unavailable", http.StatusServiceUnavailable)
		return false
	}
	if bad {
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
	data, ok := s.readBody(w, r)
	if !ok {
		return
	}
	tk.settle(int64(len(data)))
	if !s.screen(w, r, data) {
		return
	}
	n := int64(len(data))
	if !s.bytes.reserve(n, s.cfg.MaxTotalBytes) {
		storeFull(w)
		return
	}
	stored := false
	defer func() {
		if !stored {
			s.bytes.add(-n)
		}
	}()
	docID, err := randomString(16)
	if err != nil {
		s.fail(w, err)
		return
	}
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
		http.Error(w, "this document was removed", http.StatusGone)
		return
	}
	stored = true
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
	data, ok := s.readBody(w, r)
	if !ok {
		return
	}
	tk.settle(int64(len(data)))
	if !s.screen(w, r, data) {
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
	if !s.bytes.reserve(delta, s.cfg.MaxTotalBytes) {
		storeFull(w)
		return
	}
	stored := false
	defer func() {
		if !stored {
			s.bytes.add(-delta)
		}
	}()
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
		s.alarmNoDeadline()
		s.fail(w, errNoDeadline)
		return
	}
	stored = true
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
	size := s.recordedBytes(ctx, docID)
	if err := s.deleteKey(ctx, docKey(docID)); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.dropRecords(ctx, docID); err != nil {
		s.fail(w, err)
		return
	}
	s.bytes.add(-size)
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
)

// handleReport serves POST /public/report {"id": "<view id or document id>", "reason": "..."}.
// The reports are kept (report/<id>) for the operator, who takes a document down by ID. A
// document stops collecting reports after a few, so one link cannot fill the store.
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
		ID     string `json:"id"`
		Reason string `json:"reason"`
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxReportBody))
	if err != nil {
		http.Error(w, "request too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	if json.Unmarshal(data, &body) != nil || strings.TrimSpace(body.Reason) == "" || strings.TrimSpace(body.ID) == "" {
		http.Error(w, `expected {"id": "<view id>", "reason": "<why>"}`, http.StatusBadRequest)
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if len(reason) > maxReasonBytes {
		reason = strings.ToValidUTF8(reason[:maxReasonBytes], "")
	}
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
	reportID, err := randomString(16)
	if err != nil {
		s.fail(w, err)
		return
	}
	unlock := s.lock(docID)
	defer unlock()
	var meta docMeta
	if found, err := s.getJSON(ctx, docRecordPrefix+docID, &meta); err != nil {
		s.fail(w, err)
		return
	} else if found && meta.Reports < maxReportsOfDoc {
		rep := Report{ID: reportID, ViewID: view, DocID: docID, Reason: reason, At: s.now().UTC()}
		raw, _ := json.Marshal(rep)
		s.ensureCounters(ctx)
		if err := s.store.Save(ctx, docstore.ReportPrefix+reportID, raw); err != nil {
			s.fail(w, err)
			return
		}
		s.reports.add(1, int64(len(raw)))
		if s.reports.over(s.cfg.MaxReports, s.cfg.MaxReportBytes) {
			// Over the global bound: the oldest reports go, the new one stays.
			if metas, err := docstore.ListPrefix(ctx, s.store, docstore.ReportPrefix); err == nil {
				s.evictReports(context.WithoutCancel(ctx), metas)
			}
		}
		meta.Reports++
		if err := s.putJSON(ctx, docRecordPrefix+docID, meta); err != nil {
			s.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"reportId": reportID})
}

// ---- takedown

// handleTakedown serves DELETE /admin/takedown/<id> (a view ID or a document ID): the document,
// its view ID and its records are deleted and a tombstone is written, so the view ID answers 410
// from then on and the owner's session cannot create another document. The call is idempotent
// (a second one on a removed ID is 204 too). ?reason= is kept on the tombstone.
func (s *Service) handleTakedown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		methodNotAllowed(w, "DELETE")
		return
	}
	// The budget is charged before the token is looked at, so guessing it is slow too.
	if !s.allow(w, r, s.lim.Admin, "admin", "") {
		return
	}
	if s.admin == nil || !s.admin.Allow(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
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
	size := s.recordedBytes(ctx, docID)
	if err := s.deleteKey(ctx, docKey(docID)); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.dropRecords(ctx, docID); err != nil {
		s.fail(w, err)
		return
	}
	s.bytes.add(-size)
	w.WriteHeader(http.StatusNoContent)
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
	del := func(key string) {
		if err := s.deleteKey(ctx, key); err != nil {
			note(err)
			return
		}
		removed++
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

	s.recMu.Lock()
	s.bytes.begin()
	s.reports.begin()
	s.recMu.Unlock()
	liveMetas, err := docstore.ListPrefix(ctx, s.store, docKeyPrefix)
	if err != nil {
		return 0, err
	}
	live := make(map[string]bool, len(liveMetas))
	var liveBytes int64
	for _, m := range liveMetas {
		live[strings.TrimPrefix(m.ID, docKeyPrefix)] = true
		liveBytes += int64(m.Size)
	}
	if vs, err := docstore.ListPrefix(ctx, s.store, versionsKeyPrefix); err != nil {
		note(err)
	} else {
		for _, m := range vs {
			liveBytes += int64(m.Size)
		}
		s.bytes.set(liveBytes) // version snapshots count with their documents
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
	s.sweepOld(ctx, tombPrefix, s.cfg.TombstoneTTL, func(raw []byte) (time.Time, bool) {
		var t tombstone
		return t.At, json.Unmarshal(raw, &t) == nil
	}, del, note)
	kept := s.sweepOld(ctx, docstore.ReportPrefix, s.cfg.ReportTTL, func(raw []byte) (time.Time, bool) {
		var r Report
		return r.At, json.Unmarshal(raw, &r) == nil
	}, del, note)
	if kept != nil {
		removed += s.evictReports(ctx, kept.metas)
	}
	s.recountReports(ctx, note)
	return removed, first
}

// recountReports sets the report counters from a fresh listing.
func (s *Service) recountReports(ctx context.Context, note func(error)) {
	metas, err := docstore.ListPrefix(ctx, s.store, docstore.ReportPrefix)
	if err != nil {
		note(err)
		return
	}
	var b int64
	for _, m := range metas {
		b += int64(m.Size)
	}
	s.reports.set(len(metas), b)
}

// swept is what sweepOld leaves.
type swept struct{ metas []docstore.Meta }

// sweepOld deletes the records under prefix whose own timestamp is older than ttl; one that
// cannot be read as a record is deleted too once it is, by its adapter timestamp, that old. It
// returns the records that stay (nil if the listing failed).
func (s *Service) sweepOld(ctx context.Context, prefix string, ttl time.Duration, at func([]byte) (time.Time, bool), del func(string), note func(error)) *swept {
	metas, err := docstore.ListPrefix(ctx, s.store, prefix)
	if err != nil {
		note(err)
		return nil
	}
	now := s.now()
	out := &swept{}
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
		t, ok := at(d.Data)
		if !ok {
			t = d.UpdatedAt
		}
		if now.Sub(t) >= ttl {
			del(m.ID)
			continue
		}
		out.metas = append(out.metas, m)
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
