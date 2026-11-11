//go:build !(js && wasm)

package main

import (
	"testing"
	"time"

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

func TestDwellClickActivatesAControlTheRestingPointerIsOn(t *testing.T) {
	d := openWithPrefs(t, `{dwell:'fast'}`)
	d.OK(d.Str(`document.querySelector('button[data-tab="find"]').getAttribute('aria-selected')`) != "true", "the Find tab is not open to begin with")
	d.Hover(`button[data-tab="find"]`)
	d.WaitFor("the ring closes around it", `document.querySelector('button[data-tab="find"]').hasAttribute('data-gj-dwelling')`)
	d.WaitFor("the tab is open", `document.querySelector('button[data-tab="find"]').getAttribute('aria-selected') === 'true'`)
	d.NoErrors()
}

func TestDwellClickIsOffUnlessAsked(t *testing.T) {
	d := openWithPrefs(t, `{}`)
	d.Hover(`button[data-tab="find"]`)
	d.Sleep(1500)
	d.OK(d.Str(`document.querySelector('button[data-tab="find"]').getAttribute('aria-selected')`) != "true", "resting the pointer did nothing")
	d.NoErrors()
}

func TestDwellClickIsCancelledByMovingAway(t *testing.T) {
	d := openWithPrefs(t, `{dwell:'slow'}`)
	d.Hover(`button[data-tab="find"]`)
	d.Sleep(300)
	d.Hover(`button[data-tab="stats"]`)
	d.Sleep(300)
	d.Hover(".gj-menubar")
	d.Sleep(2300)
	d.OK(d.Str(`document.querySelector('button[data-tab="find"]').getAttribute('aria-selected')+document.querySelector('button[data-tab="stats"]').getAttribute('aria-selected')`) == "falsefalse", "neither tab was clicked")
	d.NoErrors()
}

func TestSwitchScanningLightsControlsAndSpaceChoosesOne(t *testing.T) {
	d := openWithPrefs(t, `{scan:'fast'}`)
	d.WaitFor("something is lit", `document.documentElement.getAttribute('data-gj-scan')`)
	d.WaitFor("the Styles tab is lit", `document.documentElement.getAttribute('data-gj-scan') === 'tab:styles'`)
	d.OK(d.Count(".gj-scan-on") == 1, "one control is lit at a time")
	d.Press(" ")
	d.WaitFor("the tab is open", `document.querySelector('button[data-tab="styles"]').getAttribute('aria-selected') === 'true'`)
	d.NoErrors()
}

func TestSwitchScanningHandsTheKeyboardToTheDocumentAndBack(t *testing.T) {
	d := openWithPrefs(t, `{scan:'fast'}`)
	d.WaitForTimeout("the document is lit", `document.documentElement.getAttribute('data-gj-scan') === 'document'`, 90*time.Second)
	d.Press(" ")
	d.WaitFor("scanning waits", `document.documentElement.hasAttribute('data-gj-scan-paused')`)
	d.Press("Escape")
	d.WaitFor("scanning goes on", `!document.documentElement.hasAttribute('data-gj-scan-paused')`)
	d.NoErrors()
}
