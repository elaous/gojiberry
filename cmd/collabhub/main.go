// Command collabhub runs the multi-user editing relay.
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

// noACLRole is the role everyone has on documents without an ACL.
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

// durationEnv reads a duration setting; ok is false when unset or invalid.
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
	// Expiry: GOJIBERRY_DOCSTORE_EXPIRY_TTL enables it.
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
	// GOJIBERRY_COLLAB_RENEW_INTERVAL sets the renewal interval.
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

// run serves the hub until ctx ends, then stops it cleanly.
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
		// Refuse writes and end event streams before the final save.
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

// remoteRenewer renews leases through the remote docstore.
func remoteRenewer(baseURL, token string) *docstore.HTTPAdapter {
	remote := docstore.NewHTTPAdapter(baseURL, &http.Client{Timeout: 10 * time.Second})
	if token != "" {
		remote.Header.Set("X-Gojiberry-Token", token)
	}
	return remote
}

// renewStartupMessage is the log line announcing renewal.
func renewStartupMessage(renewURL string) string {
	return "collabhub: collaborators' edits renew documents at " + docstore.RedactURL(renewURL)
}
