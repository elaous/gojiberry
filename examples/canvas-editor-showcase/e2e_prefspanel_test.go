//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// The Preferences tab lists every setting in groups, a search box narrows the list, and a change there is the
// same change the menu makes: applied at once and kept for the next visit. Language and theme are in it too.
func TestPreferencesTab(t *testing.T) {
	site := e2etest.NewSite(t)
	d := site.OpenURL(t, "/index.html")
	d.WaitSelector("canvas")
	d.Click(`button[data-tab="prefs"]`)
	d.WaitSelector(".gj-prefs-search")
	all := d.Count(".gj-prefs-group")
	d.OK(all == 5, "five groups: "+d.Text(".gj-prefs-count"))

	// the search narrows the list to what answers it
	d.Fill(".gj-prefs-search", "contrast")
	d.Dispatch(".gj-prefs-search", "input")
	d.WaitFor("one preference", `document.querySelector('.gj-prefs-count').textContent === '1 preference'`)
	d.OK(d.Count(".gj-prefs-group") == 1, "one group is left")
	d.Click(`.gj-prefs .gj-checkbox-input`)
	d.WaitFor("the contrast is applied", `document.documentElement.getAttribute('data-gj-contrast') === 'more'`)
	d.Fill(".gj-prefs-search", "zzz")
	d.Dispatch(".gj-prefs-search", "input")
	d.WaitFor("nothing matches", `document.querySelector('.gj-prefs-count').textContent === 'No preference matches.'`)
	d.Fill(".gj-prefs-search", "")
	d.Dispatch(".gj-prefs-search", "input")
	d.WaitFor("all again", `document.querySelectorAll('.gj-prefs-group').length === 5`)

	// the theme
	d.Select(".gj-prefs-theme", "dark")
	d.WaitFor("the dark palette", `document.documentElement.getAttribute('data-gj-theme') === 'dark' && getComputedStyle(document.body).backgroundColor === 'rgb(26, 29, 36)'`)
	// the interface language, kept for the next visit
	d.Select(".gj-prefs-lang", "de")
	d.WaitFor("German", `document.documentElement.getAttribute('lang') === 'de'`)
	d.WaitFor("kept", `JSON.parse(localStorage.getItem('showcase:a11y')).lang === 'de' && JSON.parse(localStorage.getItem('showcase:a11y')).theme === 'dark'`)
	d.Reload()
	d.WaitSelector("canvas")
	d.OK(d.Str(`document.documentElement.getAttribute('lang')`) == "de", "the chosen language is the language of the next visit")
	d.OK(d.Str(`document.documentElement.getAttribute('data-gj-theme')`) == "dark", "so is the theme")
	d.NoErrors()
}

// Typing an opening bracket adds the closing one, unless the preference is off.
func TestAutoClosePreference(t *testing.T) {
	site := e2etest.NewSite(t)
	d := site.OpenURL(t, "/index.html")
	d.WaitSelector("canvas")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("End") // the end of the line: an opener before a word stays single, one at the end pairs
	d.Type("(")
	d.Wait("s.text.includes('()')") // by default the closing bracket comes with it

	d.Click(`button[data-tab="prefs"]`)
	d.WaitSelector(".gj-prefs-search")
	d.Fill(".gj-prefs-search", "closing")
	d.Dispatch(".gj-prefs-search", "input")
	d.WaitFor("the setting", `document.querySelectorAll('.gj-prefs .gj-checkbox-input').length === 1`)
	d.Click(`.gj-prefs .gj-checkbox-input`)
	d.WaitFor("kept", `JSON.parse(localStorage.getItem('showcase:a11y')).close === 'off'`)
	d.Focus("#input")
	d.Press("End")
	d.Type("[")
	d.Wait("s.text.includes('[')")
	d.OK(d.Is("!s.text.includes('[]')"), "turned off, no closing bracket is added")
	d.NoErrors()
}
