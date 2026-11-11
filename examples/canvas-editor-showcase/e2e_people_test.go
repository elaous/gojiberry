//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestPeoplePanel(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.Click(`button[data-tab="people"]`)
	d.OK(d.Count(".gj-people-item") == 1 && d.Text(".gj-people-owner") == "owner", "the owner is listed, with no controls")

	d.Fill(".gj-people-invite-id", "ana@example.com")
	d.WaitFor("the invite button to enable", `!document.querySelector('.gj-people-invite').disabled`)
	d.Click(".gj-people-invite")
	d.WaitFor("the invited person", "document.querySelectorAll('.gj-people-item').length === 2")
	d.OK(d.Value(`.gj-people-item[data-id="ana@example.com"] .gj-people-role`) == "editor", "an invited person is an editor by default")
	d.OK(d.Value(".gj-people-invite-id") == "", "and the form is cleared")

	d.Select(`.gj-people-item[data-id="ana@example.com"] .gj-people-role`, "viewer")
	d.WaitFor("the new role", `document.querySelector('.gj-people-item[data-id="ana@example.com"] .gj-people-role').value === 'viewer'`)
	d.OK(true, "her role can be changed")

	d.Fill(".gj-people-invite-id", "you@example.com") // the owner
	d.WaitFor("the invite button to enable", `!document.querySelector('.gj-people-invite').disabled`)
	d.Click(".gj-people-invite")
	d.WaitFor("an error", "document.querySelector('.gj-doc-error')")
	d.OK(d.Count(".gj-people-item") == 2, "the owner's access cannot be changed: "+d.Text(".gj-doc-error"))

	d.Fill(".gj-people-invite-id", "  Bo@Example.COM ")
	d.WaitFor("the invite button to enable", `!document.querySelector('.gj-people-invite').disabled`)
	d.Click(".gj-people-invite")
	d.WaitSelector(`.gj-people-item[data-id="bo@example.com"]`)
	d.Fill(".gj-people-invite-id", "BO@example.com")
	d.WaitFor("the invite button to enable", `!document.querySelector('.gj-people-invite').disabled`)
	d.Click(".gj-people-invite")
	d.WaitFor("the form to clear", `document.querySelector('.gj-people-invite-id').value === ''`)
	d.OK(d.Count(".gj-people-item") == 3, "inviting the same person again does not list them twice")
	d.Fill(".gj-people-invite-id", "   ")
	d.WaitFor("a blank draft", `document.querySelector('.gj-people-invite').disabled`)
	d.OK(d.Bool(`document.querySelector('.gj-people-invite').disabled`), "a blank address cannot be invited")
	d.Fill(".gj-people-invite-id", "")

	d.Click(`.gj-people-item[data-id="ana@example.com"] .gj-people-remove`)
	d.WaitSelector(`[role="alertdialog"]`)
	d.Click(`[role="alertdialog"] .gj-dialog-actions button:last-child`)
	d.WaitFor("access removed", "document.querySelectorAll('.gj-people-item').length === 2")
	d.OK(true, "access can be removed")
	d.NoErrors()
}
