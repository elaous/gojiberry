package main

import (
	"testing"

	"github.com/elaous/gojiberry/gox"
)

func TestPageRendersInferredConfigTitle(t *testing.T) {
	got, err := gox.RenderString(Page(config))
	if err != nil {
		t.Fatal(err)
	}
	want := `<html><body><h1>gojiberry</h1></body></html>`
	if got != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}
