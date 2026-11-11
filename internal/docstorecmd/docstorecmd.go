package docstorecmd

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/elaous/gojiberry/a11ycheck"
	"github.com/elaous/gojiberry/audit"
	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/auth/passkey"
	"github.com/elaous/gojiberry/auth/sessionstore"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/docstore/exportgate"
	"github.com/elaous/gojiberry/docstore/public"
	"github.com/elaous/gojiberry/observe"
	"github.com/elaous/gojiberry/spell/dictserver"
	"github.com/elaous/gojiberry/telemetry"
	"github.com/elaous/gojiberry/userconfig"
)

func withPublic(srv *docstore.Server, adapter docstore.Adapter, expiryTTL time.Duration, limiters docstore.LimiterFactory) (*public.Service, error) {
	cfg, err := public.FromEnv(os.Getenv)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, nil
	}
	if expiryTTL <= 0 {
		return nil, errors.New("GOJIBERRY_DOCSTORE_PUBLIC needs GOJIBERRY_DOCSTORE_EXPIRY_TTL: the public offer promises that documents are deleted after the last edit")
	}
	quota, err := publicQuota(context.Background(), adapter, cfg.IPDailyBytes, os.Getenv("GOJIBERRY_DOCSTORE_PUBLIC_QUOTA"))
	if err != nil {
		return nil, err
	}
	sessions, err := publicSessions(context.Background(), adapter, cfg.MaxSessions, os.Getenv("GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS"))
	if err != nil {
		return nil, err
	}
	prio, err := publicPriorityQuota(context.Background(), adapter, cfg.MaxPriorityReportsPerReporter, os.Getenv("GOJIBERRY_DOCSTORE_PUBLIC_REPORT_COUNTERS"))
	if err != nil {
		return nil, err
	}
	var notify func(public.Report)
	if hook := os.Getenv("GOJIBERRY_DOCSTORE_PUBLIC_REPORT_WEBHOOK"); hook != "" {
		if notify, err = public.WebhookNotify(hook); err != nil {
			return nil, err
		}
	}
	var blocklist public.Blocklist
	if os.Getenv("GOJIBERRY_DOCSTORE_PUBLIC_ONLY_DOCUMENTS") == "true" {
		blocklist = public.DocumentsOnly{}
	}
	var svc *public.Service
	challenger, err := publicChallenger(func() bool { return svc != nil && svc.Busy() })
	if err != nil {
		return nil, err
	}
	svc, err = public.New(public.Options{Blocklist: blocklist, Challenger: challenger, Notify: notify, Store: adapter, Config: cfg, LimiterFactory: limiters, Quota: quota, Sessions: sessions, PriorityQuota: prio, SharedBurstWatch: prio != nil})
	if err != nil {
		return nil, err
	}
	switch {
	case srv.DocsClosed():
		log.Print("docstore: public offer: the generic /docs API is protected by required ACLs with sign-in")
	case cfg.AllowOpenDocs:
		log.Print("docstore: WARNING: GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_OPEN_DOCS=true: the generic /docs API stays OPEN to anonymous readers and writers beside the public offer; anyone can read, overwrite and delete every document that has no ACL")
	default:
		srv.CloseDocs()
		log.Print("docstore: public offer: the generic /docs API is closed (404); configure sign-in with GOJIBERRY_DOCSTORE_REQUIRE_ACL=true to keep it, or set GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_OPEN_DOCS=true to leave it open")
	}
	if err := public.RequireDocsClosed(srv, cfg.AllowOpenDocs); err != nil {
		return nil, err
	}
	for _, w := range cfg.Warnings() {
		log.Printf("docstore: WARNING: %s", w)
	}
	svc.Mount(srv)
	log.Printf("docstore: public offer enabled (takedown route %t, terms link %t)", cfg.AdminToken != "", cfg.TermsURL != "")
	return svc, nil
}

const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 5 * time.Minute
	idleTimeout       = 2 * time.Minute
)

func withTelemetry(srv *docstore.Server, value string) bool {
	if strings.ToLower(strings.TrimSpace(value)) != "on" {
		return false
	}
	srv.WithTelemetry(telemetry.NewCounters())
	log.Print("docstore: anonymous usage counts are accepted at /telemetry (see docs/telemetry.md)")
	return true
}

func withDictionaries(srv *docstore.Server, dir string) (bool, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false, nil
	}
	fsys := os.DirFS(dir)
	d := &dictserver.Server{FS: fsys}
	langs, err := d.Languages()
	if err != nil {
		return false, fmt.Errorf("reading GOJIBERRY_DOCSTORE_DICTIONARIES %q: %w", dir, err)
	}
	srv.WithRoutes("/dict/", http.StripPrefix("/dict", d))
	log.Printf("docstore: %d spelling dictionaries are served at /dict/ from %s", len(langs), dir)
	return true, nil
}

func exportGate(value string) (a11ycheck.GateMode, error) {
	m, ok := a11ycheck.ParseGateMode(value)
	if !ok {
		return a11ycheck.GateAllow, fmt.Errorf("parsing GOJIBERRY_DOCSTORE_EXPORT_GATE %q: want \"allow\", \"warn\" or \"block\"", value)
	}
	return m, nil
}

func withTelemetryNoise(srv *docstore.Server, value string) error {
	n, err := telemetry.ParseNoise(value)
	if err != nil {
		return fmt.Errorf("GOJIBERRY_DOCSTORE_TELEMETRY_NOISE %q: %w", value, err)
	}
	if n == 0 {
		return nil
	}
	if err := srv.WithTelemetryNoise(n); err != nil {
		return err
	}
	log.Printf("docstore: usage totals are reported rounded to multiples of %d", n)
	return nil
}

func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: readHeaderTimeout, ReadTimeout: readTimeout, IdleTimeout: idleTimeout}
}

func withDirectory(ctx context.Context, srv *docstore.Server, adapter docstore.Adapter, limiters docstore.LimiterFactory) error {
	signer, err := encrypt.DirectorySignerFromEnv()
	if err != nil {
		return err
	}
	if signer == nil {
		return nil
	}
	dir := &encrypt.AdapterDirectory{Store: adapter, Signer: signer}
	dir.RequireProof = os.Getenv("GOJIBERRY_DOCSTORE_DIRECTORY_REQUIRE_POP") == "true"
	if v := strings.TrimSpace(os.Getenv("GOJIBERRY_DOCSTORE_DIRECTORY_WITNESSES")); v != "" {
		if dir.Witnesses, err = parseWitnesses(v); err != nil {
			return err
		}
	}
	dir.RequireEnrollment = os.Getenv("GOJIBERRY_DOCSTORE_DIRECTORY_REQUIRE_ENROLLMENT") == "true"
	if v := strings.TrimSpace(os.Getenv("GOJIBERRY_DOCSTORE_DIRECTORY_REVOKE_WINDOW")); v != "" {
		w, err := time.ParseDuration(v)
		if err != nil || w <= 0 {
			return fmt.Errorf("parsing GOJIBERRY_DOCSTORE_DIRECTORY_REVOKE_WINDOW %q: want a positive duration such as \"48h\"", v)
		}
		dir.RevokeWindow = w
	}
	replicas := 1
	replicasSet := false
	if v := strings.TrimSpace(os.Getenv("GOJIBERRY_DOCSTORE_REPLICAS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fmt.Errorf("parsing GOJIBERRY_DOCSTORE_REPLICAS %q: want a positive whole number", v)
		}
		replicas, replicasSet = n, true
	}
	policy, err := docstore.ParseDirectoryLookupMode(os.Getenv("GOJIBERRY_DOCSTORE_DIRECTORY_LOOKUP"))
	if err != nil {
		return err
	}
	perDomain := false
	switch m := strings.ToLower(strings.TrimSpace(os.Getenv("GOJIBERRY_DOCSTORE_DIRECTORY_GLOBAL_BUDGET"))); m {
	case "", "all":
	case "domain":
		perDomain = true
	default:
		return fmt.Errorf("parsing GOJIBERRY_DOCSTORE_DIRECTORY_GLOBAL_BUDGET %q: want \"all\" or \"domain\"", m)
	}
	if err := dir.CheckStore(ctx, replicas); err != nil {
		return err
	}
	checkWriters(ctx, dir, adapter, replicas, replicasSet)
	rep, err := dir.Startup(ctx)
	if err != nil {
		return fmt.Errorf("key directory start-up check: %w", err)
	}
	log.Printf("docstore: key directory: %s", rep.Summary())
	if rep.Unknown > 0 {
		log.Printf("docstore: WARNING: %d directory records are named under unknown keys: check GOJIBERRY_DOCSTORE_DIRECTORY_KEY and GOJIBERRY_DOCSTORE_DIRECTORY_KEY_PREV; those keys cannot be found by a lookup", rep.Unknown)
	}
	if tok := strings.TrimSpace(os.Getenv("GOJIBERRY_DOCSTORE_DIRECTORY_ADMIN_TOKEN")); tok != "" {
		if len(tok) < 16 {
			return errors.New("GOJIBERRY_DOCSTORE_DIRECTORY_ADMIN_TOKEN must be at least 16 characters")
		}
		srv.WithDirectoryAdmin(adminToken(tok))
		go srv.RunUsageSampling(ctx, time.Hour) // the usage history (GET /admin/usage/history, /admin/usage/page)
	}
	if px := strings.TrimSpace(os.Getenv("GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES")); px != "" {
		srv.WithDirectoryClientIP(passkey.TrustedProxies(strings.Split(px, ",")...))
	}
	srv.WithDirectory(encrypt.DirectoryService(dir)).WithDirectoryPolicy(policy).WithDirectoryLimits(limiters).WithDirectoryGlobalPerDomain(perDomain)
	go dir.RunSweep(ctx, time.Hour)
	go dir.RunWriterWatch(ctx, replicas)
	log.Printf("docstore: key directory enabled (proof of possession required: %v, owner key required: %v)", dir.RequireProof, dir.RequireEnrollment)
	return nil
}

// compared in constant time
func adminToken(tok string) func(*http.Request) bool {
	want := sha256.Sum256([]byte(tok))
	return func(r *http.Request) bool {
		got := sha256.Sum256([]byte(r.Header.Get("X-Gojiberry-Admin-Token")))
		return subtle.ConstantTimeCompare(got[:], want[:]) == 1
	}
}

func checkWriters(ctx context.Context, dir *encrypt.AdapterDirectory, adapter docstore.Adapter, replicas int, replicasSet bool) {
	n, err := dir.Writers(ctx)
	if err != nil {
		log.Printf("docstore: WARNING: could not look for other server processes writing the key directory: %v", err)
	}
	conditional := docstore.SupportsConditional(ctx, adapter)
	switch {
	case n > 0 && replicas <= 1:
		log.Printf("docstore: ERROR: %d other server process(es) have written to this key directory in the last minutes, and GOJIBERRY_DOCSTORE_REPLICAS says %d (a restart within that time looks the same): if several processes share this store, set GOJIBERRY_DOCSTORE_REPLICAS to the real number%s", n, replicas,
			map[bool]string{true: "", false: " and move to a store with conditional writes (the log and challenge claims are atomic within one process only on this one)"}[conditional])
	case !replicasSet && conditional:
		log.Print("docstore: WARNING: GOJIBERRY_DOCSTORE_REPLICAS is not set: assuming ONE server process on this store. The directory's log is safe across processes on a store with conditional writes, but the declaration is what lets the server refuse an unsafe setup elsewhere: set it to the number of processes that share this store")
	case !replicasSet:
		log.Print("docstore: WARNING: GOJIBERRY_DOCSTORE_REPLICAS is not set: assuming ONE server process on this store, which has no conditional writes: a second process could fork the directory's log")
	}
}

func withPasskeys(ctx context.Context, srv *docstore.Server, adapter docstore.Adapter, m *auth.Manager, limiters docstore.LimiterFactory) (*auth.Manager, error) {
	rp := os.Getenv("GOJIBERRY_DOCSTORE_PASSKEY_RPID")
	if rp == "" {
		if v := os.Getenv("GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES"); v != "" {
			log.Printf("docstore: WARNING: GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES=%q is ignored: passkeys are off because GOJIBERRY_DOCSTORE_PASSKEY_RPID is not set", v)
		}
		return m, nil
	}
	var origins []string
	for _, o := range strings.Split(os.Getenv("GOJIBERRY_DOCSTORE_PASSKEY_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	if len(origins) == 0 {
		log.Fatal("docstore: GOJIBERRY_DOCSTORE_PASSKEY_ORIGINS is required with GOJIBERRY_DOCSTORE_PASSKEY_RPID")
	}
	if m == nil {
		var err error
		if m, err = auth.NewManager(ctx, auth.Config{BaseURL: origins[0]}); err != nil {
			return nil, err
		}
	}
	cfg := passkey.Config{RPID: rp, RPName: os.Getenv("GOJIBERRY_DOCSTORE_PASSKEY_NAME"), Origins: origins,
		Limiter:        limiters("passkey", passkey.DefaultPerMinute, passkey.DefaultBurst),
		AccountLimiter: limiters("passkey-account", passkey.DefaultAccountPerMinute, passkey.DefaultAccountBurst)}
	if px := strings.TrimSpace(os.Getenv("GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES")); px != "" {
		cfg.ClientIP = passkey.TrustedProxies(strings.Split(px, ",")...)
	}
	challenges, err := passkeyChallenges(ctx, adapter, os.Getenv("GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES"))
	if err != nil {
		return nil, err
	}
	cfg.Challenges = challenges
	svc, err := passkey.New(cfg,
		&passkey.AdapterStore{Adapter: adapter}, m)
	if err != nil {
		return nil, err
	}
	svc.HasOtherWay = passkey.OtherWayFromProviders(m.Providers())
	srv.WithRoutes("/auth/passkey/", svc.Handler()).Hide("passkey/")
	log.Printf("docstore: sign-in with passkeys enabled (rp=%s)", rp)
	return m, nil
}

func passkeyChallenges(ctx context.Context, adapter docstore.Adapter, raw string) (passkey.ChallengeStore, error) {
	mode, err := passkey.ParseChallengeMode(raw)
	if err != nil {
		return nil, err
	}
	if mode == "memory" {
		if n := declaredReplicas(); n > 1 {
			log.Printf("docstore: WARNING: GOJIBERRY_DOCSTORE_REPLICAS=%d but passkey ceremonies are kept in each process: a sign-in or registration begun on one replica fails on another (410). Set GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES=store (needs a store with conditional writes) or route each client to one replica", n)
		}
		return nil, nil
	}
	if ok, err := sharedStoreOrFallback(ctx, adapter, "GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES", mode, "passkey ceremonies",
		"a sign-in or registration begun on one replica fails on another (410)"); err != nil || !ok {
		return nil, err
	}
	sc, err := passkey.NewStoreChallenges(adapter, 0)
	if err != nil {
		return nil, err
	}
	sc.StartChallengeSweep(ctx, time.Minute) // counts the store before the first Begin
	log.Print("docstore: passkey ceremonies are shared through the store (records under passkey/challenge/ hold the e-mail and name of a registration in progress for the ceremony's lifetime (5 minutes by default, and about 2 minutes more until the sweep removes them), readable by anyone who can read the store: put an encrypting adapter under that keyspace if that matters)")
	return sc, nil
}

func publicQuota(ctx context.Context, adapter docstore.Adapter, limit int64, raw string) (public.Quota, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", "memory":
		if n := declaredReplicas(); n > 1 {
			log.Printf("docstore: WARNING: GOJIBERRY_DOCSTORE_REPLICAS=%d but the public offer's daily byte budget per address is kept in each process: an address can spend it once per replica. Set GOJIBERRY_DOCSTORE_PUBLIC_QUOTA=store (needs a store with conditional writes)", n)
		}
		return nil, nil
	case "store", "store-required":
	default:
		return nil, errors.New("parsing GOJIBERRY_DOCSTORE_PUBLIC_QUOTA \"" + raw + "\": want \"memory\", \"store\" or \"store-required\"")
	}
	if ok, err := sharedStoreOrFallback(ctx, adapter, "GOJIBERRY_DOCSTORE_PUBLIC_QUOTA", mode, "daily byte budgets",
		"an address can spend its budget once per replica"); err != nil || !ok {
		return nil, err
	}
	q, err := public.NewStoreQuota(adapter, limit, 24*time.Hour)
	if err != nil {
		return nil, err
	}
	log.Print("docstore: the public offer's daily byte budget per address is shared through the store (fixed calendar days, UTC)")
	return q, nil
}

func publicSessions(ctx context.Context, adapter docstore.Adapter, max int, raw string) (public.SessionStore, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", "memory":
		if n := declaredReplicas(); n > 1 {
			log.Printf("docstore: WARNING: GOJIBERRY_DOCSTORE_REPLICAS=%d but the public offer's anonymous sessions are kept in each process: a visitor's session is unknown to the other replicas, and each replica applies its own caps. Set GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS=store (needs a store with conditional writes)", n)
		}
		return nil, nil
	case "store", "store-required":
	default:
		return nil, errors.New("parsing GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS \"" + raw + "\": want \"memory\", \"store\" or \"store-required\"")
	}
	if ok, err := sharedStoreOrFallback(ctx, adapter, "GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS", mode, "anonymous sessions of the public offer",
		"a visitor's session is unknown to the other replicas and each replica applies its own caps"); err != nil || !ok {
		return nil, err
	}
	s, err := public.NewStoreSessions(adapter, max)
	if err != nil {
		return nil, err
	}
	log.Print("docstore: the public offer's anonymous sessions are shared through the store (records under public/session/ hold a session's random id, its document id and the hashed address keys; the caps count what the last recount saw plus this replica's own writes)")
	return s, nil
}

func publicPriorityQuota(ctx context.Context, adapter docstore.Adapter, limit int, raw string) (public.Quota, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", "memory":
		if n := declaredReplicas(); n > 1 {
			log.Printf("docstore: WARNING: GOJIBERRY_DOCSTORE_REPLICAS=%d but the public offer counts each reporter's priority abuse reports in each process: a reporter can use the allowance once per replica. Set GOJIBERRY_DOCSTORE_PUBLIC_REPORT_COUNTERS=store (needs a store with conditional writes)", n)
		}
		return nil, nil
	case "store", "store-required":
	default:
		return nil, errors.New("parsing GOJIBERRY_DOCSTORE_PUBLIC_REPORT_COUNTERS \"" + raw + "\": want \"memory\", \"store\" or \"store-required\"")
	}
	if limit <= 0 {
		limit = public.DefaultPriorityPerDay
	}
	if ok, err := sharedStoreOrFallback(ctx, adapter, "GOJIBERRY_DOCSTORE_PUBLIC_REPORT_COUNTERS", mode, "priority report allowances",
		"a reporter can use the allowance once per replica"); err != nil || !ok {
		return nil, err
	}
	q, err := public.NewStoreQuota(adapter, int64(limit), 24*time.Hour)
	if err != nil {
		return nil, err
	}
	log.Print("docstore: the public offer's priority-report allowance per reporter and its burst watch are shared through the store (fixed calendar days, UTC; burst records under public/burst/ hold a keyed hash of the network, never its address)")
	return q, nil
}

func declaredReplicas() int {
	n, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("GOJIBERRY_DOCSTORE_REPLICAS")))
	return n
}

func sharedStoreOrFallback(ctx context.Context, adapter docstore.Adapter, env, mode, what, consequence string) (bool, error) {
	if docstore.SupportsConditional(ctx, adapter) {
		return true, nil
	}
	cause := env + "=store needs a store with conditional writes (memory, localfs, sqlite, postgres, mongo or s3, not the blob backend)"
	if mode == "store-required" {
		return false, errors.New(cause + "; this one has none: move to a store that has them, or set " + env + "=store to fall back to per-process " + what)
	}
	log.Printf("docstore: WARNING: ================================================================\n"+
		"docstore: WARNING: %s ARE NOT SHARED BY REPLICAS\n"+
		"docstore: WARNING: cause: %s; this one has none, so %s stay in each process.\n"+
		"docstore: WARNING: effect: %s.\n"+
		"docstore: WARNING: fix: use a store with conditional writes, or run one replica / route each client to one replica.\n"+
		"docstore: WARNING: set %s=store-required to refuse to start instead of falling back.\n"+
		"docstore: WARNING: ================================================================", strings.ToUpper(what), cause, what, consequence, env)
	return false, nil
}

func withSessions(ctx context.Context, adapter docstore.Adapter, m *auth.Manager, limiters docstore.LimiterFactory) error {
	raw := os.Getenv("GOJIBERRY_DOCSTORE_SESSIONS")
	mode, err := sessionstore.ParseMode(raw)
	if err != nil {
		return err
	}
	if m == nil {
		if mode != "memory" {
			log.Printf("docstore: WARNING: GOJIBERRY_DOCSTORE_SESSIONS=%q is ignored: sign-in is off (no OpenID Connect provider and no passkeys)", raw)
		}
		return nil
	}
	clientIP := passkey.ClientIP
	if px := strings.TrimSpace(os.Getenv("GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES")); px != "" {
		clientIP = passkey.TrustedProxies(strings.Split(px, ",")...)
	} else {
		log.Print("docstore: WARNING: GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES is not set: the sign-in (login) rate limit tells clients apart by the connection's remote address, so behind a reverse proxy every client shares one budget and a few people signing in at once are refused with 429. Set it to the proxy's CIDRs (X-Forwarded-For is then believed from those proxies only)")
	}
	m.SetLoginLimiter(limiters("login", passkey.DefaultPerMinute, passkey.DefaultBurst), clientIP)
	if os.Getenv("GOJIBERRY_DOCSTORE_AUTH_MISS_LIMIT") == "true" {
		m.SetMissLimiter(limiters("auth-miss", passkey.DefaultPerMinute, passkey.DefaultBurst), clientIP)
		log.Print("docstore: requests with a bearer token that is not a session are limited per client address (GOJIBERRY_DOCSTORE_AUTH_MISS_LIMIT=true)")
	}
	if mode == "memory" {
		if n := declaredReplicas(); n > 1 {
			log.Printf("docstore: WARNING: GOJIBERRY_DOCSTORE_REPLICAS=%d but sign-in sessions are kept in each process: a token issued by one replica is unknown to the others. Set GOJIBERRY_DOCSTORE_SESSIONS=store (needs a store with conditional writes) or route each client to one replica", n)
		}
		return nil
	}
	if ok, err := sharedStoreOrFallback(ctx, adapter, "GOJIBERRY_DOCSTORE_SESSIONS", mode, "sign-in sessions",
		"a token issued by one replica is unknown to the others"); err != nil || !ok {
		return err
	}
	ss, err := sessionstore.New(adapter)
	if err != nil {
		return err
	}
	if raw := strings.TrimSpace(os.Getenv("GOJIBERRY_DOCSTORE_SESSION_CACHE")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 || d > time.Minute {
			return errors.New("parsing GOJIBERRY_DOCSTORE_SESSION_CACHE \"" + raw + "\": want a duration from 0 to 1m, such as \"5s\"")
		}
		ss.CacheFor = d
		if d > 0 {
			log.Printf("docstore: sign-in sessions are cached in each process for up to %s: a logout on one replica can take that long to reach the others", d)
		}
	}
	m.SetSessionStore(ss)
	go ss.RunSweep(ctx, time.Minute)
	log.Print("docstore: sign-in sessions are shared through the store")
	return nil
}

// Main runs the server configured from the environment.
func Main() {
	cfg := docstore.ConfigFromEnv()
	obs, err := setupObserver(context.Background(), os.Getenv)
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	adapter, err := docstore.BuildAdapter(context.Background(), cfg)
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	if handled, code := runOperatorCommand(context.Background(), adapter, os.Args[1:], os.Stdout, os.Stderr); handled {
		os.Exit(code)
	}
	adapter, _ = docstore.WithConfiguredExpiry(context.Background(), adapter, cfg)
	authManager, err := docstore.NewAuthManager(context.Background(), cfg.Auth)
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	srv := docstore.NewServer(adapter, cfg.Backend).WithGuard(cfg.Guard).WithRequireACL(cfg.RequireACL).WithObserver(obs)
	auditLog, err := withAudit(srv, os.Getenv, os.ReadFile, os.Stderr)
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	limiters := docstore.AuditLimiters(docstore.ObserveLimiters(docstore.LimiterFactoryFor(cfg.Limiter, adapter), obs), auditLog)
	if cfg.Limiter == "store" {
		go docstore.RunRateLimitSweep(context.Background(), adapter, 10*time.Minute)
		log.Print("docstore: rate limits are shared through the store")
	}
	authManager, err = withPasskeys(context.Background(), srv, adapter, authManager, limiters)
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	if err := withSessions(context.Background(), adapter, authManager, limiters); err != nil {
		log.Fatalf("docstore: %v", err)
	}
	srv = srv.WithAuth(authManager)
	if authManager != nil {
		cfg := &userconfig.Service{A: adapter, Who: srv.UserKey}
		srv.WithRoutes("/me/config", cfg).WithRoutes("/me/config/", cfg)
	}
	if err := withDirectory(context.Background(), srv, adapter, limiters); err != nil {
		log.Fatalf("docstore: %v", err)
	}
	if obs.Enabled() {
		go srv.RunUsageSampling(context.Background(), time.Hour)
	}
	withTelemetry(srv, os.Getenv("GOJIBERRY_DOCSTORE_TELEMETRY"))
	if err := withTelemetryNoise(srv, os.Getenv("GOJIBERRY_DOCSTORE_TELEMETRY_NOISE")); err != nil {
		log.Fatalf("docstore: %v", err)
	}
	if _, err := withDictionaries(srv, os.Getenv("GOJIBERRY_DOCSTORE_DICTIONARIES")); err != nil {
		log.Fatalf("docstore: %v", err)
	}
	publicSvc, err := withPublic(srv, adapter, cfg.ExpiryTTL, limiters)
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	if publicSvc != nil {
		go publicSvc.Run(context.Background(), cfg.ExpirySweep)
	}
	if authManager != nil {
		log.Printf("docstore: sign-in enabled (%d provider(s))", len(cfg.Auth.Providers))
	}
	log.Printf("docstore: listening on %s (backend=%s)", cfg.Addr, cfg.Backend)
	gate, err := exportGate(os.Getenv("GOJIBERRY_DOCSTORE_EXPORT_GATE"))
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	if gate != a11ycheck.GateAllow {
		log.Printf("docstore: export gate %q: GET /docs/{id}?for=export checks the document's accessibility first", string(gate))
	}
	if err := serve(newHTTPServer(cfg.Addr, exportgate.Wrap(srv.Handler(), gate)), obs); err != nil {
		log.Fatal(err)
	}
}

func withAudit(srv *docstore.Server, getenv func(string) string, readFile func(string) ([]byte, error), w io.Writer) (*audit.Logger, error) {
	switch strings.ToLower(strings.TrimSpace(getenv("GOJIBERRY_DOCSTORE_AUDIT"))) {
	case "", "off":
		return nil, nil
	case "on":
	default:
		return nil, errors.New("GOJIBERRY_DOCSTORE_AUDIT must be \"on\" or \"off\"")
	}
	key, err := observe.ReadSecret(observe.SinkEnv{Getenv: getenv, ReadFile: readFile}, "GOJIBERRY_DOCSTORE_AUDIT_KEY")
	if err != nil {
		return nil, err
	}
	if key != "" && len(key) < 16 {
		return nil, errors.New("GOJIBERRY_DOCSTORE_AUDIT_KEY must be at least 16 characters")
	}
	l := audit.NewLogger(w)
	srv.WithAudit(l, []byte(key))
	if key == "" {
		log.Print("docstore: the audit log is on without GOJIBERRY_DOCSTORE_AUDIT_KEY: its lines carry no Ref")
	} else {
		log.Print("docstore: the audit log is on (docs/audit.md)")
	}
	return l, nil
}

func parseWitnesses(v string) (map[string]ed25519.PublicKey, error) {
	out := map[string]ed25519.PublicKey{}
	for _, part := range strings.Split(v, ",") {
		name, key, ok := strings.Cut(strings.TrimSpace(part), ":")
		raw, err := hex.DecodeString(key)
		if !ok || name == "" || err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("GOJIBERRY_DOCSTORE_DIRECTORY_WITNESSES: want name:hexkey (a 32-byte ed25519 public key in hex), got %q", part)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("GOJIBERRY_DOCSTORE_DIRECTORY_WITNESSES: %q is named twice", name)
		}
		out[name] = ed25519.PublicKey(raw)
	}
	return out, nil
}

func publicChallenger(busy func() bool) (public.Challenger, error) {
	text := os.Getenv("GOJIBERRY_DOCSTORE_PUBLIC_POW_BITS")
	if text == "" || text == "0" {
		return nil, nil
	}
	bits, err := strconv.Atoi(text)
	if err != nil || bits < 1 || bits > 26 {
		return nil, fmt.Errorf("docstore: GOJIBERRY_DOCSTORE_PUBLIC_POW_BITS must be a number from 1 to 26, got %q", text)
	}
	secret := []byte(os.Getenv("GOJIBERRY_DOCSTORE_PUBLIC_POW_SECRET"))
	switch {
	case len(secret) == 0:
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
		log.Print("docstore: public offer: the proof-of-work secret is random for this process (set GOJIBERRY_DOCSTORE_PUBLIC_POW_SECRET to share it between replicas)")
	case len(secret) < 16:
		return nil, errors.New("docstore: GOJIBERRY_DOCSTORE_PUBLIC_POW_SECRET needs at least 16 characters")
	}
	p := &public.ProofOfWork{Secret: secret, Difficulty: bits}
	switch when := os.Getenv("GOJIBERRY_DOCSTORE_PUBLIC_POW_WHEN"); when {
	case "", "busy":
		p.Busy = busy
	case "always":
	default:
		return nil, fmt.Errorf("docstore: GOJIBERRY_DOCSTORE_PUBLIC_POW_WHEN must be busy or always, got %q", when)
	}
	return p, nil
}
