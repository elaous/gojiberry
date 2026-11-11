//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func exportHTML(d *e2etest.Tab) string {
	d.Click(`button[data-tab="transfer"]`)
	_, data := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="html"]`) })
	return string(data)
}

func TestInsertAnEquationRequireItsAlternativeAndExportIt(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Click(`button[data-tab="images"]`)
	d.WaitSelector("#eq-insert")
	d.OK(d.Has("#eq-none"), "no equation yet")

	d.Fill("#eq-formula", "x^2 + y^2 = z^2")
	d.Click("#eq-insert")
	d.WaitFor("refused", `(document.getElementById('eq-message') || {textContent: ''}).textContent.includes('needs a text alternative')`)
	d.WaitFor("the alternative has the focus", `document.activeElement && document.activeElement.id === 'eq-alt'`)
	d.OK(d.Has("#eq-none"), "nothing was inserted")

	d.Fill("#eq-alt", "x squared plus y squared equals z squared")
	d.Click("#eq-insert")
	d.WaitSelector("#eq-alt-0")
	d.OK(strings.Contains(d.Text("#eq-message"), "Equation inserted"), d.Text("#eq-message"))
	d.OK(!d.Has("#eq-missing-0"), "it has an alternative")
	d.WaitFor("the reading copy says it in words", `document.getElementById('a11y-text').textContent.includes('Equation: x squared plus y squared equals z squared')`)
	runCheck(d)
	d.WaitFor("clean", `document.getElementById('gj-check-summary').textContent.includes('Nothing found')`)

	d.Click(`button[data-tab="images"]`)
	d.Fill("#eq-alt-0", "")
	d.WaitSelector("#eq-missing-0")
	runCheck(d)
	d.WaitFor("a finding", `document.querySelectorAll('.gj-check-equation-alt').length === 1`)
	d.OK(strings.Contains(d.Text(".gj-check-equation-alt"), "no text alternative"), d.Text(".gj-check-equation-alt"))
	d.Click(".gj-check-equation-alt .gj-check-fix-button")
	d.WaitSelector("#eq-alt-0")
	d.Fill("#eq-alt-0", "x squared plus y squared equals z squared")
	runCheck(d)
	d.WaitFor("clean again", `document.getElementById('gj-check-summary').textContent.includes('Nothing found')`)

	d.Click(`button[data-tab="images"]`)
	d.ChooseFile(func() { d.Click("#img-insert") }, "bar.png", pngBytes(t))
	d.WaitSelector("#img-alt-0")
	d.Fill("#img-alt-0", "Flow chart")
	d.Fill("#img-long-0", "Three boxes: input, process and output, joined by arrows.")

	d.Click(`button[data-tab="styles"]`)
	d.Click(`.gj-stylepicker-item[data-style="Code"]`)
	d.Click(`button[data-tab="images"]`)
	d.WaitSelector("#code-lang")
	d.Fill("#code-lang", "Go")
	d.WaitFor("the language is kept", `document.getElementById('code-lang').value === 'Go'`)
	d.Fill("#code-lang", "not valid!")
	d.WaitFor("refused in words", `(document.getElementById('code-message') || {textContent: ''}).textContent.includes('letters, digits')`)
	d.Fill("#code-lang", "go")

	for _, v := range d.Axe("", axeDisabled...) {
		t.Errorf("the tab with an equation, a long description and a code language: %s", v)
	}

	html := exportHTML(d)
	for _, want := range []string{
		`<math`, `alttext="x squared plus y squared equals z squared"`,
		`aria-describedby="longdesc-`, `Three boxes: input, process and output, joined by arrows.`,
		`<code class="language-go">`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the HTML export lacks %s", want)
		}
	}
	d.NoErrors()
}

func TestAnInlineEquationStandsInItsSentence(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Control+a")
	d.Press("Delete")
	d.Type("Let ")
	d.Click(`button[data-tab="images"]`)
	d.WaitSelector("#eq-insert")
	d.SetChecked("#eq-inline input", true)
	d.Fill("#eq-formula", "x_1^2")
	d.Fill("#eq-alt", "x sub 1 squared")
	d.Click("#eq-insert")
	d.WaitSelector("#eq-alt-0")
	d.Focus("#input")
	d.Type(" be given.")
	d.WaitFor("the reading copy has the sentence", `document.getElementById('a11y-text').textContent.includes('Let ') && document.getElementById('a11y-text').textContent.includes('be given.')`)
	d.Click(`button[data-tab="transfer"]`)
	_, html := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="html"]`) })
	d.OK(strings.Contains(string(html), `display="inline" alttext="x sub 1 squared"`) && strings.Contains(string(html), "<p>Let <math") && strings.Contains(string(html), "</math> be given.</p>"), "the formula is inline MathML in the sentence of the export")
	d.NoErrors()
}
