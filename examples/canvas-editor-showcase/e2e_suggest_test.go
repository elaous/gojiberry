//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// Word suggestions: Ctrl+Space lists the words that continue the one being typed, the list is an ARIA
// listbox the input element controls (the focus never leaves the input), Down moves the highlight,
// Enter puts the word in place (and types no new line), Escape closes the list, and typing narrows it.
func TestWordSuggestions(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Home")
	d.Type("zebra zebra zeppelin ze ")
	d.Press("ArrowLeft") // the caret is at the end of "ze", before the space

	d.Press("Control+Space")
	d.WaitSelector(`#suggestions[role="listbox"]`)
	d.OK(d.Count(`#suggestions [role="option"]`) == 2, "two words continue 'ze'")
	d.OK(strings.TrimSpace(d.Text(`#suggestions [aria-selected="true"]`)) == "zebra", "the most used word is highlighted first")
	d.OK(d.Bool(`document.activeElement.id === 'input'`), "the focus stays in the input")
	d.OK(d.Str(`document.getElementById('input').getAttribute('aria-expanded')`) == "true" &&
		d.Str(`document.getElementById('input').getAttribute('aria-activedescendant')`) == "suggestions-opt-0", "the input names the highlighted option")
	d.OK(strings.Contains(d.Text(".gj-announcer"), "2 suggestions"), "the count is announced: "+d.Text(".gj-announcer"))

	d.Press("ArrowDown")
	d.WaitFor("the second option", `document.getElementById('input').getAttribute('aria-activedescendant') === 'suggestions-opt-1'`)
	d.Press("Escape")
	d.WaitFor("the list closing", `!document.getElementById('suggestions')`)
	d.OK(!d.Bool(`document.getElementById('input').hasAttribute('aria-expanded')`), "and the input is no longer expanded")

	d.Press("Control+Space")
	d.WaitSelector(`#suggestions`)
	d.Type("p") // narrows: 'zep' has one candidate
	d.WaitFor("narrowing", `document.querySelectorAll('#suggestions [role=option]').length === 1`)
	lines := strings.Count(d.State().Str("text"), "\n")
	d.Press("Enter")
	d.Wait("s.text.includes('zebra zebra zeppelin zeppelin ')")
	d.OK(strings.Count(d.State().Str("text"), "\n") == lines, "Enter accepted the word and typed no new line")
	d.WaitFor("the list closing", `!document.getElementById('suggestions')`)
	d.NoErrors()
}

// With "Suggest words as I type" on, the list opens by itself after a pause in typing, and says
// nothing when there is nothing to offer.
func TestWordSuggestionsOpenByThemselves(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify({suggest: 'auto'}))`, nil)
	d.Reload()
	d.WaitSelector("canvas")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Home")
	d.Type("zebra zebra zeppelin ze ")
	d.Press("ArrowLeft")
	d.OK(d.Count("#suggestions") == 0, "the list is not open before the pause (a key restarts the wait, an arrow cancels it)")
	d.Type("p")
	d.WaitSelector(`#suggestions[role="listbox"]`)
	d.OK(d.Count(`#suggestions [role="option"]`) == 1, "it opens by itself, narrowed to 'zep'")
	d.Press("Escape")
	d.WaitFor("closing", `!document.getElementById('suggestions')`)
	d.NoErrors()
}

// With spell check on, the dictionary's words fill the list after the document's own.
func TestWordSuggestionsUseTheDictionary(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.Click(`button[data-tab="spell"]`)
	d.SetChecked(".gj-spell-enable", true)
	d.Wait("s.spell.enabled")
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Home")
	d.Type("xylo ")
	d.Press("ArrowLeft")
	d.Press("Control+Space")
	d.WaitSelector(`#suggestions [role="option"]`)
	got := strings.Join(d.Texts(`#suggestions [role="option"]`), ",")
	d.OK(strings.Contains(got, "xylophone"), "a word the document does not have comes from the dictionary: "+got)
	d.NoErrors()
}

// With a document language that has a word list (German, French, Spanish), its common words fill the list
// after the document's own, and with no language set they do not.
func TestWordSuggestionsUseTheLanguageWordList(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Home")
	d.Type("Doku ")
	d.Press("ArrowLeft")
	d.Press("Control+Space")
	d.Sleep(300)
	d.OK(d.Count(`#suggestions [role="option"]`) == 0, "without a document language there is no German word: "+strings.Join(d.Texts(`#suggestions [role="option"]`), ","))
	d.Click(`button.gj-menubar-button:has-text("Format")`)
	d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Language of the document")`)
	d.Click(`.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("Deutsch")`)
	d.WaitFor("the language", `document.getElementById('a11y-text').getAttribute('lang') === 'de'`)
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Home")
	d.Press("ArrowRight")
	d.Press("ArrowRight")
	d.Press("ArrowRight")
	d.Press("ArrowRight")
	d.Press("Control+Space")
	d.WaitSelector(`#suggestions [role="option"]`)
	got := strings.Join(d.Texts(`#suggestions [role="option"]`), ",")
	d.OK(strings.Contains(got, "Dokument"), "the German list offers its word, with the typed capital: "+got)
	d.NoErrors()
}

// Portuguese has a list too: with it as the document language, a prefix finds its word.
func TestWordSuggestionsUseThePortugueseList(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.Click(`button.gj-menubar-button:has-text("Format")`)
	d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Language of the document")`)
	d.Click(`.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("Português")`)
	d.WaitFor("the language", `document.getElementById('a11y-text').getAttribute('lang') === 'pt'`)
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Home")
	d.Type("docum ")
	d.Press("ArrowLeft")
	d.Press("Control+Space")
	d.WaitSelector(`#suggestions [role="option"]`)
	got := strings.Join(d.Texts(`#suggestions [role="option"]`), ",")
	d.OK(strings.Contains(got, "documento"), "the Portuguese list offers its word: "+got)
	d.NoErrors()
}
