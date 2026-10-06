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
// Expiry: with GOJIBERRY_DOCSTORE_EXPIRY_TTL (and _SWEEP, _ADOPT) set, this process expires
// documents like the docstore server and collaborators' edits renew them; with
// GOJIBERRY_COLLAB_RENEW_URL (and optionally GOJIBERRY_COLLAB_RENEW_TOKEN) it asks the docstore
// server that expires the shared store to (POST /docs/{id}/renew, see docs/public-docstore.md).
//
// GOJIBERRY_COLLAB_ADDR is the listen address (default ":8081").
package main

import (
	"context"
	"github.com/elaous/gojiberry/guard"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/elaous/gojiberry/collab/hub"
	"github.com/elaous/gojiberry/docstore"
)

func main() {
	cfg := docstore.ConfigFromEnv()
	adapter, err := docstore.BuildAdapter(context.Background(), cfg)
	if err != nil {
		log.Fatalf("collabhub: %v", err)
	}
	// Expiry. With GOJIBERRY_DOCSTORE_EXPIRY_TTL set, this process wraps the store (and sweeps it)
	// exactly as the docstore server does, and the hub renews a document in-process when
	// collaborators edit it. Otherwise, with GOJIBERRY_COLLAB_RENEW_URL set (the base URL of the
	// docstore server that owns the expiry of this store), each renewal is a POST
	// /docs/{id}/renew there, carrying the Authorization of the editor and, if set,
	// GOJIBERRY_COLLAB_RENEW_TOKEN as X-Gojiberry-Token (the docstore server's guard token).
	adapter, expiring := docstore.WithConfiguredExpiry(context.Background(), adapter, cfg)
	var renewer docstore.Renewer
	if renewURL := os.Getenv("GOJIBERRY_COLLAB_RENEW_URL"); renewURL != "" {
		if expiring != nil {
			log.Fatal("collabhub: GOJIBERRY_COLLAB_RENEW_URL and GOJIBERRY_DOCSTORE_EXPIRY_TTL are both set: this process either owns the expiry or asks the docstore server to")
		}
		remote := docstore.NewHTTPAdapter(renewURL, &http.Client{Timeout: 10 * time.Second})
		if t := os.Getenv("GOJIBERRY_COLLAB_RENEW_TOKEN"); t != "" {
			remote.Header.Set("X-Gojiberry-Token", t)
		}
		renewer = remote
		log.Printf("collabhub: collaborators' edits renew documents at %s", renewURL)
	} else if expiring != nil {
		renewer = expiring
	}
	authManager, err := docstore.NewAuthManager(context.Background(), cfg.Auth)
	if err != nil {
		log.Fatalf("collabhub: %v", err)
	}
	// GOJIBERRY_COLLAB_ALLOW_ORIGINS and GOJIBERRY_COLLAB_TOKEN (sent as
	// X-Gojiberry-Token: Authorization carries the sign-in session) choose who
	// may call the hub; with neither it is open to any origin.
	hc := hub.Config{Store: adapter, Renewer: renewer, Guard: guard.FromEnvWith("GOJIBERRY_COLLAB", guard.Options{TokenHeader: "X-Gojiberry-Token", TokenQuery: "guard_token"})}
	if authManager != nil {
		who := hub.AuthUser(authManager)
		hc.User = who
		// What everyone may do on a document that has no ACL:
		// GOJIBERRY_COLLAB_NO_ACL_ROLE = owner (the default: anyone can start
		// a document), editor, viewer, or none (only shared documents open).
		noACL := hub.Owner
		switch strings.ToLower(os.Getenv("GOJIBERRY_COLLAB_NO_ACL_ROLE")) {
		case "editor":
			noACL = hub.Editor
		case "viewer":
			noACL = hub.Viewer
		case "none":
			noACL = ""
		}
		hc.Roles = hub.ACLRolesWith(adapter, who, noACL)
		log.Printf("collabhub: sign-in enabled (%d provider(s))", len(cfg.Auth.Providers))
	}
	if v := os.Getenv("GOJIBERRY_COLLAB_PRESENCE_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Fatalf("collabhub: GOJIBERRY_COLLAB_PRESENCE_INTERVAL: %v", err)
		}
		hc.PresenceInterval = d
	}
	h := hub.New(hc)
	addr := os.Getenv("GOJIBERRY_COLLAB_ADDR")
	if addr == "" {
		addr = ":8081"
	}
	srv := &http.Server{Addr: addr, Handler: h.Handler(), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
		<-stop
		// Refuse writes and end the event streams first (Shutdown would wait
		// for them forever), let the requests in flight finish, and only then
		// save: nothing can be acknowledged after the final save.
		h.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		if err := h.Close(); err != nil {
			log.Printf("collabhub: saving on exit: %v", err)
		}
	}()
	log.Printf("collabhub: listening on %s (backend=%s)", addr, cfg.Backend)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
	<-done // the final save has finished
}
