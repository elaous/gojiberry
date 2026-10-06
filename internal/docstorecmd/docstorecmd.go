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
	srv = srv.WithAuth(authManager)
	if authManager != nil {
		log.Printf("docstore: sign-in enabled (%d provider(s))", len(cfg.Auth.Providers))
	}
	log.Printf("docstore: listening on %s (backend=%s)", cfg.Addr, cfg.Backend)
	log.Fatal((&http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}).ListenAndServe())
}
