package docstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/audit"
	"github.com/elaous/gojiberry/guard"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/observe"
	"github.com/elaous/gojiberry/telemetry"
)

// Config holds the server settings, loaded from the environment.
type Config struct {
	// Guard decides who may call the server.
	Guard guard.Guard
	// RequireACL closes documents that have no access list.
	RequireACL bool
	// ExpiryTTL, when positive, makes every document expire.
	ExpiryTTL   time.Duration
	ExpirySweep time.Duration
	// ExpiryAdoptUpdated sets the adopt-update-time setting.
	ExpiryAdoptUpdated bool
	// Limiter chooses where rate limits are kept.
	Limiter string
	// ExpiryOrphans sets the orphan sweep setting.
	ExpiryOrphans     bool
	ExpiryOrphanGrace time.Duration
	// ExpiryMaxAge sets the absolute maximum document age.
	ExpiryMaxAge  time.Duration
	Addr          string
	Backend       string
	BackendConfig json.RawMessage
	Auth          AuthConfig
}

// AuthConfig configures optional sign-in.
type AuthConfig struct {
	BaseURL    string
	Providers  []auth.Provider
	SessionTTL time.Duration
	// ReturnOrigins are the extra origins a login may return to.
	ReturnOrigins []string
}

// ConfigFromEnv reads the server settings from the environment.
func ConfigFromEnv() Config {
	cfg := Config{
		Guard:         guard.FromEnvWith("GOJIBERRY_DOCSTORE", guard.Options{Public: []string{"/health"}, TokenHeader: "X-Gojiberry-Token", Headers: "Content-Type, Authorization, " + ShareHeader}),
		Addr:          envOr("GOJIBERRY_DOCSTORE_ADDR", ":8090"),
		Backend:       envOr("GOJIBERRY_DOCSTORE_BACKEND", "memory"),
		BackendConfig: json.RawMessage(os.Getenv("GOJIBERRY_DOCSTORE_BACKEND_CONFIG")),
		Auth: AuthConfig{
			BaseURL: os.Getenv("GOJIBERRY_DOCSTORE_AUTH_BASE_URL"),
		},
	}
	if raw := os.Getenv("GOJIBERRY_DOCSTORE_AUTH_PROVIDERS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.Auth.Providers); err != nil {
			log.Fatalf("docstore: parsing GOJIBERRY_DOCSTORE_AUTH_PROVIDERS: %v", err)
		}
	}
	if raw := os.Getenv("GOJIBERRY_DOCSTORE_AUTH_SESSION_TTL"); raw != "" {
		ttl, err := time.ParseDuration(raw)
		if err != nil {
			log.Fatalf("docstore: parsing GOJIBERRY_DOCSTORE_AUTH_SESSION_TTL: %v", err)
		}
		cfg.Auth.SessionTTL = ttl
	}
	cfg.RequireACL = os.Getenv("GOJIBERRY_DOCSTORE_REQUIRE_ACL") == "true"
	if raw := os.Getenv("GOJIBERRY_DOCSTORE_AUTH_RETURN_ORIGINS"); raw != "" {
		for _, o := range strings.Split(raw, ",") {
			if o = strings.TrimSpace(o); o != "" {
				cfg.Auth.ReturnOrigins = append(cfg.Auth.ReturnOrigins, o)
			}
		}
	}
	var err error
	if cfg.ExpiryTTL, cfg.ExpirySweep, err = ParseExpiryEnv(os.Getenv("GOJIBERRY_DOCSTORE_EXPIRY_TTL"), os.Getenv("GOJIBERRY_DOCSTORE_EXPIRY_SWEEP")); err != nil {
		log.Fatalf("docstore: %v", err)
	}
	if cfg.ExpiryAdoptUpdated, err = ParseExpiryAdopt(os.Getenv("GOJIBERRY_DOCSTORE_EXPIRY_ADOPT")); err != nil {
		log.Fatalf("docstore: %v", err)
	}
	if cfg.Limiter, err = ParseLimiterMode(os.Getenv("GOJIBERRY_DOCSTORE_LIMITER")); err != nil {
		log.Fatalf("docstore: %v", err)
	}
	publicOn := publicOfferOn(os.Getenv("GOJIBERRY_DOCSTORE_PUBLIC"))
	if cfg.ExpiryOrphans, cfg.ExpiryOrphanGrace, err = ParseExpiryOrphansDefault(os.Getenv("GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS"), os.Getenv("GOJIBERRY_DOCSTORE_EXPIRY_ORPHAN_GRACE"), publicOn); err != nil {
		log.Fatalf("docstore: %v", err)
	}
	if publicOn && !cfg.ExpiryOrphans {
		log.Print("docstore: WARNING: public mode with GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS=off: hub-only shared-editing state ('collab:<id>') of documents that never reached the store is never deleted, against the retention promise")
	}
	if raw := strings.TrimSpace(os.Getenv("GOJIBERRY_DOCSTORE_EXPIRY_MAX_AGE")); raw != "" {
		if cfg.ExpiryMaxAge, err = time.ParseDuration(raw); err != nil || cfg.ExpiryMaxAge <= 0 {
			log.Fatalf("docstore: parsing GOJIBERRY_DOCSTORE_EXPIRY_MAX_AGE %q: want a positive duration such as 168h", raw)
		}
	}
	return cfg
}

// ParseExpiryOrphans parses the orphan setting.
func ParseExpiryOrphans(raw, graceRaw string) (on bool, grace time.Duration, err error) {
	return ParseExpiryOrphansDefault(raw, graceRaw, false)
}

// ParseExpiryOrphansDefault is ParseExpiryOrphans with a default.
func ParseExpiryOrphansDefault(raw, graceRaw string, defaultOn bool) (on bool, grace time.Duration, err error) {
	raw, graceRaw = strings.ToLower(strings.TrimSpace(raw)), strings.TrimSpace(graceRaw)
	switch raw {
	case "":
		if !defaultOn {
			if graceRaw != "" {
				return false, 0, errors.New("GOJIBERRY_DOCSTORE_EXPIRY_ORPHAN_GRACE needs GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS=collab")
			}
			return false, 0, nil
		}
	case "off", "none", "false":
		if graceRaw != "" {
			return false, 0, errors.New("GOJIBERRY_DOCSTORE_EXPIRY_ORPHAN_GRACE needs GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS=collab")
		}
		return false, 0, nil
	case "collab":
	default:
		return false, 0, fmt.Errorf("parsing GOJIBERRY_DOCSTORE_EXPIRY_ORPHANS %q: want \"collab\" or \"off\" (or empty: off, on in public mode)", raw)
	}
	if graceRaw == "" {
		return true, DefaultOrphanGrace, nil
	}
	if grace, err = time.ParseDuration(graceRaw); err != nil || grace <= 0 {
		return false, 0, fmt.Errorf("parsing GOJIBERRY_DOCSTORE_EXPIRY_ORPHAN_GRACE %q: want a positive duration such as 1h", graceRaw)
	}
	return true, grace, nil
}

func publicOfferOn(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "on":
		return true
	}
	return false
}

// ParseExpiryAdopt parses the adopt-update-time setting.
func ParseExpiryAdopt(raw string) (updated bool, err error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "grace":
		return false, nil
	case "updated":
		return true, nil
	}
	return false, fmt.Errorf("parsing GOJIBERRY_DOCSTORE_EXPIRY_ADOPT %q: want \"updated\" (or empty for a full grace period)", raw)
}

// DefaultExpirySweep is how often expired documents are swept.
const DefaultExpirySweep = 5 * time.Minute

// ParseExpiryEnv parses an expiry duration setting.
func ParseExpiryEnv(ttlRaw, sweepRaw string) (ttl, sweep time.Duration, err error) {
	ttlRaw, sweepRaw = strings.TrimSpace(ttlRaw), strings.TrimSpace(sweepRaw)
	if ttlRaw == "" {
		if sweepRaw != "" {
			return 0, 0, errors.New("GOJIBERRY_DOCSTORE_EXPIRY_SWEEP needs GOJIBERRY_DOCSTORE_EXPIRY_TTL")
		}
		return 0, 0, nil
	}
	if ttl, err = time.ParseDuration(ttlRaw); err != nil || ttl <= 0 {
		return 0, 0, fmt.Errorf("parsing GOJIBERRY_DOCSTORE_EXPIRY_TTL %q: want a positive duration such as 24h", ttlRaw)
	}
	if sweepRaw == "" {
		return ttl, min(DefaultExpirySweep, ttl), nil
	}
	if sweep, err = time.ParseDuration(sweepRaw); err != nil || sweep <= 0 {
		return 0, 0, fmt.Errorf("parsing GOJIBERRY_DOCSTORE_EXPIRY_SWEEP %q: want a positive duration such as 5m", sweepRaw)
	}
	return ttl, sweep, nil
}

const maxDocBytes = 64 << 20

func serverError(w http.ResponseWriter, err error) {
	log.Printf("docstore: %v", err)
	http.Error(w, "storage error", http.StatusInternalServerError)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Server exposes an Adapter over HTTP.
type Server struct {
	adapter    Adapter
	backend    string
	auth       *auth.Manager
	guard      guard.Guard
	requireACL bool
	extra      []extraRoute
	hidden     []string // lowercased (Hide)
	now        func() time.Time
	aclMu      sync.Mutex
	telemetry  *telemetry.Counters
	obs        *observe.Observer

	auditLog        *audit.Logger
	auditKey        []byte
	usage           usageSeries
	usageOnce       sync.Once   // the stored series is read into usage on first use
	sampling        atomic.Bool // RunUsageSampling is running (a second call returns at once)
	usageClock      func() time.Time
	docsClosed      bool
	directory       DirectoryService
	dirLimitFactory LimiterFactory
	dirLimitOnce    sync.Once
	dirLimits       *directoryLimits
	dirPolicy       DirectoryPolicy
	dirDomainBudget bool
	dirAdmin        func(*http.Request) bool
	dirClientIP     func(*http.Request) string
}

type extraRoute struct {
	pattern string
	h       http.Handler
}

// NewServer wraps an already-constructed Adapter.
func NewServer(adapter Adapter, backendName string) *Server {
	return &Server{adapter: adapter, backend: backendName}
}

// WithAuth attaches a built auth.Manager.
func (s *Server) WithAuth(m *auth.Manager) *Server {
	s.auth = m
	return s
}

// UserKey identifies the signed-in person of a request.
func (s *Server) UserKey(r *http.Request) (key string, ok bool) {
	if s.auth == nil {
		return "", false
	}
	me, signedIn := s.auth.CurrentUser(r)
	if !signedIn || ownerKey(me) == "" {
		return "", false
	}
	return NormalizeAddress(ownerKey(me)), true
}

// WithGuard sets the access policy: CORS and origin checks.
func (s *Server) WithGuard(g guard.Guard) *Server {
	s.guard = g
	return s
}

// WithRequireACL makes a document without an ACL refused.
func (s *Server) WithRequireACL(on bool) *Server {
	s.requireACL = on
	return s
}

// WithExpiry makes the server serve expiring documents.
func (s *Server) WithExpiry(e *ExpiringAdapter) *Server {
	s.adapter = e
	return s
}

// RFC 3339, UTC
func (s *Server) setExpires(w http.ResponseWriter, r *http.Request, id string) {
	if x, ok := s.adapter.(Expirer); ok {
		if t, has, err := x.Expiry(r.Context(), id); err == nil && has {
			w.Header().Set("X-Gojiberry-Expires", t.UTC().Format(time.RFC3339))
		}
	}
}

type listItem struct {
	Meta
	Expires *time.Time `json:"expires,omitempty"`
}

// WithRoutes mounts another handler on the server.
func (s *Server) WithRoutes(pattern string, h http.Handler) *Server {
	s.extra = append(s.extra, extraRoute{pattern, h})
	return s
}

// Hide keeps ids that start with prefix away from listings.
func (s *Server) Hide(prefix string) *Server {
	s.hidden = append(s.hidden, strings.ToLower(prefix))
	return s
}

// CloseDocs shuts the generic document API.
func (s *Server) CloseDocs() *Server {
	s.docsClosed = true
	return s
}

// DocsClosed reports whether the generic document API is shut.
func (s *Server) DocsClosed() bool {
	return s.docsClosed || (s.requireACL && s.auth != nil)
}

func (s *Server) reserved(id string) bool {
	if isReservedDocID(id) {
		return true
	}
	lower := strings.ToLower(id)
	for _, p := range s.hidden {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// NewAuthManager builds an auth.Manager from the config.
func NewAuthManager(ctx context.Context, cfg AuthConfig) (*auth.Manager, error) {
	if len(cfg.Providers) == 0 {
		return nil, nil
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("docstore: AuthConfig.BaseURL is required when Providers is set")
	}
	return auth.NewManager(ctx, auth.Config{
		BaseURL:       cfg.BaseURL,
		Providers:     cfg.Providers,
		SessionTTL:    cfg.SessionTTL,
		ReturnOrigins: cfg.ReturnOrigins,
	})
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ready", s.handleReady)
	if s.docsClosed {
		closed := http.NotFoundHandler()
		mux.Handle("/quota", closed)
		mux.Handle("/docs", closed)
		mux.Handle("/docs/", closed)
	} else {
		mux.HandleFunc("/quota", s.handleQuota)
		mux.HandleFunc("/docs", s.handleList)
		mux.HandleFunc("/docs/", s.handleDoc)
	}
	if s.dirAdmin != nil {
		mux.HandleFunc("/admin/usage", s.handleUsage)
		mux.HandleFunc("/admin/usage/history", s.handleUsageHistory)
		mux.HandleFunc("/admin/usage/page", s.handleUsagePage)
		mux.HandleFunc("/admin/observe", s.handleObserve)
		if s.telemetry != nil {
			mux.HandleFunc("/admin/telemetry", s.handleTelemetryReport)
		}
	}
	if s.auth != nil {
		mux.Handle("/auth/", s.auth.Routes())
	}
	if s.auth != nil && s.directory != nil {
		mux.HandleFunc("/directory/challenge", s.handleChallenge)
		mux.HandleFunc("/directory/publish", s.handlePublish)
		mux.HandleFunc("/directory/keys/", s.handleRevoke)
		mux.HandleFunc("/directory/lookup", s.handleLookup)
		mux.HandleFunc("/directory/log", s.handleLog)
		mux.HandleFunc("/directory/log/head", s.handleLogHead)
		mux.HandleFunc("/directory/log/address", s.handleLogAddress)
		if _, ok := s.directory.(DirectoryWitnessing); ok {
			mux.HandleFunc("/directory/log/witness", s.handleWitness)
			mux.HandleFunc("/directory/log/cosigned", s.handleCosigned)
		}
		mux.HandleFunc("/directory/enroll", s.handleEnrollRoot)
		mux.HandleFunc("/directory/enroll/", s.handleEnrollSub)
		if s.dirAdmin != nil {
			mux.HandleFunc("/admin/directory/reset-enrollment/", s.handleOperatorReset)
			mux.HandleFunc("/admin/directory/unmapped", s.handleUnmapped)
		}
	}
	for _, e := range s.extra {
		mux.Handle(e.pattern, e.h)
	}
	return observe.Middleware(s.obs, guard.Or(s.guard).Wrap(s.auditSignIns(exposeHeaders(mux))))
}

// ExposedHeaders is the Access-Control-Expose-Headers value.
const ExposedHeaders = "X-Gojiberry-Expires, Retry-After, Link, X-Gojiberry-View-Id"

func exposeHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Expose-Headers", ExposedHeaders)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	out := map[string]any{"status": "ok", "backend": s.backend}
	if h, ok := s.directory.(DirectoryHealther); ok {
		out["directory"] = h.DirectoryHealth()
	}
	_ = json.NewEncoder(w).Encode(out)
}

const readyTimeout = 3 * time.Second

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
	defer cancel()
	ready := true
	if _, err := s.adapter.List(ctx); err != nil {
		log.Printf("docstore: not ready: %v", err)
		ready = false
		s.obs.Error("not_ready")
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if !ready {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"ready": ready})
}

// counts and sizes only: never an id, a name or a content
type usageResponse struct {
	Documents int           `json:"documents"`
	Records   int           `json:"records"`
	Bytes     int64         `json:"bytes"`
	Largest   int           `json:"largest"`
	Quota     quotaResponse `json:"quota"`
	// Telemetry is how many usage batches were received.
	Telemetry *telemetry.Totals `json:"telemetry,omitempty"`
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.dirAdmin(r) {
		lim := s.limits().adminFail
		if ok, wait := lim.Allow(r.Context(), "ad:"+s.clientAddr(r)); !ok {
			tooMany(w, wait)
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	out, err := s.measureUsage(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
}

type quotaResponse struct {
	Available bool  `json:"available"`
	Used      int64 `json:"used,omitempty"`
	Total     int64 `json:"total,omitempty"`
}

func (s *Server) handleQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	reporter, ok := s.adapter.(QuotaReporter)
	if !ok {
		_ = json.NewEncoder(w).Encode(quotaResponse{Available: false})
		return
	}
	q, err := reporter.Usage(r.Context())
	if err != nil {
		_ = json.NewEncoder(w).Encode(quotaResponse{Available: false})
		return
	}
	_ = json.NewEncoder(w).Encode(quotaResponse{Available: true, Used: q.Used, Total: q.Total})
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	done := s.opStart(r.Context(), "list")
	metas, err := s.adapter.List(r.Context())
	done(err, len(metas))
	if err != nil {
		serverError(w, err)
		return
	}
	// the reserved ACL keyspace never appears in a listing
	var deadlines map[string]time.Time
	if x, ok := s.adapter.(Expirer); ok {
		deadlines, _ = x.Expiries(r.Context())
	}
	visible := make([]listItem, 0, len(metas))
	for _, m := range metas {
		if s.reserved(m.ID) {
			continue
		}
		if allowed, _ := s.checkPermission(r, m.ID, http.MethodGet); !allowed {
			continue
		}
		item := listItem{Meta: m}
		if t, ok := deadlines[m.ID]; ok {
			t = t.UTC()
			item.Expires = &t
		}
		visible = append(visible, item)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(visible)
}

func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	if idEndsInRoute(r) {
		http.Error(w, "a document id may not end in /renew or /acl (those paths are routes)", http.StatusBadRequest)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/docs/")
	if id, ok := strings.CutSuffix(path, "/renew"); ok {
		s.handleRenew(w, r, id)
		return
	}
	if id, ok := strings.CutSuffix(path, "/acl"); ok {
		if id == "" || s.reserved(id) {
			http.Error(w, "missing or invalid document id", http.StatusBadRequest)
			return
		}
		s.handleACL(w, r, id)
		return
	}
	id := path
	if id == "" || s.reserved(id) {
		http.Error(w, "missing or invalid document id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPut:
		if allowed, status := s.checkPermission(r, id, http.MethodPut); !allowed {
			writePermissionError(w, status)
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDocBytes))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				http.Error(w, "document too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "reading the request body failed", http.StatusBadRequest)
			return
		}
		var saveErr error
		if match, none := r.Header.Get("If-Match"), r.Header.Get("If-None-Match"); match != "" || none != "" {
			cond, ok := s.adapter.(ConditionalAdapter)
			if !ok {
				http.Error(w, "conditional save is not supported", http.StatusNotImplemented)
				return
			}
			// If-None-Match: * means only if none; If-Match carries a digest
			prev := strings.Trim(match, `"`)
			if none != "" {
				if strings.TrimSpace(none) != "*" || match != "" {
					http.Error(w, "unsupported precondition", http.StatusBadRequest)
					return
				}
				prev = ""
			} else if prev == "" || prev == "*" {
				http.Error(w, "unsupported precondition", http.StatusBadRequest)
				return
			}
			done := s.opStart(r.Context(), "save_if")
			saveErr = cond.SaveIf(r.Context(), id, data, prev)
			done(saveErr, -1)
		} else {
			done := s.opStart(r.Context(), "save")
			saveErr = s.adapter.Save(r.Context(), id, data)
			done(saveErr, -1)
		}
		if errors.Is(saveErr, ErrConflict) {
			http.Error(w, "the document changed", http.StatusPreconditionFailed)
			return
		}
		if errors.Is(saveErr, ErrConditionalUnsupported) {
			http.Error(w, "conditional save is not supported", http.StatusNotImplemented)
			return
		}
		if saveErr != nil {
			serverError(w, saveErr)
			return
		}
		s.setExpires(w, r, id)
		if strings.TrimSpace(r.Header.Get("If-None-Match")) == "*" {
			s.audit(audit.DocumentCreated, audit.OK, s.auditActor(r, id), audit.NoReason, 0, id)
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		if allowed, status := s.checkPermission(r, id, http.MethodGet); !allowed {
			writePermissionError(w, status)
			return
		}
		done := s.opStart(r.Context(), "load")
		doc, err := s.adapter.Load(r.Context(), id)
		done(err, -1)
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		s.setExpires(w, r, id)
		_, _ = w.Write(doc.Data)
	case http.MethodDelete:
		if allowed, status := s.checkPermission(r, id, http.MethodDelete); !allowed {
			writePermissionError(w, status)
			return
		}
		actor := s.auditActor(r, id)
		done := s.opStart(r.Context(), "delete")
		err := s.adapter.Delete(r.Context(), id)
		done(err, -1)
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			s.audit(audit.DocumentDeleted, audit.Failed, actor, audit.Unavailable, 0, id)
			serverError(w, err)
			return
		}
		s.audit(audit.DocumentDeleted, audit.OK, actor, audit.NoReason, 0, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func idEndsInRoute(r *http.Request) bool {
	ep := strings.ToLower(r.URL.EscapedPath())
	return strings.HasSuffix(ep, "%2frenew") || strings.HasSuffix(ep, "%2facl")
}

func (s *Server) handleRenew(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" || s.reserved(id) {
		http.Error(w, "missing or invalid document id", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if allowed, status := s.checkPermission(r, id, methodRenew); !allowed {
		writePermissionError(w, status)
		return
	}
	rn, ok := s.adapter.(Renewer)
	if !ok {
		http.Error(w, "this store does not expire documents", http.StatusNotImplemented)
		return
	}
	done := s.opStart(r.Context(), "renew")
	err := rn.Renew(r.Context(), id)
	done(err, -1)
	switch {
	case errors.Is(err, ErrNotFound):
		w.Header().Set(GoneHeader, "1")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"gone"}`))
	case err != nil:
		serverError(w, err)
	default:
		s.setExpires(w, r, id)
		w.WriteHeader(http.StatusNoContent)
	}
}
