//go:build !(js && wasm)

package main

import (
	"context"
	"fmt"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/examples/canvas-editor-showcase/dirclient"
	"github.com/elaous/gojiberry/examples/people-keys-demo/demoserver"
	"github.com/elaous/gojiberry/internal/e2etest"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func turnOn(t *testing.T, d *e2etest.Tab, sshPub string) string {
	t.Helper()
	d.Click(`button[data-tab="save"]`)
	d.WaitSelector("#enc-panel")
	d.Click("#enc-switch-box .gj-switch-track")
	d.WaitSelector("#enc-enable")
	if sshPub != "" {
		d.Fill("#enc-ssh", sshPub)
	}
	d.Click("#enc-enable")
	d.WaitSelector("#enc-recovery-secret")
	secret := d.Value("#enc-recovery-secret")
	d.SetChecked("#enc-recovery-ack input", true)
	d.WaitFor("the commit button", `!document.querySelector('#enc-recovery-commit').disabled`)
	d.Click("#enc-recovery-commit")
	d.WaitSelector("#enc-lock") // unlocked (the word for it depends on the language)
	return secret
}

func saveConfidential(d *e2etest.Tab, name string) {
	d.Fill(".gj-saver-name", name)
	d.Click(".gj-saver-save")
	d.WaitFor("the document", "document.querySelectorAll('.gj-doclist-item').length >= 1")
	d.Click(".gj-doclist-protect")
	confirmDialog(d)
	d.WaitFor("confidential", "document.querySelector('.gj-doclist-item .gj-doc-confidential')")
}

func TestLockClosesTheOpenDocumentAndAsksAboutUnsavedEdits(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	saveConfidential(d, "Lockme plan")
	d.Click(".gj-doclist-open")
	d.WaitFor("opened", `document.querySelector('.gj-saver-status').textContent.includes('Lockme plan opened')`)
	d.OK(strings.Contains(d.State().Str("text"), "Everything on this canvas"), "the document is in the editor")

	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Type("UNSAVEDWORDS ")
	d.WaitFor("unsaved", `JSON.parse(document.getElementById('state').textContent).saveState === 'unsaved'`)
	d.OK(strings.Contains(d.Text("#enc-note"), "closes the document that is open"), "the note says what Lock does: "+d.Text("#enc-note"))
	d.Click("#enc-lock")
	d.WaitSelector(`[role="alertdialog"]`)
	d.OK(strings.Contains(d.Text(`[role="alertdialog"]`), "changes that are not saved") && strings.Contains(d.Text(`[role="alertdialog"] .gj-dialog-actions button:last-child`), "Save and lock"), "it asks: "+d.Text(`[role="alertdialog"]`))
	d.Click(`[role="alertdialog"] .gj-dialog-actions button:first-child`) // cancel: keep working
	d.WaitFor("the question closed", `!document.querySelector('[role="alertdialog"]')`)
	d.OK(d.Text("#enc-status") == "Unlocked" && strings.Contains(d.State().Str("text"), "UNSAVEDWORDS"), "cancelling keeps the store unlocked and the edit on screen")

	d.Click("#enc-lock")
	confirmDialog(d) // Save and lock
	d.WaitFor("locked", `(document.getElementById('enc-status') || {}).textContent === 'Locked'`)
	st := d.State()
	d.OK(st.Str("text") == "" && st.Str("title") == "" && st.Str("saveState") == "", fmt.Sprintf("the editor is cleared: text %q title %q saveState %q", st.Str("text"), st.Str("title"), st.Str("saveState")))
	d.OK(d.Value(".gj-saver-name") == "", "the document name is cleared: "+d.Value(".gj-saver-name"))
	d.OK(!strings.Contains(d.Str(`document.body.innerHTML`), "Lockme plan"), "the real name is nowhere on the page")
	d.WaitFor("the placeholder", `document.querySelector('.gj-doclist-item .gj-doc-name').textContent.startsWith('Confidential document')`)

	d.Click("#enc-unlock-passkey")
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	d.WaitFor("the name revealed", `document.querySelector('.gj-doclist-item .gj-doc-name').textContent.includes('Lockme plan')`)
	d.Click(".gj-doclist-open")
	d.WaitFor("opened", `JSON.parse(document.getElementById('state').textContent).text.includes('UNSAVEDWORDS')`)
	d.NoErrors()
}

func TestEraseTheEncryptedStore(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	d.Eval(`localStorage.setItem('some-other-app:note', 'keep me')`, nil)
	saveConfidential(d, "Doomed")
	d.Click("#enc-lock")
	d.WaitFor("locked", `(document.getElementById('enc-status') || {}).textContent === 'Locked'`)
	d.OK(strings.Contains(d.Text("#enc-erase-hint"), "Lost every key?"), "the way out is offered, in words: "+d.Text("#enc-erase-hint"))
	d.OK(strings.Contains(d.Text("#enc-erase-hint"), "nothing saved anywhere else"), "it says it touches no other target")

	d.Click("#enc-erase")
	question := confirmDialog(d)
	d.OK(strings.Contains(question, "cannot be undone") && strings.Contains(question, "for good"), "the first question says what it does: "+question)
	d.WaitSelector("#enc-erase-word")
	d.OK(d.Disabled("#enc-erase-go"), "nothing is erased until the word is typed")
	d.Fill("#enc-erase-word", "nope")
	d.OK(d.Disabled("#enc-erase-go"), "the wrong word does not do")
	d.Fill("#enc-erase-word", "ERASE")
	d.WaitFor("the button", `!document.querySelector('#enc-erase-go').disabled`)
	d.Click("#enc-erase-go")
	d.WaitFor("erased", `!JSON.parse(document.getElementById('state').textContent).encryption.enabled`)
	d.OK(d.Int(`Object.keys(localStorage).filter((k) => (k.startsWith('showcase:') && !k.startsWith('showcase:catalog:') && k !== 'showcase:last-target') || k === 'showcase-enc:config').length`) == 0, "the store's documents and key list are gone (a fresh empty catalog may be written)")
	d.OK(d.Str(`localStorage.getItem('some-other-app:note')`) == "keep me", "nothing else in the storage was touched")
	d.OK(!strings.Contains(storedText(d), "Doomed"), "the document is gone")

	d.WaitSelector("#enc-switch-box")
	d.Click("#enc-switch-box .gj-switch-track")
	d.WaitSelector("#enc-enable")
	d.NoErrors()
}

func TestRemoveAKeyAndReplaceThePasskey(t *testing.T) {
	pub, _ := sshKeyPair(t, "me@laptop")
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	recovery := turnOn(t, d, pub)
	saveConfidential(d, "Kept through changes")

	d.Click(`button[data-tab="people"]`)
	d.WaitFor("three owner keys", `document.querySelectorAll('.gj-recip-owner').length === 3`)
	rowOf := func(typ string) string {
		return d.Str(`(() => { const r = Array.from(document.querySelectorAll('.gj-recip-owner')).find((li) => li.querySelector('.gj-recip-type').textContent === '` + typ + `'); return r ? r.dataset.fingerprint : ''; })()`)
	}
	passkeyFP, recoveryFP, sshFP := rowOf("passkey-prf"), rowOf("age-x25519"), rowOf("ssh-ed25519")
	d.OK(passkeyFP != "" && recoveryFP != "" && sshFP != "", "the three keys are listed")
	btnOf := func(fp, class string) string { return `.gj-recip-owner[data-fingerprint="` + fp + `"] .` + class }
	d.OK(d.Disabled(btnOf(passkeyFP, "gj-recip-remove-owner")) && strings.Contains(d.Text(".gj-recip-remove-blocked"), "different key"), "the key you are unlocked with cannot be removed: "+d.Text(".gj-recip-remove-blocked"))
	d.OK(!d.Disabled(btnOf(sshFP, "gj-recip-remove-owner")) && !d.Disabled(btnOf(recoveryFP, "gj-recip-remove-owner")), "the others can")

	d.Click(btnOf(sshFP, "gj-recip-remove-owner"))
	question := confirmDialog(d)
	d.OK(strings.Contains(question, "encrypted again without it") && strings.Contains(question, "keeps what they already read"), "the question says what it does and does not do: "+question)
	d.WaitFor("two owner keys", `document.querySelectorAll('.gj-recip-owner').length === 2`)
	d.WaitFor("the notice", `(document.querySelector('.gj-recip-notice') || {textContent: ''}).textContent.includes('encrypted again without it')`)
	d.OK(d.Disabled(btnOf(recoveryFP, "gj-recip-remove-owner")), "the recovery key is kept: a passkey cannot be the only key")

	d.Click(".gj-recip-replace")
	confirmDialog(d)
	d.WaitFor("replaced", `(document.querySelector('.gj-recip-notice') || {textContent: ''}).textContent.includes('was replaced')`)
	d.OK(rowOf("passkey-prf") != "" && rowOf("passkey-prf") != passkeyFP, "the passkey is another key now")

	d.Click(`button[data-tab="save"]`)
	d.Click("#enc-lock")
	d.WaitFor("locked", `(document.getElementById('enc-status') || {}).textContent === 'Locked'`)
	d.Click("#enc-unlock-passkey")
	d.WaitFor("unlocked with the new passkey", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	d.WaitFor("the name revealed", `document.querySelector('.gj-doclist-item .gj-doc-name').textContent.includes('Kept through changes')`)
	d.Click("#enc-lock")
	d.WaitFor("locked", `(document.getElementById('enc-status') || {}).textContent === 'Locked'`)
	d.Fill("#enc-recovery-input", recovery)
	d.WaitFor("the button", `!document.querySelector('#enc-unlock-recovery').disabled`)
	d.Click("#enc-unlock-recovery")
	d.WaitFor("unlocked with the recovery key (its seal predates the changes)", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	d.Click(".gj-doclist-open")
	d.WaitFor("opened", `document.querySelector('.gj-saver-status').textContent.includes('Kept through changes opened')`)
	d.NoErrors()
}

func TestAChangedKeyListIsRefusedAtUnlock(t *testing.T) {
	attacker, _, err := encrypt.GenerateRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	recovery := turnOn(t, d, "")
	d.Reload()
	d.Click(`button[data-tab="save"]`)
	d.WaitFor("locked after a reload", `(document.getElementById('enc-status') || {}).textContent === 'Locked'`)
	d.Eval(`(() => {
		const c = JSON.parse(localStorage.getItem('showcase-enc:config'));
		const o = Object.assign({}, c.owners[1]);
		o.public = '`+attacker+`';
		o.label = 'helpful backup';
		c.owners.push(o);
		localStorage.setItem('showcase-enc:config', JSON.stringify(c));
		return 1;
	})()`, nil)
	d.Reload()
	d.Click(`button[data-tab="save"]`)
	d.WaitSelector("#enc-unlock-passkey")
	d.Click("#enc-unlock-passkey")
	d.WaitSelector("#enc-error")
	d.OK(strings.Contains(d.Text("#enc-error"), "configuration on this device was changed") && d.Text("#enc-status") == "Locked", "the passkey is refused: "+d.Text("#enc-error"))
	d.Fill("#enc-recovery-input", recovery)
	d.WaitFor("the button", `!document.querySelector('#enc-unlock-recovery').disabled`)
	d.Click("#enc-unlock-recovery")
	d.WaitFor("the recovery key refused too", `(document.getElementById('enc-error') || {textContent: ''}).textContent.includes('was changed')`)
	d.OK(d.Text("#enc-status") == "Locked", "still locked")
	d.NoErrors()
}

func TestAnInterruptedEnableIsFinishedAfterAReload(t *testing.T) {
	pub, priv := sshKeyPair(t, "me@laptop")
	site := e2etest.NewSite(t)
	d := site.Open(t, "")
	d.Eval(`localStorage.clear()`, nil)
	d.Click(`button[data-tab="save"]`)
	for _, name := range []string{"First plain", "Second plain", "Third plain"} {
		d.Fill(".gj-saver-name", name)
		d.Click(".gj-saver-save")
		d.WaitFor(name, `document.querySelector('.gj-saver-status').textContent.includes('`+name+`')`)
	}
	d.Click("#enc-switch-box .gj-switch-track")
	d.WaitSelector("#enc-enable")
	d.SetChecked("#enc-opt-passkey input", false)
	d.SetChecked("#enc-opt-recovery input", false)
	d.Fill("#enc-ssh", pub)
	d.Eval(`(() => {
		const orig = Storage.prototype.setItem;
		let n = 0;
		Storage.prototype.setItem = function (k, v) {
			if (k.startsWith('showcase:')) { n++; if (n >= 2) throw new Error('QuotaExceededError'); }
			return orig.call(this, k, v);
		};
		return 1;
	})()`, nil)
	d.Click("#enc-enable")
	d.WaitSelector("#enc-error")
	d.OK(strings.Contains(d.Text("#enc-error"), "could not be undone completely"), "the page says it could not undo: "+d.Text("#enc-error"))

	d.Reload() // the browser is healthy again
	d.Click(`button[data-tab="save"]`)
	d.WaitSelector("#enc-interrupted")
	d.OK(strings.Contains(d.Text("#enc-interrupted-text"), "did not finish") && d.State().Bool("encryption.interrupted"), "the next load knows: "+d.Text("#enc-interrupted-text"))
	d.OK(d.Has("#enc-finish") && d.Has("#enc-undo"), "finish and undo are offered")
	d.OK(strings.Contains(d.Text("#enc-interrupted-text"), "SHA256:"), "the journal's keys are shown with their fingerprints: "+d.Text("#enc-interrupted-text"))
	d.Click("#enc-finish")
	d.WaitSelector(`[role="alertdialog"]`)
	d.OK(strings.Contains(d.Text(`[role="alertdialog"]`), "SHA256:") && strings.Contains(d.Text(`[role="alertdialog"]`), "me@laptop"), "the question names the keys: "+d.Text(`[role="alertdialog"]`))
	d.Click(`[role="alertdialog"] .gj-dialog-actions button:first-child`) // cancel
	d.OK(d.Has("#enc-interrupted"), "cancelling leaves it as it was")
	d.Click("#enc-finish")
	confirmDialog(d)
	d.WaitSelector("#enc-error")
	d.OK(strings.Contains(d.Text("#enc-error"), "not vouched for"), "refused without the key: "+d.Text("#enc-error"))
	d.ChooseFile(func() { d.Click("#enc-unlock-ssh") }, "id_ed25519", priv)
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	d.Click("#enc-finish")
	confirmDialog(d)
	d.WaitFor("finished", `!document.querySelector('#enc-interrupted') && (document.getElementById('enc-message') || {textContent: ''}).textContent.includes('was finished')`)
	d.OK(strings.Contains(storedText(d), "Third plain"), "the catalog still lists the document")
	d.OK(!strings.Contains(storedText(d), "Everything on this canvas"), "no document text is left in the clear")
	d.Reload()
	d.Click(`button[data-tab="save"]`)
	d.ChooseFile(func() { d.Click("#enc-unlock-ssh") }, "id_ed25519", priv)
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	d.Click(".gj-doclist-open")
	d.WaitFor("a document opens", `document.querySelector('.gj-saver-status').textContent.includes('opened')`)
	d.NoErrors()
}

func TestAStorageQuotaFailureLeavesTheStoreAsItWas(t *testing.T) {
	pub, _ := sshKeyPair(t, "me@laptop")
	site := e2etest.NewSite(t)
	d := site.Open(t, "")
	d.Eval(`localStorage.clear()`, nil)
	d.Click(`button[data-tab="save"]`)
	for _, name := range []string{"Plain one", "Plain two"} {
		d.Fill(".gj-saver-name", name)
		d.Click(".gj-saver-save")
		d.WaitFor(name, `document.querySelector('.gj-saver-status').textContent.includes('`+name+`')`)
	}
	before := storedText(d)
	d.Click("#enc-switch-box .gj-switch-track")
	d.WaitSelector("#enc-enable")
	d.SetChecked("#enc-opt-passkey input", false)
	d.SetChecked("#enc-opt-recovery input", false)
	d.Fill("#enc-ssh", pub)
	d.Eval(`(() => {
		const orig = Storage.prototype.setItem;
		const size = () => Object.keys(localStorage).reduce((n, k) => n + k.length + localStorage.getItem(k).length, 0);
		let limit = size() + 250;
		Storage.prototype.setItem = function (k, v) {
			if (!k.startsWith('showcase:')) return orig.call(this, k, v); // the key list is small and kept
			const old = localStorage.getItem(k);
			if (old && String(v).length <= old.length) return orig.call(this, k, v); // a write that grows nothing always fits
			if (size() - (old ? k.length + old.length : 0) + k.length + String(v).length > limit) throw new Error('QuotaExceededError');
			return orig.call(this, k, v);
		};
		return 1;
	})()`, nil)
	d.Click("#enc-enable")
	d.WaitSelector("#enc-error")
	d.OK(strings.Contains(d.Text("#enc-error"), "storage") || strings.Contains(d.Text("#enc-error"), "room") || strings.Contains(d.Text("#enc-error"), "key list"), "the page says the storage is the problem: "+d.Text("#enc-error"))
	d.OK(!d.State().Bool("encryption.enabled") && !d.State().Bool("encryption.interrupted"), "encryption is not on, and nothing is left half done")
	after := storedText(d)
	d.OK(strings.Contains(after, "Everything on this canvas") && !strings.Contains(after, "showcase-enc:config"), "the documents are as they were (clear) and no key list was kept")
	_ = before
	d.NoErrors()
}

func TestPeopleTabUsesTheKeyDirectory(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	srv, err := demoserver.New(site.URL)
	if err != nil {
		t.Fatal(err)
	}
	site.Handle("/auth/", srv.Mux)
	site.Handle("/directory/", srv.Mux)
	site.Handle("/demo/", srv.Mux)
	boPub, _, err := encrypt.GenerateRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	tok, err := srv.Login("bo@example.com")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/directory/publish", strings.NewReader(`{"key":"`+boPub+`","label":"Bo's key"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	srv.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish = %d", rec.Code)
	}

	d := site.OpenURL(t, "/index.html?docstore="+url.QueryEscape(site.URL))
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	saveConfidential(d, "For Bo")
	d.Click(`button[data-tab="people"]`)
	dirHost := strings.TrimPrefix(site.URL, "http://")
	d.WaitSelector("#dir-offer")
	d.OK(d.State().Str("encryption.dir.url") == "" && d.State().Str("encryption.dir.offer") == site.URL && !d.Has(".gj-recip-dir-email"), "a link sets no directory: "+d.Text("#dir-status"))
	d.OK(d.Str(`localStorage.getItem('showcase-enc:docstore-ok') || ''`) == "", "and nothing is saved as confirmed")
	d.Click("#dir-offer-review")
	d.WaitSelector(`[role="alertdialog"]`)
	d.OK(strings.Contains(d.Text(`[role="alertdialog"]`), dirHost), "the question names the host: "+d.Text(`[role="alertdialog"]`))
	d.Click(`[role="alertdialog"] .gj-dialog-actions button:first-child`) // cancel
	d.OK(d.State().Str("encryption.dir.url") == "" && !d.Has(".gj-recip-dir-email"), "cancelling uses nothing")
	d.Click("#dir-offer-review")
	confirmDialog(d)
	d.WaitSelector(".gj-recip-dir-email")
	d.OK(strings.Contains(d.Text("#dir-status"), site.URL), "the directory is the one confirmed: "+d.Text("#dir-status"))

	d.Fill(".gj-recip-dir-email", "bo@example.com")
	d.WaitFor("the find button", `!document.querySelector('.gj-recip-dir-find').disabled`)
	d.Click(".gj-recip-dir-find")
	d.WaitSelector(".gj-recip-dir-row")
	d.OK(d.Count(".gj-recip-dir-row") == 1 && d.Has(".gj-recip-dir-row .gj-recip-verified"), "Bo's key is listed with the verified mark")
	d.OK(strings.Contains(d.Text(".gj-recip-dir-row .gj-recip-verified"), "verified by "+dirHost), "the mark names the directory: "+d.Text(".gj-recip-dir-row .gj-recip-verified"))
	d.Click(".gj-recip-pick")
	d.WaitFor("the draft", `document.querySelector('.gj-recip-parsed-fingerprint') && document.querySelector('.gj-recip-form .gj-recip-verified')`)
	d.Fill(".gj-recip-confirm", d.Text(".gj-recip-parsed-fingerprint"))
	d.WaitFor("the add button", `!document.querySelector('.gj-recip-add').disabled`)
	d.Click(".gj-recip-add")
	d.WaitFor("Bo in the list", `document.querySelectorAll('.gj-recip-item:not(.gj-recip-owner):not(.gj-recip-dir-row)').length === 1`)
	d.OK(d.Has(`.gj-recip-item:not(.gj-recip-owner):not(.gj-recip-dir-row) .gj-recip-verified`), "his row keeps the verified mark")

	d.Click(".gj-recip-publish")
	d.WaitFor("published", `(document.querySelector('.gj-recip-publish-notice') || {textContent: ''}).textContent.includes('published for your address')`)
	d.Fill(".gj-recip-dir-email", "ana@example.com")
	d.WaitFor("the find button", `!document.querySelector('.gj-recip-dir-find').disabled`)
	d.Click(".gj-recip-dir-find")
	d.WaitFor("my own key found", `document.querySelectorAll('.gj-recip-dir-row').length === 1`)
	d.OK(d.Has(".gj-recip-dir-row .gj-recip-verified"), "what I published is listed as verified")
	lookup := httptest.NewRequest("GET", "/directory/lookup?email=ana@example.com", nil)
	lookup.Header.Set("Authorization", "Bearer "+tok)
	lrec := httptest.NewRecorder()
	srv.Mux.ServeHTTP(lrec, lookup)
	d.OK(lrec.Code == http.StatusOK && strings.Contains(lrec.Body.String(), `"held":true`), "the published key carries the proof of possession: "+lrec.Body.String())

	d.Fill("#dir-url", site.URL+"/nodir")
	d.Click("#dir-save")
	confirmDialog(d) // a new address is confirmed again
	d.WaitFor("the new directory", `document.querySelector('#dir-status').textContent.includes('/nodir')`)
	d.Fill(".gj-recip-dir-email", "bo@example.com")
	d.WaitFor("the find button", `!document.querySelector('.gj-recip-dir-find').disabled`)
	d.Click(".gj-recip-dir-find")
	d.WaitFor("the message", `(document.querySelector('.gj-recip .gj-doc-error') || {textContent: ''}).textContent.includes('no key directory')`)
	d.NoErrors()
}

func TestAClosedPasskeyPromptIsExplainedAndRetriedWithoutAnotherPasskey(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	d.Click(`button[data-tab="save"]`)
	d.Click("#enc-switch-box .gj-switch-track")
	d.WaitSelector("#enc-enable")
	d.Click("#enc-enable")
	d.WaitSelector("#enc-recovery-secret")
	d.SetChecked("#enc-recovery-ack input", true)
	d.WaitFor("the commit button", `!document.querySelector('#enc-recovery-commit').disabled`)
	d.Eval(`(() => {
		const get = navigator.credentials.get.bind(navigator.credentials);
		let n = 0;
		navigator.credentials.get = function (o) {
			if (n++ === 0) { const e = new Error('The operation either timed out or was not allowed.'); e.name = 'NotAllowedError'; return Promise.reject(e); }
			return get(o);
		};
		return 1;
	})()`, nil)
	d.Click("#enc-recovery-commit")
	d.WaitSelector("#enc-orphan")
	d.OK(strings.Contains(d.Text("#enc-message"), "closed or refused") && strings.Contains(d.Text("#enc-message"), "nothing was changed"), "a closed prompt is explained, not silent: "+d.Text("#enc-message"))
	d.OK(!d.State().Bool("encryption.enabled") && d.Has("#enc-recovery-secret"), "nothing is committed and the secret is still on screen")
	cred := d.Str(`localStorage.getItem('gojiberry:passkey-local:showcase') || ''`)
	d.OK(cred != "", "the passkey that was made is remembered")
	d.Click("#enc-orphan-retry")
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	d.OK(d.Str(`localStorage.getItem('gojiberry:passkey-local:showcase') || ''`) == cred, "the retry used the passkey that was made, not another one")
	d.NoErrors()
}

func TestHiddenNamesAreDrawnTranslatedAndStoredInEnglish(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "?lang=es")
	d.VirtualAuthenticator()
	d.Click(`button[data-tab="save"]`)
	d.WaitSelector("#enc-hint")
	d.OK(strings.Contains(d.Text("#enc-hint"), "catálogo sigue siendo legible"), "the hint says what the catalog leaves readable (es): "+d.Text("#enc-hint"))
	turnOn(t, d, "")
	d.OK(d.Attr("#enc-status", "role") == "status" && d.Attr("#enc-status", "aria-live") == "polite", "the lock state is a live region")
	saveConfidential(d, "Plan secreto")
	d.Click("#enc-lock")
	d.WaitSelector("#enc-unlock-passkey")
	d.WaitFor("the placeholder in Spanish", `document.querySelector('.gj-doclist-item .gj-doc-name').textContent.startsWith('Documento confidencial')`)
	_, stranger, err := encrypt.GenerateRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	d.Fill("#enc-recovery-input", stranger)
	d.WaitFor("the button", `!document.querySelector('#enc-unlock-recovery').disabled`)
	d.Click("#enc-unlock-recovery")
	d.WaitSelector("#enc-error")
	d.OK(strings.Contains(d.Text("#enc-error"), "no es una de las claves"), "the error is in Spanish: "+d.Text("#enc-error"))
	d.OK(strings.Contains(storedText(d), "Confidential document"), "the catalog still stores the English constant")
	d.OK(!strings.Contains(storedText(d), "Documento confidencial"), "a translated string is never stored")
	labels := d.Strs(`Array.from(document.querySelectorAll('.gj-doclist-protect, .gj-doclist-reveal')).map((b) => b.getAttribute('aria-label'))`)
	d.OK(len(labels) == 2 && labels[0] != labels[1] && strings.Contains(strings.Join(labels, "|"), "Documento confidencial"), fmt.Sprintf("the row buttons have names of their own, in Spanish: %v", labels))
	d.NoErrors()
}

func TestNoAccessibilityViolationsAxeInTheKeyDirectory(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	srv, err := demoserver.New(site.URL)
	if err != nil {
		t.Fatal(err)
	}
	site.Handle("/auth/", srv.Mux)
	site.Handle("/directory/", srv.Mux)
	site.Handle("/demo/", srv.Mux)
	boTok, err := srv.Login("bo@example.com")
	if err != nil {
		t.Fatal(err)
	}
	bo := &dirclient.Client{Base: site.URL, Token: func(context.Context) (string, error) { return boTok, nil }}
	owner, _ := dirclient.GenerateOwnerKey()
	ctx := context.Background()
	if err := bo.Enroll(ctx, "bo@example.com", owner); err != nil {
		t.Fatal(err)
	}
	pub, secret, _ := encrypt.GenerateRecoveryKey()
	id, _ := encrypt.ParseAgeIdentity(secret)
	if _, err := bo.Publish(ctx, dirclient.PublishRequest{Email: "bo@example.com", Key: pub, Label: "Bo", Identity: id, Owner: owner}); err != nil {
		t.Fatal(err)
	}

	d := site.OpenURL(t, "/index.html?docstore="+url.QueryEscape(site.URL))
	d.VirtualAuthenticator()
	d.WaitFor("the page to start", `document.documentElement.lang !== '' && !!document.getElementById('input').getAttribute('aria-label')`)
	check := func(state string) {
		t.Helper()
		for _, v := range d.Axe("", axeDisabled...) {
			t.Errorf("%s: %s", state, v)
		}
	}
	check("before encryption is on")
	turnOn(t, d, "")
	saveConfidential(d, "For Bo")
	check("encryption on")
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-offer")
	check("a directory offered")
	d.Click("#dir-offer-review")
	confirmDialog(d)
	d.WaitSelector(".gj-recip-dir-email")
	check("the key directory")
	d.Fill(".gj-recip-dir-email", "bo@example.com")
	d.WaitFor("the find button", `!document.querySelector('.gj-recip-dir-find').disabled`)
	d.Click(".gj-recip-dir-find")
	d.WaitSelector("#dir-pin-0")
	check("owner keys to confirm")
	d.Click("#dir-owner-enroll")
	d.WaitSelector("#dir-owner-backup-box")
	check("the owner key and its backup")
	d.Click("#dir-log-pin")
	d.WaitSelector("#dir-log-candidates li")
	check("the log keys to compare")
	d.Click("#dir-log-pin-confirm")
	d.WaitSelector("#dir-log-watch")
	check("the log keys pinned")
	d.Click(".gj-recip-publish")
	d.WaitFor("published", `(document.querySelector('.gj-recip-publish-notice') || {textContent: ''}).textContent.includes('published for your address')`)
	d.Click("#dir-log-watch")
	d.WaitFor("checked", `(document.querySelector('#dir-log-msg') || {textContent: ''}).textContent.includes('checked up to entry')`)
	check("after watching")
}

func TestOwnerKeyFallsBackToTextWhereWebCryptoHasNoEd25519(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	srv, err := demoserver.New(site.URL)
	if err != nil {
		t.Fatal(err)
	}
	site.Handle("/auth/", srv.Mux)
	site.Handle("/directory/", srv.Mux)
	site.Handle("/demo/", srv.Mux)

	d := site.OpenURL(t, "/index.html?docstore="+url.QueryEscape(site.URL))
	d.VirtualAuthenticator()
	d.Eval(`(crypto.subtle.importKey = () => Promise.reject(new DOMException('no Ed25519', 'NotSupportedError')), 1)`, nil)
	turnOn(t, d, "")
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-offer")
	d.Click("#dir-offer-review")
	confirmDialog(d)
	d.WaitSelector("#dir-owner-enroll")
	d.Click("#dir-owner-enroll")
	d.WaitFor("enrolled", `(document.querySelector('#dir-owner-state') || {textContent: ''}).textContent.includes('this browser holds it')`)
	d.OK(d.Str(`localStorage.getItem('showcase-enc:owner:' + '`+site.URL+`') || ''`) != "", "kept as text")
	d.OK(strings.Contains(d.Text("#dir-owner-storage"), "cannot keep the owner key out of reach"), "and the panel says so: "+d.Text("#dir-owner-storage"))
	d.OK(d.Str(storedOwnerKeyProbe(site.URL)) == "none", "nothing in IndexedDB")
	d.Click("#dir-owner-hide")
	d.WaitFor("hidden", `!document.querySelector('#dir-owner-backup-box')`)
	d.Click("#dir-owner-backup")
	d.WaitSelector("#dir-owner-secret")
	d.Click(".gj-recip-publish")
	d.WaitFor("published", `(document.querySelector('.gj-recip-publish-notice') || {textContent: ''}).textContent.includes('published for your address')`)
	d.NoErrors()
}

func TestRequireOwnerSettingWithholdsTheVerifiedMark(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	srv, err := demoserver.New(site.URL)
	if err != nil {
		t.Fatal(err)
	}
	site.Handle("/auth/", srv.Mux)
	site.Handle("/directory/", srv.Mux)
	site.Handle("/demo/", srv.Mux)
	boPub, _, err := encrypt.GenerateRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	tok, err := srv.Login("bo@example.com")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/directory/publish", strings.NewReader(`{"key":"`+boPub+`","label":"Bo's key"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	srv.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish = %d", rec.Code)
	}

	d := site.OpenURL(t, "/index.html?docstore="+url.QueryEscape(site.URL))
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	saveConfidential(d, "For Bo")
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-offer-review")
	d.Click("#dir-offer-review")
	confirmDialog(d)
	d.WaitSelector(".gj-recip-dir-email")
	find := func() {
		d.Fill(".gj-recip-dir-email", "bo@example.com")
		d.WaitFor("the find button", `!document.querySelector('.gj-recip-dir-find').disabled`)
		d.Click(".gj-recip-dir-find")
		d.WaitSelector(".gj-recip-dir-row")
	}

	find()
	d.OK(d.Has(".gj-recip-dir-row .gj-recip-verified"), "by default the row carries the verified mark")

	d.SetChecked("#dir-settings .gj-checkbox-input", true)
	d.WaitFor("the old answer to go", `!document.querySelector('.gj-recip-dir-row')`)
	find()
	d.OK(!d.Has(".gj-recip-dir-row .gj-recip-verified"), "with the setting on, the server's word alone is not a verified mark")
	d.OK(strings.Contains(d.Text(".gj-recip-dir-row"), "no owner key"), "the row says why: "+d.Text(".gj-recip-dir-row"))
	d.OK(d.Str(`localStorage.getItem('showcase-enc:require-owner') || ''`) == "1", "the setting is kept")

	d.SetChecked("#dir-settings .gj-checkbox-input", false)
	d.WaitFor("the old answer to go", `!document.querySelector('.gj-recip-dir-row')`)
	find()
	d.OK(d.Has(".gj-recip-dir-row .gj-recip-verified"), "switched off, the mark is back")
	d.NoErrors()
}
