//go:build !(js && wasm)

package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestSpellCheckAndHyphenation(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.OK(!d.State().Bool("spell.enabled"), "spell check is off at first")
	d.ToggleMode()

	d.Click(`button[data-tab="spell"]`)
	d.SetChecked(".gj-spell-enable", true)
	d.Wait("s.spell.enabled")
	d.WaitFor("an unknown word", `document.querySelector('.gj-spell-item[data-word="Gojiberry"]')`)
	d.OK(d.Count(`.gj-spell-item[data-word="Gojiberry"]`) == 1, "an unknown word is listed")
	d.OK(d.State().Int("spell.spans") > 0, "and marked on the canvas")
	d.OK(d.Text(".gj-spell-info") == "Dictionary: English", "the dictionary is named")

	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("End")
	d.Type(" teh")
	d.WaitFor("the typo", `document.querySelector('.gj-spell-item[data-word="teh"]')`)
	suggestions := d.Texts(`.gj-spell-item[data-word="teh"] .gj-spell-suggestion`)
	d.OK(slices.Contains(suggestions, "the"), "the typo comes with suggestions: "+strings.Join(suggestions, ","))
	d.Click(`.gj-spell-item[data-word="teh"] .gj-spell-suggestion:text-is("the")`)
	d.Wait("!s.text.includes(' teh')")
	d.OK(strings.Contains(d.State().Str("text"), " the"), "picking a suggestion replaces the word")
	d.OK(d.Count(`.gj-spell-item[data-word="teh"]`) == 0, "and it leaves the list")

	d.Click(`.gj-spell-item[data-word="Gojiberry"] .gj-spell-add`)
	d.WaitFor("the word to be added", `document.querySelectorAll('.gj-spell-item[data-word="Gojiberry"]').length === 0`)
	d.OK(d.Bool(`JSON.parse(localStorage.getItem('showcase:dictionary')).includes('gojiberry')`), "an added word is kept in the browser")
	d.Reload()
	d.Click(`button[data-tab="spell"]`)
	d.SetChecked(".gj-spell-enable", true)
	d.Wait("s.spell.enabled")
	d.Wait("s.spell.checked") // the check has run
	d.OK(d.Count(`.gj-spell-item[data-word="Gojiberry"]`) == 0, "and still known after a reload")

	d.OK(!d.State().Bool("hyphenate"), "hyphenation is off at first")
	d.Click(`button.gj-menubar-button:has-text("View")`)
	d.Click(`.gj-menubar-panel li:has-text("Hyphenate long words")`)
	d.Wait("s.hyphenate === true")
	d.OK(true, "the View menu turns hyphenation on")
	d.NoErrors()
}
