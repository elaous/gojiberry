//go:build !(js && wasm)

package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/elaous/gojiberry/internal/e2etest"
)

const (
	secretDocName = "Salary plan 2026"
	secretDocText = "Everything on this canvas" // a phrase of the sample document's text
)

// storedText is everything the browser's localStorage holds for the showcase's store, decoded the
// way a thief of the storage reads it: each record's base64 body as bytes, with the keys.
func storedText(d *e2etest.Tab) string {
	return d.Str(`(() => {
		let all = '';
		for (const k of Object.keys(localStorage)) {
			all += k + '\n' + localStorage.getItem(k) + '\n';
			try { all += atob(JSON.parse(localStorage.getItem(k)).d) + '\n'; } catch (e) {}
		}
		return all;
	})()`)
}

func confirmDialog(d *e2etest.Tab) string {
	d.WaitSelector(`[role="alertdialog"]`)
	text := d.Text(`[role="alertdialog"]`)
	d.Click(`[role="alertdialog"] .gj-dialog-actions button:last-child`)
	return text
}

// TestEncryptedStore turns on "Encrypt this store" with a passkey (PRF, virtual authenticator) and a
// recovery key, makes a document confidential, and follows it through lock, reload, unlock, a
// sealed pack and a re-import.
func TestEncryptedStore(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost() // a passkey needs a domain name
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	d.Click(`button[data-tab="save"]`)

	// Off by default, and the store works as it always did.
	d.WaitSelector("#enc-panel")
	d.OK(!d.State().Bool("encryption.enabled") && !d.Has(".gj-doclist-protect"), "encryption is off by default: no Make confidential control")
	d.OK(strings.Contains(d.Text(`.gj-saver-select[data-field="Save to"] option[value="browser"]`), "localStorage"), "the browser target says localStorage")

	// A folder for the document, so the pack of the folder holds it.
	d.Fill(".gj-doclist-newfolder-name", "Vault")
	d.WaitFor("the create button", `!document.querySelector('.gj-doclist-newfolder').disabled`)
	d.Click(".gj-doclist-newfolder")
	d.WaitFor("the folder", "document.querySelectorAll('.gj-doclist-folder').length === 1")
	d.Click(".gj-doclist-folder-open")
	d.WaitFor("inside the folder", "document.querySelector('.gj-breadcrumb-current')")

	// Turn encryption on: a passkey and a recovery key.
	d.Click("#enc-switch-box .gj-switch-track")
	d.WaitSelector("#enc-enable")
	d.OK(d.Checked("#enc-opt-passkey input") && d.Checked("#enc-opt-recovery input"), "the form offers a passkey and a recovery key")
	d.Click("#enc-enable")
	// The recovery key's secret comes first, before anything is committed.
	d.WaitSelector("#enc-recovery-secret")
	recovery := d.Value("#enc-recovery-secret")
	d.OK(strings.HasPrefix(recovery, "AGE-SECRET-KEY-1"), "the recovery key is shown once: "+recovery[:20])
	d.OK(d.Has("#enc-recovery-warning"), "with its warning")
	d.OK(!d.Has("#enc-status") && !d.State().Bool("encryption.enabled") && !strings.Contains(storedText(d), "showcase-enc:config"), "nothing is committed while the secret is on screen")
	d.OK(d.Disabled("#enc-recovery-commit"), "the key is added only after the person says they saved it")
	d.SetChecked("#enc-recovery-ack input", true)
	d.WaitFor("the commit button", `!document.querySelector('#enc-recovery-commit').disabled`)
	d.Click("#enc-recovery-commit")
	d.WaitFor("encryption on and unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	d.WaitFor("the secret to be dropped", `!document.querySelector('#enc-recovery-secret')`)
	d.OK(!strings.Contains(d.Str(`document.body.innerHTML`), recovery), "the recovery key is nowhere in the page once saved")
	d.OK(!strings.Contains(storedText(d), recovery), "nor in the browser's storage")
	d.OK(d.Text("#enc-status") == "Unlocked", "unlocked after setup")

	// Save a document in the folder. Not confidential yet: its name is in the catalog, its text is
	// already encrypted at rest.
	d.Fill(".gj-saver-name", secretDocName)
	d.Click(".gj-saver-save")
	d.WaitFor("the document", "document.querySelectorAll('.gj-doclist-item').length === 1")
	raw := storedText(d)
	d.OK(!strings.Contains(raw, secretDocText), "the text of a saved document is not in the storage")
	d.OK(strings.Contains(raw, secretDocName), "an ordinary document's name is in the catalog, as always")
	d.OK(d.Has(".gj-doclist-protect") && !d.Has(".gj-doclist-item .gj-doc-confidential"), "an ordinary document offers Make confidential and has no lock")

	// Make it confidential: a question that says what changes.
	d.Click(".gj-doclist-protect")
	question := confirmDialog(d)
	for _, want := range []string{"name and tags move inside", "searching for its name stops working", "encrypted again"} {
		d.OK(strings.Contains(question, want), "the question says: "+want)
	}
	d.WaitFor("confidential", "document.querySelector('.gj-doclist-item .gj-doc-confidential')")
	d.OK(d.Attr(".gj-doclist-item .gj-doc-confidential [role=img], .gj-doclist-item .gj-doc-confidential [aria-label]", "aria-label") == "Confidential document", "the lock has its label")
	d.OK(strings.Contains(d.Text(".gj-doclist-item .gj-doc-name"), secretDocName), "its real name is shown while unlocked")
	d.OK(d.Text(".gj-doclist-protect") == "Make not confidential", "and the switch offers the way back")
	raw = storedText(d)
	for _, secret := range []string{secretDocName, secretDocText, "Salary"} {
		d.OK(!strings.Contains(raw, secret), "the storage holds no "+secret)
	}

	// The People tab: the store's keys, a passkey and a recovery key.
	d.Click(`button[data-tab="people"]`)
	d.WaitFor("the owner keys", `document.querySelectorAll('.gj-recip-owner').length === 2`)
	types := strings.Join(d.Texts(".gj-recip-owner .gj-recip-type"), ",")
	d.OK(strings.Contains(types, "passkey-prf") && strings.Contains(types, "age-x25519"), "the keys are listed: "+types)
	d.Click(`button[data-tab="save"]`)

	// The sealed pack of the folder: the confidential document is in it, and nothing readable of it.
	d.Click(".gj-breadcrumb-link")
	d.WaitFor("the top level", "!document.querySelector('.gj-breadcrumb-link')")
	name, pack := d.Download(func() { d.Click(".gj-doclist-folder-pack") })
	d.OK(name == "Vault.opendoc" && strings.HasPrefix(string(pack), "OPENDOC\x00"), "a pack of the folder: "+name)
	for _, secret := range []string{secretDocName, secretDocText, "Salary"} {
		d.OK(!strings.Contains(string(pack), secret), "the pack holds no "+secret)
	}
	d.OK(strings.Contains(d.Text(".gj-saver-status"), "pack of 1 documents") && !strings.Contains(d.Text(".gj-saver-status"), "left out"), "the status counts the sealed document: "+d.Text(".gj-saver-status"))

	// Lock: the key is forgotten, the list shows the placeholder.
	d.Click("#enc-lock")
	d.WaitFor("locked", `(document.getElementById('enc-status') || {}).textContent === 'Locked'`)
	d.OK(d.Text("#enc-status") == "Locked", "the panel says locked")
	d.Click(".gj-doclist-folder-open")
	d.WaitFor("inside", "document.querySelector('.gj-breadcrumb-current')")
	d.WaitFor("the placeholder", `document.querySelector('.gj-doclist-item .gj-doc-name').textContent.startsWith('Confidential document')`)
	d.OK(!strings.Contains(d.Text(".gj-doclist"), secretDocName), "the real name is gone from the list when locked")
	d.OK(d.Has(".gj-doclist-reveal"), "a Reveal action is offered for the hidden name")
	d.Click(".gj-doclist-open")
	d.WaitSelector(".gj-doc-error")
	d.OK(strings.Contains(d.Text(".gj-doc-error"), "Unlock the store"), "opening it says what to do: "+d.Text(".gj-doc-error"))
	d.Click(".gj-doclist-reveal")
	d.WaitFor("the reveal refused", `document.querySelector('.gj-doc-error') && document.querySelector('.gj-doc-error').textContent.includes('Unlock the store')`)

	// A locked export leaves the confidential document out and says how many.
	d.Click(".gj-breadcrumb-link")
	d.WaitFor("the top level", "!document.querySelector('.gj-breadcrumb-link')")
	lockedName, lockedPack := d.Download(func() { d.Click(".gj-doclist-folder-pack") })
	d.OK(lockedName == "Vault.opendoc" && !strings.Contains(string(lockedPack), secretDocText), "a locked pack has none of the document")
	d.WaitFor("the left-out count", `document.querySelector('.gj-saver-status').textContent.includes('1 confidential documents left out')`)

	// Reload: a fresh page is locked, with the placeholder and a clear message on open.
	d.Reload()
	d.Click(`button[data-tab="save"]`)
	d.WaitFor("the panel", "document.querySelector('#enc-status')")
	d.OK(d.Text("#enc-status") == "Locked", "a reload starts locked")
	d.WaitFor("the state", `JSON.parse(document.getElementById('state').textContent).encryption.enabled === true`)
	d.OK(!d.State().Bool("encryption.unlocked") && d.State().Bool("encryption.enabled"), "and the page state agrees")
	d.OK(d.Has("#enc-unlock-passkey") && d.Has("#enc-recovery-input"), "unlock is offered with the passkey and the recovery key")
	d.Click(".gj-doclist-folder-open")
	d.WaitFor("inside", "document.querySelector('.gj-breadcrumb-current')")
	d.WaitFor("the placeholder after the reload", `document.querySelector('.gj-doclist-item .gj-doc-name').textContent.startsWith('Confidential document')`)
	d.Click(".gj-doclist-open")
	d.WaitSelector(".gj-doc-error")
	d.OK(strings.Contains(d.Text(".gj-doc-error"), "Unlock the store"), "open fails with a clear message after the reload")
	// A wrong recovery key is refused; the right one unlocks.
	d.Fill("#enc-recovery-input", "AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ")
	d.WaitFor("the button", `!document.querySelector('#enc-unlock-recovery').disabled`)
	d.Click("#enc-unlock-recovery")
	d.WaitSelector("#enc-error")
	d.OK(d.Text("#enc-status") == "Locked", "a wrong key does not unlock")
	d.Fill("#enc-recovery-input", recovery)
	d.WaitFor("the button", `!document.querySelector('#enc-unlock-recovery').disabled`)
	d.Click("#enc-unlock-recovery")
	d.WaitFor("unlocked with the recovery key", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	d.WaitFor("the name revealed on unlock", `document.querySelector('.gj-doclist-item .gj-doc-name').textContent.includes('Salary plan 2026')`)
	d.Click("#enc-lock")
	d.WaitFor("locked", `(document.getElementById('enc-status') || {}).textContent === 'Locked'`)

	// Unlock with the passkey (PRF) and open the document.
	d.Click("#enc-unlock-passkey")
	d.WaitFor("unlocked with the passkey", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	d.WaitFor("the name revealed", `document.querySelector('.gj-doclist-item .gj-doc-name').textContent.includes('Salary plan 2026')`)
	d.Click(".gj-doclist-open")
	d.WaitFor("opened", `document.querySelector('.gj-saver-status').textContent.includes('Salary plan 2026 opened')`)

	// Delete it (trash, then for good), then bring it back from the sealed pack: locked it needs the
	// key and is left out; unlocked it comes in confidential again.
	d.Click(".gj-doclist-trash")
	d.WaitFor("trashed", "document.querySelectorAll('.gj-doclist-item').length === 0")
	d.Click(".gj-trash-delete")
	confirmDialog(d)
	d.WaitFor("the trash empty", "document.querySelectorAll('.gj-trash-item').length === 0")
	d.Click(".gj-breadcrumb-link")
	d.WaitFor("the top level", "!document.querySelector('.gj-breadcrumb-link')")
	// The document that was open was deleted: what the editor holds is stored nowhere, so Lock asks
	// first; this test means to drop it.
	d.OK(d.Has("#enc-lock-discard"), "locking with an unsaved document offers to discard it")
	d.Click("#enc-lock-discard")
	d.WaitSelector(`[role="alertdialog"]`)
	d.OK(strings.Contains(d.Text(`[role="alertdialog"]`), "cannot be brought back"), "discarding is asked about, saying it is lost")
	d.Click(`[role="alertdialog"] .gj-dialog-actions button:first-child`) // cancel
	d.OK(d.Str(`(document.getElementById('enc-status') || {}).textContent`) == "Unlocked", "cancelling keeps the store unlocked")
	d.Click("#enc-lock-discard")
	confirmDialog(d)
	d.WaitFor("locked", `(document.getElementById('enc-status') || {}).textContent === 'Locked'`)
	d.ChooseFile(func() { d.Click(".gj-doclist-import-pack") }, "Vault.opendoc", pack)
	d.WaitFor("the key needed", `document.querySelector('.gj-saver-status').textContent.includes('need a key')`)
	d.Click(".gj-doclist-folder-open")
	d.WaitFor("inside", "document.querySelector('.gj-breadcrumb-current')")
	d.OK(d.Count(".gj-doclist-item") == 0, "without the key the confidential document stays out")
	d.Click(".gj-breadcrumb-link")
	d.WaitFor("the top level", "!document.querySelector('.gj-breadcrumb-link')")

	d.Click("#enc-unlock-passkey")
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	d.ChooseFile(func() { d.Click(".gj-doclist-import-pack") }, "Vault.opendoc", pack)
	d.WaitFor("the pack imported", `document.querySelector('.gj-saver-status').textContent.includes('1 documents and 0 folders imported')`)
	d.Click(".gj-doclist-folder-open")
	d.WaitFor("the document is back, locked in", `document.querySelectorAll('.gj-doclist-item .gj-doc-confidential').length === 1`)
	d.Click(".gj-doclist-reveal")
	d.WaitFor("the name read with the key", `document.querySelector('.gj-doclist-item .gj-doc-name').textContent.includes('Salary plan 2026')`)
	d.OK(!strings.Contains(storedText(d), secretDocName), "the re-imported document is as encrypted as before")

	// Make it not confidential again: the name goes back to the catalog, the lock goes.
	d.Click(".gj-doclist-protect")
	confirmDialog(d)
	d.WaitFor("not confidential", `document.querySelectorAll('.gj-doclist-item .gj-doc-confidential').length === 0`)
	d.OK(strings.Contains(storedText(d), secretDocName) && !strings.Contains(storedText(d), secretDocText), "the name is in the catalog again; the text is still encrypted")

	// Locked, saving is refused and nothing is written.
	d.Click("#enc-lock")
	d.WaitFor("locked", `(document.getElementById('enc-status') || {}).textContent === 'Locked'`)
	// Saving while locked is refused too, not written.
	d.Fill(".gj-saver-name", "while locked")
	d.Click(".gj-saver-save")
	d.WaitFor("the save refused", `(document.querySelector('.gj-doc-error') || {textContent: ''}).textContent.includes('Unlock the store')`)
	d.OK(!strings.Contains(storedText(d), "while locked"), "nothing was written while locked")

	d.NoErrors()
}

// TestEncryptedStorePasskeyNeedsASecondKey: a store with only a passkey refuses to make a
// document confidential, in the words of the rule, and the People tab offers the way out.
func TestEncryptedStorePasskeyNeedsASecondKey(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	d.Click(`button[data-tab="save"]`)
	d.Click("#enc-switch-box .gj-switch-track")
	d.WaitSelector("#enc-enable")
	d.SetChecked("#enc-opt-recovery input", false)
	d.WaitSelector("#enc-second")
	d.OK(strings.Contains(d.Text("#enc-second"), "A passkey alone is risky") && strings.Contains(d.Text("#enc-second"), "EVERY document"), "the form warns that all documents are at risk: "+d.Text("#enc-second"))
	d.OK(d.Disabled("#enc-enable"), "a passkey alone needs an explicit acknowledgement")
	d.SetChecked("#enc-passkey-ack input", true)
	d.WaitFor("the button", `!document.querySelector('#enc-enable').disabled`)
	d.Click("#enc-enable")
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)

	d.Fill(".gj-saver-name", "Solo")
	d.Click(".gj-saver-save")
	d.WaitFor("the document", "document.querySelectorAll('.gj-doclist-item').length === 1")
	d.Click(".gj-doclist-protect")
	confirmDialog(d)
	d.WaitFor("the refusal", `(document.querySelector('.gj-doclist .gj-doc-error') || {textContent: ''}).textContent.includes('a passkey cannot be the only key')`)
	d.OK(!d.Has(".gj-doclist-item .gj-doc-confidential") && strings.Contains(d.Text(".gj-doclist-item .gj-doc-name"), "Solo"), "it stays an ordinary document, with its name")

	d.Click(`button[data-tab="people"]`)
	d.WaitSelector(".gj-recip-need-second")
	d.OK(d.Disabled(".gj-recip-add-mine"), "a second passkey waits for a recovery key")
	d.Click(".gj-recip-recovery")
	d.WaitSelector(".gj-recip-secret")
	d.OK(strings.HasPrefix(d.Value(".gj-recip-secret"), "AGE-SECRET-KEY-1"), "the recovery key is shown once")
	d.OK(d.Count(".gj-recip-owner") == 1 && d.Disabled(".gj-recip-recovery-commit"), "it is not an owner key until the secret is confirmed saved")
	d.SetChecked(".gj-recip-recovery-ack input", true)
	d.WaitFor("the commit button", `!document.querySelector('.gj-recip-recovery-commit').disabled`)
	d.Click(".gj-recip-recovery-commit")
	d.WaitFor("the secret to be dropped", `!document.querySelector('.gj-recip-secret')`)
	d.WaitFor("two owner keys", `document.querySelectorAll('.gj-recip-owner').length === 2`)

	d.Click(`button[data-tab="save"]`)
	d.Click(".gj-doclist-protect")
	confirmDialog(d)
	d.WaitFor("confidential", "document.querySelector('.gj-doclist-item .gj-doc-confidential')")
	d.NoErrors()
}

// TestEncryptedStoreWithAnSSHKey: the owner key is a pasted SSH public key; the store starts locked
// and the private key file unlocks it (a stranger's file does not).
func TestEncryptedStoreWithAnSSHKey(t *testing.T) {
	pub, priv := sshKeyPair(t, "me@laptop")
	_, strangerPriv := sshKeyPair(t, "stranger")
	site := e2etest.NewSite(t)
	d := site.Open(t, "")
	d.Click(`button[data-tab="save"]`)
	d.Click("#enc-switch-box .gj-switch-track")
	d.WaitSelector("#enc-enable")
	d.SetChecked("#enc-opt-passkey input", false)
	d.SetChecked("#enc-opt-recovery input", false)
	d.Fill("#enc-ssh", pub)
	d.Click("#enc-enable")
	d.WaitFor("encrypted and locked", `(document.getElementById('enc-status') || {}).textContent === 'Locked'`)
	d.OK(d.Has("#enc-unlock-ssh") && !d.Has("#enc-unlock-passkey"), "unlocking takes the private key file")
	d.ChooseFile(func() { d.Click("#enc-unlock-ssh") }, "id_ed25519", strangerPriv)
	d.WaitSelector("#enc-error")
	d.OK(strings.Contains(d.Text("#enc-error"), "not one of this store's keys") && d.Text("#enc-status") == "Locked", "a stranger's key does not unlock: "+d.Text("#enc-error"))
	d.ChooseFile(func() { d.Click("#enc-unlock-ssh") }, "id_ed25519", priv)
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)

	d.Fill(".gj-saver-name", "Contract")
	d.Click(".gj-saver-save")
	d.WaitFor("the document", "document.querySelectorAll('.gj-doclist-item').length === 1")
	d.Click(".gj-doclist-protect")
	confirmDialog(d)
	d.WaitFor("confidential: an SSH key is a key that is not a passkey", "document.querySelector('.gj-doclist-item .gj-doc-confidential')")
	d.OK(!strings.Contains(storedText(d), "Contract"), "the name is not in the storage")
	d.NoErrors()
}

func sshKeyPair(t *testing.T, comment string) (pub string, priv []byte) {
	t.Helper()
	p, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := ssh.NewPublicKey(p)
	if err != nil {
		t.Fatal(err)
	}
	blk, err := ssh.MarshalPrivateKey(k, comment)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp))) + " " + comment, pem.EncodeToMemory(blk)
}
