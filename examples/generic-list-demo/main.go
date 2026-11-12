package main

import (
	"fmt"
	"io"
	"os"

	"github.com/elaous/xango"
)

func run(out io.Writer) error {
	html, err := xango.RenderString(Page([]Person{{"Ada", "Engineer"}, {"Grace", "Admiral"}}))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, html)
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
