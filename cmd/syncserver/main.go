// Command syncserver runs the delta-sync relay.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/elaous/gojiberry/observe"
	"github.com/elaous/gojiberry/syncserver"
)

func main() {
	cfg := syncserver.ConfigFromEnv()
	srv := syncserver.NewServer(cfg)
	log.Printf("syncserver: listening on %s (region=%s)", cfg.Addr, cfg.Region)
	obs, err := observe.Setup(context.Background(), os.Getenv, "gojiberry-syncserver")
	if err != nil {
		log.Fatalf("syncserver: %v", err)
	}
	log.Fatal(observe.Serve(&http.Server{Addr: cfg.Addr, Handler: observe.Middleware(obs, srv.Handler()), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}, obs))
}
