//go:build !(js && wasm) && (experimental || exp_textformats)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// A text file opened as it is comes in for reading, whatever mode the page was in, and says it is a copy.
func TestATextFileOpensForReading(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click("#mode") // View -> Edit
	d.WaitFor("edit mode", `document.getElementById('mode').textContent.includes('edit')`)
	d.Click(`button[data-tab="transfer"]`)
	d.ChooseFile(func() { d.Click(`.gj-transfer-button[data-kind="import"][data-format="csv"]`) }, "stock.csv", []byte("item,qty\npens,3\n"))
	d.WaitFor("view mode", `document.getElementById('mode').textContent.includes('view')`)
	d.OK(strings.Contains(d.Str(`document.body.innerText`), "copy of a csv file"), "the page says it is a copy")
	// A file that does not read as the format it came in is shown as it is, and the page says what is wrong.
	d.Click(`button[data-tab="transfer"]`)
	d.ChooseFile(func() { d.Click(`.gj-transfer-button[data-kind="import"][data-format="yaml"]`) }, "bad.yaml", []byte("a: 1\nb: [unclosed\n"))
	d.WaitFor("the problem is said", `document.body.innerText.includes('does not read as valid yaml')`)
	d.NoErrors()
}
