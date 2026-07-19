// Command translateserver runs the translation proxy: with
// GOJIBERRY_TRANSLATESERVER_DEEPL_KEY configured, POST /translate
// forwards to DeepL and returns the translated text -- see
// gojiberry/translateserver for the HTTP API.
package main

import (
	"log"
	"net/http"

	"github.com/elaous/gojiberry/translateserver"
)

func main() {
	cfg := translateserver.ConfigFromEnv()
	srv, err := translateserver.NewServer(cfg)
	if err != nil {
		log.Fatalf("translateserver: %v", err)
	}
	log.Printf("translateserver: listening on %s (deepl configured: %v)", cfg.Addr, cfg.DeepLAPIKey != "")
	log.Fatal(http.ListenAndServe(cfg.Addr, srv.Handler()))
}
