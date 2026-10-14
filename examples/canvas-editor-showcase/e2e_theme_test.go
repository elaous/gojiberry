//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// The theme editor in the Styles tab checks colours as they are typed and puts a valid theme into the
// document's HTML export.
func TestThemeEditorChecksAndThemesTheExport(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	importMarkdown(d, "# Title\n\nSome words here.", "Some words")
	d.Click(`button[data-tab="styles"]`)
	d.WaitSelector("#gj-theme-text")
	d.OK(d.Has(".gj-theme-ok"), "the default theme is fine")

	// A light grey text on a white page cannot be read: the field says so at once.
	d.Fill("#gj-theme-text", "#bbbbbb")
	d.WaitSelector(".gj-theme-problems")
	d.OK(d.Attr("#gj-theme-text", "aria-invalid") == "true", "the field with the problem says so")
	d.OK(strings.Contains(d.Text(".gj-theme-problems"), "contrast"), "and the list says why: "+d.Text(".gj-theme-problems"))
	_, bad := d.Download(func() {
		d.Click(`button[data-tab="transfer"]`)
		d.Click(`.gj-transfer-button[data-kind="export"][data-format="html"]`)
	})
	d.OK(!strings.Contains(string(bad), "--gj-color-text"), "a theme with a problem is not used in the export")

	// Dark text on the page is fine and goes into the export.
	d.Click(`button[data-tab="styles"]`)
	d.Fill("#gj-theme-text", "#202020")
	d.WaitSelector(".gj-theme-ok")
	_, good := d.Download(func() {
		d.Click(`button[data-tab="transfer"]`)
		d.Click(`.gj-transfer-button[data-kind="export"][data-format="html"]`)
	})
	d.OK(strings.Contains(string(good), "--gj-color-text:#202020"), "a valid theme is in the exported page")

	// Back to the default: nothing in the export.
	d.Click(`button[data-tab="styles"]`)
	d.Click(".gj-theme .gj-button")
	_, plain := d.Download(func() {
		d.Click(`button[data-tab="transfer"]`)
		d.Click(`.gj-transfer-button[data-kind="export"][data-format="html"]`)
	})
	d.OK(!strings.Contains(string(plain), "--gj-color-text"), "after the reset the export has no theme")
	d.NoErrors()
}
