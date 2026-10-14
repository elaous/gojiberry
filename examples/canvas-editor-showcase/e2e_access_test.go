//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func openWithPrefs(t *testing.T, prefs string) *e2etest.Tab {
	t.Helper()
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify(`+prefs+`))`, nil)
	d.Reload()
	d.WaitSelector("canvas")
	return d
}

// Dwell click: resting the pointer on a tab clicks it after the pause, and only once.
func TestDwellClickActivatesAControlTheRestingPointerIsOn(t *testing.T) {
	d := openWithPrefs(t, `{dwell:'fast'}`)
	d.OK(d.Is("s.tab !== 'find'"), "the Find tab is not open to begin with")
	d.Hover(`button[data-tab="find"]`)
	d.WaitFor("the ring closes around it", `document.querySelector('button[data-tab="find"]').hasAttribute('data-gj-dwelling')`)
	d.Wait("s.tab === 'find'")
	d.OK(d.Is("s.access.dwelling === false || s.tab === 'find'"), "the tab was clicked")
	d.NoErrors()
}

// Without the setting nothing is clicked by resting.
func TestDwellClickIsOffUnlessAsked(t *testing.T) {
	d := openWithPrefs(t, `{}`)
	d.Hover(`button[data-tab="find"]`)
	d.Sleep(1500)
	d.OK(d.Is("s.tab !== 'find'"), "resting the pointer did nothing: "+d.Str("document.getElementById('state').textContent.slice(0,0)+JSON.parse(document.getElementById('state').textContent).tab"))
	d.NoErrors()
}

// Moving away before the pause cancels the click.
func TestDwellClickIsCancelledByMovingAway(t *testing.T) {
	d := openWithPrefs(t, `{dwell:'slow'}`)
	d.Hover(`button[data-tab="find"]`)
	d.Sleep(300)
	d.Hover(`button[data-tab="stats"]`)
	d.Sleep(300)
	d.Hover(".gj-menubar")
	d.Sleep(2300)
	d.OK(d.Is("s.tab !== 'find' && s.tab !== 'stats'"), "neither tab was clicked: "+d.Str("JSON.parse(document.getElementById('state').textContent).tab"))
	d.NoErrors()
}

// Switch scanning: the page's controls are lit one by one and Space chooses the lit one.
func TestSwitchScanningLightsControlsAndSpaceChoosesOne(t *testing.T) {
	d := openWithPrefs(t, `{scan:'fast'}`)
	d.WaitFor("something is lit", `JSON.parse(document.getElementById('state').textContent).access.scan !== ''`)
	d.WaitFor("the Find tab is lit", `JSON.parse(document.getElementById('state').textContent).access.scan === 'tab:find'`)
	d.OK(d.Count(".gj-scan-on") == 1, "one control is lit at a time")
	d.Press(" ")
	d.Wait("s.tab === 'find'")
	d.NoErrors()
}

// The last stop is the document: choosing it hands the keyboard to the editor, and Escape gives it back.
func TestSwitchScanningHandsTheKeyboardToTheDocumentAndBack(t *testing.T) {
	d := openWithPrefs(t, `{scan:'fast'}`)
	d.WaitFor("the document is lit", `JSON.parse(document.getElementById('state').textContent).access.scan === 'document'`)
	d.Press(" ")
	d.Wait("s.access.scanPaused === true")
	d.Press("Escape")
	d.Wait("s.access.scanPaused === false")
	d.NoErrors()
}
