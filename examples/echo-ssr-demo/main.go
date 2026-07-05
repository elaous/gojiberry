// Command echo-ssr-demo serves page.gox's Page over a real Echo
// instance (see router.go), fronted by three security middlewares --
// secure headers, CORS, and a per-client rate limit, all built into
// Echo itself -- see main_test.go for end-to-end verification against
// a real running server, and this example's README for the full
// picture.
package main

import (
	"fmt"
	"log"
)

func main() {
	addr := ":8098"
	fmt.Println("listening on", addr)
	log.Fatal(newRouter().Start(addr))
}
