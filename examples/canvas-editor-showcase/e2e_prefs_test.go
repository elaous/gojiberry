//go:build !(js && wasm)

package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// Display preferences kept in the browser are applied at start: text size, spacing, contrast and motion
// change the page's computed styles, and none set leaves the defaults.
func TestDisplayPreferencesAreApplied(t *testing.T) {
	site := e2etest.NewSite(t)
	d := site.OpenURL(t, "/index.html")
	d.WaitSelector("canvas")
	size := func() float64 {
		var f float64
		d.Eval(`parseFloat(getComputedStyle(document.documentElement).fontSize)`, &f)
		return f
	}
	base := size()
	d.OK(!d.Bool(`document.documentElement.hasAttribute('data-gj-text')`), "no preference: no attribute")

	d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify({text: 'large', spacing: 'wide', contrast: 'more', motion: 'reduce'}))`, nil)
	d.Reload()
	d.WaitSelector("canvas")
	d.OK(size() == base*1.25, "text size is 125%")
	d.OK(d.Str(`getComputedStyle(document.body).letterSpacing`) != "normal", "wide spacing sets the letter spacing")
	d.OK(d.Str(`getComputedStyle(document.documentElement).getPropertyValue('--gj-text').trim()`) == "#000000", "more contrast darkens the text")
	d.OK(d.Str(`getComputedStyle(document.documentElement).getPropertyValue('--gj-border').trim()`) == "#4b5563", "and the borders")

	d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify({text: 'bogus', contrast: 'x'}))`, nil)
	d.Reload()
	d.WaitSelector("canvas")
	d.OK(size() == base && !d.Bool(`document.documentElement.hasAttribute('data-gj-text') || document.documentElement.hasAttribute('data-gj-contrast')`), "unknown values are ignored")
	d.NoErrors()
}

// The View menu's display preferences change the page at once, are kept for the next visit, and
// reset to the defaults.
func TestDisplayPreferencesMenu(t *testing.T) {
	site := e2etest.NewSite(t)
	d := site.OpenURL(t, "/index.html")
	d.WaitSelector("canvas")
	choose := func(item string) {
		d.Click(`button.gj-menubar-button:has-text("View")`)
		d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Display preferences")`)
		entry := `.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("` + item + `")`
		d.WaitSelector(entry)
		// The submenu is long and scrolls when the window is short: bring the entry into view, as a person would.
		d.Eval(`(() => { const e = Array.from(document.querySelectorAll('.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub)')).find((x) => x.textContent.includes(`+"`"+item+"`"+`)); if (e) e.scrollIntoView({ block: 'nearest' }); return true; })()`, nil)
		d.Click(entry)
	}
	root := func(attr string) string {
		return d.Str(`document.documentElement.getAttribute('` + attr + `') || ''`)
	}

	choose("Text size: 150%")
	d.WaitFor("the size", `document.documentElement.getAttribute('data-gj-text') === 'larger'`)
	choose("More contrast")
	d.WaitFor("the contrast", `document.documentElement.getAttribute('data-gj-contrast') === 'more'`)
	choose("Reduce motion")
	choose("Wide text spacing")
	d.WaitFor("the spacing", `document.documentElement.getAttribute('data-gj-spacing') === 'wide'`)
	d.OK(root("data-gj-motion") == "reduce", "motion is reduced")

	d.Reload()
	d.WaitSelector("canvas")
	d.OK(root("data-gj-text") == "larger" && root("data-gj-contrast") == "more", "the choices are kept for the next visit")

	d.Click(`button.gj-menubar-button:has-text("View")`)
	d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Display preferences")`)
	d.OK(d.Has(`.gj-menubar-panel li.gj-menu-item[aria-checked="true"]:has-text("More contrast")`) && d.Has(`.gj-menubar-panel li.gj-menu-item[aria-checked="true"]:has-text("Text size: 150%")`), "the menu shows what is on")
	d.Click(`.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("Text size: default")`)
	choose("Reset to defaults")
	d.WaitFor("reset", `!document.documentElement.hasAttribute('data-gj-text') && !document.documentElement.hasAttribute('data-gj-contrast') && !document.documentElement.hasAttribute('data-gj-motion')`)
	d.NoErrors()
}

// Large targets make what can be pressed at least 44 pixels each way, and the distraction-free mode hides
// the toolbar, the side panel and the status bar; the menus stay, and turning either off brings it back.
func TestLargeTargetsAndDistractionFreeMode(t *testing.T) {
	site := e2etest.NewSite(t)
	d := site.OpenURL(t, "/index.html")
	d.WaitSelector(".gj-docview-sidebar button")
	small := func() float64 {
		var f float64
		d.Eval(`Math.min(...Array.from(document.querySelectorAll('.gj-docview-sidebar button')).map((b) => b.getBoundingClientRect().height))`, &f)
		return f
	}
	before := small()
	d.OK(before > 0 && before < 44, "buttons are small by default")
	d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify({targets: 'large'}))`, nil)
	d.Reload()
	d.WaitSelector(".gj-docview-sidebar button")
	d.OK(small() >= 44, "large targets: the smallest side-panel button is now "+d.Str(`String(Math.min(...Array.from(document.querySelectorAll('.gj-docview-sidebar button')).map((b) => b.getBoundingClientRect().height)))`))

	d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify({focus: 'on'}))`, nil)
	d.Reload()
	d.WaitSelector(".gj-docview-sidebar button")
	for _, sel := range []string{".gj-docview-toolbar", ".gj-docview-sidebar", ".gj-docview-status"} {
		d.OK(d.Str(`getComputedStyle(document.querySelector('`+sel+`')).display`) == "none", sel+" is hidden")
	}
	d.OK(d.Has(`button.gj-menubar-button:has-text("View")`), "the menus stay")
	// and the menu brings it back
	d.Click(`button.gj-menubar-button:has-text("View")`)
	d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Display preferences")`)
	d.OK(d.Has(`.gj-menubar-panel li.gj-menu-item[aria-checked="true"]:has-text("Distraction-free mode")`), "the menu shows it is on")
	d.Click(`.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("Distraction-free mode")`)
	d.WaitFor("back", `!document.documentElement.hasAttribute('data-gj-focus') && getComputedStyle(document.querySelector('.gj-docview-sidebar')).display !== 'none'`)
	d.NoErrors()
}

// Reading aloud: the document is handed to the browser's speech engine a sentence at a time, the
// sentence being spoken is shown with its controls, next/pause/stop work, and the speed is a kept
// preference. The speech engine is replaced by a recorder (a headless browser has no voice).
func TestReadAloud(t *testing.T) {
	site := e2etest.NewSite(t)
	d := site.OpenURL(t, "/index.html")
	d.WaitSelector("canvas")
	d.Eval(`(() => {
		const spoken = [];
		window.__spoken = spoken;
		window.__ctl = [];
		window.SpeechSynthesisUtterance = function (text) { this.text = text; };
		Object.defineProperty(window, 'speechSynthesis', { configurable: true, value: {
			speak(u) { spoken.push({text: u.text, lang: u.lang || '', rate: u.rate}); },
			cancel() { window.__ctl.push('cancel'); }, pause() { window.__ctl.push('pause'); }, resume() { window.__ctl.push('resume'); },
			getVoices() { return []; } } });
	})()`, nil)
	pick := func(item string) {
		d.Click(`button.gj-menubar-button:has-text("Read aloud")`)
		d.Click(`.gj-menubar-panel li.gj-menu-item:has-text("` + item + `")`)
	}

	pick("Read the document aloud")
	d.WaitFor("the first sentence", `window.__spoken.length === 1`)
	d.WaitSelector("#read-bar")
	first := d.Str(`window.__spoken[0].text`)
	d.OK(first != "" && d.Str(`document.getElementById('read-now').textContent`) == first, "the sentence being spoken is shown")
	d.OK(d.Bool(`window.__spoken[0].rate === 1`), "normal speed by default")

	d.Click(`#read-bar button:has-text("Next sentence")`)
	d.WaitFor("the next sentence", `window.__spoken.length === 2 && window.__spoken[1].text !== window.__spoken[0].text`)
	d.Click(`#read-bar button:has-text("Pause")`)
	d.WaitFor("pause", `window.__ctl.includes('pause')`)
	d.WaitSelector(`#read-bar button:has-text("Resume")`)
	d.Click(`#read-bar button:has-text("Stop reading")`)
	d.WaitFor("the bar goes", `!document.getElementById('read-bar')`)

	pick("Reading speed: fast")
	d.OK(d.Str(`localStorage.getItem('showcase:a11y')`) != "" && d.Bool(`JSON.parse(localStorage.getItem('showcase:a11y')).speed === 'fast'`), "the speed is kept")
	pick("Read the document aloud")
	d.WaitFor("fast speech", `window.__spoken[window.__spoken.length - 1].rate === 1.5`)
	d.NoErrors()
}

// Low-memory mode keeps fewer undo steps: asked for in the View menu, kept for the next visit, and it
// can be turned off again; with no choice the page stays at the default on this machine.
func TestLowMemoryModeKeepsFewerUndoSteps(t *testing.T) {
	site := e2etest.NewSite(t)
	d := site.OpenURL(t, "/index.html")
	d.WaitSelector("canvas")
	limit := func() int {
		d.WaitFor("the state", `document.getElementById('state').textContent !== '(pending)'`)
		return d.Int(`JSON.parse(document.getElementById('state').textContent).historyLimit`)
	}
	choose := func(item string) {
		d.Click(`button.gj-menubar-button:has-text("View")`)
		d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Display preferences")`)
		entry := `.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("` + item + `")`
		d.WaitSelector(entry)
		// The submenu is long and scrolls when the window is short: bring the entry into view, as a person would.
		d.Eval(`(() => { const e = Array.from(document.querySelectorAll('.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub)')).find((x) => x.textContent.includes(`+"`"+item+"`"+`)); if (e) e.scrollIntoView({ block: 'nearest' }); return true; })()`, nil)
		d.Click(entry)
	}
	d.OK(limit() == 200, "the default keeps 200 steps")
	choose("Save memory")
	d.WaitFor("the choice is kept", `JSON.parse(localStorage.getItem('showcase:a11y')).memory === 'low'`)
	d.Reload()
	d.WaitSelector("canvas")
	d.OK(limit() == 20, "the choice is kept for the next visit")
	choose("Save memory")
	d.WaitFor("turned off", `JSON.parse(localStorage.getItem('showcase:a11y')).memory === 'normal'`)
	d.Reload()
	d.WaitSelector("canvas")
	d.OK(limit() == 200, "turned off, the default again")
	// A long history can be asked for too.
	choose("Keep many undo steps")
	d.WaitFor("the choice is kept", `JSON.parse(localStorage.getItem('showcase:a11y')).memory === 'high'`)
	d.Reload()
	d.WaitSelector("canvas")
	d.OK(limit() == 1000, "many steps are kept for the next visit")
	d.NoErrors()
}

// The display preferences go out to a file and come back from one, and a file brings in only the values the
// page knows.
func TestPreferencesExportAndImport(t *testing.T) {
	site := e2etest.NewSite(t)
	d := site.OpenURL(t, "/index.html")
	d.WaitSelector("canvas")
	open := func(item string) {
		d.Click(`button.gj-menubar-button:has-text("View")`)
		d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Display preferences")`)
		entry := `.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("` + item + `")`
		d.WaitSelector(entry)
		d.Eval(`(() => { const e = Array.from(document.querySelectorAll('.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub)')).find((x) => x.textContent.includes(`+"`"+item+"`"+`)); if (e) e.scrollIntoView({ block: 'nearest' }); return true; })()`, nil)
	}
	root := func(attr string) string {
		return d.Str(`document.documentElement.getAttribute('` + attr + `') || ''`)
	}
	file := []byte(`{"text":"larger","contrast":"more","motion":"sideways","unknown":true}`)
	open("Import preferences")
	d.ChooseFile(func() {
		d.Click(`.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("Import preferences")`)
	}, "prefs.json", file)
	d.WaitFor("the text size is applied", `document.documentElement.getAttribute('data-gj-text') === 'larger'`)
	d.OK(root("data-gj-contrast") == "more", "the contrast is applied")
	d.OK(root("data-gj-motion") == "", "a value the page does not know is not applied: "+root("data-gj-motion"))

	open("Export these preferences")
	name, data := d.Download(func() {
		d.Click(`.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("Export these preferences")`)
	})
	d.OK(name == "display-preferences.json" && strings.Contains(string(data), `"text": "larger"`) && strings.Contains(string(data), `"contrast": "more"`), "the file has the settings: "+name+" "+string(data))

	open("Import preferences")
	d.ChooseFile(func() {
		d.Click(`.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("Import preferences")`)
	}, "bad.json", []byte("not json"))
	d.WaitFor("the refusal is said", `(document.querySelector('.gj-announcer') || {}).textContent === 'That file is not a preferences file'`)
	d.OK(root("data-gj-text") == "larger", "a bad file changes nothing")
	d.NoErrors()
}

// The saving of the low-memory mode, measured: the same long document, the same two hundred steps of editing,
// once with the default history and once with "Save memory". The Go heap in use (after a collection) is read
// from the page. It is a measure in this browser, not on a small phone; the test only asks that the mode
// saves a real part of what the history costs.
func TestLowMemoryModeSavesMemoryOnALongDocument(t *testing.T) {
	var md strings.Builder
	for i := 0; i < 150; i++ {
		fmt.Fprintf(&md, "Paragraph %d of a long document with some words in it, to give each undo step something to copy.\n\n", i)
	}
	measure := func(mem string) (heapMB float64, steps int) {
		d := e2etest.NewSite(t).Open(t, "?lang=en")
		d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify({memory: `+e2etest.J(mem)+`}))`, nil)
		d.ReloadPage()
		d.WaitSelector("canvas")
		d.Click(`button[data-tab="transfer"]`)
		d.ChooseFile(func() { d.Click(`.gj-transfer-button[data-kind="import"][data-format="markdown"]`) }, "long.md", []byte(md.String()))
		d.WaitForTimeout("the long document", "JSON.parse(document.getElementById('state').textContent).text.includes('Paragraph 149')", 120e9)
		d.ToggleMode()
		d.CanvasClick(240, 164)
		d.Focus("#input")
		d.Press("Control+Home")
		for i := 0; i < 250; i++ { // each Enter is a step of its own, with the whole text behind it
			d.Press("Enter")
		}
		d.Sleep(500)
		heap := d.Float(`window.gjHeap()`)
		steps = d.Int(`JSON.parse(document.getElementById('state').textContent).historyLimit`)
		return heap / (1 << 20), steps
	}
	normal, nSteps := measure("normal")
	low, lSteps := measure("low")
	t.Logf("history %d steps: heap %.1f MB; history %d steps: heap %.1f MB", nSteps, normal, lSteps, low)
	if nSteps != 200 || lSteps != 20 {
		t.Fatalf("the histories were %d and %d steps, want 200 and 20", nSteps, lSteps)
	}
	if low >= normal*0.7 {
		t.Errorf("the low-memory mode saved too little: %.1f MB against %.1f MB", low, normal)
	}
}

// Typing in a long document stays quick. The whole layout used to be worked out again for every paragraph of the
// reading copy on every key (the copy asks where each paragraph is), and the style of every letter was looked up
// by hashing its whole format: 150 paragraphs took 11 seconds a key. They take a third of a second now; the bound
// here is loose (a slow machine is not a failure) but far under what the quadratic cost gave.
func TestTypingStaysQuickInALongDocument(t *testing.T) {
	var md strings.Builder
	for i := 0; i < 150; i++ {
		fmt.Fprintf(&md, "Paragraph %d of a long document with some words in it, to give each key something to lay out.\n\n", i)
	}
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button[data-tab="transfer"]`)
	d.ChooseFile(func() { d.Click(`.gj-transfer-button[data-kind="import"][data-format="markdown"]`) }, "long.md", []byte(md.String()))
	d.WaitForTimeout("the long document", "JSON.parse(document.getElementById('state').textContent).text.includes('Paragraph 149')", 120e9)
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Control+Home")
	start := time.Now()
	for i := 0; i < 20; i++ {
		d.Press("a")
	}
	per := time.Since(start) / 20
	t.Logf("%v a letter in a document of 150 paragraphs", per)
	if per > 3*time.Second {
		t.Errorf("a letter took %v in a document of 150 paragraphs; it should take well under a second", per)
	}
	d.NoErrors()
}
