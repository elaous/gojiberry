//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// Suggestion mode: with it on (Ctrl+Alt+S or the Review tab's switch) typing and deleting propose changes,
// the Review tab lists them as sentences, and each can be accepted or rejected with a button or with
// Ctrl+Alt+Y and Ctrl+Alt+D.
func TestSuggestionMode(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	original := d.State().Str("text")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Home")
	d.Press("Control+Alt+s")
	d.WaitFor("the mode announced", `document.querySelector('.gj-announcer').textContent.startsWith('Suggesting')`)
	d.Type("ZED ")
	d.Wait("s.text.includes('ZED ')")
	d.Click(`button[data-tab="review"]`)
	d.WaitSelector(".gj-proposal")
	d.OK(strings.Contains(d.Text(".gj-proposal-text"), "added 'ZED '"), "the change is a sentence: "+d.Text(".gj-proposal-text"))
	d.OK(strings.Contains(d.Text(".gj-proposals-count"), "1 suggested change"), "and counted: "+d.Text(".gj-proposals-count"))

	// reject it: the text is as it was
	d.Click(".gj-proposal-reject")
	d.Wait("s.text === " + e2etest.J(original))
	d.WaitFor("no proposals", `!document.querySelector('.gj-proposal')`)

	// again, and accept with the keyboard
	d.Focus("#input")
	d.Press("Home")
	d.Type("YES ")
	d.WaitFor("a proposal", `document.querySelector('.gj-proposal')`)
	d.Focus("#input")
	d.Press("Control+Alt+y")
	d.WaitFor("accepted", `!document.querySelector('.gj-proposal')`)
	d.OK(strings.Contains(d.State().Str("text"), "YES "), "accepted text stays")

	// deleting proposes a removal; the text is still there until it is accepted
	d.Press("Control+Alt+s") // off
	d.Press("Control+Alt+s") // on
	d.Press("Home")
	d.Press("Delete")
	d.WaitFor("a removal", `document.querySelector('.gj-proposal')`)
	d.OK(strings.Contains(d.State().Str("text"), "YES "), "struck text is still in the text")
	d.OK(strings.Contains(d.Text(".gj-proposal-text"), "removed 'Y'"), d.Text(".gj-proposal-text"))
	d.Click(".gj-proposals-accept-all")
	d.WaitFor("settled", `!document.querySelector('.gj-proposal')`)
	d.Wait("s.text.includes('\\nES ') && !s.text.includes('YES ')")
	d.NoErrors()
}
