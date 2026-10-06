//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestADamagedSavedFileIsRestoredFromItsPreviousCopy corrupts the file of a document
// saved in the browser's localStorage, as a cut write or a bad disk would, and opens it:
// the text of the previous save comes back, and the status line says what happened.
func TestADamagedSavedFileIsRestoredFromItsPreviousCopy(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.Eval(`localStorage.clear()`, nil)
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Type("FIRSTSAVE ")
	d.Click(`button[data-tab="save"]`)
	d.Fill(".gj-saver-name", "doc")
	d.Click(".gj-saver-save")
	d.WaitSelector(".gj-doclist-item")
	d.WaitFor("saved", `(document.querySelector('.gj-status-item[data-item="save"]') || {dataset: {}}).dataset.state === 'saved'`)
	d.CanvasClick(240, 164)
	d.Type("SECONDSAVE ")
	d.Click(".gj-saver-save")
	d.WaitFor("the second save to be the current file", `(() => {
		const c = JSON.parse(atob(JSON.parse(localStorage.getItem('showcase:catalog:index')).d));
		return Object.values(c.docs).some((e) => e.prev && e.sum);
	})()`)

	// Cut the current file in half.
	d.Eval(`(() => {
		const c = JSON.parse(atob(JSON.parse(localStorage.getItem('showcase:catalog:index')).d));
		const e = Object.values(c.docs)[0];
		const rec = JSON.parse(localStorage.getItem('showcase:' + e.key));
		const bytes = atob(rec.d);
		rec.d = btoa(bytes.slice(0, Math.floor(bytes.length / 2)));
		localStorage.setItem('showcase:' + e.key, JSON.stringify(rec));
		return 1
	})()`, nil)

	d.Click(".gj-doclist-item .gj-doclist-open")
	d.WaitFor("the restoration to be reported", `(document.querySelector('.gj-saver-status') || {textContent: ''}).textContent.includes('was damaged')`)
	text := d.State().Str("text")
	d.OK(strings.Contains(text, "FIRSTSAVE") && !strings.Contains(text, "SECONDSAVE"), "the previous save's text came back: "+text[:min(60, len(text))])

	// Opening it again reads the restored file, quietly.
	d.Click(".gj-doclist-item .gj-doclist-open")
	d.WaitFor("a plain open", `(document.querySelector('.gj-saver-status') || {textContent: ''}).textContent.includes('opened')`)

	// And the check finds nothing wrong now.
	d.Click(".gj-doclist-check")
	d.WaitFor("the check's report", `(document.querySelector('.gj-saver-status') || {textContent: ''}).textContent.startsWith('Checked 1 documents: all fine')`)
}

// TestTheCatalogIsUpdatedUnderTheBrowsersLock holds the Web Lock that another tab would
// hold while it saves, and shows a save in this page waits for it.
func TestTheCatalogIsUpdatedUnderTheBrowsersLock(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.Eval(`localStorage.clear()`, nil)
	d.OK(d.Is("typeof s.persisted === 'boolean'"), "the page asked about persistent storage")
	d.Click(`button[data-tab="save"]`)
	d.Fill(".gj-saver-name", "locked")
	d.Eval(`(() => { window.__held = new Promise((held) => navigator.locks.request('gojiberry-docstore:showcase:#cas', () => new Promise((release) => { window.__release = release; held(true) }))); return 1 })()`, nil)
	d.Eval(`window.__held`, nil)
	d.Click(".gj-saver-save")
	d.Sleep(700) // a negative check: the save must not get through while the lock is held elsewhere
	d.OK(d.Count(".gj-doclist-item") == 0, "nothing was saved while the lock was held")
	d.Eval(`window.__release()`, nil)
	d.WaitSelector(".gj-doclist-item")
}

// TestTwoTabsDoNotLoseACatalogUpdate plays the other tab: while this page's save waits
// for the lock, the catalog is changed under it (a folder the other tab made). The save
// notices the catalog is not the one it read, starts over on the new one, and keeps both.
func TestTwoTabsDoNotLoseACatalogUpdate(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.Eval(`localStorage.clear()`, nil)
	d.Click(`button[data-tab="save"]`)
	d.Fill(".gj-saver-name", "first")
	d.Click(".gj-saver-save")
	d.WaitSelector(".gj-doclist-item")
	d.WaitFor("saved", `(document.querySelector('.gj-status-item[data-item="save"]') || {dataset: {}}).dataset.state === 'saved'`)

	d.Eval(`(() => { window.__held = new Promise((held) => navigator.locks.request('gojiberry-docstore:showcase:#cas', () => new Promise((release) => { window.__release = release; held(true) }))); return 1 })()`, nil)
	d.Eval(`window.__held`, nil)
	d.Fill(".gj-saver-name", "second")
	d.Click(".gj-saver-save-new")
	d.Sleep(500) // the save has read the catalog and waits for the lock
	// The other tab's change, made while the lock is held.
	d.Eval(`(() => {
		const k = 'showcase:catalog:index';
		const rec = JSON.parse(localStorage.getItem(k));
		const c = JSON.parse(atob(rec.d));
		c.folders = Object.assign(c.folders || {}, {'11111111-1111-4111-8111-111111111111': {name: 'From the other tab'}});
		rec.d = btoa(JSON.stringify(c));
		localStorage.setItem(k, JSON.stringify(rec));
		return 1
	})()`, nil)
	d.Eval(`window.__release()`, nil)
	d.WaitFor("both documents", "document.querySelectorAll('.gj-doclist-item').length === 2")
	var folders int
	d.Eval(`(() => {
		const c = JSON.parse(atob(JSON.parse(localStorage.getItem('showcase:catalog:index')).d));
		return Object.values(c.folders || {}).filter((f) => f.name === 'From the other tab').length
	})()`, &folders)
	d.OK(folders == 1, "the other tab's folder is still in the catalog")
}
