//go:build !(js && wasm)

package main

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestTheConfirmedServerIsASaveTarget(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	backing := docstore.NewMemoryAdapter()
	site.Handle("/docs", docstore.NewServer(backing, "memory").Handler())
	option := `.gj-saver-select[data-field="Save to"] option[value="server"]`

	d := site.OpenURL(t, "/index.html?docstore="+url.QueryEscape(site.URL))
	d.Click(`button[data-tab="save"]`)
	d.WaitSelector(`.gj-saver-select[data-field="Save to"]`)
	d.OK(!d.Has(option), "no server target before one is confirmed")

	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-offer-review")
	d.Click("#dir-offer-review")
	confirmDialog(d)
	d.Click(`button[data-tab="save"]`)
	d.WaitSelector(option)
	d.OK(strings.Contains(d.Text(option), "the server can read"), "the label says the server can read the documents: "+d.Text(option))

	d.Select(`.gj-saver-select[data-field="Save to"]`, "server")
	d.WaitFor("the server to be the target", `document.querySelector('.gj-saver-select[data-field="Save to"]').value === 'server'`)
	d.Sleep(800)
	d.Fill(".gj-saver-name", "on the server")
	d.Click(".gj-saver-save")
	d.WaitSelector(`.gj-doclist-item:has(.gj-doc-name:text-is("on the server"))`)
	metas, err := backing.List(context.Background())
	if err != nil || len(metas) < 2 { // the catalog and the document
		t.Fatalf("the server holds %v (%v), want the catalog and the document", metas, err)
	}

	d.Click(`button[data-tab="people"]`)
	d.Click("#dir-clear")
	d.Click(`button[data-tab="save"]`)
	d.WaitFor("the target to go", `!document.querySelector('.gj-saver-select[data-field="Save to"] option[value="server"]')`)
	d.NoErrors()
}
