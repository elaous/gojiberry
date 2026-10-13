//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
	"github.com/elaous/gojiberry/internal/screenreader"
)

// ghostText is what the faint suggestion shows, "" when it is hidden.
func ghostText(d *e2etest.Tab) string {
	return d.Str(`(() => { const g = document.getElementById('ghost'); return g && g.style.display !== 'none' ? g.textContent : '' })()`)
}

// openGhost opens the editor with the given preferences in Edit mode, the caret on an empty line of its
// own (the ghost text shows only at the end of a line), with a screen reader watching the live regions.
func openGhost(t *testing.T, prefs string) (*e2etest.Tab, *screenreader.Reader) {
	t.Helper()
	d, sr := openTyping(t, prefs)
	d.Press("Enter") // an empty line above the paragraph: the caret ends its line there
	d.Press("ArrowUp")
	d.Type("zebra zebra zeppelin ze")
	return d, sr
}

// The ghost text is off unless the person chose it: nothing is drawn, and Tab does what it always did.
func TestGhostTextIsOffByDefault(t *testing.T) {
	d, _ := openGhost(t, "")
	d.Sleep(300)
	d.OK(ghostText(d) == "", "no ghost text without the preference: "+ghostText(d))
	d.NoErrors()
}

// With the preference on, the rest of the best word is shown after the caret as a picture the
// accessibility tree does not contain; only Tab puts it in, the other keys never do.
func TestGhostTextIsAcceptedByTabAlone(t *testing.T) {
	d, sr := openGhost(t, `{ghost:'on'}`)
	d.WaitFor("the ghost text", `document.getElementById('ghost') && document.getElementById('ghost').style.display !== 'none'`)
	d.OK(ghostText(d) == "bra", "the rest of the most used word: "+ghostText(d))
	d.OK(d.Attr("#ghost", "aria-hidden") == "true", "the picture is hidden from assistive technology")
	seen := false
	for _, it := range sr.Items() {
		seen = seen || it.Name == "bra" || it.Phrase == "bra"
	}
	d.OK(!seen, "the accessibility tree does not contain the ghost text")
	before := d.State().Str("text")
	d.OK(!strings.Contains(before, "zeppelin zebra"), "not inserted yet")

	// Keys that are not Tab leave the text alone: the arrow keys and Enter (which starts a new line).
	d.Press("ArrowLeft")
	d.Press("ArrowRight")
	d.OK(ghostText(d) == "" || ghostText(d) == "bra", "arrows may hide it, never insert it")
	d.OK(d.State().Str("text") == before, "arrows inserted nothing")

	d.Press("Backspace") // "ze" becomes "z": the ghost follows the word
	d.Type("e")
	d.WaitFor("the ghost text again", `document.getElementById('ghost').style.display !== 'none'`)
	d.OK(ghostText(d) == "bra", "back at ze: "+ghostText(d))

	d.Press("Tab")
	d.Wait("s.text.includes('zebra zebra zeppelin zebra\\n')")
	d.OK(!strings.Contains(d.State().Str("text"), "\t"), "Tab accepted the suggestion and typed no tab")
	d.OK(d.Bool(`document.activeElement.id === 'input'`), "the focus stayed in the input")
	d.OK(ghostText(d) == "", "the ghost text is gone once accepted")
	d.NoErrors()
}

// Escape hides the suggestion until the next letter; Enter does not insert it.
func TestGhostTextEscapeAndEnter(t *testing.T) {
	d, _ := openGhost(t, `{ghost:'on'}`)
	d.WaitFor("the ghost text", `document.getElementById('ghost') && document.getElementById('ghost').style.display !== 'none'`)
	d.Press("Escape")
	d.WaitFor("it hiding", `document.getElementById('ghost').style.display === 'none'`)
	d.Sleep(700)
	d.OK(ghostText(d) == "", "dismissed stays dismissed")
	d.Type("p")
	d.WaitFor("the next word's ghost text", `document.getElementById('ghost').style.display !== 'none'`)
	d.OK(ghostText(d) == "pelin", "after another letter it comes back: "+ghostText(d))
	d.Press("Enter")
	d.Sleep(300)
	text := d.State().Str("text")
	d.OK(strings.Contains(text, "zep\n") || strings.Contains(text, "zep"), "Enter did not complete the word")
	d.OK(!strings.Contains(text, "zeppelin zeppelin"), "Enter inserted nothing: "+text[max(0, len(text)-30):])
	d.NoErrors()
}

// The suggestion is announced once, politely, a moment after typing stops, with the key that accepts it.
func TestGhostTextIsAnnouncedPolitely(t *testing.T) {
	d, sr := openGhost(t, `{ghost:'on'}`)
	got := said(t, d, sr, "Suggestion: zebra. Press Tab to accept.")
	d.OK(strings.Contains(got, "Suggestion: zebra. Press Tab to accept."), "announced: "+got)
	d.OK(d.Attr(".gj-announcer", "aria-live") == "polite", "in the polite region")
	for _, a := range sr.Announcements() {
		d.OK(!a.Assertive, "never assertive: "+a.Text)
	}
	d.Press("Tab")
	got = said(t, d, sr, "Inserted zebra")
	d.OK(strings.Contains(got, "Inserted zebra"), "accepting says what went in: "+got)
	d.NoErrors()
}

// It does not show while the caret is inside text (it would cover it), and the preference is in the menu.
func TestGhostTextNeverCoversText(t *testing.T) {
	d, _ := openGhost(t, `{ghost:'on'}`)
	d.WaitFor("the ghost text", `document.getElementById('ghost') && document.getElementById('ghost').style.display !== 'none'`)
	d.Type(" more")
	d.Press("ArrowLeft")
	d.Press("ArrowLeft")
	d.Press("ArrowLeft")
	d.Press("ArrowLeft")
	d.Press("ArrowLeft")
	d.Press("ArrowLeft")
	d.Type("zeb")
	d.Sleep(300)
	d.OK(ghostText(d) == "", "no ghost text in the middle of a line: "+ghostText(d))
	d.NoErrors()
}
