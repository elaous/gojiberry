//go:build !(js && wasm)

package main

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/examples/people-keys-demo/demoserver"
	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestThePeopleTabIsTheServersAccessList(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	srv, err := demoserver.New(site.URL)
	if err != nil {
		t.Fatal(err)
	}
	backing := docstore.NewMemoryAdapter()
	docs := docstore.NewServer(backing, "memory").WithAuth(srv.Auth)
	site.Handle("/auth/", srv.Mux)
	site.Handle("/demo/", srv.Mux)
	site.Handle("/docs", docs.Handler())
	site.Handle("/docs/", docs.Handler())

	d := site.OpenURL(t, "/index.html?lang=en&docstore="+url.QueryEscape(site.URL))
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-offer-review")
	d.OK(strings.Contains(d.Text("#people-where"), "kept in this page only"), "before a server: "+d.Text("#people-where"))
	d.Click("#dir-offer-review")
	confirmDialog(d)

	d.Click(`button[data-tab="save"]`)
	d.WaitSelector(`.gj-saver-select[data-field="Save to"] option[value="server"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "server")
	d.WaitFor("the server to be the target", `document.querySelector('.gj-saver-select[data-field="Save to"]').value === 'server'`)
	d.Sleep(800)
	d.Fill(".gj-saver-name", "shared plan")
	d.Click(".gj-saver-save")
	d.WaitSelector(`.gj-doclist-item`)

	d.Click(`button[data-tab="people"]`)
	d.WaitFor("the server's list", `(document.getElementById('people-where') || {textContent: ''}).textContent.includes("This list is the server's")`)

	d.Fill(".gj-people-invite-id", "bo@example.com")
	d.WaitFor("the invite button", `!document.querySelector('.gj-people-invite').disabled`)
	d.Click(".gj-people-invite")
	d.WaitSelector(`.gj-people-item[data-id="bo@example.com"]`)
	ids, err := backing.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var aclKey string
	for _, m := range ids {
		if strings.HasSuffix(m.ID, "/acl") || strings.Contains(m.ID, "acl") {
			aclKey = m.ID
		}
	}
	if aclKey == "" {
		t.Fatalf("the server holds no access list: %v", ids)
	}
	got, err := backing.Load(context.Background(), aclKey)
	if err != nil || !strings.Contains(string(got.Data), "bo@example.com") {
		t.Fatalf("the server's list = %q (%v)", got.Data, err)
	}

	d.Click("#link-create")
	d.WaitSelector("#link-new-url")
	d.OK(strings.Contains(d.Value("#link-new-url"), "#share="), "the link: "+d.Value("#link-new-url"))
	d.WaitFor("the link in the list", `document.querySelectorAll('#who-list li[data-kind="link"]').length === 1`)
	d.Click(`#who-list li[data-kind="link"] button`)
	d.WaitFor("the link gone", `document.querySelectorAll('#who-list li[data-kind="link"]').length === 0`)

	d.NoErrors()
}
