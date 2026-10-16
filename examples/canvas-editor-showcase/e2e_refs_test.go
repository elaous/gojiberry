//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// A source is typed once; the document cites it in the chosen style and ends with the list of references, in the
// same style; changing the style changes how the source is written.
func TestCiteASourceAndInsertTheListOfReferences(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Click(`button[data-tab="refs"]`)
	d.WaitSelector("#ref-add")
	d.OK(d.Bool(`document.getElementById('ref-add').disabled === false`), "editing is on")
	d.Fill("#ref-authors", "Silva, Ana Maria\nCosta, Bruno")
	d.Fill("#ref-title", "Writing for Readers")
	d.Fill("#ref-publisher", "Harbour Press")
	d.Fill("#ref-year", "2020")
	d.Click("#ref-add")
	d.WaitSelector("#ref-entry-0")
	d.OK(strings.Contains(d.Text("#ref-entry-0"), "Silva, A. M., & Costa, B. (2020). Writing for Readers. Harbour Press."), "APA entry: "+d.Text("#ref-entry-0"))

	d.Click("#ref-cite-0")
	d.WaitFor("the citation is in the text", `document.getElementById('a11y-text').textContent.includes('(Silva & Costa, 2020)')`)

	d.Select("#ref-style", "mla")
	d.WaitFor("MLA entry", `document.getElementById('ref-entry-0').textContent.startsWith('Silva, Ana Maria, and Bruno Costa. Writing for Readers.')`)
	d.Click("#ref-bibliography")
	d.WaitFor("the list is in the text", `document.getElementById('a11y-text').textContent.includes('Silva, Ana Maria, and Bruno Costa. Writing for Readers. Harbour Press, 2020.')`)

	d.Click(`button[data-tab="transfer"]`)
	_, html := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="html"]`) })
	s := string(html)
	d.OK(strings.Contains(s, "(Silva &amp; Costa, 2020)") && strings.Contains(s, "<em>Writing for Readers</em>"), "the export has the citation and the title in italics")
	d.NoErrors()
}
