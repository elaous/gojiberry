//go:build !(js && wasm) && (experimental || exp_textformats)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestATextFileOpensForReading(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click("#mode") // View -> Edit
	d.WaitFor("edit mode", `document.getElementById('mode').textContent.includes('edit')`)
	d.Click(`button[data-tab="transfer"]`)
	d.ChooseFile(func() { d.Click(`.gj-transfer-button[data-kind="import"][data-format="csv"]`) }, "stock.csv", []byte("item,qty\npens,3\n"))
	d.WaitFor("view mode", `document.getElementById('mode').textContent.includes('view')`)
	d.OK(strings.Contains(d.Str(`document.body.innerText`), "copy of a csv file"), "the page says it is a copy")
	d.Click(`button[data-tab="transfer"]`)
	d.ChooseFile(func() { d.Click(`.gj-transfer-button[data-kind="import"][data-format="yaml"]`) }, "bad.yaml", []byte("a: 1\nb: [unclosed\n"))
	d.WaitFor("the problem is said", `document.body.innerText.includes('does not read as valid yaml')`)
	d.ChooseFile(func() { d.Click(`.gj-transfer-button[data-kind="import"][data-format="anytext"]`) }, "notes.weird", []byte("just some words\n"))
	d.WaitFor("any text file", `document.body.innerText.includes('copy of a anytext file')`)
	d.ChooseFile(func() { d.Click(`.gj-transfer-button[data-kind="import"][data-format="htmlsource"]`) }, "page.html", []byte("<p class=\"a\">hi</p>\n"))
	d.WaitFor("the HTML source", `document.body.innerText.includes('copy of a htmlsource file')`)
	d.NoErrors()
}
