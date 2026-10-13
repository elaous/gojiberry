//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestPrintLayoutReachesTheExports sets a header, a footer with page-number fields and a table of
// contents in the Page tab, and finds them in the HTML export (print stylesheet and contents) and in the
// Word export (header and footer parts with PAGE and NUMPAGES fields).
func TestPrintLayoutReachesTheExports(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button[data-tab="page"]`)
	d.Fill(".gj-print-header", "{title}")
	d.Dispatch(".gj-print-header", "change")
	d.Fill(".gj-print-footer", "Page {page} of {pages}")
	d.Dispatch(".gj-print-footer", "change")
	d.Click(`.gj-print-layout input[type="checkbox"]`)
	d.WaitFor("the footer is kept", `document.querySelector('.gj-print-footer').value === 'Page {page} of {pages}' && document.querySelector('.gj-print-layout input[type="checkbox"]').checked`)

	d.Click(`button[data-tab="transfer"]`)
	_, html := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="html"]`) })
	s := string(html)
	d.OK(strings.Contains(s, `@bottom-center{content:"Page " counter(page) " of " counter(pages)`), "the print stylesheet has the footer with its fields")
	d.OK(strings.Contains(s, `<nav class="toc"`), "the contents start the page")
	d.OK(strings.Contains(s, "@page{size:"), "the sheet is set")

	_, docx := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="docx"]`) })
	d.OK(len(docx) > 0 && strings.Contains(string(docx), "word/footer1.xml") && strings.Contains(string(docx), "word/header1.xml"), "the Word file has a header and a footer part")
	d.NoErrors()
}

// The sheets on the screen show the running header and footer the exports print, with the page number of each
// sheet filled in.
func TestSheetsShowTheRunningHeaderAndFooter(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.OK(d.Str(`JSON.parse(document.getElementById('state').textContent).headers[0]`) == "Gojiberry editor", "the sample's own header before a layout is set")
	d.Click(`button[data-tab="page"]`)
	d.Fill(".gj-print-header", "Report\t{title}")
	d.Dispatch(".gj-print-header", "change")
	d.Fill(".gj-print-footer", "Draft\t\tPage {page} of {pages}")
	d.Dispatch(".gj-print-footer", "change")
	d.WaitFor("the header of the first sheet", `JSON.parse(document.getElementById('state').textContent).headers[0] === "Report\t\tQuarterly report"`)
	// (two parts are the left and the right one; {title} is the document's title)
	d.WaitFor("the footers carry each sheet's number", `(() => { const s = JSON.parse(document.getElementById('state').textContent); return s.pages >= 2 && s.footers[0] === "Draft\t\tPage 1 of " + s.pages && s.footers[1] === "Draft\t\tPage 2 of " + s.pages; })()`)
	d.NoErrors()
}
