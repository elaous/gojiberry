//go:build !(js && wasm)

package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/auth/passkey"
	"github.com/elaous/gojiberry/e2etest"
)

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

	d.Fill(".gj-passkey-label", "Again")
	d.Click(".gj-passkey-add")
	d.WaitSelector(".gj-doc-error")
	d.OK(strings.Contains(d.Text(".gj-doc-error"), "already has a passkey"), "a second passkey on the same authenticator is refused: "+d.Text(".gj-doc-error"))
	d.RemoveVirtualAuthenticator(laptop)
	d.VirtualAuthenticator()
	d.Fill(".gj-passkey-label", "Phone")
	d.Click(".gj-passkey-add")
	d.WaitFor("two passkeys", "document.querySelectorAll('.gj-passkey-item').length === 2")
	d.OK(strings.Contains(strings.Join(d.Texts(".gj-passkey-item .gj-doc-name"), ","), "Phone"), "the new passkey has its name")

	d.ReloadPage()
	d.WaitSelector(".gj-passkey-signin")
	d.OK(d.Text("#status") == "not signed in", "the session was in the page only")
	d.Click(".gj-passkey-signin")
	d.WaitFor("signed in with a passkey", `document.getElementById('status').textContent === 'signed in' && document.querySelectorAll('.gj-passkey-item').length === 2`)

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

func TestPasskeyAsAnEncryptionKey(t *testing.T) {
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
	d.Fill(".gj-passkey-email", "ana@opendocs.io")
	d.WaitFor("the create button", `!document.querySelector('.gj-passkey-create').disabled`)
	d.Click(".gj-passkey-create")
	d.WaitFor("signed in", `document.getElementById('status').textContent === 'signed in'`)

	d.Click("#derive")
	d.WaitFor("a key", `document.getElementById('derived').textContent.startsWith('age1')`)
	key := d.Text("#derived")
	d.Eval(`document.getElementById('derived').textContent = ''`, nil)
	d.Click("#derive")
	d.WaitFor("the key again", `document.getElementById('derived').textContent.startsWith('age1')`)
	d.OK(d.Text("#derived") == key, "the same passkey gives the same key: "+key+" / "+d.Text("#derived"))
}
