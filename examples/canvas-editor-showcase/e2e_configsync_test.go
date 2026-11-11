//go:build !(js && wasm)

package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/internal/e2etest"
	"github.com/elaous/gojiberry/userconfig"
)

func settingsSite(t *testing.T) (*e2etest.Site, *userconfig.Store) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	backing := docstore.NewMemoryAdapter()
	svc := &userconfig.Service{A: backing, Who: func(*http.Request) (string, bool) { return "ana", true }}
	site.Handle("/docs", docstore.NewServer(backing, "memory").Handler())
	site.Handle("/me/config", svc)
	site.Handle("/me/config/", svc)
	return site, &userconfig.Store{A: backing, User: "ana"}
}

func openOnSettingsServer(t *testing.T, site *e2etest.Site) *e2etest.Tab {
	d := site.OpenURL(t, "/index.html?lang=en&docstore="+url.QueryEscape(site.URL))
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-offer-review")
	d.Click("#dir-offer-review")
	confirmDialog(d)
	return d
}

func TestPreferencesFollowThePersonWhenAsked(t *testing.T) {
	site, store := settingsSite(t)
	a := openOnSettingsServer(t, site)
	a.Click(`button[data-tab="prefs"]`)
	a.WaitSelector(".gj-prefs-sync input")
	a.OK(!a.Bool(`document.querySelector('.gj-prefs-sync input').checked`), "keeping them on the server is off by default")
	a.Click(".gj-prefs [type=checkbox]:not(.gj-prefs-sync *)") // a preference changes, still only on this device
	a.Sleep(800)
	if items, _ := store.List(context.Background()); contains(items, "prefs.display") {
		t.Fatalf("a preference reached the server before it was asked: %v", items)
	}
	a.Click(".gj-prefs-sync input")
	a.WaitFor("the preferences reach the server", `true`)
	a.Sleep(1500)
	items, err := store.List(context.Background())
	if err != nil || !contains(items, "prefs.display") {
		t.Fatalf("the settings on the server = %v (%v), want prefs.display", items, err)
	}
	a.NoErrors()

	b := openOnSettingsServer(t, site)
	b.Click(`button[data-tab="prefs"]`)
	b.WaitSelector(".gj-prefs-sync input")
	b.Click(".gj-prefs-sync input")
	b.WaitFor("the other device's preferences are in force", `[...document.documentElement.attributes].some((x) => x.name.startsWith('data-gj-') && x.value !== '')`)
	b.NoErrors()
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestShareAThemeAsAFile(t *testing.T) {
	a := e2etest.NewSite(t).Open(t, "?lang=en")
	a.Click(`button[data-tab="styles"]`)
	a.WaitSelector("#gj-theme-text")
	a.Fill("#gj-theme-text", "#202040")
	a.WaitSelector(".gj-theme-ok")
	a.Fill("#gj-theme-name", "Warm paper")
	a.Click(".gj-theme-library .gj-button")
	a.WaitSelector(".gj-theme-saved-name")
	a.Fill("#theme-share-note", "From Ana, easy on the eyes")
	_, file := a.Download(func() { a.Click(".gj-theme-saved-list li:first-child .gj-button:nth-child(3)") })
	if !strings.Contains(string(file), `"gojiberry-share"`) || strings.Contains(string(file), "ana@") {
		t.Fatalf("the share file = %s", file)
	}

	b := e2etest.NewSite(t).Open(t, "?lang=en")
	b.Click(`button[data-tab="styles"]`)
	b.WaitSelector("#theme-import")
	b.ChooseFile(func() { b.Click("#theme-import") }, "warm.json", file)
	b.WaitSelector("#theme-import-add")
	b.OK(strings.Contains(b.Text(".gj-theme-share-review"), "warm-paper") && strings.Contains(b.Text(".gj-theme-share-review"), "From Ana"), "the review shows the theme and the note: "+b.Text(".gj-theme-share-review"))
	b.OK(!b.Has(".gj-theme-saved-name"), "nothing is added before the answer")
	b.Click("#theme-import-add")
	b.WaitSelector(".gj-theme-saved-name")
	b.OK(b.Text(".gj-theme-saved-name") == "Warm paper", "the theme is in the list")

	b.ChooseFile(func() { b.Click("#theme-import") }, "warm.json", file)
	b.WaitSelector("#theme-import-add")
	b.Click("#theme-import-add")
	b.WaitFor("two themes", `document.querySelectorAll('.gj-theme-saved-name').length === 2`)

	bad := strings.Replace(string(file), `"sha256":"`, `"sha256":"0`, 1)
	b.ChooseFile(func() { b.Click("#theme-import") }, "bad.json", []byte(bad))
	b.WaitFor("a refusal in words", `document.querySelector('.gj-theme-library [role="status"]') && document.querySelector('.gj-theme-library [role="status"]').textContent.includes('cannot be used')`)
	b.OK(!b.Has("#theme-import-add"), "a refused file is not offered")
	b.NoErrors()
}

func TestAFontOfMyOwnIsKeptAndUsable(t *testing.T) {
	var font []byte
	for _, p := range []string{"/usr/share/fonts/truetype/dejavu/DejaVuSerif.ttf", "/usr/share/fonts/truetype/freefont/FreeSerif.ttf"} {
		if b, err := os.ReadFile(p); err == nil {
			font = b
			break
		}
	}
	if font == nil {
		t.Skip("no system font to add")
	}
	site, store := settingsSite(t)
	d := openOnSettingsServer(t, site)
	d.Click(`button[data-tab="styles"]`)
	d.WaitSelector("#fonts-choose")

	d.ChooseFile(func() { d.Click("#fonts-choose") }, "My Serif.ttf", []byte("not a font"))
	d.WaitSelector("#fonts-file")
	d.Fill("#fonts-licence", "Made by me, free to use")
	d.Click("#fonts-add")
	d.WaitFor("refused as no font", `document.getElementById('fonts-message') && document.getElementById('fonts-message').textContent.includes('not a TrueType')`)

	d.ChooseFile(func() { d.Click("#fonts-choose") }, "My Serif.ttf", font)
	d.Fill("#fonts-licence", "")
	d.Click("#fonts-add")
	d.WaitFor("refused for no licence", `document.getElementById('fonts-message').textContent.includes('licence note')`)
	d.Fill("#fonts-licence", "Bitstream Vera licence: free to use and share")
	d.Click("#fonts-add")
	d.WaitFor("added", `document.getElementById('fonts-message').textContent.includes('added')`)
	d.OK(strings.Contains(d.Text(".gj-font-stored"), "Bitstream Vera"), "its licence is shown: "+d.Text(".gj-font-stored"))
	d.WaitFor("the browser has the font", `[...document.fonts].some((f) => f.family.replace(/"/g, '') === 'my-serif' && f.status === 'loaded')`)

	d.Click(`button[data-tab="format"]`)
	d.Click(".gj-fontselector-control")
	d.Fill(".gj-fontselector-search", "my-serif")
	d.Click(`.gj-fontselector-option[data-family="my-serif"]`)
	d.OK(!strings.Contains(d.Str(`document.getElementById('state').textContent`), "was not added"), "no download was tried")

	items, err := store.List(context.Background())
	for i := 0; i < 20 && !contains(items, "font.my-serif"); i++ {
		d.Sleep(300)
		items, err = store.List(context.Background())
	}
	if err != nil || !contains(items, "font.my-serif") {
		t.Fatalf("the settings on the server = %v (%v), want font.my-serif", items, err)
	}

	e := openOnSettingsServer(t, site)
	e.WaitFor("the other device has the font", `[...document.fonts].some((f) => f.family.replace(/"/g, '') === 'my-serif' && f.status === 'loaded')`)

	d.Click(`button[data-tab="styles"]`)
	d.Click("#fonts-delete-my-serif")
	d.WaitFor("gone from the list", `!document.querySelector('.gj-font-stored')`)
	d.NoErrors()
}
