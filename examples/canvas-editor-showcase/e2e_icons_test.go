//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestIconsDrawInTheBrowser: the sidebar tabs, the Edit menu and the document
// buttons show Material Symbols; they are masked spans (an <svg> built by the DOM
// runtime would not draw), so check the browser really applies the mask and gives
// them a size, and that the names of the buttons are unchanged.
func TestIconsDrawInTheBrowser(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.OK(d.Count(`button[data-tab="save"] .gj-material`) == 1, "a tab has an icon")
	d.OK(d.Text(`button[data-tab="save"]`) == "Save", "and its name is the same: "+d.Text(`button[data-tab="save"]`))
	d.OK(d.Is(`(() => { const e = document.querySelector('button[data-tab="save"] .gj-material'); const s = getComputedStyle(e);
		return (s.maskImage || s.webkitMaskImage).startsWith('url("data:image/svg+xml') && e.getBoundingClientRect().width > 8 && s.backgroundColor !== 'rgba(0, 0, 0, 0)'; })()`), "the mask is applied and the icon has a size and a colour")

	d.Click(`button.gj-menubar-button:has-text("Edit")`)
	d.OK(d.Count(`.gj-menubar-panel .gj-menu-icon .gj-material`) > 0, "commands in the Edit menu have icons")

	d.MouseClick(600, 700, 1) // the backdrop closes the menu
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Fill(".gj-saver-name", "plan")
	d.Click(".gj-saver-save")
	d.WaitSelector(".gj-doclist-item .gj-doclist-open .gj-material")
	d.OK(d.Text(".gj-doclist-item .gj-doclist-open") == "Open", "a document button keeps its name: "+d.Text(".gj-doclist-item .gj-doclist-open"))
}
