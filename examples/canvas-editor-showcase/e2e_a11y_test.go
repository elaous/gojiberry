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

// Export can be gated on the check: with "block" an export of a document with errors is refused in words
// until the person allows that one export; "warn" exports and says so.
func TestExportGate(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Click(`button[data-tab="images"]`)
	d.ChooseFile(func() { d.Click("#img-insert") }, "bar.png", pngBytes(t)) // a picture with no description: an error
	d.WaitSelector("#img-alt-0")
	export := func() {
		d.Click(`button[data-tab="transfer"]`)
		d.Click(`.gj-transfer-button[data-kind="export"][data-format="html"]`)
	}
	// default: no gate
	name, _ := d.Download(export)
	d.OK(strings.HasSuffix(name, ".html"), "by default the export goes ahead: "+name)

	d.Click(`button[data-tab="check"]`)
	d.Click(`.gj-check-gate[data-gate="block"]`)
	d.OK(d.Attr(`.gj-check-gate[data-gate="block"]`, "aria-pressed") == "true", "the choice is shown as pressed")
	export()
	d.WaitFor("refused", `(document.getElementById('transfer-message') || {textContent: ''}).textContent.includes('Not exported: the document has 1 accessibility errors')`)

	// allow this one export
	d.Click(`button[data-tab="check"]`)
	d.Click("#gj-check-override")
	name, _ = d.Download(export)
	d.OK(strings.HasSuffix(name, ".html"), "the allowed export goes ahead: "+name)
	export()
	d.WaitFor("and the next is refused again", `document.getElementById('transfer-message').textContent.includes('Not exported')`)

	// warn: exports and says how many errors
	d.Click(`button[data-tab="check"]`)
	d.Click(`.gj-check-gate[data-gate="warn"]`)
	name, _ = d.Download(export)
	d.OK(strings.HasSuffix(name, ".html") && d.Text(".gj-announcer") == "Exported with 1 accessibility errors", "warn exports and says so: "+d.Text(".gj-announcer"))

	// describing the picture satisfies the block
	d.Click(`button[data-tab="check"]`)
	d.Click(`.gj-check-gate[data-gate="block"]`)
	d.Click(`button[data-tab="images"]`)
	d.Fill("#img-alt-0", "A bar chart")
	name, _ = d.Download(export)
	d.OK(strings.HasSuffix(name, ".html"), "no errors: not blocked: "+name)
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

// A stretch of text can be tagged with its language: the screen-reader copy writes it as a span with a
// lang attribute (so a reader switches voice), the tag is chosen from the Format menu and can be taken
// off again.
func TestSelectionLanguageReachesTheReadingCopy(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.CanvasDblClick(240, 164)
	d.WaitFor("the selection", "document.querySelector('.gj-a11y-selection')")
	pick := func(item string) {
		d.Click(`button.gj-menubar-button:has-text("Format")`)
		d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Language of the selection")`)
		d.Click(`.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("` + item + `")`)
	}
	d.OK(d.Count(`#a11y-text [lang]`) == 0, "no text has a language of its own at first")
	pick("Français")
	d.WaitFor("the lang attribute", `document.querySelectorAll('#a11y-text span[lang="fr"]').length === 1`)
	d.OK(d.Text(".gj-announcer") == "Language of the selection: Français", "the change is announced: "+d.Text(".gj-announcer"))
	d.OK(strings.TrimSpace(d.Text(`#a11y-text span[lang="fr"]`)) != "", "the span holds the selected words")
	pick("Same as the document")
	d.WaitFor("the tag gone", `document.querySelectorAll('#a11y-text [lang]').length === 0`)
	d.NoErrors()
}

// The picture on the page has a description, and the reading copy says it; an image without one
// is named as lacking it rather than passed over.
func TestImageDescriptionIsInTheReadingCopy(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.OK(strings.Contains(d.Text("#a11y-text"), "Image: A sample picture used to try text wrapping"), "the description is read: "+d.Text("#a11y-text"))
	d.NoErrors()
}

// The Check tab runs the accessibility check over the document, announces the result, lists what it
// found with a fix for each, and takes the caret to a finding.
func TestCheckTabListsFindingsAndGoesToThem(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button[data-tab="check"]`)
	d.WaitSelector("#gj-check-run")
	d.OK(strings.Contains(d.Text("#gj-check-summary"), "Not checked yet"), "before a check: "+d.Text("#gj-check-summary"))
	d.Click("#gj-check-run")
	d.WaitFor("a result", `!document.getElementById('gj-check-summary').textContent.includes('Not checked yet')`)
	d.OK(strings.Contains(d.Text("#gj-check-summary"), "Nothing found"), "the sample document is clean: "+d.Text("#gj-check-summary"))
	d.OK(d.Text(".gj-announcer") == "Accessibility check: nothing found", "and it is announced: "+d.Text(".gj-announcer"))

	// A document that skips a heading level.
	d.Click(`button[data-tab="transfer"]`)
	d.ChooseFile(func() { d.Click(`.gj-transfer-button[data-kind="import"][data-format="markdown"]`) }, "doc.md", []byte("# Title\n\nSome words here.\n\n### Deep heading\n\nMore words."))
	d.Wait("s.text.includes('More words')")
	d.Click(`button[data-tab="check"]`)
	d.Click("#gj-check-run")
	d.WaitFor("a finding", `document.querySelectorAll('.gj-check-item').length === 1`)
	d.OK(strings.Contains(d.Text("#gj-check-summary"), "1 findings: 0 errors, 1 warnings"), d.Text("#gj-check-summary"))
	d.OK(strings.Contains(d.Text(".gj-check-item"), "warning: ") && strings.Contains(d.Text(".gj-check-item"), "Deep heading") && strings.Contains(d.Text(".gj-check-fix"), "level 2"), "it says what and how to fix: "+d.Text(".gj-check-item"))
	d.OK(d.Text(".gj-announcer") == "Accessibility check: 1 findings", "announced: "+d.Text(".gj-announcer"))
	for _, v := range d.Axe("", axeDisabled...) {
		t.Errorf("the Check tab with a finding: %s", v)
	}
	d.OK(d.Has(".gj-check-fix-button") && strings.Contains(d.Text(".gj-check-fix-button"), "level 2"), "the heading has a one-click fix: "+d.Text(".gj-check-fix-button"))
	d.Click(".gj-check-go")
	d.WaitFor("the caret at the heading", `(() => { const s = JSON.parse(document.getElementById('state').textContent); return s.cursors && s.cursors[0].Caret === s.text.indexOf('Deep heading') })()`)
	// the fix makes it a level 2 heading, and the check run again finds nothing
	d.Click(".gj-check-fix-button")
	d.WaitFor("fixed", `document.getElementById('gj-check-summary').textContent.includes('Nothing found')`)
	d.NoErrors()
}

// The language of the document is chosen in the Format menu: the reading copy is tagged with it, it is
// kept with the document, and an HTML export says it.
func TestDocumentLanguageIsInTheReadingCopyAndTheExport(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.OK(d.Attr("#a11y-text", "lang") == "", "no document language at first")
	d.Click(`button.gj-menubar-button:has-text("Format")`)
	d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Language of the document")`)
	d.Click(`.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("Français")`)
	d.WaitFor("tagged", `document.getElementById('a11y-text').getAttribute('lang') === 'fr'`)
	d.OK(d.Text(".gj-announcer") == "Language of the document: Français", "announced: "+d.Text(".gj-announcer"))
	d.Click(`button[data-tab="transfer"]`)
	_, data := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="html"]`) })
	d.OK(strings.Contains(string(data), `<html lang="fr">`), "the export says it: "+string(data)[:min(len(data), 120)])
	d.NoErrors()
}
