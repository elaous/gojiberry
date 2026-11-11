// Command translateserver runs the translation server.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/elaous/gojiberry/observe"
	"github.com/elaous/gojiberry/translate/server"
)

func main() {
	cfg := server.ConfigFromEnv()
	srv, err := server.NewServer(cfg)
	if err != nil {
		log.Fatalf("translateserver: %v", err)
	}
	log.Printf("translateserver: listening on %s (deepl configured: %v)", cfg.Addr, cfg.DeepLAPIKey != "")
	obs, err := observe.Setup(context.Background(), os.Getenv, "gojiberry-translateserver")
	if err != nil {
		log.Fatalf("translateserver: %v", err)
	}
	log.Fatal(observe.Serve(&http.Server{Addr: cfg.Addr, Handler: observe.Middleware(obs, srv.Handler()), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}, obs))
}
