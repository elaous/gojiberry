//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// An input method composes text in the editor's input: the preedit stays there untouched (clearing it would end
// the composition), the keys the method uses are not the editor's, and the text goes into the document once, when
// the composition ends, whichever order the browser sends the last events in.
func TestInputMethodCompositionInsertsOnceAtTheEnd(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("End")
	count := func(s string) int {
		return strings.Count(d.Str(`JSON.parse(document.getElementById('state').textContent).text`), s)
	}
	fire := func(js string) {
		d.Eval(`(() => { const i = document.getElementById('input'); `+js+`; return true; })()`, nil)
	}

	// Chrome's order: composing input events, then compositionend.
	fire(`i.dispatchEvent(new CompositionEvent('compositionstart', { data: '' }))`)
	fire(`i.value = 'ni'; i.dispatchEvent(new InputEvent('input', { isComposing: true, data: 'ni' }))`)
	d.OK(d.Str(`document.getElementById('input').value`) == "ni", "the preedit stays in the input while composing")
	before := d.Str(`JSON.parse(document.getElementById('state').textContent).text.length + ''`)
	fire(`i.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', isComposing: true, keyCode: 229, bubbles: true }))`)
	d.OK(d.Str(`JSON.parse(document.getElementById('state').textContent).text.length + ''`) == before, "Enter that chooses a candidate is not a new line")
	fire(`i.value = '你好'; i.dispatchEvent(new InputEvent('input', { isComposing: true, data: '你好' })); i.dispatchEvent(new CompositionEvent('compositionend', { data: '你好' }))`)
	d.Wait("s.text.includes('你好')")
	d.OK(count("你好") == 1, "the committed text is in the document once")
	d.OK(d.Str(`document.getElementById('input').value`) == "", "and the input is empty again")

	eBefore := count("é")
	// Safari's order: compositionend first, then the input event with the text.
	fire(`i.dispatchEvent(new CompositionEvent('compositionstart', { data: '' })); i.value = 'é'; i.dispatchEvent(new CompositionEvent('compositionend', { data: 'é' })); i.dispatchEvent(new InputEvent('input', { isComposing: false, data: 'é' }))`)
	d.Wait("s.text.includes('é')")
	d.OK(count("é") == eBefore+1, "one copy, not two, from the input event after the end")
	d.OK(d.Str(`document.getElementById('input').value`) == "", "and the input is empty")
	d.NoErrors()
}

// The Language tab can make the language it detected the language of the document.
func TestDetectedLanguageCanBecomeTheDocumentLanguage(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.Click(`button[data-tab="language"]`)
	d.WaitFor("a detected language", `(document.querySelector('.gj-lang-detected') || {}).textContent === 'Language: English'`)
	d.OK(d.Count(".gj-lang-docuse") == 1, "the button is offered")
	d.Click(".gj-lang-docuse")
	d.WaitFor("the document language is set", `document.getElementById('a11y-text').getAttribute('lang') === 'en'`)
	d.WaitFor("the button is off", `document.querySelector('.gj-lang-docuse').disabled`)
	d.NoErrors()
}
