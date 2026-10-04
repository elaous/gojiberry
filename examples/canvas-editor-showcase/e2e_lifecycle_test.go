//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestDocumentLifecycle drives the Save tab's lifecycle: versions (snapshot,
// edit, go back), duplicate, tags and filtering by them, and the trash with
// restore and delete forever.
func TestDocumentLifecycle(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Fill(".gj-saver-name", "plan")
	d.Click(".gj-saver-save")
	d.WaitFor("the saved entry", `document.querySelector('.gj-saver-entry[data-key="plan.json"]')`)
	original := d.State().Str("text")

	// Versions: snapshot, change the text, open the snapshot to go back.
	d.OK(d.Text(".gj-saver-versions h4") == "Versions of plan", "the versions of the named document are shown")
	d.Click(".gj-saver-version-save")
	d.WaitFor("a version", "document.querySelector('.gj-saver-version')")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("End")
	d.Type(" CHANGED")
	d.Wait("s.text.includes('CHANGED')")
	d.Click(`button[data-tab="save"]`)
	d.Click(".gj-saver-version .gj-saver-version-open")
	d.Wait("s.text === " + e2etest.J(original))
	d.OK(true, "opening a version brings the text back")
	d.Click(".gj-saver-version .gj-saver-version-delete")
	d.WaitFor("no versions", "document.querySelectorAll('.gj-saver-version').length === 0")
	d.OK(true, "a version can be deleted")

	// Duplicate and tags.
	d.Click(`.gj-saver-entry[data-key="plan.json"] .gj-saver-duplicate`)
	d.WaitFor("the copy", `document.querySelector('.gj-saver-entry[data-key="Copy of plan.json"]')`)
	d.OK(d.Count(".gj-saver-entry") == 2, "Duplicate saves a copy")
	d.Fill(`.gj-saver-entry[data-key="plan.json"] .gj-saver-tags`, "rpg, draft")
	d.Dispatch(`.gj-saver-entry[data-key="plan.json"] .gj-saver-tags`, "change")
	d.WaitFor("the rpg tag", `window.__e2e.first('.gj-saver-tagchip:text-is("rpg")')`)
	d.OK(d.Count(".gj-saver-tagchip") == 3, "the tags appear as filters (All, draft, rpg)")
	d.Click(`.gj-saver-tagchip:text-is("rpg")`)
	d.WaitFor("one entry", "document.querySelectorAll('.gj-saver-entry').length === 1")
	d.OK(d.Count(`.gj-saver-entry[data-key="plan.json"]`) == 1, "filtering by a tag leaves only the documents with it")
	d.Click(`.gj-saver-tagchip:text-is("All")`)
	d.WaitFor("two entries", "document.querySelectorAll('.gj-saver-entry').length === 2")

	// Trash.
	d.Click(`.gj-saver-entry[data-key="Copy of plan.json"] .gj-saver-trash`)
	d.WaitFor("a trashed entry", "document.querySelector('.gj-saver-trashed')")
	d.OK(d.Count(".gj-saver-entry") == 1 && d.Count(".gj-saver-trashed") == 1, "Trash moves it out of the list into the trash")
	d.Click(".gj-saver-trashed .gj-saver-restore")
	d.WaitFor("restored", "document.querySelectorAll('.gj-saver-entry').length === 2 && document.querySelectorAll('.gj-saver-trashed').length === 0")
	d.OK(true, "Restore brings it back")
	d.Click(`.gj-saver-entry[data-key="Copy of plan.json"] .gj-saver-trash`)
	d.WaitFor("a trashed entry", "document.querySelector('.gj-saver-trashed')")
	d.Click(".gj-saver-trashed .gj-saver-purge")
	d.WaitFor("an empty trash", "document.querySelectorAll('.gj-saver-trashed').length === 0")
	d.OK(d.Count(".gj-saver-entry") == 1, "Delete forever removes it for good")
	d.NoErrors()
}
