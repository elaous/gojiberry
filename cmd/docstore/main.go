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
	"time"

	"github.com/elaous/gojiberry/docstore"
)

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
	srv := docstore.NewServer(adapter, cfg.Backend).WithAuth(authManager)
	if authManager != nil {
		log.Printf("docstore: sign-in enabled (%d provider(s))", len(cfg.Auth.Providers))
	}
	log.Printf("docstore: listening on %s (backend=%s)", cfg.Addr, cfg.Backend)
	log.Fatal((&http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}).ListenAndServe())
}
