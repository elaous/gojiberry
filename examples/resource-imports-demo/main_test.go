package main

import (
	"os"
	"testing"

	"github.com/elaous/gojiberry/gox"
)

func TestPageRendersConfigTitleAndCSS(t *testing.T) {
	css, err := os.ReadFile("style.css")
	if err != nil {
		t.Fatal(err)
	}
	got, err := gox.RenderString(Page(config))
	if err != nil {
		t.Fatal(err)
	}
	want := `<html><head><title>resource-imports-demo</title><style data-gox-style="github.com/elaous/gojiberry/examples/resource-imports-demo/style.css">` + string(css) + `</style></head><body><h1>gojiberry</h1></body></html>`
	if got != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}
