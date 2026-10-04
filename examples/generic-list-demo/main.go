package main

import (
	"fmt"
	"os"

	"github.com/elaous/gojiberry/xango"
)

func main() {
	html, err := xango.RenderString(Page([]Person{{"Ada", "Engineer"}, {"Grace", "Admiral"}}))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(html)
}
