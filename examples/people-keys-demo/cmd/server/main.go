package main

import (
	"compress/gzip"
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/elaous/gojiberry/examples/people-keys-demo/demoserver"
	"github.com/elaous/gojiberry/wasmserve"
)

func main() {
	dir := flag.String("dir", ".", "the directory with index.html, main.wasm and wasm_exec.js")
	addr := flag.String("addr", "localhost:8080", "where to listen")
	flag.Parse()
	s, err := demoserver.New("http://" + *addr)
	if err != nil {
		log.Fatal(err)
	}
	s.Mux.Handle("/", wasmserve.Handler(os.DirFS(*dir), wasmserve.Options{GzipLevel: gzip.BestSpeed}))
	log.Printf("open http://%s/", *addr)
	log.Fatal(http.ListenAndServe(*addr, s.Mux))
}
