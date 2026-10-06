// Command docstore runs the document persistence HTTP server. The
// backend it stores documents in -- in-memory, local filesystem,
// Postgres, MongoDB, or S3 -- is chosen entirely by
// GOJIBERRY_DOCSTORE_BACKEND (and that backend's own env vars); see
// gojiberry/docstore.Config for the full list. This is the server a
// client's "remote" storage adapter talks to, so switching the actual
// storage technology behind a deployment never requires touching the
// client at all.
package main

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
)

// withPasskeys turns on sign-in with passkeys when GOJIBERRY_DOCSTORE_PASSKEY_RPID is set (with
// GOJIBERRY_DOCSTORE_PASSKEY_ORIGINS, comma separated, and GOJIBERRY_DOCSTORE_PASSKEY_NAME): the
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
	svc, err := passkey.New(passkey.Config{RPID: rp, RPName: os.Getenv("GOJIBERRY_DOCSTORE_PASSKEY_NAME"), Origins: origins},
		&passkey.AdapterStore{Adapter: adapter}, m)
	if err != nil {
		return nil, err
	}
	// A passkey linked to an OpenID Connect identity can be removed even when it is the last.
	svc.HasOtherWay = func(_ context.Context, id string) bool {
		for _, p := range m.Providers() {
			if strings.HasPrefix(id, p.Name+":") {
				return true
			}
		}
		return false
	}
	srv.WithRoutes("/auth/passkey/", svc.Handler()).Hide("passkey/")
	log.Printf("docstore: sign-in with passkeys enabled (rp=%s)", rp)
	return m, nil
}

func main() {
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
	srv = srv.WithAuth(authManager)
	if authManager != nil {
		log.Printf("docstore: sign-in enabled (%d provider(s))", len(cfg.Auth.Providers))
	}
	log.Printf("docstore: listening on %s (backend=%s)", cfg.Addr, cfg.Backend)
	log.Fatal((&http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}).ListenAndServe())
}
