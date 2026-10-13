// Package docstorecmd is the body of the docstore server's main, shared by the
// dependency-free build (cmd/docstore) and the one with every backend linked
// (docstore/backends/cmd/docstore).
package docstorecmd

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/elaous/gojiberry/a11ycheck"
	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/auth/passkey"
	"github.com/elaous/gojiberry/auth/sessionstore"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/docstore/exportgate"
	"github.com/elaous/gojiberry/docstore/public"
	"github.com/elaous/gojiberry/telemetry"
	"github.com/elaous/gojiberry/userconfig"
)

// withPublic turns on the public (anonymous) offer when GOJIBERRY_DOCSTORE_PUBLIC=true (limits and
// secrets in GOJIBERRY_DOCSTORE_PUBLIC_*, see public.FromEnv): POST /public/session, the session's
// one document under /public/doc, read-only /public/view/<id>, POST /public/report and, with
// GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_TOKEN, DELETE /admin/takedown/<id> and GET /admin/health. Unset, nothing is mounted.
//
// The offer promises deletion 24 hours after the last edit, so it refuses to start unless
// expiry is on (GOJIBERRY_DOCSTORE_EXPIRY_TTL, and the adapter is the expiring one).
//
// It also refuses to start with the generic /docs API open: that API would let any visitor read and
// write every document that has no ACL. /docs counts as closed when sign-in is configured with
// GOJIBERRY_DOCSTORE_REQUIRE_ACL=true (srv must already have its auth manager, see Main). Otherwise
// withPublic closes it (Server.CloseDocs: 404 for /docs, /docs/{id}, its /acl and /quota), unless the
// operator sets GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_OPEN_DOCS=true, which keeps it open and says so loudly
// in the log. Behind a reverse proxy, GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES must name it ("none"
// says there is none and silences the startup warning); with an admin token it is required: the
// server does not start with it unset, since one caller's wrong admin guesses behind an unnamed
// proxy would lock the operator out.
//
// limiters is where the offer's request budgets live (GOJIBERRY_DOCSTORE_LIMITER): with "store"
// the replicas that share the store share them, as for the directory and the passkey routes.
//
// It returns the service so the caller can run its sweep.
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
	svc, err := public.New(public.Options{Store: adapter, Config: cfg, LimiterFactory: limiters, Quota: quota, Sessions: sessions, PriorityQuota: prio})
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

// Timeouts of the HTTP server: a client has this long to send a whole request (header timeout
// aside), so a slow body cannot hold a connection (and its memory) for ever. The public routes set
// tighter ones of their own per request (public.Config.RequestTimeout). The write side is not
// bounded here: a document download may be large and slow, and the public routes bound theirs.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 5 * time.Minute
	idleTimeout       = 2 * time.Minute
)

// newHTTPServer is the server Main runs.
// withTelemetry mounts the anonymous usage receiver (package telemetry, docs/telemetry.md) when the
// operator set GOJIBERRY_DOCSTORE_TELEMETRY=on. It is off, and the route does not exist, otherwise: that
// is the operator's kill switch. It reports whether it is on.
func withTelemetry(srv *docstore.Server, value string) bool {
	if strings.ToLower(strings.TrimSpace(value)) != "on" {
		return false
	}
	srv.WithTelemetry(telemetry.NewCounters())
	log.Print("docstore: anonymous usage counts are accepted at /telemetry (see docs/telemetry.md)")
	return true
}

// exportGate reads GOJIBERRY_DOCSTORE_EXPORT_GATE: "" or "allow" (no gate), "warn" or "block". With a gate
// set, GET /docs/{id}?for=export (or the header X-Gojiberry-Purpose: export) runs the accessibility
// checker on the document first; see package exportgate for what it does and does not cover. Any other
// value stops the server rather than silently running without the gate the operator meant.
func exportGate(value string) (a11ycheck.GateMode, error) {
	m, ok := a11ycheck.ParseGateMode(value)
	if !ok {
		return a11ycheck.GateAllow, fmt.Errorf("parsing GOJIBERRY_DOCSTORE_EXPORT_GATE %q: want \"allow\", \"warn\" or \"block\"", value)
	}
	return m, nil
}

// withTelemetryNoise reads GOJIBERRY_DOCSTORE_TELEMETRY_NOISE: off (the default) or a rounding step from 2
// to 1000. With a step, /admin/telemetry reports each total rounded at random to a multiple of it before
// the k threshold (telemetry.Counters.SetNoise). A value that is not one stops the server.
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

// withDirectory turns on the verified e-mail key directory (POST /directory/challenge and
// /directory/publish, DELETE /directory/keys/<fingerprint>, GET /directory/lookup, /directory/log,
// /directory/log/head and /directory/log/address, and the owner key routes under
// /directory/enroll) when GOJIBERRY_DOCSTORE_DIRECTORY_KEY holds a 32-byte seed in hex: it signs
// the records and keys the hashes that name them and the log's addresses, so it must be kept. To
// rotate it, put the new seed there and the old one (a comma separated list) in
// GOJIBERRY_DOCSTORE_DIRECTORY_KEY_PREV: the old records keep verifying, and at start-up, before
// the server listens, those that a log entry stands behind are moved to the new key
// (AdapterDirectory.Startup), after which the old seed can be removed. If the old key leaked, do
// not rotate through it: remove it from the list instead. Unset, the directory is off. Options,
// all off by default:
//
//   - GOJIBERRY_DOCSTORE_DIRECTORY_REQUIRE_POP=true: a key can be published only with proof of
//     possession (answer to POST /directory/challenge);
//   - GOJIBERRY_DOCSTORE_DIRECTORY_REQUIRE_ENROLLMENT=true: a key can be published only for an
//     address that has enrolled an owner key (enrolling itself stays open on the provider
//     session), so every publication carries the owner's signature;
//   - GOJIBERRY_DOCSTORE_DIRECTORY_LOOKUP=domain: a user can look up only addresses of their own
//     verified e-mail domain (default "any");
//   - GOJIBERRY_DOCSTORE_DIRECTORY_REVOKE_WINDOW=48h: how long a revoked key is remembered
//     (default 24h);
//   - GOJIBERRY_DOCSTORE_DIRECTORY_GLOBAL_BUDGET=domain: the whole-server lookup budget is one per
//     e-mail domain of the caller (default one for everybody).
//
// GOJIBERRY_DOCSTORE_REPLICAS (default 1) says how many server processes share the store. The
// directory's log and challenge claims are atomic across processes only on a store with
// conditional writes: with more than one replica on a store without them withDirectory refuses to
// enable the directory (an error), and with one it logs a warning.
//
// "directory/", "directory-log/" and "directory-enroll/" are reserved from the document API either
// way. The routes need sign-in, so with no session manager they are not mounted. limiters is where
// the routes' rate limits live (GOJIBERRY_DOCSTORE_LIMITER). The start-up census (records moved,
// skipped, and named under unknown keys) is logged and shown by /health.
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
	// Before serving: a record a retired key signed moves to the current key now, under the same
	// rules as a publish (the address lock and conditional writes), not while requests are in
	// flight.
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
	// Behind a reverse proxy the per-client budgets (lookups per address, wrong admin tokens) need
	// the proxy's addresses, so that X-Forwarded-For is believed from it and from nobody else;
	// without them one budget is shared by everybody behind the proxy.
	if px := strings.TrimSpace(os.Getenv("GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES")); px != "" {
		srv.WithDirectoryClientIP(passkey.TrustedProxies(strings.Split(px, ",")...))
	}
	srv.WithDirectory(encrypt.DirectoryService(dir)).WithDirectoryPolicy(policy).WithDirectoryLimits(limiters).WithDirectoryGlobalPerDomain(perDomain)
	go dir.RunSweep(ctx, time.Hour)
	go dir.RunWriterWatch(ctx, replicas)
	log.Printf("docstore: key directory enabled (proof of possession required: %v, owner key required: %v)", dir.RequireProof, dir.RequireEnrollment)
	return nil
}

// adminToken is the operator check of the directory routes: the header X-Gojiberry-Admin-Token
// equals tok (compared in constant time).
func adminToken(tok string) func(*http.Request) bool {
	want := sha256.Sum256([]byte(tok))
	return func(r *http.Request) bool {
		got := sha256.Sum256([]byte(r.Header.Get("X-Gojiberry-Admin-Token")))
		return subtle.ConstantTimeCompare(got[:], want[:]) == 1
	}
}

// checkWriters is the start-up's look at the store for other processes writing the same
// directory, against what GOJIBERRY_DOCSTORE_REPLICAS declares (see encrypt.AdapterDirectory.Writers).
// It never refuses to start (a restart within the last minutes looks like another writer): it says,
// loudly, what the store shows, and, when the variable is unset on a store that can carry several
// writers, that the default of one is only a default.
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

// withPasskeys turns on sign-in with passkeys when GOJIBERRY_DOCSTORE_PASSKEY_RPID is set (with
// GOJIBERRY_DOCSTORE_PASSKEY_ORIGINS, comma separated, GOJIBERRY_DOCSTORE_PASSKEY_NAME and, behind
// a reverse proxy, GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES, comma separated CIDRs): the
// accounts are kept in the same store under "passkey/", which the server hides from its own
// HTTP routes. With no OpenID Connect provider the session manager is made for passkeys alone.
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
	// The routes' rate limit lives where GOJIBERRY_DOCSTORE_LIMITER says (a budget per process, or
	// one shared through the store).
	cfg := passkey.Config{RPID: rp, RPName: os.Getenv("GOJIBERRY_DOCSTORE_PASSKEY_NAME"), Origins: origins,
		Limiter: limiters("passkey", passkey.DefaultPerMinute, passkey.DefaultBurst)}
	// Behind a reverse proxy the per-client rate limit needs the proxy's addresses, so that
	// X-Forwarded-For is believed from it and from nobody else.
	if px := strings.TrimSpace(os.Getenv("GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES")); px != "" {
		cfg.ClientIP = passkey.TrustedProxies(strings.Split(px, ",")...)
	}
	// Started ceremonies live in this process, or (GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES=store)
	// in the store, so that a ceremony begun on one replica finishes on another.
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
	// A passkey linked to an OpenID Connect identity can be removed even when it is the last
	// (the provider name must match exactly; "passkey" is reserved).
	svc.HasOtherWay = passkey.OtherWayFromProviders(m.Providers())
	srv.WithRoutes("/auth/passkey/", svc.Handler()).Hide("passkey/")
	log.Printf("docstore: sign-in with passkeys enabled (rp=%s)", rp)
	return m, nil
}

// passkeyChallenges is the ChallengeStore GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES asks for: nil (the
// per-process default) for "" or "memory", or a passkey.StoreChallenges over adapter for "store"
// and "store-required". On a store without working conditional writes "store" prints a prominent
// warning and falls back to the per-process store (the server keeps running; ceremonies are not
// shared), and "store-required" is an error. Expired records are swept in the background until ctx
// ends. The memory mode with GOJIBERRY_DOCSTORE_REPLICAS above 1 says that ceremonies are not shared.
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

// publicQuota is the per-address daily byte budget GOJIBERRY_DOCSTORE_PUBLIC_QUOTA asks for: nil (the
// per-process default) for "" or "memory", or a public.StoreQuota for "store" and "store-required",
// which replicas on one store share. On a store without conditional writes "store" warns and falls
// back to the per-process quota, and "store-required" is an error.
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

// publicSessions is where the public offer keeps its anonymous sessions, from
// GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS: nil (the per-process default) for "" or "memory", or a
// public.StoreSessions for "store" and "store-required", which replicas on one store share (a session
// begun on one is valid on the others, and the caps are applied to counts rebuilt from the store). On a
// store without conditional writes "store" warns and falls back to memory, and "store-required" is an
// error.
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

// publicPriorityQuota is the allowance of priority abuse reports per reporter GOJIBERRY_DOCSTORE_PUBLIC_REPORT_COUNTERS
// asks for: nil (counted in each process) for "" or "memory", or a public.StoreQuota for "store" and
// "store-required", so replicas on one store share the day's allowance of each reporter. The total number of
// reports and the burst watch stay per process.
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
	if limit <= 0 { // a negative limit is no limit; zero is the default and is filled in by the service
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
	log.Print("docstore: the public offer's priority-report allowance per reporter is shared through the store (fixed calendar days, UTC)")
	return q, nil
}

// declaredReplicas is GOJIBERRY_DOCSTORE_REPLICAS as a number (0 if unset or not a number; the
// directory's own check reports a malformed value).
func declaredReplicas() int {
	n, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("GOJIBERRY_DOCSTORE_REPLICAS")))
	return n
}

// sharedStoreOrFallback is the check shared by the "store" and "store-required" modes of
// GOJIBERRY_DOCSTORE_SESSIONS and GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES. It reports whether adapter
// has working conditional writes. If not, "store-required" is an error, and "store" prints a
// multi-line warning that names the cause, what is lost (what, consequence) and the fix, and
// returns false so the caller keeps the state in each process.
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

// withSessions chooses where the session manager keeps its bearer sessions and OpenID Connect login
// states, from GOJIBERRY_DOCSTORE_SESSIONS:
//
//   - "" or "memory": in this process (the default). A token issued by one replica is unknown to
//     another, so behind several replicas route by the bearer token or run one.
//   - "store": in the store, under "auth/session/" and "auth/state/" (auth/sessionstore), so
//     replicas that share the store share sign-ins, logouts and logins in progress. On a store without
//     conditional writes it prints a warning that names the fix and keeps sessions in memory.
//   - "store-required": as "store", but the server does not start on a store without conditional
//     writes.
//
// Expired records are swept in the background until ctx ends. With no session manager (no
// provider and no passkeys) there is nothing to configure and a set variable is reported as ignored.
//
// In every mode (the in-process default included) the OpenID Connect login routes get a request
// budget per client address, from limiters ("login", the passkey routes' numbers): a login writes
// a state record from a route that needs no sign-in, so without a budget a flood of logins fills
// the bound on pending logins and every real login is refused until the sweep clears it. The
// client is the connection's remote address, or, with GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES
// (the list the passkey routes use), the forwarded address from those proxies.
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
	// (A manager with passkeys only and no OpenID Connect provider mounts no login route, so the limiter
	// is installed and unused there: harmless, and not worth a second code path.)
	m.SetLoginLimiter(limiters("login", passkey.DefaultPerMinute, passkey.DefaultBurst), clientIP)
	if os.Getenv("GOJIBERRY_DOCSTORE_AUTH_MISS_LIMIT") == "true" {
		// a client that keeps sending bearer tokens that are not sessions is not looked up in the store
		// for a while; clients behind one shared address share the budget, hence off by default
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

// Main runs the server configured from the environment. It returns only after a graceful stop (a signal, when operator observability is on); a failure ends the process.
func Main() {
	cfg := docstore.ConfigFromEnv()
	// Operator observability is off unless GOJIBERRY_OBS=on (docs/observability.md). It is set up
	// first so that the sweeps and the limiters report to it from their start.
	obs, err := setupObserver(context.Background(), os.Getenv)
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	adapter, err := docstore.BuildAdapter(context.Background(), cfg)
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	// `docstore backup <file>` and `docstore restore <file>` work on the store itself, as it is
	// (before any expiry wrapper), and exit.
	if handled, code := runOperatorCommand(context.Background(), adapter, os.Args[1:], os.Stdout, os.Stderr); handled {
		os.Exit(code)
	}
	// With GOJIBERRY_DOCSTORE_EXPIRY_TTL set, every document expires that long after its last
	// write; everything below (passkeys, directory, the server) uses the wrapped store, whose
	// policy leaves their own keyspaces alone. Documents that already exist get a full
	// lifetime from the first sweep unless GOJIBERRY_DOCSTORE_EXPIRY_ADOPT=updated.
	// POST /docs/{id}/renew (a collaboration hub in another process, see collab/hub) works
	// whenever the store expires.
	adapter, _ = docstore.WithConfiguredExpiry(context.Background(), adapter, cfg)
	authManager, err := docstore.NewAuthManager(context.Background(), cfg.Auth)
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	srv := docstore.NewServer(adapter, cfg.Backend).WithGuard(cfg.Guard).WithRequireACL(cfg.RequireACL).WithObserver(obs)
	// Rate limits (the directory's and the passkey routes'): per process by default, or kept in
	// the store so replicas share them (GOJIBERRY_DOCSTORE_LIMITER=store). Stale budget records
	// are deleted in the background.
	limiters := docstore.ObserveLimiters(docstore.LimiterFactoryFor(cfg.Limiter, adapter), obs)
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
	// The manager is attached before the public offer is wired: whether /docs counts as closed
	// depends on it (WithRequireACL with sign-in).
	srv = srv.WithAuth(authManager)
	if authManager != nil {
		// per-person settings (roadmap 52); the server stores the bytes the signed-in client sends
		cfg := &userconfig.Service{A: adapter, Who: srv.UserKey}
		srv.WithRoutes("/me/config", cfg).WithRoutes("/me/config/", cfg)
	}
	if err := withDirectory(context.Background(), srv, adapter, limiters); err != nil {
		log.Fatalf("docstore: %v", err)
	}
	withTelemetry(srv, os.Getenv("GOJIBERRY_DOCSTORE_TELEMETRY"))
	if err := withTelemetryNoise(srv, os.Getenv("GOJIBERRY_DOCSTORE_TELEMETRY_NOISE")); err != nil {
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
