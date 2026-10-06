// Package docstorecmd is the body of the docstore server's main, shared by the
// dependency-free build (cmd/docstore) and the one with every backend linked
// (docstore/backends/cmd/docstore).
package docstorecmd

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/auth/passkey"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

// withDirectory turns on the verified e-mail key directory (POST /directory/challenge and
// /directory/publish, DELETE /directory/keys/<fingerprint>, GET /directory/lookup and
// /directory/log) when GOJIBERRY_DOCSTORE_DIRECTORY_KEY holds a 32-byte seed in hex: it signs the
// records and keys the hash that names them, so it must be kept. To rotate it, put the new seed
// there and the old one (a comma separated list) in GOJIBERRY_DOCSTORE_DIRECTORY_KEY_PREV: the old
// records keep verifying, and are moved to the new key at start-up (AdapterDirectory.Resign), after
// which the old seed can be removed. Unset, the directory is off. Options, all off by default:
//
//   - GOJIBERRY_DOCSTORE_DIRECTORY_REQUIRE_POP=true: a key can be published only with proof of
//     possession (answer to POST /directory/challenge);
//   - GOJIBERRY_DOCSTORE_DIRECTORY_LOOKUP=domain: a user can look up only addresses of their own
//     verified e-mail domain (default "any");
//   - GOJIBERRY_DOCSTORE_DIRECTORY_REVOKE_WINDOW=48h: how long a revoked key is remembered
//     (default 24h).
//
// "directory/" and "directory-log/" are reserved from the document API either way. The routes need
// sign-in, so with no session manager they are not mounted. limiters is where the routes' rate
// limits live (GOJIBERRY_DOCSTORE_LIMITER).
func withDirectory(ctx context.Context, srv *docstore.Server, adapter docstore.Adapter, limiters docstore.LimiterFactory) {
	signer, err := encrypt.DirectorySignerFromEnv()
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	if signer == nil {
		return
	}
	dir := &encrypt.AdapterDirectory{Store: adapter, Signer: signer}
	dir.RequireProof = os.Getenv("GOJIBERRY_DOCSTORE_DIRECTORY_REQUIRE_POP") == "true"
	if v := strings.TrimSpace(os.Getenv("GOJIBERRY_DOCSTORE_DIRECTORY_REVOKE_WINDOW")); v != "" {
		w, err := time.ParseDuration(v)
		if err != nil || w <= 0 {
			log.Fatalf("docstore: parsing GOJIBERRY_DOCSTORE_DIRECTORY_REVOKE_WINDOW %q: want a positive duration such as \"48h\"", v)
		}
		dir.RevokeWindow = w
	}
	policy, err := docstore.ParseDirectoryLookupMode(os.Getenv("GOJIBERRY_DOCSTORE_DIRECTORY_LOOKUP"))
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	srv.WithDirectory(encrypt.DirectoryService(dir)).WithDirectoryPolicy(policy).WithDirectoryLimits(limiters)
	if strings.TrimSpace(os.Getenv(encrypt.DirectoryKeyPrevEnv)) != "" {
		go func() { // finishes a rotation: records the retired keys signed move to the current key
			if n, err := dir.Resign(ctx); err != nil {
				log.Printf("docstore: key directory: moving records to the current key: %v", err)
			} else {
				log.Printf("docstore: key directory: %d record(s) moved to the current key (once it reports 0 the retired keys can be removed)", n)
			}
		}()
	}
	go dir.RunSweep(ctx, time.Hour)
	log.Printf("docstore: key directory enabled (proof of possession required: %v)", dir.RequireProof)
}

// withPasskeys turns on sign-in with passkeys when GOJIBERRY_DOCSTORE_PASSKEY_RPID is set (with
// GOJIBERRY_DOCSTORE_PASSKEY_ORIGINS, comma separated, GOJIBERRY_DOCSTORE_PASSKEY_NAME and, behind
// a reverse proxy, GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES, comma separated CIDRs): the
// accounts are kept in the same store under "passkey/", which the server hides from its own
// HTTP routes. With no OpenID Connect provider the session manager is made for passkeys alone.
func withPasskeys(ctx context.Context, srv *docstore.Server, adapter docstore.Adapter, m *auth.Manager, limiters docstore.LimiterFactory) (*auth.Manager, error) {
	rp := os.Getenv("GOJIBERRY_DOCSTORE_PASSKEY_RPID")
	if rp == "" {
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

// Main runs the server configured from the environment and does not return.
func Main() {
	cfg := docstore.ConfigFromEnv()
	adapter, err := docstore.BuildAdapter(context.Background(), cfg)
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	// With GOJIBERRY_DOCSTORE_EXPIRY_TTL set, every document expires that long after its last
	// write; everything below (passkeys, directory, the server) uses the wrapped store, whose
	// policy leaves their own keyspaces alone. Documents that already exist get a full
	// lifetime from the first sweep unless GOJIBERRY_DOCSTORE_EXPIRY_ADOPT=updated.
	if cfg.ExpiryTTL > 0 {
		ea := docstore.NewExpiringAdapter(adapter, docstore.FixedTTL(cfg.ExpiryTTL))
		ea.AdoptUpdatedAt = cfg.ExpiryAdoptUpdated
		go ea.Run(context.Background(), cfg.ExpirySweep)
		adapter = ea
		log.Printf("docstore: documents expire %s after their last write (sweep every %s)", cfg.ExpiryTTL, cfg.ExpirySweep)
	}
	authManager, err := docstore.NewAuthManager(context.Background(), cfg.Auth)
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	srv := docstore.NewServer(adapter, cfg.Backend).WithGuard(cfg.Guard).WithRequireACL(cfg.RequireACL)
	// Rate limits (the directory's and the passkey routes'): per process by default, or kept in
	// the store so replicas share them (GOJIBERRY_DOCSTORE_LIMITER=store). Stale budget records
	// are deleted in the background.
	limiters := docstore.LimiterFactoryFor(cfg.Limiter, adapter)
	if cfg.Limiter == "store" {
		go docstore.RunRateLimitSweep(context.Background(), adapter, 10*time.Minute)
		log.Print("docstore: rate limits are shared through the store")
	}
	authManager, err = withPasskeys(context.Background(), srv, adapter, authManager, limiters)
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	withDirectory(context.Background(), srv, adapter, limiters)
	srv = srv.WithAuth(authManager)
	if authManager != nil {
		log.Printf("docstore: sign-in enabled (%d provider(s))", len(cfg.Auth.Providers))
	}
	log.Printf("docstore: listening on %s (backend=%s)", cfg.Addr, cfg.Backend)
	log.Fatal((&http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}).ListenAndServe())
}
