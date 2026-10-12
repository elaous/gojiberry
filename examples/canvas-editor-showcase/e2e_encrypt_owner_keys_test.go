//go:build !(js && wasm)

package main

import (
	"context"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/examples/canvas-editor-showcase/dirclient"
	"github.com/elaous/gojiberry/examples/people-keys-demo/demoserver"
	"github.com/elaous/gojiberry/internal/e2etest"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The owner key of my address: made and enrolled from the People tab, kept in this browser, backed
// up; with it "Publish my keys" is signed; without it the directory refuses and the page says how
// to get it back; a backup restores it, and a stranger's key is not accepted.
func TestOwnerKeyForMyDirectoryEntry(t *testing.T) {
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
	turnOn(t, d, "")
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-offer")
	d.Click("#dir-offer-review")
	confirmDialog(d)
	d.WaitSelector("#dir-owner-check")

	d.Click("#dir-owner-check")
	d.WaitFor("the state", `(document.querySelector('#dir-owner-state') || {textContent: ''}).textContent.includes('has no owner key')`)
	d.OK(strings.Contains(d.Text("#dir-owner-nudge"), "only the directory server's word"), "an address without an owner key gets a standing notice: "+d.Text("#dir-owner-nudge"))
	d.Click("#dir-owner-enroll")
	d.WaitFor("enrolled", `(document.querySelector('#dir-owner-state') || {textContent: ''}).textContent.includes('this browser holds it')`)
	d.OK(!d.Has("#dir-owner-nudge"), "and the notice goes once it has one")
	d.WaitSelector("#dir-owner-backup-box")
	secret := d.Str(`document.querySelector('#dir-owner-secret').value`)
	d.OK(secret != "" && d.Has("#dir-owner-backup-warning"), "the new key is shown for a backup, with its warning")
	// kept as a key the page cannot copy out: no text in localStorage, and the stored key refuses to be exported
	d.OK(d.Str(`localStorage.getItem('showcase-enc:owner:' + '`+site.URL+`') || ''`) == "", "no copy of the owner key as text")
	d.OK(strings.Contains(d.Text("#dir-owner-storage"), "cannot be copied"), "and the panel says so: "+d.Text("#dir-owner-storage"))
	d.OK(d.Str(storedOwnerKeyProbe(site.URL)) == "non-extractable, export refused", "the stored key is non-extractable: "+d.Str(storedOwnerKeyProbe(site.URL)))

	// signed publish works (the browser signs with the key it will not hand over)
	d.Click(".gj-recip-publish")
	d.WaitFor("published", `(document.querySelector('.gj-recip-publish-notice') || {textContent: ''}).textContent.includes('published for your address')`)

	// the backup is shown once: hiding it asks, and then it cannot be shown again
	d.Click("#dir-owner-hide")
	confirmDialog(d)
	d.WaitFor("hidden", `!document.querySelector('#dir-owner-backup-box')`)
	d.OK(d.Bool(`document.querySelector('#dir-owner-backup').disabled`) && d.Has("#dir-owner-nobackup"), "the backup cannot be shown again, and the panel says why")

	// forget it: the directory now refuses, and the message says what to do
	d.Click("#dir-owner-forget")
	confirmDialog(d)
	d.WaitSelector("#dir-owner-restore-open")
	d.Click(".gj-recip-publish")
	d.WaitFor("the refusal", `(document.querySelector('.gj-recip .gj-doc-error') || {textContent: ''}).textContent.includes('owner key')`)

	// a stranger's key is not accepted as the address's
	d.Click("#dir-owner-restore-open")
	d.WaitSelector("#dir-owner-restore")
	other, err := dirclient.GenerateOwnerKey()
	if err != nil {
		t.Fatal(err)
	}
	d.Fill("#dir-owner-restore", dirclient.ExportOwnerKey(other))
	d.Click("#dir-owner-restore-go")
	d.WaitFor("not an owner key", `(document.querySelector('#dir-owner-msg') || {textContent: ''}).textContent.includes('not an owner key of')`)
	// the backup restores it
	d.Fill("#dir-owner-restore", secret)
	d.Click("#dir-owner-restore-go")
	d.WaitFor("restored", `(document.querySelector('#dir-owner-state') || {textContent: ''}).textContent.includes('this browser holds it')`)
	d.Click(".gj-recip-publish")
	d.WaitFor("published again", `(document.querySelector('.gj-recip-publish-notice') || {textContent: ''}).textContent.includes('published for your address')`)

	// the restored key is also kept out of reach, and is there after a reload (from IndexedDB)
	d.OK(d.Str(storedOwnerKeyProbe(site.URL)) == "non-extractable, export refused", "the restored key is non-extractable")
	d.Reload()
	d.Click(`button[data-tab="people"]`)
	d.WaitFor("the key loaded after a reload", `(document.querySelector('#dir-owner-storage') || {textContent: ''}).textContent.includes('cannot be copied')`)
	d.OK(d.Str(`localStorage.getItem('showcase-enc:owner:' + '`+site.URL+`') || ''`) == "", "with no text in localStorage")
	// forgetting removes it
	d.Click("#dir-owner-forget")
	confirmDialog(d)
	d.WaitSelector("#dir-owner-restore-open")
	d.OK(d.Str(storedOwnerKeyProbe(site.URL)) == "none", "forgetting removes the stored key: "+d.Str(storedOwnerKeyProbe(site.URL)))
	d.NoErrors()
}

// Watching my address: the server's log keys are pinned after the person agrees, the log position
// and the pins survive a reload, and an entry somebody else made for the address is reported and
// stays reported until dismissed.
func TestWatchMyDirectoryEntry(t *testing.T) {
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
	turnOn(t, d, "")
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-offer")
	d.Click("#dir-offer-review")
	confirmDialog(d)
	d.WaitSelector("#dir-log-pin")

	// without pinned keys the log is not read
	d.OK(!d.Has("#dir-log-watch"), "no watch button before the keys are pinned")
	d.Click("#dir-log-pin")
	d.WaitSelector("#dir-log-candidates li")
	// the keys are shown with fingerprints to compare; a fingerprint that is not theirs pins nothing
	d.OK(regexp.MustCompile(`: [0-9a-f]{4} [0-9a-f]{4} [0-9a-f]{4} [0-9a-f]{4}$`).MatchString(d.Text("#dir-log-candidates li")), "each key has a fingerprint: "+d.Text("#dir-log-candidates li"))
	d.Fill("#dir-log-expected", "dead beef dead beef")
	d.Click("#dir-log-pin-confirm")
	d.WaitFor("the refusal", `(document.querySelector('#dir-owner-msg') || {textContent: ''}).textContent.includes('Nothing was pinned')`)
	d.OK(!d.Has("#dir-log-watch"), "nothing was pinned")
	// the real one, typed the way a person copies it (upper case, colons), pins
	fp := strings.ToUpper(strings.ReplaceAll(strings.SplitN(d.Text("#dir-log-candidates li"), ": ", 2)[1], " ", ":"))
	d.Fill("#dir-log-expected", fp)
	d.Click("#dir-log-pin-confirm")
	d.WaitSelector("#dir-log-watch")
	d.OK(strings.Contains(d.Text("#dir-log-state"), "Pinned log keys"), "the pinned key IDs are shown: "+d.Text("#dir-log-state"))

	// my own entry confirms the log name of my address and is not reported
	d.Click(".gj-recip-publish")
	d.WaitFor("published", `(document.querySelector('.gj-recip-publish-notice') || {textContent: ''}).textContent.includes('published for your address')`)
	d.Click("#dir-log-watch")
	d.WaitFor("checked", `(document.querySelector('#dir-log-msg') || {textContent: ''}).textContent.includes('checked up to entry')`)
	d.OK(!d.Has("#dir-log-alerts"), "my own entries are not alerts")

	// somebody else, signed in as the same address, publishes a key
	other, _, err := encrypt.GenerateRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	tok, err := srv.Login("ana@example.com")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/directory/publish", strings.NewReader(`{"key":"`+other+`","label":"not me"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	srv.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish = %d", rec.Code)
	}
	d.Click("#dir-log-watch")
	d.WaitSelector("#dir-log-alerts")
	d.OK(d.Count("#dir-log-alerts li") == 1, "one entry that is not mine: "+d.Text("#dir-log-alerts"))

	// after a reload the pins, the position and the alert are still there
	d.Reload()
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-log-state")
	d.OK(d.Has("#dir-log-watch") && d.Has("#dir-log-alerts"), "the pins and the alert survive a reload")
	d.Click("#dir-log-watch")
	d.WaitFor("checked again", `(document.querySelector('#dir-log-msg') || {textContent: ''}).textContent.includes('0 new for your address')`)
	d.OK(d.Count("#dir-log-alerts li") == 1, "and it is not reported twice")
	d.Click("#dir-log-clear")
	d.OK(!d.Has("#dir-log-alerts"), "dismissed")
	d.NoErrors()
}

// Another person's entries signed by an owner key: the picker does not show the strongest mark on
// the directory's word; after the person confirms the owner key's fingerprint (a dialog says to
// compare it another way) the row earns it, and the pin survives a reload.
func TestConfirmAnotherPersonsOwnerKey(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	srv, err := demoserver.New(site.URL)
	if err != nil {
		t.Fatal(err)
	}
	site.Handle("/auth/", srv.Mux)
	site.Handle("/directory/", srv.Mux)
	site.Handle("/demo/", srv.Mux)
	// Bo enrolls an owner key and publishes a key signed by it.
	boTok, err := srv.Login("bo@example.com")
	if err != nil {
		t.Fatal(err)
	}
	bo := &dirclient.Client{Base: site.URL, Token: func(context.Context) (string, error) { return boTok, nil }}
	owner, err := dirclient.GenerateOwnerKey()
	if err != nil {
		t.Fatal(err)
	}
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
	turnOn(t, d, "")
	saveConfidential(d, "For Bo")
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-offer")
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
	d.OK(!d.Has(".gj-recip-dir-row .gj-recip-owner-signed"), "the directory's word alone does not earn the owner-signed mark")
	d.WaitSelector("#dir-pin-0")
	fp := dirclient.OwnerFingerprint(owner)
	d.OK(strings.Contains(d.Text("#dir-pin-0"), fp), "the owner key's fingerprint is shown to compare: "+d.Text("#dir-pin-0"))
	d.Click("#dir-pin-confirm-0")
	d.WaitSelector(`[role="alertdialog"]`)
	d.OK(strings.Contains(d.Text(`[role="alertdialog"]`), "another way"), "the question says to compare it elsewhere: "+d.Text(`[role="alertdialog"]`))
	confirmDialog(d)
	d.WaitSelector(".gj-recip-dir-row .gj-recip-owner-signed")
	d.OK(!d.Has("#dir-pin-0"), "nothing is left to confirm")

	// the pin is kept in this browser for this directory, to be restored on the next visit
	d.OK(strings.Contains(d.Str(`localStorage.getItem('showcase-enc:pins:' + '`+site.URL+`') || ''`), "bo@example.com"), "the confirmed owner key is kept")
	// looking again shows the mark on the first look, without asking
	find()
	d.OK(d.Has(".gj-recip-dir-row .gj-recip-owner-signed") && !d.Has("#dir-pin-0"), "and it still counts on the next lookup")
	d.NoErrors()
}

// storedOwnerKeyProbe is a JavaScript expression (it awaits) that says what is stored for the owner key
// of the directory at url: "none", "extractable", or "non-extractable, export refused".
func storedOwnerKeyProbe(url string) string {
	return `new Promise(resolve => {
		const open = indexedDB.open('gojiberry-showcase-owner', 1);
		open.onupgradeneeded = () => open.result.createObjectStore('keys', {keyPath: 'id'});
		open.onerror = () => resolve('no database');
		open.onsuccess = () => {
			const get = open.result.transaction('keys', 'readonly').objectStore('keys').get(` + "`" + url + "`" + `);
			get.onerror = () => resolve('read failed');
			get.onsuccess = async () => {
				const rec = get.result;
				if (!rec) { resolve('none'); return; }
				if (rec.key.extractable) { resolve('extractable'); return; }
				try { await crypto.subtle.exportKey('pkcs8', rec.key); resolve('exported'); }
				catch (e) { resolve('non-extractable, export refused'); }
			};
		};
	})`
}

// An owner key an earlier version of the page kept as text is moved, on the next visit, to a key the
// page cannot copy out, and the text is removed.
func TestOwnerKeyKeptAsTextIsMovedOutOfReach(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.OpenURL(t, "/index.html")
	k, err := dirclient.GenerateOwnerKey()
	if err != nil {
		t.Fatal(err)
	}
	rec := `{"email":"ana@example.com","key":"` + dirclient.ExportOwnerKey(k) + `"}`
	d.Eval(`(() => {
		localStorage.setItem('showcase-enc:docstore', `+"`"+site.URL+"`"+`);
		localStorage.setItem('showcase-enc:docstore-ok', `+"`"+site.URL+"`"+`);
		localStorage.setItem('showcase-enc:owner:' + `+"`"+site.URL+"`"+`, `+"`"+rec+"`"+`);
		return 1;
	})()`, nil)
	d.Reload()
	d.WaitFor("the key moved", `localStorage.getItem('showcase-enc:owner:' + `+"`"+site.URL+"`"+`) === null`)
	d.OK(d.Str(storedOwnerKeyProbe(site.URL)) == "non-extractable, export refused", "now kept out of reach: "+d.Str(storedOwnerKeyProbe(site.URL)))
	d.NoErrors()
}

// Replacing the owner key from the page: the holder rotates it (the old key stops publishing), and a
// person without the key asks for a reset, which the directory completes only after its wait.
func TestRotateAndResetTheOwnerKeyFromThePage(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	srv, err := demoserver.New(site.URL)
	if err != nil {
		t.Fatal(err)
	}
	site.Handle("/auth/", srv.Mux)
	site.Handle("/directory/", srv.Mux)
	site.Handle("/demo/", srv.Mux)
	state := func(sub string) string {
		return `(document.querySelector('#dir-owner-state') || {textContent: ''}).textContent.includes('` + sub + `')`
	}

	d := site.OpenURL(t, "/index.html?docstore="+url.QueryEscape(site.URL))
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-offer")
	d.Click("#dir-offer-review")
	confirmDialog(d)
	d.WaitSelector("#dir-owner-enroll")
	d.Click("#dir-owner-enroll")
	d.WaitFor("enrolled", state("this browser holds it"))
	first := d.Str(`document.querySelector('#dir-owner-secret').value`)

	// rotate: a new key, signed in by the old; the new one publishes
	d.Click("#dir-owner-rotate")
	confirmDialog(d)
	d.WaitFor("rotated", `(document.querySelector('#dir-owner-msg') || {textContent: ''}).textContent.includes('Owner key replaced')`)
	second := d.Str(`document.querySelector('#dir-owner-secret').value`)
	d.OK(second != "" && second != first, "the new key has its own backup")
	d.Click(".gj-recip-publish")
	d.WaitFor("published", `(document.querySelector('.gj-recip-publish-notice') || {textContent: ''}).textContent.includes('published for your address')`)

	// the first key's backup is no longer an owner key of the address
	d.Click("#dir-owner-forget")
	confirmDialog(d)
	d.WaitSelector("#dir-owner-restore-open")
	d.Click("#dir-owner-restore-open")
	d.WaitSelector("#dir-owner-restore")
	d.Fill("#dir-owner-restore", first)
	d.Click("#dir-owner-restore-go")
	d.WaitFor("refused", `(document.querySelector('#dir-owner-msg') || {textContent: ''}).textContent.includes('not an owner key of')`)

	// lost the key: ask for a reset, which waits
	d.Click("#dir-owner-reset-request")
	confirmDialog(d)
	d.WaitSelector("#dir-owner-pending-box")
	third := d.Str(`document.querySelector('#dir-owner-pending-secret').value`)
	d.OK(third != "" && third != second, "the key for the reset is shown to be saved")
	d.WaitFor("the pending state", state("reset waiting"))
	d.Click("#dir-owner-reset-complete")
	// The message of the request itself also mentions the wait ("once the wait is over"): only the refusal of
	// this early attempt counts, or the clock below could move before the attempt was answered.
	d.WaitFor("the wait", `(t => t.includes('wait') && !t.includes('reset requested'))((document.querySelector('#dir-owner-msg') || {textContent: ''}).textContent.toLowerCase())`)
	d.OK(!d.Has("#dir-owner-backup"), "the page still holds no owner key")

	// after the directory's wait the reset completes and the new key publishes
	srv.Dir.Now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	d.Click("#dir-owner-reset-complete")
	d.WaitFor("completed", state("this browser holds it"))
	d.OK(!d.Has("#dir-owner-pending-box"), "the pending key is gone")
	d.Click(".gj-recip-publish")
	d.WaitFor("published with the reset key", `(document.querySelector('.gj-recip-publish-notice') || {textContent: ''}).textContent.includes('published for your address')`)
	d.NoErrors()
}

// The panel states the limit in plain words and lets the person check a recovery key without using it:
// the right key is accepted, another key and rubbish are told apart, the typed secret is cleared, and
// nothing is unlocked.
func TestCheckTheRecoveryKeyWithoutUsingIt(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	secret := turnOn(t, d, "")
	d.OK(strings.Contains(d.Text("#enc-limit"), "nobody can open its documents, including us"), "the limit is stated plainly: "+d.Text("#enc-limit"))
	d.OK(!d.Has("#enc-one-key"), "two keys: no nudge")

	d.Click("#enc-lock")
	d.WaitFor("locked", `!document.querySelector('#enc-lock')`)
	result := func() string { return d.Text("#enc-check-result") }
	check := func(v string) {
		d.Fill("#enc-check-input", v)
		d.Click("#enc-check-recovery")
		d.WaitSelector("#enc-check-result")
	}
	_, otherSecret, err := encrypt.GenerateRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	check(otherSecret)
	d.OK(strings.Contains(result(), "not one of this store's recovery keys"), "another key: "+result())
	check("rubbish")
	d.OK(strings.Contains(result(), "not a recovery key"), "rubbish: "+result())
	check(secret)
	d.OK(strings.HasPrefix(result(), "Yes"), "the right key: "+result())
	d.OK(d.Value("#enc-check-input") == "", "the typed secret is cleared")
	d.OK(d.Has("#enc-unlock-recovery") && !d.Has("#enc-lock"), "checking did not unlock the store")
	d.OK(d.Str(`localStorage.getItem('showcase-enc:recovery-checked') || ''`) != "", "the time of the check is kept")
	d.NoErrors()
}

// A printable sheet of the recovery key is offered while its secret is on screen, and the person is
// reminded to check the key until they have, and can put the reminder off.
func TestRecoverySheetAndReminder(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	d.Click(`button[data-tab="save"]`)
	d.WaitSelector("#enc-panel")
	d.Click("#enc-switch-box .gj-switch-track")
	d.WaitSelector("#enc-enable")
	d.Click("#enc-enable")
	d.WaitSelector("#enc-recovery-secret")
	secret := d.Value("#enc-recovery-secret")

	// Print opens a page built in the browser; capture it instead of opening a window
	d.Eval(`window.__sheet = ''; window.open = (u) => { const x = new XMLHttpRequest(); x.open('GET', u, false); x.send(); window.__sheet = x.responseText; }`, nil)
	d.Click("#enc-recovery-print")
	d.WaitFor("the sheet", `window.__sheet.length > 0`)
	sheet := d.Str(`window.__sheet`)
	for _, want := range []string{secret, "Fingerprint", "nobody can open the documents", "window.print()"} {
		d.OK(strings.Contains(sheet, want), "the sheet has "+want)
	}
	d.OK(!strings.Contains(sheet, "http://") && !strings.Contains(sheet, "https://"), "and refers to nothing outside itself")

	d.SetChecked("#enc-recovery-ack input", true)
	d.WaitFor("the commit button", `!document.querySelector('#enc-recovery-commit').disabled`)
	d.Click("#enc-recovery-commit")
	d.WaitSelector("#enc-lock")

	// never checked: reminded; put off for a week: quiet; checked: no reminder after the snooze either
	d.WaitSelector("#enc-check-due")
	d.OK(strings.Contains(d.Text("#enc-check-due"), "have not checked"), "reminded: "+d.Text("#enc-check-due"))
	d.Click("#enc-check-later")
	d.WaitFor("quiet", `!document.querySelector('#enc-check-due')`)
	d.Eval(`localStorage.removeItem('showcase-enc:recovery-snooze')`, nil)
	d.Reload()
	d.Click(`button[data-tab="save"]`)
	d.WaitSelector("#enc-check-due")
	d.Fill("#enc-check-input", secret)
	d.Click("#enc-check-recovery")
	d.WaitFor("checked", `!document.querySelector('#enc-check-due') && !!document.querySelector('#enc-check-result')`)
	d.NoErrors()
}

// The keys list says when each key last unlocked the store in this browser, and that one never has.
func TestKeyListShowsWhenEachKeyWasLastUsedHere(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	secret := turnOn(t, d, "")
	detail := func(typ string) string {
		return d.Str(`(() => { const r = Array.from(document.querySelectorAll('.gj-recip-owner')).find((li) => li.querySelector('.gj-recip-type').textContent === '` + typ + `'); return r ? r.querySelector('.gj-recip-detail').textContent : ''; })()`)
	}
	d.Click("#enc-lock")
	d.WaitFor("locked", `!document.querySelector('#enc-lock')`)
	d.Fill("#enc-recovery-input", secret)
	d.Click("#enc-unlock-recovery")
	d.WaitSelector("#enc-lock")
	d.Click(`button[data-tab="people"]`)
	d.WaitFor("two keys", `document.querySelectorAll('.gj-recip-owner').length === 2`)
	today := time.Now().UTC().Format("2006-01-02")
	d.OK(detail("age-x25519") == "Last used in this browser: "+today, "the recovery key was just used: "+detail("age-x25519"))
	d.OK(detail("passkey-prf") == "Not used in this browser", "the passkey was not: "+detail("passkey-prf"))
	d.NoErrors()
}

// On a browser that has never seen the store's key list, the first unlock with an SSH key shows the
// list's fingerprint and waits for the person to say it matches; the fingerprint is the one shown where
// the store was made, and the question is not asked again.
func TestFirstSSHUnlockOnANewBrowserAsksForTheListFingerprint(t *testing.T) {
	pub, priv := sshKeyPair(t, "me@laptop")
	site := e2etest.NewSite(t)
	d := site.Open(t, "")
	d.Click(`button[data-tab="save"]`)
	d.Click("#enc-switch-box .gj-switch-track")
	d.WaitSelector("#enc-enable")
	d.SetChecked("#enc-opt-passkey input", false)
	d.SetChecked("#enc-opt-recovery input", false)
	d.Fill("#enc-ssh", pub)
	d.Click("#enc-enable")
	d.WaitFor("locked", `(document.getElementById('enc-status') || {}).textContent === 'Locked'`)
	shown := strings.TrimPrefix(d.Text("#enc-list-fingerprint"), "Key list fingerprint: ")
	d.OK(strings.Count(shown, " ") == 7, "the maker sees the list's fingerprint: "+shown)

	// a browser that has not seen this list (the key has not sealed it either): nothing remembered
	d.Eval(`localStorage.removeItem('showcase-enc:trusted-lists')`, nil)
	d.Reload()
	d.Click(`button[data-tab="save"]`)
	d.WaitSelector("#enc-unlock-ssh")
	d.ChooseFile(func() { d.Click("#enc-unlock-ssh") }, "id_ed25519", priv)
	d.WaitSelector("#enc-first-use")
	d.OK(d.Text("#enc-first-use-fingerprint") == shown, "it shows the same fingerprint: "+d.Text("#enc-first-use-fingerprint"))
	d.OK(d.Text("#enc-status") == "Locked", "nothing unlocked yet")
	d.Click("#enc-first-use-cancel")
	d.WaitFor("gone", `!document.querySelector('#enc-first-use')`)
	d.ChooseFile(func() { d.Click("#enc-unlock-ssh") }, "id_ed25519", priv)
	d.WaitSelector("#enc-first-use")
	d.ChooseFile(func() { d.Click("#enc-first-use-ok") }, "id_ed25519", priv)
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)

	// and it is not asked again
	d.Click("#enc-lock")
	d.WaitFor("locked", `(document.getElementById('enc-status') || {}).textContent === 'Locked'`)
	d.Reload()
	d.Click(`button[data-tab="save"]`)
	d.WaitSelector("#enc-unlock-ssh")
	d.ChooseFile(func() { d.Click("#enc-unlock-ssh") }, "id_ed25519", priv)
	d.WaitFor("unlocked without a question", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	d.NoErrors()
}

// Watching names a reset of the owner key as its own kind of alert: somebody else with a session of the
// address asks for a reset and completes it after the directory's wait, and the page lists the request
// and the reset, each marked with its kind and worded for what it means, and keeps them across a reload.
func TestWatchNamesAResetOfTheOwnerKeyOnThePage(t *testing.T) {
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
	turnOn(t, d, "")
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-offer")
	d.Click("#dir-offer-review")
	confirmDialog(d)
	d.WaitSelector("#dir-log-pin")
	d.Click("#dir-log-pin")
	d.WaitSelector("#dir-log-candidates li")
	fp := strings.SplitN(d.Text("#dir-log-candidates li"), ": ", 2)[1]
	d.Fill("#dir-log-expected", fp)
	d.Click("#dir-log-pin-confirm")
	d.WaitSelector("#dir-log-watch")
	// my own entry confirms the log name of my address
	d.Click(".gj-recip-publish")
	d.WaitFor("published", `(document.querySelector('.gj-recip-publish-notice') || {textContent: ''}).textContent.includes('published for your address')`)
	d.Click("#dir-log-watch")
	d.WaitFor("checked", `(document.querySelector('#dir-log-msg') || {textContent: ''}).textContent.includes('checked up to entry')`)
	d.OK(!d.Has("#dir-log-alerts"), "nothing yet")

	// Another session of the same address enrolls an owner key, asks for a reset and, once the wait is
	// over, completes it with a key the page never had.
	now := time.Now()
	srv.Dir.Now = func() time.Time { return now }
	tok, err := srv.Login("ana@example.com")
	if err != nil {
		t.Fatal(err)
	}
	other := &dirclient.Client{Base: site.URL, Token: func(context.Context) (string, error) { return tok, nil }, Now: func() time.Time { return now }}
	ctx := context.Background()
	first, _ := dirclient.GenerateOwnerKey()
	second, _ := dirclient.GenerateOwnerKey()
	if err := other.Enroll(ctx, "ana@example.com", first); err != nil {
		t.Fatal(err)
	}
	if _, err := other.RequestReset(ctx); err != nil {
		t.Fatal(err)
	}
	d.Click("#dir-log-watch")
	d.WaitFor("the request", `document.querySelectorAll('#dir-log-alerts li[data-kind="reset-request"]').length === 1`)
	d.OK(d.Count(`#dir-log-alerts li[data-kind="reset"]`) == 0, "no reset yet")
	now = now.Add(25 * time.Hour)
	if err := other.CompleteReset(ctx, "ana@example.com", second); err != nil {
		t.Fatal(err)
	}
	d.Click("#dir-log-watch")
	d.WaitFor("the reset", `document.querySelectorAll('#dir-log-alerts li[data-kind="reset"]').length === 1`)
	reset := d.Text(`#dir-log-alerts li[data-kind="reset"]`)
	d.OK(strings.Contains(reset, "was reset") && strings.Contains(reset, "no longer signs"), "the reset is worded for what it means: "+reset)
	d.OK(strings.Contains(d.Text(`#dir-log-alerts li[data-kind="reset-request"]`), "cancel the reset"), "the request says what to do: "+d.Text(`#dir-log-alerts li[data-kind="reset-request"]`))
	d.OK(d.Count(`#dir-log-alerts li[data-kind="entry"]`) == 1, "the enrollment is a plain entry: "+d.Text("#dir-log-alerts"))

	// the kinds are kept across a reload
	d.Reload()
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-log-alerts")
	d.OK(d.Count(`#dir-log-alerts li[data-kind="reset"]`) == 1 && d.Count(`#dir-log-alerts li[data-kind="reset-request"]`) == 1, "kinds survive a reload: "+d.Text("#dir-log-alerts"))
	d.NoErrors()
}
