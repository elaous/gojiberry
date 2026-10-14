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
	d.OK(all == 6, "six groups: "+d.Text(".gj-prefs-count"))

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

// The text size setting makes the text on the sheets larger too, not only the page around the canvas.
func TestTextSizeSettingReachesTheTextOnTheSheets(t *testing.T) {
	site := e2etest.NewSite(t)
	d := site.OpenURL(t, "/index.html")
	d.WaitSelector("canvas")
	line := func() float64 {
		d.WaitFor("the state", `document.getElementById('state').textContent !== '(pending)'`)
		var h float64
		d.Eval(`JSON.parse(document.getElementById('state').textContent).lineHeights[0]`, &h)
		return h
	}
	before := line()
	d.Click(`button[data-tab="prefs"]`)
	d.WaitSelector(".gj-prefs-text")
	d.Select(".gj-prefs-text", "largest")
	d.WaitFor("the lines are taller", `JSON.parse(document.getElementById('state').textContent).lineHeights[0] > `+e2etest.J(before)+` * 1.9`)
	d.Select(".gj-prefs-text", "")
	d.WaitFor("and back", `Math.abs(JSON.parse(document.getElementById('state').textContent).lineHeights[0] - `+e2etest.J(before)+`) < 0.5`)
	d.NoErrors()
}

// A shortcut the person changes in the Keys tab is still changed on the next visit.
func TestChangedShortcutsAreKept(t *testing.T) {
	site := e2etest.NewSite(t)
	d := site.OpenURL(t, "/index.html")
	d.WaitSelector("canvas")
	d.Click(`button[data-tab="keys"]`)
	d.Click(`[data-action="change:underline"]`)
	d.WaitSelector(`[data-capture="underline"]`)
	d.Focus(`[data-capture="underline"]`)
	d.Press("Control+Shift+u")
	d.WaitFor("kept", `(localStorage.getItem('showcase:keys') || '').includes('"underline"')`)
	d.Reload()
	d.WaitSelector("canvas")
	d.Click(`button[data-tab="keys"]`)
	d.WaitFor("the new shortcut is listed after the visit", `Array.from(document.querySelectorAll('.gj-keybindings-keys')).some((e) => /Shift/.test(e.textContent) && e.closest('tr, li, div').textContent.includes('Underline'))`)
	d.Click(`[data-action="reset:underline"]`)
	d.WaitFor("back to the default", `!/Shift\+U":"underline"/.test(localStorage.getItem('showcase:keys') || '')`)
	d.NoErrors()
}
