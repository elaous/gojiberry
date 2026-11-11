//go:build !(js && wasm)

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestSearchAndReplaceAcrossDocuments(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Fill(".gj-saver-name", "alpha")
	d.Click(".gj-saver-save")
	d.WaitSelector(`.gj-doclist-item:has(.gj-doc-name:text-is("alpha"))`)
	d.Fill(".gj-saver-name", "beta")
	d.Click(".gj-saver-save-new")
	d.WaitFor("two documents", "document.querySelectorAll('.gj-doclist-item').length === 2")
	open := d.Attr(".gj-doclist-current", "data-id")
	if open == "" {
		t.Fatal("no open document")
	}

	d.Click(`button[data-tab="find"]`)
	d.WaitSelector("#search-all")
	d.Fill("#sa-query", "canvas")
	d.Click("#sa-search")
	d.WaitFor("the hits", "document.querySelectorAll('#sa-hits .sa-hit').length === 2")
	d.OK(d.Count("#sa-hits .sa-hit") == 2, "both documents hold the word")
	d.OK(strings.Contains(strings.ToLower(d.Text("#sa-hits .sa-hit")), "canvas"), "a hit shows the words around the match: "+d.Text("#sa-hits .sa-hit"))
	d.Fill("#sa-query", "zzzzqx")
	d.Click("#sa-search")
	d.WaitSelector("#sa-none")

	d.Fill("#sa-query", "canvas")
	d.Fill("#sa-repl", "leaf")
	d.Click("#sa-preview")
	d.WaitSelector("#sa-changes")
	d.OK(d.Count("#sa-changes .sa-change") == 1, "the open document is left out of the preview")
	d.OK(strings.Contains(d.Text("#sa-changes .sa-after"), "leaf"), "the preview shows the new text: "+d.Text("#sa-changes .sa-after"))
	d.Click("#sa-apply")
	d.WaitSelector("#sa-undo")
	d.Fill("#sa-query", "leaf")
	d.Click("#sa-search")
	d.WaitFor("the replaced document", "document.querySelectorAll('#sa-hits .sa-hit').length === 1")
	d.OK(d.Attr("#sa-hits .sa-hit", "data-id") != open, "only the other document changed")

	other := d.Attr("#sa-hits .sa-hit", "data-id")
	d.Click("#sa-open-" + other)
	d.WaitFor("the replaced document", `JSON.parse(document.getElementById('state').textContent).text.includes('leaf')`)
	d.OK(!d.Is("s.text.includes('canvas')"), "the opened document has the new text only")
	d.Click(`button[data-tab="save"]`)
	d.WaitFor("the version kept before the replacement", "document.querySelector('.gj-versions-item')")
	d.OK(d.Count(".gj-versions-item") == 1 && d.Is("document.querySelector('.gj-versions-item .gj-versions-name').value.startsWith('Before replace')"), "the history keeps a named version: "+d.Text(".gj-versions"))
	d.Click(".gj-versions-item .gj-versions-open")
	d.WaitFor("the old version", `JSON.parse(document.getElementById('state').textContent).text.includes('canvas')`)
	d.Click(`button[data-tab="find"]`)
	d.WaitSelector("#sa-undo")

	d.Click("#sa-undo")
	d.WaitFor("the undo", "!document.querySelector('#sa-undo')")
	d.Fill("#sa-query", "leaf")
	d.Click("#sa-search")
	d.WaitSelector("#sa-none")
	d.Fill("#sa-query", "canvas")
	d.Click("#sa-search")
	d.WaitFor("both again", "document.querySelectorAll('#sa-hits .sa-hit').length === 2")
	d.NoErrors()
}

func TestSearchAllDocumentsInTheEncryptedStore(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	saveConfidential(d, "Secret plans")
	d.Click(`button[data-tab="find"]`)
	d.WaitSelector("#search-all")
	d.Fill("#sa-query", "canvas")
	d.Click("#sa-search")
	d.WaitFor("the hit", "document.querySelectorAll('#sa-hits .sa-hit').length === 1")
	d.OK(strings.Contains(strings.ToLower(d.Text("#sa-hits .sa-hit")), "canvas"), "the hit comes from the decrypted text: "+d.Text("#sa-hits .sa-hit"))
	d.NoErrors()
}

func sealedIndex(d *e2etest.Tab, target string, flip bool) []int {
	var out []int
	d.Eval(`(async () => {
		const db = await new Promise((ok, no) => { const q = indexedDB.open('gojiberry-showcase-search', 1); q.onsuccess = () => ok(q.result); q.onerror = () => no(q.error) });
		const rec = await new Promise((ok, no) => { const q = db.transaction('sealed').objectStore('sealed').get('index/`+target+`'); q.onsuccess = () => ok(q.result); q.onerror = () => no(q.error) });
		if (!rec) { db.close(); return null }
		const bytes = Array.from(rec.data);
		if (`+fmt.Sprint(flip)+`) {
			rec.data[Math.floor(rec.data.length / 2)] ^= 1;
			await new Promise((ok, no) => { const t = db.transaction('sealed', 'readwrite'); t.objectStore('sealed').put(rec); t.oncomplete = ok; t.onerror = () => no(t.error) });
		}
		db.close();
		return bytes
	})()`, &out)
	return out
}

func indexLine(d *e2etest.Tab) (indexed, read int, kept bool) {
	d.WaitSelector("#sa-index")
	el := `document.getElementById('sa-index').dataset`
	return d.Int("Number(" + el + ".indexed)"), d.Int("Number(" + el + ".read)"), d.Bool(el + ".kept === 'true'")
}

func searchFor(d *e2etest.Tab, q string, hits int) {
	d.Fill("#sa-query", q)
	d.Click("#sa-search")
	d.WaitFor("the hits", fmt.Sprintf("document.querySelectorAll('#sa-hits .sa-hit').length === %d", hits))
}

func TestSearchAllKeepsTheSealedIndexBetweenVisits(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "browser")
	d.Fill(".gj-saver-name", "alpha")
	d.Click(".gj-saver-save")
	d.WaitSelector(`.gj-doclist-item:has(.gj-doc-name:text-is("alpha"))`)
	d.Fill(".gj-saver-name", "beta")
	d.Click(".gj-saver-save-new")
	d.WaitFor("two documents", "document.querySelectorAll('.gj-doclist-item').length === 2")
	d.Click(`button[data-tab="find"]`)
	d.WaitSelector("#search-all")
	searchFor(d, "canvas", 2)
	n, read, kept := indexLine(d)
	d.OK(n == 2 && read == 2 && !kept, fmt.Sprintf("the first search reads both documents: %d %d %v", n, read, kept))
	sealed := sealedIndex(d, "browser", false)
	d.OK(len(sealed) > 40, "the index was kept")
	raw := make([]byte, len(sealed))
	for i, b := range sealed {
		raw[i] = byte(b)
	}
	d.OK(!strings.Contains(strings.ToLower(string(raw)), "canvas") && !strings.Contains(string(raw), "alpha"), "what is kept is sealed: it shows no word of a document")

	d.Reload()
	d.Click(`button[data-tab="find"]`)
	d.WaitSelector("#search-all")
	searchFor(d, "canvas", 2)
	n, read, kept = indexLine(d)
	d.OK(n == 2 && read == 0 && kept, fmt.Sprintf("the second visit uses the kept index and reads nothing: %d %d %v", n, read, kept))

	d.Click(`button[data-tab="save"]`)
	d.Fill(".gj-saver-name", "gamma")
	d.Click(".gj-saver-save-new")
	d.WaitFor("three documents", "document.querySelectorAll('.gj-doclist-item').length === 3")
	d.Reload()
	d.Click(`button[data-tab="find"]`)
	d.WaitSelector("#search-all")
	searchFor(d, "canvas", 3)
	n, read, kept = indexLine(d)
	d.OK(n == 3 && read == 1 && kept, fmt.Sprintf("only the new document is read: %d %d %v", n, read, kept))

	before := sealedIndex(d, "browser", true)
	d.OK(len(before) > 0, "there is an index to alter")
	d.Reload()
	d.Click(`button[data-tab="find"]`)
	d.WaitSelector("#search-all")
	searchFor(d, "canvas", 3)
	n, read, kept = indexLine(d)
	d.OK(n == 3 && read == 3 && !kept, fmt.Sprintf("an altered index is rebuilt: %d %d %v", n, read, kept))
	after := sealedIndex(d, "browser", false)
	d.OK(len(after) > 0 && fmt.Sprint(after) != fmt.Sprint(before), "and a good one is kept in its place")
	d.NoErrors()
}

func TestSearchAllKeepsTheEncryptedStoresIndexSealedUnderTheOwnerKey(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	saveConfidential(d, "Secret plans")
	d.Click(`button[data-tab="find"]`)
	d.WaitSelector("#search-all")
	searchFor(d, "canvas", 1)
	n, read, kept := indexLine(d)
	d.OK(n == 1 && read == 1 && !kept, fmt.Sprintf("the first search reads the document: %d %d %v", n, read, kept))
	sealed := sealedIndex(d, "browser", false)
	d.OK(len(sealed) > 40, "the index was kept")
	raw := make([]byte, len(sealed))
	for i, b := range sealed {
		raw[i] = byte(b)
	}
	for _, secret := range []string{"canvas", "Canvas", "Secret", "plans"} {
		d.OK(!strings.Contains(string(raw), secret), "the kept index shows no "+secret)
	}

	d.Reload()
	d.Click(`button[data-tab="find"]`)
	d.WaitSelector("#search-all")
	d.Fill("#sa-query", "canvas")
	d.Click("#sa-search")
	d.WaitSelector("#sa-index")
	n, read, kept = indexLine(d)
	d.OK(n == 0 && !kept && !d.Has("#sa-hits .sa-hit"), fmt.Sprintf("a locked store gives no hit and uses no kept index: %d %d %v", n, read, kept))
	d.OK(fmt.Sprint(sealedIndex(d, "browser", false)) == fmt.Sprint(sealed), "and the kept index is left as it was")

	d.Click(`button[data-tab="save"]`)
	d.Click("#enc-unlock-passkey")
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	d.Click(`button[data-tab="find"]`)
	searchFor(d, "canvas", 1)
	n, read, kept = indexLine(d)
	d.OK(n == 1 && read == 0 && kept, fmt.Sprintf("after the unlock the kept index is used: %d %d %v", n, read, kept))
	d.OK(strings.Contains(strings.ToLower(d.Text("#sa-hits .sa-hit")), "canvas"), "and the hit shows the decrypted words: "+d.Text("#sa-hits .sa-hit"))

	sealedIndex(d, "browser", true)
	d.Reload()
	d.Click(`button[data-tab="save"]`)
	d.Click("#enc-unlock-passkey")
	d.WaitFor("unlocked", `(document.getElementById('enc-status') || {}).textContent === 'Unlocked'`)
	d.Click(`button[data-tab="find"]`)
	searchFor(d, "canvas", 1)
	n, read, kept = indexLine(d)
	d.OK(n == 1 && read == 1 && !kept, fmt.Sprintf("an altered index is rebuilt: %d %d %v", n, read, kept))
	d.NoErrors()
}
