// Command gin-ssr-demo serves page.gox's Page over a real Gin engine
// (see router.go), fronted by three security middlewares: secure
// headers, CORS, and a per-client rate limit -- see main_test.go for
// end-to-end verification against a real running server, and this
// example's README for the full picture.
package main

import (
	"fmt"
	"log"
)

func main() {
	addr := ":8097"
	fmt.Println("listening on", addr)
	log.Fatal(newRouter().Run(addr))
}
