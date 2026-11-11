package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/elaous/gojiberry/fontserver"
	"github.com/elaous/gojiberry/observe"
)

func main() {
	cfg := fontserver.ConfigFromEnv()
	srv := fontserver.NewServer(cfg)
	log.Printf("fontserver: listening on %s (fonts dir %s)", cfg.Addr, cfg.FontsDir)
	obs, err := observe.Setup(context.Background(), os.Getenv, "gojiberry-fontserver")
	if err != nil {
		log.Fatalf("fontserver: %v", err)
	}
	log.Fatal(observe.Serve(&http.Server{Addr: cfg.Addr, Handler: observe.Middleware(obs, srv.Handler()), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}, obs))
}
