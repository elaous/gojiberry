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
	d.Click(`.gj-print-layout > label.gj-checkbox:last-child input`) // the contents switch is the last control
	d.WaitFor("the footer is kept", `document.querySelector('.gj-print-footer').value === 'Page {page} of {pages}' && document.querySelector('.gj-print-layout > label.gj-checkbox:last-child input').checked`)

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

// "Different first page" gives the first sheet a header and footer of its own (empty: none), on the screen and in
// the exports.
func TestFirstSheetCanHaveItsOwnHeaderAndFooter(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button[data-tab="page"]`)
	d.Fill(".gj-print-header", "Running")
	d.Dispatch(".gj-print-header", "change")
	d.WaitFor("the header is kept", `document.querySelector('.gj-print-header').value === 'Running'`)
	d.OK(!d.Has(".gj-print-firstheader"), "the first page's fields appear only when asked for")
	d.Click(`.gj-print-layout input[type="checkbox"]:not(:checked)`) // "Different first page" is the first box in the panel
	d.WaitSelector(".gj-print-firstheader")
	d.Fill(".gj-print-firstheader", "Cover")
	d.Dispatch(".gj-print-firstheader", "change")
	d.WaitFor("the first sheet's header", `(() => { const s = JSON.parse(document.getElementById('state').textContent); return s.headers[0] === "\tCover\t" && s.pages >= 2 && s.headers[1] === "\tRunning\t"; })()`)
	d.Click(`button[data-tab="transfer"]`)
	_, html := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="html"]`) })
	d.OK(strings.Contains(string(html), "@page :first{"), "the print stylesheet has a rule for the first page")
	d.NoErrors()
}

// "Different odd and even pages" gives the even sheets a header of their own, and the odd ones keep the running one.
func TestEvenSheetsCanHaveTheirOwnHeader(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button[data-tab="page"]`)
	d.Fill(".gj-print-header", "Odd")
	d.Dispatch(".gj-print-header", "change")
	d.WaitFor("the header is kept", `document.querySelector('.gj-print-header').value === 'Odd'`)
	d.OK(!d.Has(".gj-print-evenheader"), "the even pages' fields appear only when asked for")
	d.Click(`label.gj-checkbox:has-text("Different odd and even pages") input`)
	d.WaitSelector(".gj-print-evenheader")
	d.Fill(".gj-print-evenheader", "Even")
	d.Dispatch(".gj-print-evenheader", "change")
	d.WaitFor("the headers alternate", `(() => { const s = JSON.parse(document.getElementById('state').textContent); return s.pages >= 3 && s.headers[0] === "\tOdd\t" && s.headers[1] === "\tEven\t" && s.headers[2] === "\tOdd\t"; })()`)
	d.Click(`button[data-tab="transfer"]`)
	_, html := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="html"]`) })
	d.OK(strings.Contains(string(html), "@page :left{"), "the print stylesheet has a rule for the even pages")
	d.NoErrors()
}
