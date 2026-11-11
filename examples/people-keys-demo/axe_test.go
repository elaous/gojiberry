//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/examples/people-keys-demo/demoserver"
	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestNoAccessibilityViolationsAxe(t *testing.T) {
	site := e2etest.NewSite(t, "../../components/base.css", "../../components/button.css", "../../components/documents.css")
	site.UseLocalhost()
	srv, err := demoserver.New(site.URL)
	if err != nil {
		t.Fatal(err)
	}
	site.Handle("/auth/", srv.Mux)
	site.Handle("/directory/", srv.Mux)
	site.Handle("/demo/", srv.Mux)
	d := site.OpenURL(t, "/index.html", e2etest.Options{Width: 1200, Height: 900, ConsoleErrors: true})
	d.Sleep(1500)
	for _, v := range d.Axe("", "heading-order") {
		t.Errorf("the page: %s", v)
	}
}
