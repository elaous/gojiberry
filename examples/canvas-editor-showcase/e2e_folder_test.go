//go:build !(js && wasm)

package main

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

const pickerShim = `window.showDirectoryPicker = async () => (await navigator.storage.getDirectory()).getDirectoryHandle('picked', {create: true});`

const listPicked = `window.__files = null; (async () => { const d = await (await navigator.storage.getDirectory()).getDirectoryHandle('picked', {create: true}); const n = []; for await (const [k] of d.entries()) n.push(k); window.__files = n })(); 1`

func resetFolder(d *e2etest.Tab) {
	d.Eval(`window.__clean = false; localStorage.clear(); (async () => {
		const r = await navigator.storage.getDirectory();
		try { await r.removeEntry('picked', {recursive: true}) } catch (e) {}
		await new Promise((done) => { const q = indexedDB.deleteDatabase('gojiberry-folders'); q.onsuccess = q.onerror = q.onblocked = done });
		window.__clean = true })(); 1`, nil)
	d.WaitFor("the clean-up", "window.__clean === true")
	d.Reload()
}

func TestSavingToAChosenFolder(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	resetFolder(d)
	d.Eval(pickerShim, nil)
	d.Click(`button[data-tab="save"]`)
	d.OK(d.Has(".gj-folder-choose"), "with no folder chosen the picker offers Choose a folder")
	d.Click(".gj-folder-choose")
	d.WaitFor("the folder to be in use", `(document.querySelector('.gj-folder-name') || {textContent: ''}).textContent === 'Saving to the folder picked'`)
	d.Fill(".gj-saver-name", "plan")
	d.WaitFor("the form to be ready again", `!document.querySelector('.gj-saver-save').disabled`)
	d.Click(".gj-saver-save")
	d.WaitFor("the saved document", "document.querySelectorAll('.gj-doclist-item').length === 1")

	d.Eval(listPicked, nil)
	d.WaitFor("the folder listing", "window.__files && window.__files.length >= 2")
	names := d.Strs("window.__files")
	catalog := base64.RawURLEncoding.EncodeToString([]byte("catalog:index"))
	hasCatalog, hasDoc := false, false
	for _, n := range names {
		raw, err := base64.RawURLEncoding.DecodeString(n)
		if err != nil {
			t.Fatalf("a file that is not named as the Go tools do: %q", n)
		}
		hasCatalog = hasCatalog || n == catalog
		hasDoc = hasDoc || strings.HasSuffix(string(raw), ".json")
	}
	d.OK(hasCatalog && hasDoc, "the catalog and the document are files in the folder: "+strings.Join(names, ","))

	d.Reload()
	d.Eval(pickerShim, nil)
	d.Click(`button[data-tab="save"]`)
	d.WaitFor("the remembered folder", `(document.querySelector('.gj-folder-name') || {textContent: ''}).textContent === 'Saving to the folder picked'`)
	d.WaitFor("the restored folder to be selected", `document.querySelector('.gj-saver-select[data-field="Save to"]').value === 'folder'`)
	d.WaitFor("the document in the folder", `Array.from(document.querySelectorAll('.gj-doclist-item .gj-doc-name')).some((e) => e.textContent === 'plan')`)

	d.Click(".gj-folder-forget")
	d.WaitFor("the picker again", "!!document.querySelector('.gj-folder-choose')")
	d.WaitFor("the folder gone from the targets", `document.querySelectorAll('.gj-saver-select[data-field="Save to"] option[value="folder"]').length === 0`)
	d.Eval(listPicked, nil)
	d.WaitFor("the listing", "window.__files && window.__files.length >= 2")
	d.NoErrors()
}

func TestAFolderWhosePermissionLapsedNeedsAClick(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	resetFolder(d)
	d.Eval(pickerShim, nil)
	d.Click(`button[data-tab="save"]`)
	d.Click(".gj-folder-choose")
	d.WaitFor("the folder in use", "!!document.querySelector('.gj-folder-name')")

	d.OnNewDocument(`(() => {
		let granted = false;
		FileSystemDirectoryHandle.prototype.queryPermission = async () => granted ? 'granted' : 'prompt';
		FileSystemDirectoryHandle.prototype.requestPermission = async () => { granted = true; return 'granted' };
	})()`)
	d.Reload()
	d.Click(`button[data-tab="save"]`)
	d.WaitFor("the reconnect state", "!!document.querySelector('.gj-folder-reconnect-button')")
	d.OK(strings.Contains(d.Text(".gj-folder-reconnect"), "needs your permission again"), "it says why: "+d.Text(".gj-folder-reconnect"))
	d.OK(d.Count(`.gj-saver-select[data-field="Save to"] option[value="folder"]`) == 0, "saves do not go to a folder that is not allowed")
	d.Click(".gj-folder-reconnect-button")
	d.WaitFor("the folder back", `(document.querySelector('.gj-folder-name') || {textContent: ''}).textContent === 'Saving to the folder picked'`)
	d.OK(d.Count(`.gj-saver-select[data-field="Save to"] option[value="folder"]`) == 1, "the folder is a target again")
	d.NoErrors()
}

func TestAFolderWhosePermissionLapsesDuringTheSession(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	resetFolder(d)
	d.Eval(pickerShim, nil)
	d.Click(`button[data-tab="save"]`)
	d.Click(".gj-folder-choose")
	d.WaitFor("the folder in use", "!!document.querySelector('.gj-folder-name')")
	d.Fill(".gj-saver-name", "first")
	d.WaitFor("the form to be ready", `!document.querySelector('.gj-saver-save').disabled`)
	d.Click(".gj-saver-save")
	d.WaitFor("the first document", "document.querySelectorAll('.gj-doclist-item').length === 1")

	d.Eval(`(() => {
		window.__orig = FileSystemFileHandle.prototype.createWritable;
		FileSystemFileHandle.prototype.createWritable = async function () { throw new DOMException('permission lapsed', 'NotAllowedError') };
		FileSystemDirectoryHandle.prototype.queryPermission = async () => 'prompt';
		FileSystemDirectoryHandle.prototype.requestPermission = async () => { FileSystemFileHandle.prototype.createWritable = window.__orig; return 'granted' };
	})(); 1`, nil)
	d.Fill(".gj-saver-name", "second")
	d.WaitFor("the form to be ready", `!document.querySelector('.gj-saver-save').disabled`)
	d.Click(".gj-saver-save")
	d.WaitFor("the reconnect state", "!!document.querySelector('.gj-folder-reconnect-button')")
	d.WaitFor("the lapsed folder to leave the targets", `document.querySelectorAll('.gj-saver-select[data-field="Save to"] option[value="folder"]').length === 0`)
	d.OK(d.Count(".gj-folder-name") == 0, "the picker no longer says it is saving to the folder")

	d.Click(".gj-folder-reconnect-button")
	d.WaitFor("the folder back", `(document.querySelector('.gj-folder-name') || {textContent: ''}).textContent === 'Saving to the folder picked'`)
	d.WaitFor("the form to be ready", `!document.querySelector('.gj-saver-save').disabled`)
	d.Fill(".gj-saver-name", "third")
	d.Click(".gj-saver-save")
	d.WaitFor("the saved document", `Array.from(document.querySelectorAll('.gj-doclist-item .gj-doc-name')).some((e) => e.textContent === 'third')`)
}
