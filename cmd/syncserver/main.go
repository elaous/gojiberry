// Command syncserver runs the delta-sync relay. Every deployment
// detail — listen address, region label, merge strategy — comes from
// the environment (GOJIBERRY_SYNC_ADDR, GOJIBERRY_SYNC_REGION,
// GOJIBERRY_SYNC_STRATEGY -- "lww", the default, or "char-crdt"); this
// same binary is what points at a domestic Brazilian data center for
// the reference deployment, or any other operator's own
// infrastructure, without a rebuild. Every client syncing through a
// given deployment must use the same strategy this relay does -- see
// syncserver.Config.Strategy's doc comment for what goes wrong
// otherwise.
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/elaous/gojiberry/syncserver"
)

func main() {
	cfg := syncserver.ConfigFromEnv()
	srv := syncserver.NewServer(cfg)
	log.Printf("syncserver: listening on %s (region=%s)", cfg.Addr, cfg.Region)
	log.Fatal((&http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}).ListenAndServe())
}
