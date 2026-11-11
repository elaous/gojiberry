//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
	"github.com/elaous/gojiberry/internal/screenreader"
)

func ghostText(d *e2etest.Tab) string { return d.State().Str("ghost") }

func openGhost(t *testing.T, prefs string) (*e2etest.Tab, *screenreader.Reader) {
	t.Helper()
	d, sr := openTyping(t, prefs)
	d.Press("Enter")
	d.Press("ArrowUp")
	d.Type("zebra zebra zeppelin ze")
	return d, sr
}

func TestGhostTextIsOffByDefault(t *testing.T) {
	d, _ := openGhost(t, "")
	d.Sleep(300)
	d.OK(ghostText(d) == "", "no ghost text without the preference: "+ghostText(d))
	d.NoErrors()
}

func TestGhostTextIsAcceptedByTabAlone(t *testing.T) {
	d, sr := openGhost(t, `{ghost:'on'}`)
	d.Wait("s.ghost !== ''")
	d.OK(ghostText(d) == "bra", "the rest of the most used word: "+ghostText(d))
	d.OK(!d.Has("#ghost"), "it is drawn by the canvas, not as an element a reader could find")
	seen := false
	for _, it := range sr.Items() {
		seen = seen || it.Name == "bra" || it.Phrase == "bra"
	}
	d.OK(!seen, "the accessibility tree does not contain the ghost text")
	before := d.State().Str("text")
	d.OK(!strings.Contains(before, "zeppelin zebra"), "not inserted yet")

	d.Press("ArrowLeft")
	d.Press("ArrowRight")
	d.OK(ghostText(d) == "" || ghostText(d) == "bra", "arrows may hide it, never insert it")
	d.OK(d.State().Str("text") == before, "arrows inserted nothing")

	d.Press("Backspace") // "ze" becomes "z": the ghost follows the word
	d.Type("e")
	d.Wait("s.ghost !== ''")
	d.OK(ghostText(d) == "bra", "back at ze: "+ghostText(d))

	d.Press("Tab")
	d.Wait("s.text.includes('zebra zebra zeppelin zebra\\n')")
	d.OK(!strings.Contains(d.State().Str("text"), "\t"), "Tab accepted the suggestion and typed no tab")
	d.OK(d.Bool(`document.activeElement.id === 'input'`), "the focus stayed in the input")
	d.OK(ghostText(d) == "", "the ghost text is gone once accepted")
	d.NoErrors()
}

func TestGhostTextEscapeAndEnter(t *testing.T) {
	d, _ := openGhost(t, `{ghost:'on'}`)
	d.Wait("s.ghost !== ''")
	d.Press("Escape")
	d.Wait("s.ghost === ''")
	d.Sleep(700)
	d.OK(ghostText(d) == "", "dismissed stays dismissed")
	d.Type("p")
	d.Wait("s.ghost !== ''")
	d.OK(ghostText(d) == "pelin", "after another letter it comes back: "+ghostText(d))
	d.Press("Enter")
	d.Sleep(300)
	text := d.State().Str("text")
	d.OK(strings.Contains(text, "zep\n") || strings.Contains(text, "zep"), "Enter did not complete the word")
	d.OK(!strings.Contains(text, "zeppelin zeppelin"), "Enter inserted nothing: "+text[max(0, len(text)-30):])
	d.NoErrors()
}

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

func TestGhostTextInTheMiddleOfALine(t *testing.T) {
	d, _ := openGhost(t, `{ghost:'on'}`)
	d.Wait("s.ghost !== ''")
	d.Type(" more")
	for i := 0; i < 5; i++ { // back to just after "ze", before " more"
		d.Press("ArrowLeft")
	}
	d.Type("b")
	d.Sleep(300)
	d.Wait("s.ghost !== ''")
	d.OK(ghostText(d) != "", "a suggestion mid-line: "+ghostText(d))
	d.OK(!d.Has("#ghost"), "and still no element")
	d.NoErrors()
}
