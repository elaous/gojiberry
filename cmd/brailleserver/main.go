package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	brailleserver "github.com/elaous/gojiberry/braille/server"
	"github.com/elaous/gojiberry/observe"
)

func main() {
	cfg := brailleserver.ConfigFromEnv()
	srv := brailleserver.NewServer(cfg)
	log.Printf("brailleserver: listening on %s (lou_translate=%q table=%q)", cfg.Addr, cfg.LouTranslateBinary, cfg.Table)
	obs, err := observe.Setup(context.Background(), os.Getenv, "gojiberry-brailleserver")
	if err != nil {
		log.Fatalf("brailleserver: %v", err)
	}
	log.Fatal(observe.Serve(&http.Server{Addr: cfg.Addr, Handler: observe.Middleware(obs, srv.Handler()), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}, obs))
}
