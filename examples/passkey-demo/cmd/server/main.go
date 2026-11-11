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
	mux.Handle("/", wasmserve.Handler(os.DirFS(*dir), wasmserve.Options{GzipLevel: gzip.BestSpeed}))
	log.Printf("open %s/", origin)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
