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

	"github.com/elaous/gojiberry/docstore"
)

func main() {
	cfg := docstore.ConfigFromEnv()
	adapter, err := docstore.BuildAdapter(context.Background(), cfg)
	if err != nil {
		log.Fatalf("docstore: %v", err)
	}
	srv := docstore.NewServer(adapter, cfg.Backend)
	log.Printf("docstore: listening on %s (backend=%s)", cfg.Addr, cfg.Backend)
	log.Fatal(http.ListenAndServe(cfg.Addr, srv.Handler()))
}
