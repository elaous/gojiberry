// Command syncserver runs the delta-sync relay. Every deployment
// detail — listen address, region label — comes from the environment
// (GOJIBERRY_SYNC_ADDR, GOJIBERRY_SYNC_REGION); this same binary is
// what points at a domestic Brazilian data center for the reference
// deployment, or any other operator's own infrastructure, without a
// rebuild.
package main

import (
	"log"
	"net/http"

	"github.com/elaous/gojiberry/syncserver"
)

func main() {
	cfg := syncserver.ConfigFromEnv()
	srv := syncserver.NewServer(cfg)
	log.Printf("syncserver: listening on %s (region=%s)", cfg.Addr, cfg.Region)
	log.Fatal(http.ListenAndServe(cfg.Addr, srv.Handler()))
}
