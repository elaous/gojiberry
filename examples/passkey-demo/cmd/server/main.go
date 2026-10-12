// Command server serves the passkey demo page and the passkey routes on localhost:
//
//	GOOS=js GOARCH=wasm go build -o examples/passkey-demo/main.wasm ./examples/passkey-demo
//	cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/passkey-demo/
//	cp components/base.css components/button.css components/documents.css examples/passkey-demo/
//	go run ./examples/passkey-demo/cmd/server -dir examples/passkey-demo
//
// then open http://localhost:8080/ (not 127.0.0.1: a passkey is bound to a domain name). Accounts
// are kept in memory.
package main

import (
	"compress/gzip"
	"context"
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/auth/passkey"
	"github.com/elaous/gojiberry/wasmserve"
)

func main() {
	dir := flag.String("dir", ".", "the directory with index.html, main.wasm and wasm_exec.js")
	addr := flag.String("addr", "localhost:8080", "where to listen")
	flag.Parse()
	origin := "http://" + *addr
	mgr, err := auth.NewManager(context.Background(), auth.Config{BaseURL: origin})
	if err != nil {
		log.Fatal(err)
	}
	svc, err := passkey.New(passkey.Config{RPID: "localhost", RPName: "gojiberry demo", Origins: []string{origin}}, passkey.NewMemoryStore(), mgr)
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/auth/passkey/", svc.Handler())
	mux.Handle("/auth/", mgr.Routes())
	// wasmserve sends main.wasm as application/wasm (streaming compile) and gzipped, with an ETag so a
	// reload is a 304; the fast gzip level keeps a rebuilt wasm from costing seconds on its first request.
	mux.Handle("/", wasmserve.Handler(os.DirFS(*dir), wasmserve.Options{GzipLevel: gzip.BestSpeed}))
	log.Printf("open %s/", origin)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
