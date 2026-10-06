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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/auth/passkey"
	"github.com/elaous/gojiberry/docstore"
)

// Limiter limits requests per key ("<group>|<client>"); it is the passkey one, so the same
// implementations (a shared Redis budget, a gateway's decision, passkey.Unlimited) plug in here.
type Limiter = passkey.Limiter

// Limiters are the per-group request budgets. A nil one is a passkey.RateLimiter made from the
// Config's figures. Keys: Session "session|<ip>", Write "write|<user id>", View "view|<ip>",
// Report "report|<ip>", Admin "admin|<ip>" (a generous default of 20 a minute: it is what slows
// down guessing the admin token).
type Limiters struct {
	Session, Write, View, Report, Admin Limiter
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
	Sessions   SessionStore               // NewMemorySessionStore(Config.MaxSessions)
	Limiters   Limiters                   // passkey.RateLimiter from the Config
	Quota      Quota                      // NewMemoryQuota(Config.IPDailyBytes, 24h)
	Admin      AdminGuard                 // TokenAdmin(Config.AdminToken)
	Blocklist  Blocklist                  // NoBlocklist
	Challenger Challenger                 // NoChallenge
	ClientIP   func(*http.Request) string // passkey.TrustedProxies(Config.TrustedProxies...), else the remote address
	Now        func() time.Time           // time.Now; use the clock of the ExpiringAdapter in tests
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
	if s.sessions == nil {
		s.sessions = NewMemorySessionStore(cfg.MaxSessions)
	}
	rl := func(l Limiter, perMinute float64) Limiter {
		if l != nil {
			return l
		}
		r := passkey.NewRateLimiter(perMinute, burst(perMinute))
		r.Now = s.now
		return r
	}
	s.lim.Session = rl(s.lim.Session, cfg.SessionsPerMinute)
	s.lim.Write = rl(s.lim.Write, cfg.WritesPerMinute)
	s.lim.View = rl(s.lim.View, cfg.ViewsPerMinute)
	s.lim.Report = rl(s.lim.Report, cfg.ReportsPerMinute)
	s.lim.Admin = rl(s.lim.Admin, 20)
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

// Handler serves /public/session, /public/doc, /public/view/<id> and /public/report.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
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
// document), and the terms link.
func (s *Service) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
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
func (s *Service) live(ctx context.Context, docID string) (bool, error) {
	key := docKey(docID)
	metas, err := docstore.ListPrefix(ctx, s.store, key)
	if err != nil {
		return false, err
	}
	for _, m := range metas {
		if m.ID == key {
			return true, nil
		}
	}
	return false, nil
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

func (s *Service) fail(w http.ResponseWriter, err error) {
	log.Printf("public docstore: %v", err)
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

// allow asks the limiter for group's budget of the client of r (or of key, if not "").
func (s *Service) allow(w http.ResponseWriter, r *http.Request, l Limiter, group, who string) bool {
	if who == "" {
		who = s.clientIP(r)
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

func (s *Service) expiresOf(ctx context.Context, docID string) (time.Time, bool) {
	if x, ok := s.store.(docstore.Expirer); ok {
		if t, has, err := x.Expiry(ctx, docKey(docID)); err == nil && has {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
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
	sess := Session{ID: id, Created: now, Expires: now.Add(s.cfg.SessionTTL)}
	switch err := s.sessions.Create(r.Context(), TokenKey(token), sess, now); {
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

// readBody reads the document body, refusing more than MaxDocBytes (413) and an empty one (400).
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

// charge takes len(data) bytes from the client address's daily budget.
func (s *Service) charge(w http.ResponseWriter, r *http.Request, n int) bool {
	ok, retry, err := s.quota.Take(r.Context(), "ip:"+s.clientIP(r), int64(n))
	if err != nil {
		s.fail(w, err)
		return false
	}
	if !ok {
		tooMany(w, retry)
	}
	return ok
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
	if !s.allow(w, r, s.lim.Write, "write", sess.ID) {
		return
	}
	data, ok := s.readBody(w, r)
	if !ok || !s.screen(w, r, data) {
		return
	}
	// One document per session. A document that is gone (expired) frees the session; one that
	// was taken down does not: that session is done.
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
	if !s.charge(w, r, len(data)) {
		return
	}
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
	exp, has := s.expiresOf(ctx, docID)
	if !has {
		// The store did not give the document a deadline (a policy that skips it): never keep a
		// public document that would live for ever.
		rollback()
		s.fail(w, errors.New("a public document got no expiry; check the expiry policy"))
		return
	}
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
	if !s.allow(w, r, s.lim.Write, "write", sess.ID) {
		return
	}
	data, ok := s.readBody(w, r)
	if !ok || !s.screen(w, r, data) {
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
	if !s.charge(w, r, len(data)) {
		return
	}
	if err := s.store.Save(ctx, docKey(docID), data); err != nil {
		s.fail(w, err)
		return
	}
	meta.Versions++
	meta.Bytes = len(data)
	if err := s.putJSON(ctx, docRecordPrefix+docID, meta); err != nil {
		s.fail(w, err)
		return
	}
	s.renewSession(ctx, key, sess)
	s.setExpires(ctx, w, docID)
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
		if err := s.putJSON(ctx, docstore.ReportPrefix+reportID, rep); err != nil {
			s.fail(w, err)
			return
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
	docID, view, found, err := s.resolve(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	reason := strings.ToValidUTF8(r.URL.Query().Get("reason"), "")
	if len(reason) > 200 {
		reason = strings.ToValidUTF8(reason[:200], "")
	}
	unlock := s.lock(docID)
	defer unlock()
	// The tombstone first: from this write on the view ID answers 410, whatever fails below
	// (a retry finishes the deletion).
	if err := s.putJSON(ctx, tombPrefix+view, tombstone{At: s.now().UTC(), Reason: reason}); err != nil {
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
	w.WriteHeader(http.StatusNoContent)
}

// ---- sweep

// recordGrace is how long a document record may exist without its document before the sweep
// calls it an orphan: creation writes the records first.
const recordGrace = time.Minute

// Sweep removes what the ExpiringAdapter does not: the records of documents that expired or are
// gone (metadata and view-ID records, which are in a system keyspace and so never expire by
// themselves), and the reports and tombstones past their own TTL. It returns how many records it
// deleted. Run calls it on an interval.
func (s *Service) Sweep(ctx context.Context) (removed int, err error) {
	var first error
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

	liveMetas, err := docstore.ListPrefix(ctx, s.store, docKeyPrefix)
	if err != nil {
		return 0, err
	}
	live := make(map[string]bool, len(liveMetas))
	for _, m := range liveMetas {
		live[strings.TrimPrefix(m.ID, docKeyPrefix)] = true
	}
	docs, err := docstore.ListPrefix(ctx, s.store, docRecordPrefix)
	if err != nil {
		return 0, err
	}
	known := map[string]bool{} // views of document records that stay
	for _, m := range docs {
		docID := strings.TrimPrefix(m.ID, docRecordPrefix)
		var meta docMeta
		found, err := s.getJSON(ctx, m.ID, &meta)
		if err != nil {
			note(err)
			continue
		}
		if !found || live[docID] || now.Sub(meta.Created) < recordGrace {
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
	s.sweepOld(ctx, docstore.ReportPrefix, s.cfg.ReportTTL, func(raw []byte) (time.Time, bool) {
		var r Report
		return r.At, json.Unmarshal(raw, &r) == nil
	}, del, note)
	return removed, first
}

// sweepOld deletes the records under prefix whose own timestamp is older than ttl; one that
// cannot be read as a record is deleted too once it is, by its adapter timestamp, that old.
func (s *Service) sweepOld(ctx context.Context, prefix string, ttl time.Duration, at func([]byte) (time.Time, bool), del func(string), note func(error)) {
	metas, err := docstore.ListPrefix(ctx, s.store, prefix)
	if err != nil {
		note(err)
		return
	}
	now := s.now()
	for _, m := range metas {
		d, err := s.store.Load(ctx, m.ID)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			note(err)
			continue
		}
		t, ok := at(d.Data)
		if !ok {
			t = d.UpdatedAt
		}
		if now.Sub(t) >= ttl {
			del(m.ID)
		}
	}
}

// Run sweeps every interval until ctx is done; errors go to the log.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.Sweep(ctx); err != nil {
				log.Printf("public docstore: sweep: %v", err)
			}
		}
	}
}
