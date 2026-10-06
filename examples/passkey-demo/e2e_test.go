//go:build !(js && wasm)

package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/auth/passkey"
	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestPasskeysInARealBrowser runs the whole thing in headless Chrome with a virtual authenticator
// (no dialog): make an account with a passkey, add a second, sign in again without typing, and
// remove one; the last one stays.
func TestPasskeysInARealBrowser(t *testing.T) {
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
	laptop := d.VirtualAuthenticator()
	d.WaitSelector(".gj-passkey")
	d.OK(d.Has(".gj-passkey-signin") && d.Has(".gj-passkey-create"), "signed out: the page offers sign-in and sign-up: "+d.Text("#status"))
	d.OK(strings.Contains(d.Text(".gj-passkey-note"), "two factors in one"), "it says a passkey is two factors")

	d.Fill(".gj-passkey-email", "ana@opendocs.io")
	d.WaitFor("the create button", `!document.querySelector('.gj-passkey-create').disabled`)
	d.Click(".gj-passkey-create")
	d.WaitFor("a new account signed in", `document.getElementById('status').textContent === 'signed in' && document.querySelectorAll('.gj-passkey-item').length === 1`)

	// The same authenticator cannot hold two passkeys for one account: the page says so.
	d.Fill(".gj-passkey-label", "Again")
	d.Click(".gj-passkey-add")
	d.WaitSelector(".gj-doc-error")
	d.OK(strings.Contains(d.Text(".gj-doc-error"), "already has a passkey"), "a second passkey on the same authenticator is refused: "+d.Text(".gj-doc-error"))
	// A second passkey, on another authenticator (a phone), named.
	d.RemoveVirtualAuthenticator(laptop)
	d.VirtualAuthenticator()
	d.Fill(".gj-passkey-label", "Phone")
	d.Click(".gj-passkey-add")
	d.WaitFor("two passkeys", "document.querySelectorAll('.gj-passkey-item').length === 2")
	d.OK(strings.Contains(strings.Join(d.Texts(".gj-passkey-item .gj-doc-name"), ","), "Phone"), "the new passkey has its name")

	// A fresh visit: sign in with no user name, the authenticator offers the account.
	d.ReloadPage()
	d.WaitSelector(".gj-passkey-signin")
	d.OK(d.Text("#status") == "not signed in", "the session was in the page only")
	d.Click(".gj-passkey-signin")
	d.WaitFor("signed in with a passkey", `document.getElementById('status').textContent === 'signed in' && document.querySelectorAll('.gj-passkey-item').length === 2`)

	// Remove one (asks first); the last stays.
	confirm := func() { d.Click(`[role="alertdialog"] .gj-dialog-actions button:last-child`) }
	d.Click(".gj-passkey-item:last-child .gj-passkey-remove")
	confirm()
	d.WaitFor("one passkey", "document.querySelectorAll('.gj-passkey-item').length === 1")
	d.Click(".gj-passkey-remove")
	confirm()
	d.WaitSelector(".gj-doc-error")
	d.OK(strings.Contains(d.Text(".gj-doc-error"), "only way to sign in"), "the last passkey stays, and says why: "+d.Text(".gj-doc-error"))
	d.OK(d.Count(".gj-passkey-item") == 1, "still one")
	d.NoErrors()
}
