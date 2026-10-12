//go:build !(js && wasm)

package main

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func importMarkdown(d *e2etest.Tab, md, waitFor string) {
	d.Click(`button[data-tab="transfer"]`)
	d.ChooseFile(func() { d.Click(`.gj-transfer-button[data-kind="import"][data-format="markdown"]`) }, "doc.md", []byte(md))
	d.Wait("s.text.includes('" + waitFor + "')")
}

func runCheck(d *e2etest.Tab) {
	d.Click(`button[data-tab="check"]`)
	d.Click("#gj-check-run")
	d.WaitFor("a result", `!document.getElementById('gj-check-summary').textContent.includes('Not checked yet')`)
}

// The Check tab finds a table whose header row is empty and paragraphs that only look like a list, says
// what is wrong and how to fix it, and goes to each.
func TestCheckTabFindsTableHeadersAndTypedLists(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	importMarkdown(d, "Intro words here.\n\n| Name | |\n|---|---|\n| Ana | 31 |\n\n• milk\n\n• eggs\n\nEnd words.", "End words")
	runCheck(d)
	d.WaitFor("two findings", `document.querySelectorAll('.gj-check-item').length === 2`)
	d.OK(d.Count(".gj-check-table-header-empty") == 1 && strings.Contains(d.Text(".gj-check-table-header-empty"), "column 2"), "the empty header cell is named: "+d.Text(".gj-check-list"))
	d.OK(d.Count(".gj-check-list-fake") == 1 && strings.Contains(d.Text(".gj-check-list-fake"), "2 paragraphs start with typed bullets"), "the typed bullets are found: "+d.Text(".gj-check-list-fake"))
	d.OK(strings.Contains(d.Text("#gj-check-summary"), "2 findings: 0 errors, 2 warnings"), d.Text("#gj-check-summary"))
	d.OK(!d.Has(".gj-check-fix-button"), "nothing is changed for the person where it is not safe")
	for _, v := range d.Axe(".gj-check", axeDisabled...) {
		t.Errorf("the Check tab with these findings: %s", v)
	}
	d.Click(".gj-check-list-fake .gj-check-go")
	d.WaitFor("the caret at the first typed bullet", `(() => { const s = JSON.parse(document.getElementById('state').textContent); return s.cursors && s.cursors[0].Caret === s.text.indexOf('• milk') })()`)

	// Naming the empty column clears its finding.
	importMarkdown(d, "Intro words here.\n\n| Name | Age |\n|---|---|\n| Ana | 31 |\n\n• milk\n\nEnd words.", "End words")
	runCheck(d)
	d.WaitFor("nothing found", `document.getElementById('gj-check-summary').textContent.includes('Nothing found')`)
	d.NoErrors()
}

// An empty heading is an error with a one-click fix that turns it into a plain paragraph; the check runs
// again after it.
func TestCheckTabFixesAnEmptyHeading(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	importMarkdown(d, "Intro words here.\n\nBody words.", "Body words")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Press("Home")
	d.Press("Enter")   // an empty paragraph above the one the caret was in
	d.Press("ArrowUp") // and the caret in it
	d.Click(`button[data-tab="styles"]`)
	d.Click(`.gj-stylepicker-item[data-style="Heading 2"]`)
	runCheck(d)
	d.WaitFor("the empty heading", `document.querySelectorAll('.gj-check-heading-empty').length === 1`)
	d.OK(strings.Contains(d.Text("#gj-check-summary"), "1 findings: 1 errors, 0 warnings"), d.Text("#gj-check-summary"))
	d.OK(strings.Contains(d.Text(".gj-check-fix-button"), "Make it a plain paragraph"), "the fix is offered: "+d.Text(".gj-check-fix-button"))
	before := d.State().Str("text")
	d.Click(".gj-check-fix-button")
	d.WaitFor("fixed and checked again", `document.getElementById('gj-check-summary').textContent.includes('Nothing found')`)
	d.OK(d.State().Str("text") == before, "the text is untouched by the fix")
	d.OK(!d.Has(".gj-check-heading-empty"), "the heading finding is gone")
	d.NoErrors()
}

// The operator's minimum gate is part of the page the host serves (a <meta> in index.html): the person
// can raise the gate but not lower it, and under "block" cannot allow an export with errors anyway. A link
// cannot set it.
func TestOperatorMinimumExportGate(t *testing.T) {
	index, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	serve := func(site *e2etest.Site, content string) {
		page := strings.Replace(string(index), `<meta name="gojiberry-min-export-gate" content="">`, `<meta name="gojiberry-min-export-gate" content="`+content+`">`, 1)
		if page == string(index) {
			t.Fatal("index.html has no operator gate meta tag to set")
		}
		site.Handle("/index.html", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(page))
		}))
	}
	// withPictureNoAlt leaves a picture with no description in the document: one error.
	withPictureNoAlt := func(d *e2etest.Tab) {
		d.ToggleMode()
		d.CanvasClick(240, 164)
		d.Click(`button[data-tab="images"]`)
		d.ChooseFile(func() { d.Click("#img-insert") }, "bar.png", pngBytes(t))
		d.WaitSelector("#img-alt-0")
	}
	export := func(d *e2etest.Tab) {
		d.Click(`button[data-tab="transfer"]`)
		d.Click(`.gj-transfer-button[data-kind="export"][data-format="html"]`)
	}

	t.Run("block", func(t *testing.T) {
		site := e2etest.NewSite(t)
		serve(site, "block")
		d := site.Open(t, "?lang=en&export-gate=none&min-export-gate=none")
		withPictureNoAlt(d)
		d.Click(`button[data-tab="check"]`)
		d.OK(d.Attr(`.gj-check-gate[data-gate="block"]`, "aria-pressed") == "true", "the gate in force is shown")
		d.OK(d.Disabled(`.gj-check-gate[data-gate=""]`) && d.Disabled(`.gj-check-gate[data-gate="warn"]`) && !d.Disabled(`.gj-check-gate[data-gate="block"]`), "the lower choices cannot be pressed")
		d.OK(strings.Contains(d.Text("#gj-check-floor"), "administrator") && !d.Has("#gj-check-override"), "a note says who set it and there is no override: "+d.Text(".gj-check"))
		d.Eval(`document.querySelector('.gj-check-gate[data-gate=""]').click()`, nil) // even a script's click does nothing
		export(d)
		d.WaitFor("refused", `(document.getElementById('transfer-message') || {textContent: ''}).textContent.includes('this site does not allow exporting')`)
		d.OK(d.Str(`localStorage.getItem('showcase:export-gate') || ''`) == "", "the person's lower choice was not even kept")
		d.Click(`button[data-tab="check"]`)
		for _, v := range d.Axe(".gj-check", axeDisabled...) {
			t.Errorf("the Check tab with an operator gate: %s", v)
		}
		// describing the picture satisfies it
		d.Click(`button[data-tab="images"]`)
		d.Fill("#img-alt-0", "A bar chart")
		name, _ := d.Download(func() { export(d) })
		d.OK(strings.HasSuffix(name, ".html"), "no errors: not blocked: "+name)
		d.NoErrors()
	})

	t.Run("warn", func(t *testing.T) {
		site := e2etest.NewSite(t)
		serve(site, "warn")
		d := site.Open(t, "?lang=en")
		withPictureNoAlt(d)
		d.Click(`button[data-tab="check"]`)
		d.OK(d.Attr(`.gj-check-gate[data-gate="warn"]`, "aria-pressed") == "true" && d.Disabled(`.gj-check-gate[data-gate=""]`) && !d.Disabled(`.gj-check-gate[data-gate="block"]`), "warn is in force, none is out, block can be chosen")
		name, _ := d.Download(func() { export(d) })
		d.OK(strings.HasSuffix(name, ".html") && d.Text(".gj-announcer") == "Exported with 1 accessibility errors", "warn exports and says so: "+name+" "+d.Text(".gj-announcer"))
		// the person raises it: their own block can be allowed for one export
		d.Click(`button[data-tab="check"]`)
		d.Click(`.gj-check-gate[data-gate="block"]`)
		d.OK(d.Has("#gj-check-override"), "the person's own block keeps its override")
		export(d)
		d.WaitFor("refused", `(document.getElementById('transfer-message') || {textContent: ''}).textContent.includes('Not exported: the document has 1 accessibility errors. Fix them')`)
		d.Click(`button[data-tab="check"]`)
		d.Click("#gj-check-override")
		name, _ = d.Download(func() { export(d) })
		d.OK(strings.HasSuffix(name, ".html"), "the allowed export goes ahead: "+name)
		d.NoErrors()
	})

	t.Run("a typo is read as block", func(t *testing.T) {
		site := e2etest.NewSite(t)
		serve(site, "blok")
		d := site.Open(t, "?lang=en")
		withPictureNoAlt(d)
		export(d)
		d.WaitFor("refused", `(document.getElementById('transfer-message') || {textContent: ''}).textContent.includes('Not exported')`)
	})
}
