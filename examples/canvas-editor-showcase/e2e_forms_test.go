//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// A form field is added from the bar of kinds, given a label and a pattern, filled in with the problem said as it is
// typed, locked by the owner (its pen goes), and emptied by "Clear form fields"; the exports carry the label and the
// value.
func TestFormFieldsAddFillLockAndClear(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Click(`button[data-tab="forms"]`)
	d.WaitSelector("#forms-bar")
	d.OK(d.Has("#forms-add-email") && d.Has("#forms-add-checkbox") && d.Has("#forms-add-select"), "the bar has the kinds")

	d.Click("#forms-add-text")
	d.WaitSelector("#forms-editor")
	d.Fill("#forms-prop-label", "Order number")
	d.Fill("#forms-prop-pattern", `[A-Z]{2}\d{3}`)
	d.Click("#forms-edit-done")
	d.WaitSelector("#forms-item-0")

	d.Click("#forms-fill-0")
	d.WaitSelector("#forms-input")
	d.Fill("#forms-input", "AB1234")
	d.WaitSelector("#forms-fill-problem")
	d.OK(strings.Contains(d.Text("#forms-fill-problem"), "pattern"), "a value that does not match the pattern is said: "+d.Text("#forms-fill-problem"))
	d.Fill("#forms-input", "AB123")
	d.WaitFor("the problem is gone", `!document.getElementById('forms-fill-problem')`)
	d.Click("#forms-fill-done")
	d.WaitFor("the value is listed", `document.getElementById('forms-value-0') && document.getElementById('forms-value-0').textContent === 'AB123'`)

	d.Click("#forms-add-checkbox")
	d.WaitSelector("#forms-editor")
	d.Fill("#forms-prop-label", "I agree")
	d.Click("#forms-edit-done")
	d.WaitSelector("#forms-item-1")

	d.Click(`button[data-tab="transfer"]`)
	_, md := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="markdown"]`) })
	d.OK(strings.Contains(string(md), "Order number: AB123"), "the export has the label and the value: "+string(md))

	d.Click(`button[data-tab="forms"]`)
	d.WaitSelector("#forms-lock-0")
	_, js := d.Download(func() { d.Click("#forms-answers-json") })
	d.OK(strings.Contains(string(js), `"field1": "AB123"`) && strings.Contains(string(js), `"field2": false`), "the answers as JSON: "+string(js))
	_, csv := d.Download(func() { d.Click("#forms-answers-csv") })
	d.OK(strings.HasPrefix(string(csv), "field1,field2\nAB123,false"), "and as CSV: "+string(csv))
	d.Fill("#forms-submit-to", "http://example.org")
	d.Click("#forms-webform")
	d.WaitFor("a wrong address is refused", `document.getElementById('forms-message').textContent.includes('https://')`)
	d.Fill("#forms-submit-to", "https://example.org/in")
	_, page := d.Download(func() { d.Click("#forms-webform") })
	d.OK(strings.Contains(string(page), `<form action="https://example.org/in" method="post">`) && strings.Contains(string(page), `<button type="submit">Send</button>`), "the web form: "+string(page))
	d.Click("#forms-lock-0")
	d.WaitFor("a locked field has no pen", `document.getElementById('forms-locked-0') && !document.getElementById('forms-edit-0')`)
	d.Click("#forms-lock-0")
	d.WaitFor("unlocked again", `!document.getElementById('forms-locked-0') && document.getElementById('forms-edit-0')`)

	d.Click("#forms-clear")
	d.WaitFor("emptied", `!document.getElementById('forms-value-0')`)
	d.OK(strings.Contains(d.Text("#forms-message"), "Cleared 1 form field"), "it says how many: "+d.Text("#forms-message"))
	d.NoErrors()
}
