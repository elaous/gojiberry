// Command collabhub runs the multi-user editing relay (see
// gojiberry/collab/hub): clients stream a document's changes in and out over
// HTTP, and each document is saved through the same docstore backend
// docstore itself uses, so GOJIBERRY_DOCSTORE_BACKEND (memory, localfs,
// postgres, sqlite, s3, ...) and its settings choose where documents live.
//
// Who may do what comes from the access lists docstore keeps (the
// "acl:<id>" records): the owner, editors and viewers. With sign-in
// configured (the same GOJIBERRY_DOCSTORE_AUTH_* settings docstore reads) a
// caller is identified by its bearer token; without it, every document is
// open to everyone, as in docstore.
//
// GOJIBERRY_COLLAB_ADDR is the listen address (default ":8081").
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/elaous/gojiberry/collab/hub"
	"github.com/elaous/gojiberry/docstore"
)

func main() {
	cfg := docstore.ConfigFromEnv()
	adapter, err := docstore.BuildAdapter(context.Background(), cfg)
	if err != nil {
		log.Fatalf("collabhub: %v", err)
	}
	authManager, err := docstore.NewAuthManager(context.Background(), cfg.Auth)
	if err != nil {
		log.Fatalf("collabhub: %v", err)
	}
	hc := hub.Config{Store: adapter}
	if authManager != nil {
		who := hub.AuthUser(authManager)
		hc.User = who
		hc.Roles = hub.ACLRoles(adapter, who)
		log.Printf("collabhub: sign-in enabled (%d provider(s))", len(cfg.Auth.Providers))
	}
	h := hub.New(hc)
	addr := os.Getenv("GOJIBERRY_COLLAB_ADDR")
	if addr == "" {
		addr = ":8081"
	}
	srv := &http.Server{Addr: addr, Handler: h.Handler()}
	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
		<-stop
		_ = srv.Shutdown(context.Background())
		if err := h.Close(); err != nil {
			log.Printf("collabhub: saving on exit: %v", err)
		}
	}()
	log.Printf("collabhub: listening on %s (backend=%s)", addr, cfg.Backend)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
