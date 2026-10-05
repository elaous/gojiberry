//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestPageSetupAndStatusBar changes the paper, orientation and margins in the
// Page tab and watches the sheets and the status bar follow; the status bar also
// shows the pages, the save state and the heading the caret is under.
func TestPageSetupAndStatusBar(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	item := func(id string) string { return d.Text(`.gj-status-item[data-item="` + id + `"]`) }
	d.OK(item("setup") == "A4 / portrait", "the status bar says the page setup: "+item("setup"))
	d.OK(d.Is("s.pageSetup.width === 595 && s.pageSetup.height === 842"), "the sheets are A4")
	d.OK(d.Count(`.gj-status-item[data-item="save"]`) == 0, "nothing says saved before anything was saved or edited")

	d.Click(`button[data-tab="page"]`)
	d.Select(".gj-pagesetup-paper", "letter")
	d.Wait("s.pageSetup.width === 612")
	d.Select(".gj-pagesetup-orientation", "landscape")
	d.Wait("s.pageSetup.width === 792 && s.pageSetup.height === 612")
	d.OK(item("setup") == "Letter / landscape", "the status bar follows: "+item("setup"))
	d.Select(".gj-pagesetup-preset", "narrow")
	d.Wait("s.pageSetup.margins.every((m) => m === 36)")
	d.Fill(`.gj-pagesetup-margin[data-side="top"]`, "100")
	d.Dispatch(`.gj-pagesetup-margin[data-side="top"]`, "change")
	d.Wait("s.pageSetup.margins[0] === 100 && s.pageSetup.margins[1] === 36")

	// The File menu opens the same tab.
	d.Click(`button[data-tab="stats"]`)
	d.Click(`button.gj-menubar-button:has-text("File")`)
	d.Click(`.gj-menubar-panel .gj-menu-label:text-is("Page setup…")`)
	d.Wait("s.tab === 'page'")

	// Saving shows in the status bar, and an edit afterwards makes it unsaved.
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Fill(".gj-saver-name", "plan")
	d.Click(".gj-saver-save")
	d.WaitFor("Saved", `document.querySelector('.gj-status-item[data-item="save"]') && document.querySelector('.gj-status-item[data-item="save"]').textContent === 'Saved'`)
	// Changing the page setup rebuilds the flow but edits nothing: still saved.
	d.Click(`button[data-tab="page"]`)
	d.Select(".gj-pagesetup-paper", "a4")
	d.Wait("s.pageSetup.width === 842 || s.pageSetup.width === 595")
	d.Sleep(300)
	d.OK(item("save") == "Saved", "a page setup change is not an edit: "+item("save"))
	d.ToggleMode()
	d.CanvasClick(200, 260)
	d.Type("x")
	d.Wait("s.saveState === 'unsaved'")
	d.OK(item("save") == "Unsaved changes", "an edit after saving: "+item("save"))
}
