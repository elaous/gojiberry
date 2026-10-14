//go:build !(js && wasm)

package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/auth/passkey"
	"github.com/elaous/gojiberry/internal/e2etest"
)

// The passkey page is under the accessibility gate, signed out and signed in. Heading order is
// exempt: the panel component brings its own h4, to sit under the host page's headings.
func TestNoAccessibilityViolationsAxe(t *testing.T) {
	site := e2etest.NewSite(t, "../../components/base.css", "../../components/button.css", "../../components/documents.css")
	site.UseLocalhost()
	mgr, err := auth.NewManager(context.Background(), auth.Config{BaseURL: site.URL})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := passkey.New(passkey.Config{RPID: "localhost", RPName: "demo", Origins: []string{site.URL}}, passkey.NewMemoryStore(), mgr)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/auth/passkey/", svc.Handler())
	mux.Handle("/auth/", mgr.Routes())
	site.Handle("/auth/", mux)

	d := site.OpenURL(t, "/index.html", e2etest.Options{ConsoleErrors: true})
	d.VirtualAuthenticator()
	d.WaitSelector(".gj-passkey")
	for _, v := range d.Axe("", "heading-order") {
		t.Errorf("signed out: %s", v)
	}
	d.Fill(".gj-passkey-email", "ana@opendocs.io")
	d.WaitFor("the create button", `!document.querySelector('.gj-passkey-create').disabled`)
	d.Click(".gj-passkey-create")
	d.WaitFor("signed in", `document.getElementById('status').textContent === 'signed in' && document.querySelectorAll('.gj-passkey-item').length === 1`)
	for _, v := range d.Axe("", "heading-order") {
		t.Errorf("signed in: %s", v)
	}
}
