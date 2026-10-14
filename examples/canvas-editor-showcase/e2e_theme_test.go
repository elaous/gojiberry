//go:build !(js && wasm)

package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/internal/e2etest"
	"github.com/elaous/gojiberry/userconfig"
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

// The theme is drawn on the sheets, and a theme can be saved under a name, kept across a reload, used again and
// deleted (the settings store of package userconfig; here on the device only: no server is confirmed).
func TestThemeOnTheCanvasAndSavedThemes(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button[data-tab="styles"]`)
	d.WaitSelector("#gj-theme-background")
	hasColour := func(r, g, b int) bool {
		for x := 20; x < 900; x += 40 {
			for y := 20; y < 600; y += 40 {
				if p := d.Pixel(x, y); p[0] == r && p[1] == g && p[2] == b {
					return true
				}
			}
		}
		return false
	}
	d.OK(!hasColour(255, 251, 230), "the sheet is white before the theme")
	d.Fill("#gj-theme-background", "#fffbe6")
	d.WaitFor("the sheet in the theme's colour", `true`)
	d.Sleep(300)
	d.OK(hasColour(255, 251, 230), "the sheet is drawn in the theme's page colour")

	// A theme that cannot be read is not drawn.
	d.Fill("#gj-theme-text", "#fffbe0")
	d.Sleep(300)
	d.OK(d.Has(".gj-theme-problems"), "the problem is listed")

	// Save a good one under a name; it survives a reload.
	d.Fill("#gj-theme-text", "#202040")
	d.WaitSelector(".gj-theme-ok")
	d.Fill("#gj-theme-name", "Warm paper")
	d.Click(".gj-theme-library .gj-button")
	d.WaitSelector(".gj-theme-saved-name")
	d.OK(d.Text(".gj-theme-saved-name") == "Warm paper", "saved: "+d.Text(".gj-theme-saved-list"))
	d.ReloadPage()
	d.Click(`button[data-tab="styles"]`)
	d.WaitSelector(".gj-theme-saved-name")
	d.OK(d.Text(".gj-theme-saved-name") == "Warm paper", "kept on this device")

	// Use it again (the reload dropped the document's theme), then delete it.
	d.OK(!hasColour(255, 251, 230), "the new page starts without the theme")
	d.Click(".gj-theme-saved-list li:first-child .gj-button")
	d.Sleep(300)
	d.OK(hasColour(255, 251, 230), "using the saved theme draws it")
	d.Click(".gj-theme-saved-list li:first-child .gj-button:last-child")
	d.WaitFor("the list is empty", `!document.querySelector('.gj-theme-saved-name')`)
	d.NoErrors()
}

// With a docstore server confirmed, a saved theme is sent to the person's settings there (and another device
// would pull it).
func TestSavedThemesReachTheSettingsServer(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	backing := docstore.NewMemoryAdapter()
	site.Handle("/docs", docstore.NewServer(backing, "memory").Handler())
	site.Handle("/me/config", (&userconfig.Service{A: backing, Who: func(*http.Request) (string, bool) { return "ana", true }}))
	site.Handle("/me/config/", (&userconfig.Service{A: backing, Who: func(*http.Request) (string, bool) { return "ana", true }}))

	d := site.OpenURL(t, "/index.html?lang=en&docstore="+url.QueryEscape(site.URL))
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-offer-review")
	d.Click("#dir-offer-review")
	confirmDialog(d)
	d.Click(`button[data-tab="styles"]`)
	d.WaitSelector("#gj-theme-name")
	d.Fill("#gj-theme-text", "#202040")
	d.Fill("#gj-theme-name", "Warm paper")
	d.Click(".gj-theme-library .gj-button")
	d.WaitFor("the server heard", `(document.querySelector('.gj-theme-library [role="status"]') || {textContent: ''}).textContent.includes('in step with the server')`)
	items, err := (&userconfig.Store{A: backing, User: "ana"}).List(context.Background())
	if err != nil || len(items) != 1 || items[0] != "tokens.warm-paper" {
		t.Fatalf("the server's settings = %v (%v), want tokens.warm-paper", items, err)
	}
	d.NoErrors()
}
