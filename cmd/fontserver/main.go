// Command fontserver runs the font CDN: a local directory scan
// (GOJIBERRY_FONTSERVER_DIR) plus, with an API key configured
// (GOJIBERRY_FONTSERVER_GOOGLE_KEY), a cached proxy of the Google
// Fonts catalog. See gojiberry/fontserver for the HTTP API.
package main

import (
	"log"
	"net/http"

	"github.com/elaous/gojiberry/fontserver"
)

func main() {
	cfg := fontserver.ConfigFromEnv()
	srv := fontserver.NewServer(cfg)
	log.Printf("fontserver: listening on %s (fonts dir %s)", cfg.Addr, cfg.FontsDir)
	log.Fatal(http.ListenAndServe(cfg.Addr, srv.Handler()))
}
