package main

import (
	"fmt"
	"os"

	"github.com/elaous/gojiberry/gox"
)

func main() {
	html, err := gox.RenderString(Page([]Person{{"Ada", "Engineer"}, {"Grace", "Admiral"}}))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(html)
}
