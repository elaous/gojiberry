//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

const skewClock = `window.__skew = 0;
(function () {
  const D = Date;
  class Skewed extends D {
    constructor(...a) { if (a.length === 0) { super(D.now() + window.__skew); } else { super(...a); } }
    static now() { return D.now() + window.__skew; }
  }
  Date = Skewed;
})();`

func newLink(d *e2etest.Tab, role, life string) (token string) {
	d.Select("#link-role", role)
	d.Select("#link-life", life)
	d.Click("#link-create")
	d.WaitSelector("#link-new-url")
	token = d.Value("#link-new-url")
	d.Click("#link-dismiss")
	d.WaitFor("the new-link box to close", `!document.getElementById('link-new-url')`)
	return token
}

func checkLink(d *e2etest.Tab, token string) string {
	d.Fill("#link-try", token)
	d.Click("#link-check")
	d.WaitFor("an answer", `document.getElementById('link-result').textContent !== ''`)
	return d.Text("#link-result")
}

func TestShareLinks(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.OnNewDocument(skewClock)
	d.Reload()
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#who-list")
	d.OK(d.Count("#who-list li") == 1 && d.Attr("#who-list li", "data-kind") == "owner", "only the owner can see it at first")
	d.OK(d.Attr("#who-list", "aria-labelledby") == "who-heading" && d.Text("#who-heading") == "Who can see this", "the list is a labelled list")

	viewTok := newLink(d, "viewer", "1h")
	d.OK(strings.Contains(viewTok, "#share=") && len(viewTok) > 60, "the link carries a long secret: "+viewTok)
	d.WaitFor("the link in the list", `document.querySelectorAll('#who-list li').length === 2`)
	d.OK(d.Attr(`#who-list li[data-kind="link"]`, "data-role") == "viewer", "the link is view-only")
	d.OK(strings.Contains(d.Text(`#who-list li[data-kind="link"]`), "Link ending") && strings.Contains(d.Text(`#who-list li[data-kind="link"]`), "Can view"),
		"the end and the role are written in the list: "+d.Text(`#who-list li[data-kind="link"]`))
	d.OK(d.Bool(`(() => { const ms = Date.parse(document.querySelector('#who-list li[data-kind="link"]').dataset.expires) - Date.now(); return ms > 3500e3 && ms <= 3600e3; })()`),
		"the link ends in an hour")
	secret := viewTok[strings.Index(viewTok, "#share=")+len("#share="):]
	d.OK(len(secret) >= 40 && !strings.Contains(d.Text("#who-list"), secret[:20]), "the list never shows the secret")
	d.OK(strings.Contains(checkLink(d, viewTok), "This link works: Can view and comment"), "the link works: "+d.Text("#link-result"))

	editTok := newLink(d, "editor", "7d")
	d.WaitFor("three lines", `document.querySelectorAll('#who-list li').length === 3`)
	d.OK(d.Count(`#who-list li[data-kind="link"][data-role="editor"]`) == 1 && strings.Contains(d.Text(`#who-list li[data-role="editor"]`), "Can edit"), "an edit link is listed as such")
	d.OK(strings.Contains(checkLink(d, editTok), "Can edit"), "the edit link works")
	d.OK(strings.Contains(checkLink(d, "not-a-link"), "does not work"), "a made-up link is refused")
	d.OK(strings.Contains(checkLink(d, viewTok[:len(viewTok)-1]), "does not work"), "a link with a letter missing is refused")

	d.Eval(`window.__skew = 2 * 3600 * 1000`, nil)
	d.OK(strings.Contains(checkLink(d, viewTok), "does not work"), "an expired link is refused: "+d.Text("#link-result"))
	d.WaitFor("the expired link to leave the list", `document.querySelectorAll('#who-list li[data-kind="link"]').length === 1`)
	d.OK(strings.Contains(checkLink(d, editTok), "This link works"), "the longer link still works")
	d.Eval(`window.__skew = 0`, nil)

	other := newLink(d, "viewer", "1d")
	d.WaitFor("three lines again", `document.querySelectorAll('#who-list li').length === 3`)
	d.OK(strings.Contains(checkLink(d, other), "This link works"), "before revoking")
	d.Click(`#who-list li[data-role="viewer"] button`)
	d.WaitFor("the revoked link to leave", `document.querySelectorAll('#who-list li[data-kind="link"]').length === 1`)
	d.OK(strings.Contains(checkLink(d, other), "does not work"), "a revoked link is refused")
	d.OK(strings.Contains(checkLink(d, editTok), "This link works"), "another link is unaffected")

	d.Fill(".gj-people-invite-id", "ana@example.com")
	d.WaitFor("the invite button", `!document.querySelector('.gj-people-invite').disabled`)
	d.Click(".gj-people-invite")
	d.WaitFor("ana in the list", `!!document.querySelector('#who-list li[data-kind="person"]')`)
	d.OK(strings.Contains(d.Text(`#who-list li[data-kind="person"]`), "ana@example.com: Can edit"), "a person is listed with a role: "+d.Text(`#who-list li[data-kind="person"]`))

	for _, v := range d.Axe("#links", axeDisabled...) {
		t.Errorf("the links section: %s", v)
	}
	d.NoErrors()
}
