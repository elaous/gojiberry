//go:build !(js && wasm)

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/examples/people-keys-demo/demoserver"
	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestPeopleKeysInARealBrowser runs the People tab's key controls in headless Chrome with a
// virtual authenticator that does PRF: the second-key rule, a recovery key shown once, "add my
// passkey" with no typing, and the verified mark, which comes from the server's directory only.
func TestPeopleKeysInARealBrowser(t *testing.T) {
	site := e2etest.NewSite(t, "../../components/base.css", "../../components/button.css", "../../components/documents.css")
	site.UseLocalhost()
	srv, err := demoserver.New(site.URL)
	if err != nil {
		t.Fatal(err)
	}
	site.Handle("/auth/", srv.Mux)
	site.Handle("/directory/", srv.Mux)
	site.Handle("/demo/", srv.Mux)

	// Bo, signed in with a provider that verified bo@example.com, publishes a key.
	publish := func(email, key string) int {
		t.Helper()
		tok, err := srv.Login(email)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/directory/publish", strings.NewReader(`{"key":"`+key+`","label":"Bo's recovery key"}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		srv.Mux.ServeHTTP(rec, req)
		return rec.Code
	}
	boPub, _, err := encrypt.GenerateRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	if code := publish("bo@example.com", boPub); code != http.StatusOK {
		t.Fatalf("publish = %d", code)
	}
	// A passkey session cannot publish or look up, even with an address typed at registration.
	pkTok, err := srv.Auth.StartSession(auth.User{ID: "passkey:x", Email: "bo@example.com", VerifiedEmail: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/directory/publish", `{"key":"` + boPub + `"}`},
		{"GET", "/directory/lookup?email=bo@example.com", ""},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		req.Header.Set("Authorization", "Bearer "+pkTok)
		rec := httptest.NewRecorder()
		srv.Mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("passkey session %s %s = %d, want 403", c.method, c.path, rec.Code)
		}
	}
	strangerPub, _, err := encrypt.GenerateRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}

	d := site.OpenURL(t, "/index.html", e2etest.Options{ConsoleErrors: true})
	d.VirtualAuthenticator()
	d.WaitSelector("#make-passkey")
	d.Click("#make-passkey")
	d.WaitFor("a passkey account", `document.getElementById('status').textContent === 'signed in'`)

	// Second-key rule: no key but a passkey would be left, so the page says so and the button is off.
	d.WaitSelector(".gj-recip-need-second")
	d.OK(strings.Contains(d.Text(".gj-recip-need-second"), "a passkey cannot be the only key"), "the second-key message: "+d.Text(".gj-recip-need-second"))
	d.OK(d.Disabled(".gj-recip-add-mine"), "add my passkey waits for a recovery key")

	// A recovery key: shown once, with its warning, and gone after "I have saved it".
	d.Click(".gj-recip-recovery")
	d.WaitSelector(".gj-recip-secret")
	secret := d.Value(".gj-recip-secret")
	d.OK(strings.HasPrefix(secret, "AGE-SECRET-KEY-1"), "the recovery secret is shown: "+secret)
	d.OK(d.Has(".gj-recip-recovery-warning"), "with a warning that it cannot be recovered")
	d.OK(d.Has(".gj-recip-need-second") && d.Count(".gj-recip-owner") == 0, "the key is not an owner key until the secret is confirmed saved")
	d.SetChecked(".gj-recip-recovery-ack input", true)
	d.WaitFor("the commit button", `!document.querySelector('.gj-recip-recovery-commit').disabled`)
	d.Click(".gj-recip-recovery-commit")
	d.WaitFor("the secret to be dropped", `!document.querySelector('.gj-recip-secret')`)
	d.OK(!d.Has(".gj-recip-need-second"), "a recovery key lifts the rule once it is added")
	d.OK(!strings.Contains(d.Str(`document.body.innerHTML`), secret), "the secret is nowhere in the page once saved")
	d.OK(d.Count(".gj-recip-owner") == 1, "the recovery key is listed as an owner key")

	// Add my passkey: no fingerprint is typed.
	d.OK(!d.Disabled(".gj-recip-add-mine"), "the button is on")
	d.Click(".gj-recip-add-mine")
	d.WaitSelector(".gj-recip-my-fingerprint")
	fp := d.Text(".gj-recip-my-fingerprint")
	d.OK(strings.HasPrefix(fp, "SHA256:"), "the passkey's key was added: "+fp)
	d.OK(d.Count(".gj-recip-owner") == 2 && strings.Contains(strings.Join(d.Texts(".gj-recip-owner .gj-recip-type"), ","), "passkey-prf"), "it is listed as an owner key")

	// A pasted key is label only, whatever its text says.
	d.Fill(".gj-recip-key", strangerPub)
	d.WaitSelector(".gj-recip-parsed-fingerprint")
	d.OK(d.Has(".gj-recip-label-only") && !d.Has(".gj-recip-verified"), "a pasted key is label only")

	// A directory lookup: Bo's key is verified because the server says so.
	d.Fill(".gj-recip-dir-email", "bo@example.com")
	d.WaitFor("the find button", `!document.querySelector('.gj-recip-dir-find').disabled`)
	d.Click(".gj-recip-dir-find")
	d.WaitSelector(".gj-recip-dir-row")
	d.OK(d.Count(".gj-recip-dir-row") == 1 && d.Has(".gj-recip-dir-row .gj-recip-verified"), "the published key is listed with the verified mark")
	d.Click(".gj-recip-pick")
	d.WaitFor("the key as a draft", `document.querySelector('.gj-recip-parsed-fingerprint') && document.querySelector('.gj-recip-parsed .gj-recip-verified, .gj-recip-form .gj-recip-verified')`)
	bobFP := d.Text(".gj-recip-parsed-fingerprint")
	d.Fill(".gj-recip-confirm", bobFP)
	d.WaitFor("the add button", `!document.querySelector('.gj-recip-add').disabled`)
	d.Click(".gj-recip-add")
	d.WaitFor("Bo in the list", `document.querySelectorAll('.gj-recip-item:not(.gj-recip-owner):not(.gj-recip-dir-row)').length === 1`)
	d.OK(d.Has(`.gj-recip-item:not(.gj-recip-owner):not(.gj-recip-dir-row) .gj-recip-verified`), "Bo's row keeps the verified mark")

	// Someone with no published key finds nothing.
	d.Fill(".gj-recip-dir-email", "nobody@example.com")
	d.WaitFor("the find button", `!document.querySelector('.gj-recip-dir-find').disabled`)
	d.Click(".gj-recip-dir-find")
	d.WaitSelector(".gj-recip-dir-none")
	d.NoErrors()
}

// lookupAs is a lookup for address, made through the demo's routes by a provider user.
func lookupAs(t *testing.T, srv *demoserver.Server, asEmail, address string) []docstore.DirectoryKey {
	t.Helper()
	tok, err := srv.Login(asEmail)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/directory/lookup?email="+address, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	srv.Mux.ServeHTTP(rec, req)
	var out struct {
		Keys []docstore.DirectoryKey `json:"keys"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("lookup: %d %s", rec.Code, rec.Body)
	}
	return out.Keys
}

// eventually polls cond (the page acts on its own goroutine) until it holds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for range 100 {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestMyDirectoryEntryInARealBrowser runs the owner-held identity in headless Chrome with a
// virtual authenticator that does PRF: the passkey's key is published with proof of possession
// made by the passkey itself, an owner key is made and kept in the browser, every later publish is
// signed by it (the mark a lookup shows is checked by the client), and the watcher reports an
// entry for my address that the page did not make, and a forged one that the owner rules expose.
func TestMyDirectoryEntryInARealBrowser(t *testing.T) {
	site := e2etest.NewSite(t, "../../components/base.css", "../../components/button.css", "../../components/documents.css")
	site.UseLocalhost()
	srv, err := demoserver.New(site.URL)
	if err != nil {
		t.Fatal(err)
	}
	site.Handle("/auth/", srv.Mux)
	site.Handle("/directory/", srv.Mux)
	site.Handle("/demo/", srv.Mux)

	d := site.OpenURL(t, "/index.html", e2etest.Options{ConsoleErrors: true})
	d.VirtualAuthenticator()
	d.WaitSelector("#make-passkey")
	d.Click("#make-passkey")
	d.WaitFor("a passkey account", `document.getElementById('status').textContent === 'signed in'`)
	// A passkey cannot be the only key: a recovery key first, then my passkey.
	d.Click(".gj-recip-recovery")
	d.WaitSelector(".gj-recip-secret")
	d.Click(".gj-recip-recovery-done")
	d.WaitFor("the secret to be dropped", `!document.querySelector('.gj-recip-secret')`)
	d.Click(".gj-recip-add-mine")
	d.WaitSelector(".gj-recip-my-fingerprint")

	// Publish my passkey's key: the page answers the challenge with the passkey's PRF identity.
	d.Click("#dir-publish")
	d.WaitFor("the publication", `document.getElementById('dir-status').textContent.startsWith('published')`)
	keys := lookupAs(t, srv, "bo@example.com", "ana@example.com")
	if len(keys) != 1 || !keys[0].Verified || !keys[0].Held || keys[0].OwnerEnrolled || keys[0].OwnerSigned {
		t.Fatalf("published key: %+v", keys)
	}

	// Somebody else, with a session of my address, publishes a key for it: the watcher says so.
	strangerPub, _, err := encrypt.GenerateRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := srv.Login("ana@example.com")
	req := httptest.NewRequest("POST", "/directory/publish", strings.NewReader(`{"key":"`+strangerPub+`","label":"not mine"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	srv.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish by another device: %d %s", rec.Code, rec.Body)
	}
	d.Click("#dir-watch")
	d.WaitFor("the alert", `document.querySelectorAll('#dir-alerts .dir-alert').length === 1`)
	d.OK(strings.Contains(d.Text("#dir-alerts .dir-alert"), "someone published a key for your address"), "the alert: "+d.Text("#dir-alerts .dir-alert"))
	d.OK(strings.Contains(d.Text("#dir-status"), "log checked"), "status: "+d.Text("#dir-status"))

	// Enroll an owner key (made in the browser); the next publish is signed by it.
	d.Click("#dir-enroll")
	d.WaitFor("the enrollment", `document.getElementById('dir-owner').textContent.indexOf('owner key held') === 0`)
	d.OK(d.Str(`localStorage.getItem('gj-owner-key') || ''`) != "", "the owner key is kept by the page, not the server")
	d.Click("#dir-publish")
	eventually(t, "the owner-signed publication", func() bool {
		for _, k := range lookupAs(t, srv, "bo@example.com", "ana@example.com") {
			if k.OwnerSigned && k.Held {
				return true
			}
		}
		return false
	})
	// The session alone, from another device, can no longer publish for the address.
	req = httptest.NewRequest("POST", "/directory/publish", strings.NewReader(`{"key":"`+strangerPub+`"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	srv.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("publish without the owner key: %d", rec.Code)
	}

	// The picker shows the strongest mark, because the client checked the signature.
	d.Fill(".gj-recip-dir-email", "ana@example.com")
	d.WaitFor("the find button", `!document.querySelector('.gj-recip-dir-find').disabled`)
	d.Click(".gj-recip-dir-find")
	d.WaitSelector(".gj-recip-dir-row")
	d.OK(d.Has(".gj-recip-dir-row .gj-recip-owner-signed"), "an owner-signed key shows the owner mark")
	d.OK(strings.Contains(d.Text(".gj-recip-dir-row .gj-recip-owner-signed"), "signed by its owner"), "the mark's text: "+d.Text(".gj-recip-dir-row .gj-recip-owner-signed"))

	// A server that holds the directory key writes an entry for my address with no owner
	// signature: the watcher shows that the owner rules were broken.
	ctx := context.Background()
	head, err := srv.Dir.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	forged := encrypt.SignLogEntry(srv.Dir.Signer, encrypt.LogEntry{Seq: head.Seq + 1, Prev: head.Hash, At: time.Now().Unix(), Op: encrypt.OpPublish,
		Address: srv.Dir.LogAddressFor("ana@example.com"), Fingerprint: "SHA256:forged"})
	data, _ := json.Marshal(forged)
	if err := srv.Store.Save(ctx, fmt.Sprintf("directory-log/e/%012d", forged.Seq), data); err != nil {
		t.Fatal(err)
	}
	d.Click("#dir-watch")
	d.WaitFor("the violation", `Array.from(document.querySelectorAll('#dir-alerts .dir-alert')).some(e => e.textContent.indexOf('breaks the owner rules') >= 0)`)

	// Revoke it, signed by the owner key.
	d.Click("#dir-revoke")
	eventually(t, "the revocation", func() bool {
		for _, k := range lookupAs(t, srv, "bo@example.com", "ana@example.com") {
			if k.Fingerprint == d.Text(".gj-recip-my-fingerprint") {
				return false
			}
		}
		return true
	})
	d.NoErrors()
}
