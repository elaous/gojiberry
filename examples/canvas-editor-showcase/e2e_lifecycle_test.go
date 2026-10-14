//go:build !(js && wasm)

package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestDocumentLifecycle drives the Save tab's lifecycle: versions (snapshot,
// edit, go back), duplicate, tags and filtering by them, and the trash with
// restore and delete forever.
func TestDocumentLifecycle(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	// A document row, found by its name (the key is a UUID).
	row := func(name string) string {
		return fmt.Sprintf(`.gj-doclist-item:has(.gj-doc-name:text-is(%q))`, name)
	}
	// Deleting for good asks first: this answers the dialog.
	confirm := func() { d.Click(`[role="alertdialog"] .gj-dialog-actions button:last-child`) }
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Fill(".gj-saver-name", "plan")
	d.Click(".gj-saver-save")
	d.WaitSelector(row("plan"))
	id := d.Attr(row("plan"), "data-id")
	d.OK(regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id), "a saved document has a UUID: "+id)

	// Saving again replaces that document; Save as new makes another one with the
	// same name and a different ID.
	d.Click(".gj-saver-save")
	d.WaitFor("saved", `(document.querySelector('.gj-status-item[data-item="save"]') || {dataset: {}}).dataset.state === 'saved'`)
	// A negative check: nothing to wait for, so give a wrong second document time to
	// show up before saying there is still one.
	d.Sleep(600)
	d.OK(d.Count(".gj-doclist-item") == 1, "Save again replaces the same document")
	d.Click(".gj-saver-save-new")
	d.WaitFor("a second document", "document.querySelectorAll('.gj-doclist-item').length === 2")
	ids := d.Strs(`Array.from(document.querySelectorAll('.gj-doclist-item')).map((e) => e.dataset.id)`)
	d.OK(len(ids) == 2 && ids[0] != ids[1] && d.Count(row("plan")) == 2, "two documents may share a name: they have different IDs")
	// Put the first back as the open one, and drop the extra document.
	extra := ids[0]
	if extra == id {
		extra = ids[1]
	}
	d.Click(`.gj-doclist-item[data-id="` + extra + `"] .gj-doclist-delete`)
	d.WaitSelector(`[role="alertdialog"]`)
	d.OK(strings.Contains(d.Text(`[role="alertdialog"]`), "cannot be undone"), "deleting for good asks first")
	d.Click(`[role="alertdialog"] .gj-dialog-actions button:first-child`)
	d.WaitFor("the dialog to close", `!document.querySelector('[role="alertdialog"]')`)
	d.OK(d.Count(".gj-doclist-item") == 2, "Cancel leaves the document alone")
	d.Click(`.gj-doclist-item[data-id="` + extra + `"] .gj-doclist-delete`)
	confirm()
	d.WaitFor("one document", "document.querySelectorAll('.gj-doclist-item').length === 1")
	d.Click(`.gj-doclist-item[data-id="` + id + `"] .gj-doclist-open`)
	d.WaitFor("the document to open", "document.querySelector('.gj-doclist-current')")
	original := d.State().Str("text")

	// Versions: snapshot, change the text, open the snapshot to go back.
	d.OK(d.Text(".gj-versions h4") == "Versions of plan", "the versions of the named document are shown")
	d.Click(".gj-versions-save")
	d.WaitFor("a version", "document.querySelector('.gj-versions-item')")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("End")
	d.Type(" CHANGED")
	d.Wait("s.text.includes('CHANGED')")
	d.Click(`button[data-tab="save"]`)
	d.Click(".gj-versions-item .gj-versions-open")
	d.Wait("s.text === " + e2etest.J(original))
	d.OK(true, "opening a version brings the text back")
	d.Click(".gj-versions-item .gj-versions-delete")
	d.WaitFor("no versions", "document.querySelectorAll('.gj-versions-item').length === 0")
	d.OK(true, "a version can be deleted")

	// Duplicate and tags.
	d.Click(row("plan") + " .gj-doclist-duplicate")
	d.WaitSelector(row("Copy of plan"))
	d.OK(d.Count(".gj-doclist-item") == 2, "Duplicate saves a copy")
	d.Fill(row("plan")+" .gj-doclist-tags", "rpg, draft")
	d.Dispatch(row("plan")+" .gj-doclist-tags", "change")
	d.WaitFor("the rpg tag", `window.__e2e.first('.gj-tagchip:text-is("rpg")')`)
	d.OK(d.Count(".gj-tagchip") == 3, "the tags appear as filters (All, draft, rpg)")
	d.Click(`.gj-tagchip:text-is("rpg")`)
	d.WaitFor("one entry", "document.querySelectorAll('.gj-doclist-item').length === 1")
	d.OK(d.Count(row("plan")) == 1, "filtering by a tag leaves only the documents with it")
	d.Click(`.gj-tagchip:text-is("All")`)
	d.WaitFor("two entries", "document.querySelectorAll('.gj-doclist-item').length === 2")

	// Trash.
	d.Click(row("Copy of plan") + " .gj-doclist-trash")
	d.WaitFor("a trashed entry", "document.querySelector('.gj-trash-item')")
	d.OK(d.Count(".gj-doclist-item") == 1 && d.Count(".gj-trash-item") == 1, "Trash moves it out of the list into the trash")
	d.Click(".gj-trash-item .gj-trash-restore")
	d.WaitFor("restored", "document.querySelectorAll('.gj-doclist-item').length === 2 && document.querySelectorAll('.gj-trash-item').length === 0")
	d.OK(true, "Restore brings it back")
	d.Click(row("Copy of plan") + " .gj-doclist-trash")
	d.WaitFor("a trashed entry", "document.querySelector('.gj-trash-item')")
	d.Click(".gj-trash-item .gj-trash-delete")
	confirm()
	d.WaitFor("an empty trash", "document.querySelectorAll('.gj-trash-item').length === 0")
	d.OK(d.Count(".gj-doclist-item") == 1, "Delete forever removes it for good")
	d.NoErrors()
}

// TestCapacityMeter checks the Save tab shows how full the chosen store is.
func TestCapacityMeter(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.WaitSelector(".gj-capacity")
	d.OK(strings.Contains(d.Text(".gj-capacity"), "%"), "the meter shows a percentage: "+d.Text(".gj-capacity"))
}

// A saved version can be compared with the document as it is now: the Save tab lists what was added,
// removed and reworded, in words a screen reader can read.
func TestCompareAVersionWithTheDocument(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Fill(".gj-saver-name", "plan")
	d.Click(".gj-saver-save")
	d.WaitSelector(".gj-doclist-item")
	d.Click(".gj-versions-save")
	d.WaitSelector(".gj-versions-item")

	d.Click(".gj-versions-item .gj-versions-compare")
	d.WaitSelector(".gj-diff-none")
	d.OK(strings.Contains(d.Text(".gj-diff-none"), "The two versions are the same"), "nothing changed yet: "+d.Text(".gj-diff-none"))

	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("End")
	d.Type(" CHANGED")
	d.Wait("s.text.includes('CHANGED')")
	d.Click(`button[data-tab="save"]`)
	d.Click(".gj-versions-item .gj-versions-compare")
	d.WaitFor("the changes", `document.querySelector('.gj-diff-item')`)
	d.OK(strings.Contains(d.Text(".gj-diff-summary"), "change"), "the count: "+d.Text(".gj-diff-summary"))
	d.OK(d.Count(".gj-diff ins") >= 1, "the added words are marked")
	d.OK(strings.Contains(d.Text(".gj-diff-item .gj-sr-only"), "Edited"), "and said in words: "+d.Text(".gj-diff-item .gj-sr-only"))
	d.NoErrors()
}

// A version can be named and kept: the name stays in the list, who saved it is shown, and a kept version
// cannot be deleted until it is let go.
func TestNameAndKeepAVersion(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Fill(".gj-saver-name", "plan")
	d.Click(".gj-saver-save")
	d.WaitSelector(".gj-doclist-item")
	d.Click(".gj-versions-save")
	d.WaitSelector(".gj-versions-item")
	d.OK(strings.Contains(d.Text(".gj-versions-author"), "by you@example.com"), "who saved it: "+d.Text(".gj-versions-item"))
	d.OK(strings.Contains(d.Text(".gj-versions-item"), "(UTC"), "the time is in the viewer's zone, and says which: "+d.Text(".gj-versions-item"))

	d.Fill(".gj-versions-name", "Sent to the printer")
	d.Dispatch(".gj-versions-name", "change")
	d.WaitFor("the name saved", `document.querySelector('.gj-status-item, .gj-saver-status, [role="status"]') && document.body.textContent.includes('Version renamed')`)
	d.SetChecked(".gj-versions-pin", true)
	d.WaitFor("the version kept", `document.querySelector('.gj-versions-pin') && document.querySelector('.gj-versions-pin').checked && document.querySelector('.gj-versions-delete').disabled`)
	d.OK(d.Disabled(".gj-versions-delete"), "a kept version cannot be deleted")
	d.OK(d.Str(`document.querySelector('.gj-versions-name').value`) == "Sent to the printer", "and keeps its name")

	d.SetChecked(".gj-versions-pin", false)
	d.WaitFor("let go", `!document.querySelector('.gj-versions-delete').disabled`)
	d.Click(".gj-versions-delete")
	d.WaitFor("deleted", `document.querySelectorAll('.gj-versions-item').length === 0`)
	d.NoErrors()
}

// Restore goes back to a version and keeps what was being edited as a version of its own.
func TestRestoreAVersionKeepsTheCurrentText(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Fill(".gj-saver-name", "plan")
	d.Click(".gj-saver-save")
	d.WaitSelector(".gj-doclist-item")
	original := d.State().Str("text")
	d.Click(".gj-versions-save")
	d.WaitSelector(".gj-versions-item")

	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("End")
	d.Type(" CHANGED")
	d.Wait("s.text.includes('CHANGED')")
	d.Click(`button[data-tab="save"]`)
	d.Click(".gj-versions-item .gj-versions-restore")
	d.Wait("s.text === " + e2etest.J(original))
	d.WaitFor("two versions", `document.querySelectorAll('.gj-versions-item').length === 2`)
	d.OK(d.Bool(`Array.from(document.querySelectorAll('.gj-versions-name')).filter(e => e.value.startsWith('Before restoring')).length === 1`), "the edited text is kept as a named version")
	// restoring that one brings the edit back
	d.Eval(`Array.from(document.querySelectorAll('.gj-versions-item')).find(li => li.querySelector('.gj-versions-name').value.startsWith('Before restoring')).querySelector('.gj-versions-restore').click()`, nil)
	d.Wait("s.text.includes('CHANGED')")
	d.NoErrors()
}
