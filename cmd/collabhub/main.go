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
// GOJIBERRY_COLLAB_RENEW_INTERVAL and GOJIBERRY_COLLAB_EXPIRY_TTL tune the renewals. The hub's
// role rule must match the docstore's (GOJIBERRY_COLLAB_NO_ACL_ROLE against
// GOJIBERRY_DOCSTORE_REQUIRE_ACL), or renewals are refused: GET /healthz shows renewFailures.
//
// GOJIBERRY_COLLAB_ADDR is the listen address (default ":8081").
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/elaous/gojiberry/collab/hub"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/guard"
	"github.com/elaous/gojiberry/observe"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, nil); err != nil {
		log.Fatalf("collabhub: %v", err)
	}
}

// noACLRole is what everyone may do on a document that has no ACL (GOJIBERRY_COLLAB_NO_ACL_ROLE): owner
// (the default: anyone can start a document), editor, viewer, or none (only shared documents open).
func noACLRole(v string) hub.Role {
	switch strings.ToLower(v) {
	case "editor":
		return hub.Editor
	case "viewer":
		return hub.Viewer
	case "none":
		return ""
	}
	return hub.Owner
}

// durationEnv reads a duration setting; ok is false when it is not set. A value that does not parse, or
// is not positive when positive is set, is an error that names the setting.
func durationEnv(name, want string, positive bool) (d time.Duration, ok bool, err error) {
	v := os.Getenv(name)
	if v == "" {
		return 0, false, nil
	}
	d, err = time.ParseDuration(v)
	if err != nil || (positive && d <= 0) {
		return 0, false, fmt.Errorf("%s %q: want %s", name, v, want)
	}
	return d, true, nil
}

// buildHub makes the hub from the environment.
func buildHub(ctx context.Context) (*hub.Hub, docstore.Config, error) {
	cfg := docstore.ConfigFromEnv()
	adapter, err := docstore.BuildAdapter(ctx, cfg)
	if err != nil {
		return nil, cfg, err
	}
	// Expiry. With GOJIBERRY_DOCSTORE_EXPIRY_TTL set, this process wraps the store (and sweeps it)
	// exactly as the docstore server does, and the hub renews a document in-process when
	// collaborators edit it. Otherwise, with GOJIBERRY_COLLAB_RENEW_URL set (the base URL of the
	// docstore server that owns the expiry of this store), each renewal is a POST
	// /docs/{id}/renew there, carrying the Authorization of the editor and, if set,
	// GOJIBERRY_COLLAB_RENEW_TOKEN as X-Gojiberry-Token (the docstore server's guard token).
	adapter, expiring := docstore.WithConfiguredExpiry(ctx, adapter, cfg)
	var renewer docstore.Renewer
	if renewURL := os.Getenv("GOJIBERRY_COLLAB_RENEW_URL"); renewURL != "" {
		if expiring != nil {
			return nil, cfg, errors.New("GOJIBERRY_COLLAB_RENEW_URL and GOJIBERRY_DOCSTORE_EXPIRY_TTL are both set: this process either owns the expiry or asks the docstore server to")
		}
		renewer = remoteRenewer(renewURL, os.Getenv("GOJIBERRY_COLLAB_RENEW_TOKEN"))
		log.Print(renewStartupMessage(renewURL))
	} else if expiring != nil {
		renewer = expiring
	}
	authManager, err := docstore.NewAuthManager(ctx, cfg.Auth)
	if err != nil {
		return nil, cfg, err
	}
	// GOJIBERRY_COLLAB_ALLOW_ORIGINS and GOJIBERRY_COLLAB_TOKEN (sent as
	// X-Gojiberry-Token: Authorization carries the sign-in session) choose who
	// may call the hub; with neither it is open to any origin.
	hc := hub.Config{Store: adapter, Renewer: renewer, ExpiryTTL: cfg.ExpiryTTL, Guard: guard.FromEnvWith("GOJIBERRY_COLLAB", guard.Options{TokenHeader: "X-Gojiberry-Token", TokenQuery: "guard_token"})}
	if authManager != nil {
		who := hub.AuthUser(authManager)
		hc.User = who
		hc.Roles = hub.ACLRolesWith(adapter, who, noACLRole(os.Getenv("GOJIBERRY_COLLAB_NO_ACL_ROLE")))
		log.Printf("collabhub: sign-in enabled (%d provider(s))", len(cfg.Auth.Providers))
	}
	if d, ok, err := durationEnv("GOJIBERRY_COLLAB_PRESENCE_INTERVAL", "a duration such as 5s", false); err != nil {
		return nil, cfg, err
	} else if ok {
		hc.PresenceInterval = d
	}
	// GOJIBERRY_COLLAB_RENEW_INTERVAL is the least time between two renewals of a document (default
	// one minute; lowered to a quarter of the TTL when it is known). With GOJIBERRY_COLLAB_RENEW_URL
	// the hub cannot see the docstore server's lifetime: GOJIBERRY_COLLAB_EXPIRY_TTL says what it is,
	// for that clamp and for the warning about an interval longer than it.
	if d, ok, err := durationEnv("GOJIBERRY_COLLAB_RENEW_INTERVAL", "a duration such as 1m", true); err != nil {
		return nil, cfg, err
	} else if ok {
		hc.RenewInterval = d
	}
	if d, ok, err := durationEnv("GOJIBERRY_COLLAB_EXPIRY_TTL", "a positive duration such as 24h", true); err != nil {
		return nil, cfg, err
	} else if ok {
		hc.ExpiryTTL = d
	}
	return hub.New(hc), cfg, nil
}

// run serves the hub until ctx ends, then stops it cleanly. onListen, when set, is told the address
// once the port is open (for tests that listen on port 0).
func run(ctx context.Context, onListen func(net.Addr)) error {
	h, cfg, err := buildHub(ctx)
	if err != nil {
		return err
	}
	obs, err := observe.Setup(ctx, os.Getenv, "gojiberry-collabhub")
	if err != nil {
		return err
	}
	addr := os.Getenv("GOJIBERRY_COLLAB_ADDR")
	if addr == "" {
		addr = ":8081"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if onListen != nil {
		onListen(ln.Addr())
	}
	srv := &http.Server{Handler: observe.Middleware(obs, h.Handler()), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		// Refuse writes and end the event streams first (Shutdown would wait
		// for them forever), let the requests in flight finish, and only then
		// save: nothing can be acknowledged after the final save.
		h.Stop()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		if err := h.Close(); err != nil {
			log.Printf("collabhub: saving on exit: %v", err)
		}
	}()
	log.Printf("collabhub: listening on %s (backend=%s)", ln.Addr(), cfg.Backend)
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	<-done // the final save has finished
	if obs != nil {
		cctx, cancel := context.WithTimeout(context.Background(), observe.ShutdownGrace)
		defer cancel()
		if err := obs.Close(cctx); err != nil {
			log.Printf("collabhub: closing the observability sinks: %v", err)
		}
	}
	return nil
}

// remoteRenewer is the docstore.Renewer that renews through the docstore server at baseURL; token,
// when set, is sent as X-Gojiberry-Token (the server's guard token).
func remoteRenewer(baseURL, token string) *docstore.HTTPAdapter {
	remote := docstore.NewHTTPAdapter(baseURL, &http.Client{Timeout: 10 * time.Second})
	if token != "" {
		remote.Header.Set("X-Gojiberry-Token", token)
	}
	return remote
}

// renewStartupMessage is the log line announcing the remote renewals. It never holds the raw
// URL, which may carry a user name, password or token.
func renewStartupMessage(renewURL string) string {
	return "collabhub: collaborators' edits renew documents at " + docstore.RedactURL(renewURL)
}
