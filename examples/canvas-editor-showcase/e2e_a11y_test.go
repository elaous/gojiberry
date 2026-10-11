//go:build !(js && wasm)

package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestAccessibilityAndTranslation: the canvas has a screen-reader copy of the
// document with its structure, a live region says what happened, and the
// interface language switches from the View menu.
func TestAccessibilityAndTranslation(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.OK(d.Attr("#stage", "role") == "textbox" && d.Attr("#stage", "aria-label") == "Document", "the canvas is a labelled text box")
	d.OK(d.Attr("#input", "aria-describedby") == "a11y-text", "the editing input is described by the document text")
	d.OK(d.Attr("#a11y-text", "role") == "document" && d.Attr("#a11y-text", "aria-label") == "Document text", "the document text is a labelled region")
	d.OK(d.Text("#a11y-text h1") == "Gojiberry editor", "headings keep their level: "+d.Text("#a11y-text h1"))
	d.OK(d.Count("#a11y-text h2") >= 1 && d.Count("#a11y-text ul li") >= 2 && d.Count("#a11y-text p") >= 3, "with paragraphs and lists")
	d.OK(d.Count("#a11y-text table") == 1 && d.Count("#a11y-text table th") == 2, "and the table as a table with its header")
	var box []float64
	d.Eval(`(r => [r.width, r.height])(document.getElementById('a11y-text').getBoundingClientRect())`, &box)
	d.OK(box[0] <= 1 && box[1] <= 1, "it is hidden from sight, not from readers")
	d.OK(d.Str(`getComputedStyle(document.getElementById('a11y-text')).display`) != "none", "and not display:none")

	// The text follows the edits, and the selection is in it.
	d.ToggleMode()
	d.OK(d.Text(".gj-announcer") == "Edit mode", "switching mode is announced")
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("End")
	d.Type(" ZZTOP")
	d.WaitFor("typed text", "document.getElementById('a11y-text').textContent.includes('ZZTOP')")
	d.OK(true, "typed text reaches the screen-reader copy")
	d.CanvasDblClick(240, 164)
	d.WaitFor("the selection", "document.querySelector('.gj-a11y-selection')")
	d.OK(len(d.Text(".gj-a11y-selection")) > 10 && d.Text(".gj-a11y-selection")[:10] == "Selected: ", "the selection is announced in it")

	// Find announces how many matches.
	d.Focus("#input")
	d.Press("Control+f")
	d.Fill(".gj-find-input", "the")
	d.WaitFor("a match count", "/matches/.test(document.querySelector('.gj-announcer').textContent)")
	d.OK(regexp.MustCompile(`^\d+ matches$`).MatchString(d.Text(".gj-announcer")), "find says how many matches: "+d.Text(".gj-announcer"))
	d.Click(".gj-find-close")

	// Translation of the interface.
	d.OK(d.Attr("html", "lang") == "en" && d.Attr("html", "dir") == "ltr", "the page says it is English, left to right")
	d.Click(`button.gj-menubar-button:has-text("View")`)
	d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Interface language")`)
	d.Click(`.gj-menubar-panel .gj-menu-label:text-is("Español")`)
	d.WaitFor("Spanish", "document.documentElement.lang === 'es'")
	d.OK(d.Text(`button[data-tab="find"]`) == "Buscar", "the tabs are in Spanish: "+d.Text(`button[data-tab="find"]`))
	d.OK(d.Count(`button.gj-menubar-button:has-text("Ver")`) == 1, "and the menus")
	d.OK(regexp.MustCompile(`páginas`).MatchString(d.Text(`.gj-status-item[data-item="pages"]`)) && regexp.MustCompile(`^\d+ palabras$`).MatchString(d.Text(`.gj-status-item[data-item="words"]`)) && strings.Contains(d.Text(`.gj-status-item[data-item="setup"]`), "vertical"), "and the status bar: "+d.Text(".gj-docview-status"))
	d.Click(`button[data-tab="save"]`)
	d.OK(d.Text(".gj-saver-save") == "Guardar", "and the components")
	d.OK(d.Attr("#a11y-text", "aria-label") == "Texto del documento" && d.Attr("#stage", "aria-label") == "Documento", "including what a screen reader hears")
	d.NoErrors()
}

// The screen-reader copy follows the structure, not only the text: turning a
// paragraph into a heading changes it with the text unchanged.
func TestScreenReaderCopyFollowsStructure(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.ToggleMode()
	headings := func() int { return d.Count("#a11y-text h1, #a11y-text h2, #a11y-text h3") }
	before := headings()
	d.CanvasClick(240, 164)
	text := d.State().Str("text")
	d.Click(`button[data-tab="styles"]`)
	d.Click(`.gj-stylepicker-item[data-style="Heading 2"]`)
	d.WaitFor("one more heading", "document.querySelectorAll('#a11y-text h1, #a11y-text h2, #a11y-text h3').length === "+itoa(before+1))
	d.OK(d.State().Str("text") == text, "the text did not change")
	d.Click(`.gj-stylepicker-item[data-style="Normal"]`)
	d.WaitFor("back to the first count", "document.querySelectorAll('#a11y-text h1, #a11y-text h2, #a11y-text h3').length === "+itoa(before))
	_ = headings
}

func itoa(n int) string { return strconv.Itoa(n) }

// The font selector works with the keyboard alone: Down on its button opens the list with the focus in
// the search field, Down again goes to the first option, Enter picks it and the focus returns to the
// button; Escape closes the list and gives the focus back too.
func TestFontSelectorKeyboard(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button[data-tab="format"]`)
	active := func() string {
		return d.Str(`(document.activeElement.className || '') + '|' + (document.activeElement.getAttribute('data-family') || '')`)
	}
	d.Focus(".gj-fontselector-control")
	d.Press("ArrowDown")
	d.WaitFor("the search field", `document.activeElement.classList.contains('gj-fontselector-search')`)
	d.Press("ArrowDown")
	d.WaitFor("the first option", `document.activeElement.classList.contains('gj-fontselector-option')`)
	d.Press("ArrowDown")
	d.OK(strings.Contains(active(), "gj-fontselector-option"), "the focus moves among the options: "+active())
	family := d.Str(`document.activeElement.getAttribute('data-family')`)
	d.Press("Enter")
	d.WaitFor("the list closed", `!document.querySelector('.gj-fontselector-panel')`)
	d.OK(strings.Contains(active(), "gj-fontselector-control"), "the focus is back on the button: "+active())
	d.OK(strings.Contains(d.Text(".gj-fontselector-control"), family), "and it shows the choice "+family+": "+d.Text(".gj-fontselector-control"))

	d.Press("ArrowDown")
	d.WaitFor("open again", `!!document.querySelector('.gj-fontselector-panel')`)
	d.Press("Escape")
	d.WaitFor("closed by Escape", `!document.querySelector('.gj-fontselector-panel')`)
	d.OK(strings.Contains(active(), "gj-fontselector-control"), "Escape returns the focus to the button: "+active())
	d.NoErrors()
}
