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

func editConfig(d *e2etest.Tab, body string) {
	d.Eval(`(() => {
		const c = JSON.parse(localStorage.getItem('showcase-enc:config'));
		`+body+`
		localStorage.setItem('showcase-enc:config', JSON.stringify(c));
		return 1;
	})()`, nil)
}

func docsText(d *e2etest.Tab) string {
	return d.Str(`Object.keys(localStorage).filter(k => !k.startsWith('showcase-enc:')).sort().map(k => k + '\n' + localStorage.getItem(k)).join('\n')`)
}

func reloadToSave(d *e2etest.Tab) {
	d.Reload()
	d.Click(`button[data-tab="save"]`)
	d.WaitSelector("#enc-panel")
}

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
	d.Eval(`localStorage.setItem('showcase-enc:config', `+e2etest.J(older)+`); 1`, nil)
	reloadToSave(d)
	d.Fill("#enc-recovery-input", recovery)
	d.WaitFor("the button", `!document.querySelector('#enc-unlock-recovery').disabled`)
	d.Click("#enc-unlock-recovery")
	d.WaitSelector("#enc-error")
	d.OK(strings.Contains(d.Text("#enc-error"), "configuration on this device was changed") && d.Text("#enc-status") == "Locked", "the recovery key refuses the older list: "+d.Text("#enc-error"))
	d.NoErrors()
}

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

	d.Eval(`localStorage.removeItem('showcase-enc:config')`, nil)
	reloadToSave(d)
	d.WaitSelector("#enc-broken")
	d.OK(d.State().Str("encryption.broken") == "missing" && !d.State().Bool("encryption.enabled"), "the page knows the configuration is missing")
	d.OK(strings.Contains(d.Text("#enc-broken"), "Configuration missing: encrypted files found"), "it says so: "+d.Text("#enc-broken"))
	d.OK(!d.Has("#enc-switch-box"), "there is no 'encryption off' switch to press")
	d.Fill(".gj-saver-name", "Plain attempt")
	d.Click(".gj-saver-save")
	d.Sleep(300)
	d.OK(!strings.Contains(storedText(d), "Plain attempt"), "nothing was saved in the clear")
	d.OK(d.Str(`Object.keys(localStorage).filter(k => k.startsWith('showcase:')).sort().join(',')`) == files, "the stored files are as they were")

	d.Fill("#enc-restore-recovery-input", recovery)
	d.WaitFor("the button", `!document.querySelector('#enc-restore-recovery').disabled`)
	d.Click("#enc-restore-recovery")
	d.WaitFor("restored", `document.querySelector('#enc-status') && !document.querySelector('#enc-broken')`)
	d.Wait("s.encryption.enabled && s.encryption.unlocked && s.encryption.broken === ''")
	d.OK(d.State().Bool("encryption.enabled") && d.State().Bool("encryption.unlocked") && d.State().Str("encryption.broken") == "", "the store is on and unlocked again")
	d.WaitFor("the document is listed", "document.querySelectorAll('.gj-doclist-item').length >= 1")
	d.Click(".gj-doclist-open")
	d.WaitFor("it opens", `document.querySelector('.gj-saver-status').textContent.includes('opened')`)

	d.Eval(`localStorage.setItem('showcase-enc:config', '{not json')`, nil)
	reloadToSave(d)
	d.WaitSelector("#enc-broken")
	d.OK(d.State().Str("encryption.broken") == "corrupt" && strings.Contains(d.Text("#enc-broken"), "could not be read"), "corrupt: "+d.Text("#enc-broken"))
	d.Fill(".gj-saver-name", "Plain attempt two")
	d.Click(".gj-saver-save")
	d.Sleep(300)
	d.OK(!strings.Contains(storedText(d), "Plain attempt two"), "nothing was saved in the clear beside the ciphertext")

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
	d.OK(d.Str(`Object.keys(localStorage).filter(k => k.startsWith('showcase') && k !== 'showcase:last-target' && k !== 'showcase:catalog:index').join(',')`) == "", "nothing is left in storage: "+d.Str(`Object.keys(localStorage).join(',')`))
	d.OK(!strings.Contains(storedText(d), "Kept in ciphertext") && d.Count(".gj-doclist-item") == 0, "no document is left")
	d.NoErrors()
}

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

func TestAStaleTabLocksItselfAndRefusesToWrite(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	d.Fill(".gj-saver-name", "Before")
	d.Click(".gj-saver-save")
	d.WaitFor("saved", "document.querySelectorAll('.gj-doclist-item').length >= 1")

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

	d.Goto(site, "/index.html?docstore="+url.QueryEscape("http://evil.example"))
	d.Click(`button[data-tab="save"]`)
	d.Click("#enc-unlock-passkey")
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	people()
	d.OK(d.State().Str("encryption.dir.url") == "" && d.State().Str("encryption.dir.offer") == "" && d.Has("#dir-refused"), "http://evil.example is refused: "+d.Text("#dir-status"))

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
