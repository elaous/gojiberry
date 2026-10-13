// Command brailleserver runs the braille-transcription proxy:
// GOJIBERRY_BRAILLESERVER_LOU_TRANSLATE_BINARY enables /translate
// (real Grade 1/Grade 2 output via a local liblouis install). Left
// empty, the server still starts -- that route just reports 503 until
// configured. See gojiberry/braille/server for the HTTP API.
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
