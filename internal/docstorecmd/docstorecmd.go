// Package docstorecmd is the body of the docstore server's main, shared by the
// dependency-free build (cmd/docstore) and the one with every backend linked
// (docstore/backends/cmd/docstore).
package docstorecmd

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/auth/passkey"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/docstore/public"
)

// withPublic turns on the public (anonymous) offer when GOJIBERRY_DOCSTORE_PUBLIC=true (limits and
// secrets in GOJIBERRY_DOCSTORE_PUBLIC_*, see public.FromEnv): POST /public/session, the session's
// one document under /public/doc, read-only /public/view/<id>, POST /public/report and, with
// GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_TOKEN, DELETE /admin/takedown/<id>. Unset, nothing is mounted.
//
// The offer promises deletion 24 hours after the last edit, so it refuses to start unless
// expiry is on (GOJIBERRY_DOCSTORE_EXPIRY_TTL, and the adapter is the expiring one). It returns
// the service so the caller can run its sweep.
func withPublic(srv *docstore.Server, adapter docstore.Adapter, expiryTTL time.Duration) (*public.Service, error) {
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
	svc, err := public.New(public.Options{Store: adapter, Config: cfg})
	if err != nil {
		return nil, err
	}
	svc.Mount(srv)
	log.Printf("docstore: public offer enabled (takedown route %t, terms link %t)", cfg.AdminToken != "", cfg.TermsURL != "")
	return svc, nil
}

// withDirectory turns on the verified e-mail key directory (POST /directory/publish, GET
// /directory/lookup) when GOJIBERRY_DOCSTORE_DIRECTORY_KEY holds a 32-byte seed in hex: it signs
// the records and keys the hash that names them, so it must be kept and not changed. Unset, the
// directory is off. "directory/" is reserved from the document API either way. The routes need
// sign-in, so with no session manager they are not mounted.
func withDirectory(srv *docstore.Server, adapter docstore.Adapter) {
	signer, err := encrypt.DirectorySignerFromEnv()
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	if signer == nil {
		return
	}
	srv.WithDirectory(encrypt.DirectoryService(&encrypt.AdapterDirectory{Store: adapter, Signer: signer}))
	log.Print("docstore: key directory enabled")
}

// withPasskeys turns on sign-in with passkeys when GOJIBERRY_DOCSTORE_PASSKEY_RPID is set (with
// GOJIBERRY_DOCSTORE_PASSKEY_ORIGINS, comma separated, GOJIBERRY_DOCSTORE_PASSKEY_NAME and, behind
// a reverse proxy, GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES, comma separated CIDRs): the
// accounts are kept in the same store under "passkey/", which the server hides from its own
// HTTP routes. With no OpenID Connect provider the session manager is made for passkeys alone.
func withPasskeys(ctx context.Context, srv *docstore.Server, adapter docstore.Adapter, m *auth.Manager) (*auth.Manager, error) {
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
	cfg := passkey.Config{RPID: rp, RPName: os.Getenv("GOJIBERRY_DOCSTORE_PASSKEY_NAME"), Origins: origins}
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
	authManager, err = withPasskeys(context.Background(), srv, adapter, authManager)
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	withDirectory(srv, adapter)
	publicSvc, err := withPublic(srv, adapter, cfg.ExpiryTTL)
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	if publicSvc != nil {
		go publicSvc.Run(context.Background(), cfg.ExpirySweep)
	}
	srv = srv.WithAuth(authManager)
	if authManager != nil {
		log.Printf("docstore: sign-in enabled (%d provider(s))", len(cfg.Auth.Providers))
	}
	log.Printf("docstore: listening on %s (backend=%s)", cfg.Addr, cfg.Backend)
	log.Fatal((&http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}).ListenAndServe())
}
