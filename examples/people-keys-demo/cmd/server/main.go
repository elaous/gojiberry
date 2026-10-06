// Command server serves the people-keys demo page and its routes on localhost:
//
//	GOOS=js GOARCH=wasm go build -o examples/people-keys-demo/main.wasm ./examples/people-keys-demo
//	cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/people-keys-demo/
//	cp components/base.css components/button.css components/documents.css examples/people-keys-demo/
//	go run ./examples/people-keys-demo/cmd/server -dir examples/people-keys-demo
//
// then open http://localhost:8080/ (not 127.0.0.1: a passkey is bound to a domain name).
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/elaous/gojiberry/examples/people-keys-demo/demoserver"
)

func main() {
	dir := flag.String("dir", ".", "the directory with index.html, main.wasm and wasm_exec.js")
	addr := flag.String("addr", "localhost:8080", "where to listen")
	flag.Parse()
	s, err := demoserver.New("http://" + *addr)
	if err != nil {
		log.Fatal(err)
	}
	s.Mux.Handle("/", http.FileServer(http.Dir(*dir)))
	log.Printf("open http://%s/", *addr)
	log.Fatal(http.ListenAndServe(*addr, s.Mux))
}
