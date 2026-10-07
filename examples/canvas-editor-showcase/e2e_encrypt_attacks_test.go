//go:build !(js && wasm)

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/internal/e2etest"
)

// The attacks of the review of PR #81 against the page, made the way someone who can write its
// localStorage would make them.

// editConfig changes the stored key configuration with a script (c is the parsed config).
func editConfig(d *e2etest.Tab, body string) {
	d.Eval(`(() => {
		const c = JSON.parse(localStorage.getItem('showcase-enc:config'));
		`+body+`
		localStorage.setItem('showcase-enc:config', JSON.stringify(c));
		return 1;
	})()`, nil)
}

// docsText is what the page stored for its documents (not the key configuration, which a forged
// journal is written into).
func docsText(d *e2etest.Tab) string {
	return d.Str(`Object.keys(localStorage).filter(k => !k.startsWith('showcase-enc:')).sort().map(k => k + '\n' + localStorage.getItem(k)).join('\n')`)
}

func reloadToSave(d *e2etest.Tab) {
	d.Reload()
	d.Click(`button[data-tab="save"]`)
	d.WaitSelector("#enc-panel")
}

// A Version 2 configuration whose seals were stripped, or whose version was set back to 1 with an
// owner added, is refused for the passkey and the recovery key alike: Version is written by whoever
// writes the storage, so it decides nothing.
func TestAConfigWithItsSealsStrippedIsRefusedAtUnlock(t *testing.T) {
	attacker, _, err := encrypt.GenerateRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"seals stripped, still version 2":     `delete c.seals; delete c.links;`,
		"version set back and an owner added": `c.v = 1; delete c.seals; delete c.links; c.owners.push(Object.assign({}, c.owners[1], {public: '` + attacker + `', label: 'helpful backup'}));`,
	} {
		t.Run(name, func(t *testing.T) {
			site := e2etest.NewSite(t)
			site.UseLocalhost()
			d := site.Open(t, "")
			d.VirtualAuthenticator()
			recovery := turnOn(t, d, "")
			d.OK(d.Str(`JSON.parse(localStorage.getItem('showcase-enc:config')).v+''`) == "2", "a version 2 configuration to start with")
			reloadToSave(d)
			editConfig(d, body)
			reloadToSave(d)
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
		})
	}
}

// An older configuration that was genuine (its seals and links valid) is refused by the anchor in the
// files' signed frames: the files were sealed under the list that replaced it.
func TestAnOlderGenuineConfigPutBackIsRefusedInThePage(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	recovery := turnOn(t, d, "")
	d.Fill(".gj-saver-name", "Anchored")
	d.Click(".gj-saver-save")
	d.WaitFor("saved", "document.querySelectorAll('.gj-doclist-item').length >= 1")
	older := d.Str(`localStorage.getItem('showcase-enc:config')`)
	// The owner replaces the passkey: every file is sealed under the new list, which becomes the config.
	// (A passkey is replaced from another key: lock, unlock with the recovery key.)
	d.Click(`button[data-tab="save"]`)
	d.Click("#enc-lock")
	d.WaitFor("locked", `(document.getElementById('enc-status') || {}).textContent === 'Locked'`)
	d.Fill("#enc-recovery-input", recovery)
	d.WaitFor("the button", `!document.querySelector('#enc-unlock-recovery').disabled`)
	d.Click("#enc-unlock-recovery")
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector(".gj-recip-replace")
	d.Click(".gj-recip-replace")
	confirmDialog(d)
	d.WaitFor("replaced", `localStorage.getItem('showcase-enc:config') !== `+e2etest.J(older)+` && !JSON.parse(localStorage.getItem('showcase-enc:config')).pending`)
	d.Sleep(500)
	d.OK(d.Str(`localStorage.getItem('showcase-enc:config')`) != older, "the list changed")
	// Someone puts the older, genuine configuration back.
	d.Eval(`localStorage.setItem('showcase-enc:config', `+e2etest.J(older)+`); 1`, nil)
	reloadToSave(d)
	d.Fill("#enc-recovery-input", recovery)
	d.WaitFor("the button", `!document.querySelector('#enc-unlock-recovery').disabled`)
	d.Click("#enc-unlock-recovery")
	d.WaitSelector("#enc-error")
	d.OK(strings.Contains(d.Text("#enc-error"), "configuration on this device was changed") && d.Text("#enc-status") == "Locked", "the recovery key refuses the older list: "+d.Text("#enc-error"))
	d.NoErrors()
}

// A deleted configuration over encrypted files is not "encryption off": the page says so, stays
// read-only, restores from a key that opens the files, and Erase needs the exact word.
func TestADeletedOrCorruptConfigIsNotEncryptionOff(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	recovery := turnOn(t, d, "")
	d.Fill(".gj-saver-name", "Kept in ciphertext")
	d.Click(".gj-saver-save")
	d.WaitFor("saved", "document.querySelectorAll('.gj-doclist-item').length >= 1")
	files := d.Str(`Object.keys(localStorage).filter(k => k.startsWith('showcase:')).sort().join(',')`)

	// 1. The configuration is deleted.
	d.Eval(`localStorage.removeItem('showcase-enc:config')`, nil)
	reloadToSave(d)
	d.WaitSelector("#enc-broken")
	d.OK(d.State().Str("encryption.broken") == "missing" && !d.State().Bool("encryption.enabled"), "the page knows the configuration is missing")
	d.OK(strings.Contains(d.Text("#enc-broken"), "Configuration missing: encrypted files found"), "it says so: "+d.Text("#enc-broken"))
	d.OK(!d.Has("#enc-switch-box"), "there is no 'encryption off' switch to press")
	// Read-only: a save is refused and no plain text is written beside the ciphertext.
	d.Fill(".gj-saver-name", "Plain attempt")
	d.Click(".gj-saver-save")
	d.Sleep(300)
	d.OK(!strings.Contains(storedText(d), "Plain attempt"), "nothing was saved in the clear")
	d.OK(d.Str(`Object.keys(localStorage).filter(k => k.startsWith('showcase:')).sort().join(',')`) == files, "the stored files are as they were")

	// 2. Restore with the recovery key.
	d.Fill("#enc-restore-recovery-input", recovery)
	d.WaitFor("the button", `!document.querySelector('#enc-restore-recovery').disabled`)
	d.Click("#enc-restore-recovery")
	d.WaitFor("restored", `document.querySelector('#enc-status') && !document.querySelector('#enc-broken')`)
	d.Wait("s.encryption.enabled && s.encryption.unlocked && s.encryption.broken === ''")
	d.OK(d.State().Bool("encryption.enabled") && d.State().Bool("encryption.unlocked") && d.State().Str("encryption.broken") == "", "the store is on and unlocked again")
	d.WaitFor("the document is listed", "document.querySelectorAll('.gj-doclist-item').length >= 1")
	d.Click(".gj-doclist-open")
	d.WaitFor("it opens", `document.querySelector('.gj-saver-status').textContent.includes('opened')`)

	// 3. A configuration that is not JSON is a corrupt state, not "off".
	d.Eval(`localStorage.setItem('showcase-enc:config', '{not json')`, nil)
	reloadToSave(d)
	d.WaitSelector("#enc-broken")
	d.OK(d.State().Str("encryption.broken") == "corrupt" && strings.Contains(d.Text("#enc-broken"), "could not be read"), "corrupt: "+d.Text("#enc-broken"))
	d.Fill(".gj-saver-name", "Plain attempt two")
	d.Click(".gj-saver-save")
	d.Sleep(300)
	d.OK(!strings.Contains(storedText(d), "Plain attempt two"), "nothing was saved in the clear beside the ciphertext")

	// 4. Erase needs the word exactly: "erase" is not "ERASE".
	d.Click("#enc-erase")
	confirmDialog(d)
	d.WaitSelector("#enc-erase-word")
	d.Fill("#enc-erase-word", "erase")
	d.OK(d.Disabled("#enc-erase-go"), "a lower-case word does not erase")
	d.Fill("#enc-erase-word", "Erase")
	d.OK(d.Disabled("#enc-erase-go"), "nor a capitalised one")
	d.Fill("#enc-erase-word", "ERASE")
	d.OK(!d.Disabled("#enc-erase-go"), "the exact word does")
	d.Click("#enc-erase-go")
	d.WaitFor("erased", `!document.querySelector('#enc-broken') && document.querySelector('#enc-switch-box')`)
	d.OK(d.State().Str("encryption.broken") == "" && !d.State().Bool("encryption.enabled"), "the store is plain again")
	// What the page itself writes again after the erase (the chosen target, an empty catalog) is not data.
	d.OK(d.Str(`Object.keys(localStorage).filter(k => k.startsWith('showcase') && k !== 'showcase:last-target' && k !== 'showcase:catalog:index').join(',')`) == "", "nothing is left in storage: "+d.Str(`Object.keys(localStorage).join(',')`))
	d.OK(!strings.Contains(storedText(d), "Kept in ciphertext") && d.Count(".gj-doclist-item") == 0, "no document is left")
	d.NoErrors()
}

// A journal that someone wrote (owners replaced, a journal that adds the attacker next to the real
// keys) is not finished by the real person's passkey.
func TestAForgedJournalIsRefusedInThePage(t *testing.T) {
	attacker, _, err := encrypt.GenerateRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	d.Fill(".gj-saver-name", "Mine")
	d.Click(".gj-saver-save")
	d.WaitFor("saved", "document.querySelectorAll('.gj-doclist-item').length >= 1")
	reloadToSave(d)
	before := docsText(d)
	editConfig(d, `
		const mine = c.owners.slice();
		const evil = Object.assign({}, c.owners[1], {public: '`+attacker+`', label: 'helpful backup'});
		c.pending = {op: 'add', to: mine.concat([evil])};
		c.owners = [evil]; delete c.seals; delete c.links;`)
	reloadToSave(d)
	d.WaitSelector("#enc-interrupted")
	d.OK(strings.Contains(d.Text("#enc-interrupted-text"), "helpful backup") && strings.Contains(d.Text("#enc-interrupted-text"), "SHA256:"), "the journal's keys are shown: "+d.Text("#enc-interrupted-text"))
	d.Click("#enc-unlock-passkey") // the real passkey is in the journal's list
	d.WaitFor("unlocked (read-only)", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	d.Click("#enc-finish")
	confirmDialog(d)
	d.WaitSelector("#enc-error")
	d.OK(strings.Contains(d.Text("#enc-error"), "not vouched for"), "finishing is refused: "+d.Text("#enc-error"))
	d.Click("#enc-undo")
	confirmDialog(d)
	d.WaitSelector("#enc-error")
	d.OK(strings.Contains(d.Text("#enc-error"), "not vouched for") && d.State().Bool("encryption.interrupted"), "undoing is refused too: "+d.Text("#enc-error"))
	d.OK(docsText(d) == before, "nothing was re-encrypted to the attacker")
	d.NoErrors()
}

// Another tab changed the keys: this one is locked by the storage event and refuses to write; a
// write attempted without the event (the stored configuration differs) is refused the same way.
func TestAStaleTabLocksItselfAndRefusesToWrite(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	d.Fill(".gj-saver-name", "Before")
	d.Click(".gj-saver-save")
	d.WaitFor("saved", "document.querySelectorAll('.gj-doclist-item').length >= 1")

	// The storage event, as the browser fires it in the tabs that did not write.
	editConfig(d, `c.v = 3;`)
	d.Eval(`(() => { window.dispatchEvent(new StorageEvent('storage', {key: 'showcase-enc:config', storageArea: localStorage})); return 1; })()`, nil)
	d.WaitSelector("#enc-stale")
	d.OK(d.State().Bool("encryption.stale") && d.Text("#enc-status") == "Locked", "the tab is locked")
	d.OK(strings.Contains(d.Text("#enc-stale"), "changed in another tab"), "and says why: "+d.Text("#enc-stale"))
	d.OK(!d.Has("#enc-lock"), "with no way to unlock it")
	d.Click("#enc-unlock-passkey")
	d.WaitSelector("#enc-error")
	d.OK(strings.Contains(d.Text("#enc-error"), "another tab"), "unlocking is refused: "+d.Text("#enc-error"))
	d.NoErrors()

	// Without the event: the configuration differs at the moment of the write.
	d.Reload()
	d.Click(`button[data-tab="save"]`)
	d.Click("#enc-unlock-passkey")
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	editConfig(d, `c.v = 4;`)
	d.Fill(".gj-saver-name", "After the other tab")
	d.Click(".gj-saver-save")
	d.WaitFor("refused", `document.querySelector('#enc-stale') || (document.querySelector('.gj-saver-status') || {textContent: ''}).textContent.includes('another tab')`)
	d.OK(!strings.Contains(storedText(d), "After the other tab"), "the stale tab wrote nothing")
}

// Lock clears what was made from the document's text: a translation, the issues a grammar check found,
// and the replies typed to comments.
func TestLockClearsTranslationsIssuesAndReplyDrafts(t *testing.T) {
	tr := httptest.NewServer(cors(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Text, TargetLang string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]string{"translatedText": "[" + req.TargetLang + "] " + req.Text, "detectedSourceLanguage": "EN"})
	}))
	defer tr.Close()
	gr := httptest.NewServer(cors(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Prompt string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		_, text, _ := strings.Cut(req.Prompt, "\nText:\n")
		i := strings.Index(text, "canvas")
		answer := "[]"
		if i >= 0 {
			b, _ := json.Marshal([]map[string]any{{"offset": utf8.RuneCountInString(text[:i]), "length": 6, "message": "plural", "suggestion": "canvases"}})
			answer = string(b)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"response": answer})
	}))
	defer gr.Close()
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "?translate="+url.QueryEscape(tr.URL)+"&grammar="+url.QueryEscape(gr.URL)+"&model=m1")
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	d.ToggleMode()
	d.Click(`button[data-tab="language"]`)
	d.CanvasDblClick(240, 164)
	d.Click(`button[data-tab="stats"]`)
	d.Click(`button[data-tab="language"]`)
	d.Select(".gj-lang-target", "FR")
	d.Click(".gj-lang-translate")
	d.WaitFor("a translation", "document.querySelector('.gj-lang-translation')")
	d.Click(".gj-lang-check")
	d.WaitFor("an issue", "document.querySelector('.gj-lang-issue')")
	// A comment on the selection and a reply typed and not sent.
	d.AnswerDialog("Review this")
	d.Focus("#input")
	d.Press("Control+Alt+n")
	d.Wait("s.comments === 1")
	d.Click(`button[data-tab="comments"]`)
	d.WaitSelector(".gj-comment-reply-row input")
	d.Fill(".gj-comment-reply-row input", "a reply I have not sent")
	d.Wait("s.drafts === 1")
	d.OK(d.State().Str("language.translated") != "" && d.State().Int("language.issues") == 1 && d.State().Int("drafts") == 1, "the text-derived state is there before the lock")

	d.Click(`button[data-tab="save"]`)
	if d.Has("#enc-lock-discard") {
		d.Click("#enc-lock-discard")
		confirmDialog(d)
	} else {
		d.Click("#enc-lock")
	}
	d.WaitFor("locked", `(document.getElementById('enc-status') || {}).textContent === 'Locked'`)
	d.Wait("s.language.translated === '' && s.language.issues === 0 && s.drafts === 0")
	d.OK(d.State().Str("language.translated") == "" && d.State().Int("language.issues") == 0 && d.State().Int("drafts") == 0,
		"Lock cleared the translation, the issues and the reply drafts")
	d.NoErrors()
}

// A link cannot set the key directory: it is offered, the person confirms it, and what is not an
// https address (http only on this machine) is refused. The session token belongs to one address.
func TestTheKeyDirectoryFromALinkIsNeverAppliedUnasked(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	people := func() {
		d.Click(`button[data-tab="people"]`)
		d.WaitSelector("#dir-settings")
	}

	// An address that is not https (and not local) is refused outright, not even offered.
	d.Goto(site, "/index.html?docstore="+url.QueryEscape("http://evil.example"))
	d.Click(`button[data-tab="save"]`)
	d.Click("#enc-unlock-passkey")
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	people()
	d.OK(d.State().Str("encryption.dir.url") == "" && d.State().Str("encryption.dir.offer") == "" && d.Has("#dir-refused"), "http://evil.example is refused: "+d.Text("#dir-status"))

	// https://dir.example is only offered: nothing is saved as confirmed, nothing uses it.
	d.Goto(site, "/index.html?docstore="+url.QueryEscape("https://dir.example"))
	d.Click(`button[data-tab="save"]`)
	d.Click("#enc-unlock-passkey")
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	people()
	d.WaitSelector("#dir-offer")
	d.OK(d.State().Str("encryption.dir.url") == "" && d.State().Str("encryption.dir.offer") == "https://dir.example" && !d.Has(".gj-recip-dir-email"), "offered, not used")
	d.OK(d.Str(`localStorage.getItem('showcase-enc:docstore-ok') || ''`) == "", "and not saved as confirmed")
	d.Click("#dir-offer-ignore")
	d.WaitFor("the offer to go", `!document.querySelector('#dir-offer')`)
	d.OK(!d.Has("#dir-offer") && d.Str(`localStorage.getItem('showcase-enc:docstore') || ''`) == "", "ignored")

	// An address written into storage directly (never confirmed) is only offered too.
	d.Eval(`localStorage.setItem('showcase-enc:docstore', 'https://planted.example'); localStorage.setItem('showcase-enc:docstoken', 'stolen'); 1`, nil)
	d.Goto(site, "/index.html")
	d.Click(`button[data-tab="save"]`)
	d.Click("#enc-unlock-passkey")
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	people()
	d.OK(d.State().Str("encryption.dir.url") == "" && d.State().Str("encryption.dir.offer") == "https://planted.example" && !d.State().Bool("encryption.dir.tokenSet"), "a planted address is not used and its token is dropped")
	d.OK(d.Str(`localStorage.getItem('showcase-enc:docstoken') || ''`) == "", "the planted token is gone from storage")
	d.Click("#dir-offer-ignore")
	d.WaitFor("the offer to go", `!document.querySelector('#dir-offer')`)

	// The token is a password field, tied to its address, and dropped when the address changes.
	d.OK(d.Attr("#dir-token", "type") == "password", "the token field is a password field")
	d.Fill("#dir-url", "http://localhost:1")
	d.Click("#dir-save")
	d.OK(strings.Contains(confirmDialog(d), "localhost:1"), "a new address is confirmed, naming the host")
	d.WaitFor("in use", `document.querySelector('#dir-status').textContent.includes('localhost:1')`)
	d.Fill("#dir-token", "secret-token")
	d.Click("#dir-save")
	d.WaitFor("the token set", `document.querySelector('#dir-token').getAttribute('aria-label').includes('is set')`)
	d.OK(d.State().Bool("encryption.dir.tokenSet") && d.Str(`localStorage.getItem('showcase-enc:docstoken-for') || ''`) == "http://localhost:1", "the token is kept for that address")
	d.Fill("#dir-url", "http://localhost:2")
	d.Fill("#dir-token", "typed-for-the-new-address")
	d.Click("#dir-save")
	confirmDialog(d)
	d.WaitFor("the new address", `document.querySelector('#dir-status').textContent.includes('localhost:2')`)
	d.OK(!d.State().Bool("encryption.dir.tokenSet") && d.Str(`localStorage.getItem('showcase-enc:docstoken') || ''`) == "", "the token was dropped with the old address")
	d.NoErrors()
}
