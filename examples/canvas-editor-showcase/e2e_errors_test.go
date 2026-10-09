//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// goWords are pieces of Go text a person must not be shown as the message of an error.
var goWords = []string{"ErrForgedFrame", "ErrUnsignedAfterFlip", "ErrUnreadable", "ErrDowngrade", "ErrNoKey", "ErrStaleConfig", "ErrLocked",
	"Repair(", "AcceptUnsigned", "Sharing.", "encrypt:", "docstore:", "encstore"}

// visible is the text of the element as it is drawn: what is inside a closed Details is not.
func visible(d *e2etest.Tab, selector string) string {
	return d.Str(`(document.querySelector(` + e2etest.J(selector) + `) || {innerText: ''}).innerText`)
}

// checkErrorBox asserts the error UX: the message is an alert in words, no Go text is visible, and the
// technical text is behind a closed native Details that holds it (and a Copy button).
func checkErrorBox(t *testing.T, d *e2etest.Tab, box, message, detail string) {
	t.Helper()
	shown := visible(d, box)
	d.OK(strings.Contains(shown, message), "the message is in words: "+shown)
	for _, w := range goWords {
		d.OK(!strings.Contains(shown, w), "no Go text is visible ("+w+"): "+shown)
	}
	d.OK(!strings.Contains(shown, detail), "the detail is not drawn while Details is closed: "+shown)
	d.OK(d.Has(box+` [role="alert"]`), "the message is an alert")
	d.OK(d.Has(box+" details > summary") && d.Has(box+" details .gj-doc-error-copy"), "Details is a native disclosure with Copy")
	d.OK(!d.Bool(`document.querySelector(`+e2etest.J(box)+` + ' details').open`), "and starts closed")
	d.OK(strings.Contains(d.Text(box+" details pre"), detail), "the technical text is kept for support: "+d.Text(box+" details pre"))
}

// A confidential document opened while the store is locked: the store's reply is a no-key error, said in
// words, in the page's language.
func TestALockedStoreErrorIsInWordsWithDetailsBehindADisclosure(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	saveConfidential(d, "Plan")
	d.Click("#enc-lock")
	d.WaitFor("locked", `(document.getElementById('enc-status') || {}).textContent === 'Locked'`)
	d.Click(".gj-doclist-open")
	d.WaitSelector(".gj-doc-error-box")
	checkErrorBox(t, d, ".gj-doc-error-box", "None of the keys unlocked here opens this document. Unlock the store", "no key that is unlocked here opens it")
	d.NoErrors()
}

func TestAnErrorIsTranslated(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "?lang=de")
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	saveConfidential(d, "Plan")
	d.Click("#enc-lock")
	d.WaitFor("locked", `document.getElementById('enc-status') && document.getElementById('enc-unlock-passkey') !== null`)
	d.Click(".gj-doclist-open")
	d.WaitSelector(".gj-doc-error-box")
	shown := visible(d, ".gj-doc-error-box")
	d.OK(strings.Contains(shown, "Keiner der hier entsperrten Schlüssel öffnet dieses Dokument"), "German: "+shown)
	d.OK(strings.Contains(d.Text(".gj-doc-error-box summary"), "Details"), "the disclosure is labelled")
}

// A stored file damaged outside the app, which no group knows: a generic message, the text behind Details.
func TestAnUnknownErrorIsGenericWithDetails(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	saveConfidential(d, "Plan")
	d.Eval(`(() => {
		for (const k of Object.keys(localStorage)) {
			if (k.includes('catalog') || k.includes('enc:')) continue;
			try {
				const rec = JSON.parse(localStorage.getItem(k));
				const bytes = atob(rec.d);
				if (bytes.indexOf('age-encryption.org') < 0) continue;
				rec.d = btoa(bytes.slice(0, Math.floor(bytes.length / 2)));
				localStorage.setItem(k, JSON.stringify(rec));
			} catch (e) {}
		}
		return 1;
	})()`, nil)
	d.Click(".gj-doclist-open")
	d.WaitSelector(".gj-doc-error-box")
	checkErrorBox(t, d, ".gj-doc-error-box", "Something went wrong", "failed to decrypt")
}

// A tab whose keys were changed in another tab says so in words; the sentinel's own text is behind Details.
func TestTheStaleTabErrorHidesTheGoText(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	editConfig(d, `c.v = 3;`)
	d.Eval(`(() => { window.dispatchEvent(new StorageEvent('storage', {key: 'showcase-enc:config', storageArea: localStorage})); return 1; })()`, nil)
	d.WaitSelector("#enc-stale")
	d.Click("#enc-unlock-passkey")
	d.WaitSelector("#enc-error")
	checkErrorBox(t, d, "#enc-error", "changed in another tab or window", "key configuration was changed in another tab")
}
