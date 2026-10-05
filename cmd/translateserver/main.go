// Command translate/server runs the translation proxy: with
// GOJIBERRY_TRANSLATESERVER_DEEPL_KEY configured, POST /translate
// forwards to DeepL and returns the translated text -- see
// gojiberry/translate/server for the HTTP API.
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/elaous/gojiberry/translate/server"
)

func main() {
	cfg := server.ConfigFromEnv()
	srv, err := server.NewServer(cfg)
	if err != nil {
		log.Fatalf("translate/server: %v", err)
	}
	log.Printf("translate/server: listening on %s (deepl configured: %v)", cfg.Addr, cfg.DeepLAPIKey != "")
	log.Fatal((&http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}).ListenAndServe())
}
