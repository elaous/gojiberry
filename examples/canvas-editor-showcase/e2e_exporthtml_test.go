//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// The HTML file the editor exports is itself accessible: axe finds nothing wrong with it (language, title,
// landmarks, heading order, table headers, image descriptions, link names), whatever the document holds.
func TestExportedHTMLPassesAxe(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	importMarkdown(d, "# Report\n\nIntro with a [link](https://example.org) and **bold** words.\n\n## Numbers\n\n| Name | Age |\n|---|---|\n| Ana | 31 |\n| Bo | 28 |\n\n- one\n- two\n\n1. first\n2. second\n\n> A quote.\n\n```go\nfmt.Println(1)\n```\n\n### Detail\n\nEnd words.", "End words")
	// The page language is the document's own to declare (here the detected one); the export carries it.
	d.Click(`button[data-tab="language"]`)
	d.WaitSelector(".gj-lang-docuse")
	d.Click(".gj-lang-docuse")
	d.WaitFor("the document language", `document.getElementById('a11y-text').getAttribute('lang') === 'en'`)
	d.Click(`button[data-tab="transfer"]`)
	_, html := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="html"]`) })
	page := string(html)
	for _, want := range []string{"<html lang=", "<title>", "<main", "<th"} {
		if !strings.Contains(page, want) {
			t.Errorf("the exported page lacks %q", want)
		}
	}
	// Show the exported page in this tab (axe runs against the page that is there) and check it.
	d.Eval(`(h => { document.open(); document.write(h); document.close(); return true })(`+e2etest.J(page)+`)`, nil)
	d.WaitFor("the exported page", `document.querySelector('table') && document.querySelector('h1')`)
	for _, v := range d.Axe("", nil...) {
		t.Errorf("the exported HTML: %s", v)
	}
}
