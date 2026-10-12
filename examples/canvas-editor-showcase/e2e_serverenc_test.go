//go:build !(js && wasm)

package main

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/internal/e2etest"
)

// The end-to-end encrypted store can live on the docstore server the person confirmed: choosing that (and
// reloading) makes the store's target "server", the documents on the server are ciphertext, a reload
// starts locked, and the recovery key unlocks it. A second browser with nothing stored finds encrypted
// files and no key list, and restores the store from the recovery key.
func TestEncryptedStoreOnTheServer(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	backing := docstore.NewMemoryAdapter()
	site.Handle("/docs", docstore.NewServer(backing, "memory").Handler())

	d := site.OpenURL(t, "/index.html?docstore="+url.QueryEscape(site.URL))
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-offer-review")
	d.Click("#dir-offer-review")
	confirmDialog(d)
	location := `#dir-settings .gj-checkbox:has-text("end-to-end encrypted store") input`
	d.WaitSelector(location)
	d.SetChecked(location, true)
	d.WaitSelector("#enc-location-reload")
	d.Click("#enc-location-reload")
	d.WaitSelector(`button[data-tab="save"]`)

	d.VirtualAuthenticator()
	recovery := turnOn(t, d, "")
	d.OK(d.Str(`localStorage.getItem('showcase-enc:config') || ''`) == "", "the key list of the server store is not the browser store's")
	d.Select(`.gj-saver-select[data-field="Save to"]`, "server")
	d.WaitFor("the server target", `document.querySelector('.gj-saver-select[data-field="Save to"]').value === 'server'`)
	d.Sleep(600)
	saveConfidential(d, "Server secret")

	// On the server the files are ciphertext: not a trace of the document's text.
	ctx := context.Background()
	metas, err := backing.List(ctx)
	if err != nil || len(metas) == 0 {
		t.Fatalf("the server holds nothing: %v %v", metas, err)
	}
	encrypted := 0
	for _, m := range metas {
		x, err := backing.Load(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(x.Data), secretDocText) || strings.Contains(string(x.Data), "Server secret") {
			t.Errorf("%s holds plaintext", m.ID)
		}
		if encrypt.IsEncrypted(x.Data) {
			encrypted++
		}
	}
	if encrypted == 0 {
		t.Error("no file on the server is encrypted")
	}

	// A reload starts locked, and the recovery key opens the store again.
	d.Reload()
	d.Click(`button[data-tab="save"]`)
	d.WaitFor("the panel", "document.querySelector('#enc-status')")
	d.OK(d.Text("#enc-status") == "Locked", "a reload starts locked")
	d.Fill("#enc-recovery-input", recovery)
	d.WaitFor("the button", `!document.querySelector('#enc-unlock-recovery').disabled`)
	d.Click("#enc-unlock-recovery")
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)

	// The store keeps when each key was used, from which device: name this one, unlock again, and the keys
	// list says so (the record is in the store, so the other devices read it too).
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#enc-device-label")
	d.Fill("#enc-device-label", "kitchen tablet")
	d.Reload()
	d.Click(`button[data-tab="save"]`)
	d.WaitFor("the panel", "document.querySelector('#enc-status')")
	d.Fill("#enc-recovery-input", recovery)
	d.WaitFor("the button", `!document.querySelector('#enc-unlock-recovery').disabled`)
	d.Click("#enc-unlock-recovery")
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	d.Click(`button[data-tab="people"]`)
	d.WaitFor("the record of this device", `document.body.textContent.includes('Last used on kitchen tablet')`)
	d.WaitSelector("#enc-keylist-text")
	keyList := d.Value("#enc-keylist-text")
	code := strings.TrimPrefix(d.Text("#enc-keylist-code"), "Key list code: ")
	d.OK(strings.Contains(keyList, "gojiberry-keylist/1") && !strings.Contains(keyList, "AGE-SECRET-KEY") && code != "", "the key list is public text with a code: "+code)

	// Another browser with nothing stored: the server has encrypted files but this browser has no key list,
	// which the page says, and the recovery key restores the store.
	other := site.OpenURL(t, "/index.html?docstore="+url.QueryEscape(site.URL))
	other.Click(`button[data-tab="people"]`)
	other.WaitSelector("#dir-offer-review")
	other.Click("#dir-offer-review")
	confirmDialog(other)
	other.SetChecked(location, true)
	other.WaitSelector("#enc-location-reload")
	other.Click("#enc-location-reload")
	other.WaitSelector(`button[data-tab="save"]`)
	other.Click(`button[data-tab="save"]`)
	other.WaitSelector("#enc-keylist-input")
	other.Fill("#enc-keylist-input", keyList)
	other.WaitFor("the button", `!document.querySelector('#enc-keylist-import').disabled`)
	other.Click("#enc-keylist-import")
	d2 := confirmDialog(other)
	other.OK(strings.Contains(d2, code), "the question shows the code to compare: "+d2)
	other.WaitSelector(`button[data-tab="save"]`)
	other.Click(`button[data-tab="save"]`)
	other.WaitFor("the panel", "document.querySelector('#enc-status')")
	other.OK(other.Text("#enc-status") == "Locked", "the second device starts locked with the whole key list")
	other.Fill("#enc-recovery-input", recovery)
	other.WaitFor("the button", `!document.querySelector('#enc-unlock-recovery').disabled`)
	other.Click("#enc-unlock-recovery")
	other.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	other.WaitFor("the document's name", `document.querySelector('.gj-doclist-item .gj-doc-name') && document.querySelector('.gj-doclist-item .gj-doc-name').textContent.includes('Server secret')`)
	other.NoErrors()
	d.NoErrors()
}
