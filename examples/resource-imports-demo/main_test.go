package main

import (
	"testing"

	"github.com/elaous/gojiberry/gox"
)

func TestPageRendersConfigTitleAndCSS(t *testing.T) {
	got, err := gox.RenderString(Page(config))
	if err != nil {
		t.Fatal(err)
	}
	want := `<html><head><style>` + style_css + `</style></head><body><h1>gojiberry</h1></body></html>`
	if got != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}
