//go:build !(js && wasm)

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/public"
	"github.com/elaous/gojiberry/docstore/public/client"
	"github.com/elaous/gojiberry/internal/e2etest"
)

func publicOffer(t *testing.T) (*httptest.Server, *docstore.MemoryAdapter) {
	t.Helper()
	inner := docstore.NewMemoryAdapter()
	ea := docstore.NewExpiringAdapter(inner, docstore.FixedTTL(24*time.Hour))
	svc, err := public.New(public.Options{Store: ea, Config: public.Config{
		AdminToken: "admin-token-for-the-test", NoProxy: true, TermsURL: "https://example.org/terms",
		SessionsPerHour: 1000, MaxSessionsPerAddress: 1000, WritesPerMinute: 1000, WritesPerMinutePerAddress: 100000, ViewsPerMinute: 1000, ReportsPerMinute: 1000,
	}})
	if err != nil {
		t.Fatal(err)
	}
	srv := docstore.NewServer(ea, "memory")
	svc.Mount(srv)
	h := srv.Handler()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "authorization, content-type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts, inner
}

func fragmentOf(link string) string { _, f, _ := strings.Cut(link, "#"); return f }

func TestPublicLinkPublishesAndTheLinksOpen(t *testing.T) {
	offer, store := publicOffer(t)
	site := e2etest.NewSite(t)
	api := "?lang=en&publicapi=" + offer.URL

	d := site.Open(t, api)
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Type("Hello public ")
	d.Click(`button[data-tab="public"]`)
	d.OK(d.Has("#public-publish") && !d.Has("#public-edit"), "nothing is published until the person says so")
	d.Click("#public-publish")
	d.WaitSelector("#public-edit")
	edit, view := d.Value("#public-edit"), d.Value("#public-view")
	d.OK(strings.Contains(edit, "#edit=") && strings.Contains(view, "#view="), "two links: "+edit+" "+view)
	d.OK(strings.Contains(d.Text("#public-countdown"), "unless edited") && d.Attr("#public-countdown", "data-warn") == "", "the countdown: "+d.Text("#public-countdown"))
	d.OK(d.Attr("#public-terms", "href") == "https://example.org/terms", "the terms link the server sent")
	d.OK(d.Has("#public-keep") && d.Has("#public-download") && d.Has("#public-discard"), "keep, download and delete are offered")

	v := site.Open(t, api+"#"+fragmentOf(view))
	v.WaitSelector("#public-viewer")
	v.WaitFor("the document", `document.getElementById('state').textContent.includes('Hello public')`)
	v.OK(v.Is("document.querySelector('meta[name=robots]') && document.querySelector('meta[name=robots]').content.includes('noindex')"), "the page says noindex")
	v.OK(v.Str("location.hash") == "", "the link's secret is not left in the address bar: "+v.Str("location.hash"))
	v.OK(!v.Has("#public-edit"), "a view link shows no edit link")
	v.Click("#public-report-open")
	v.Select("#public-report-cat", client.CategorySpam)
	v.Fill("#public-report-reason", "this is spam")
	v.Click("#public-report-send")
	v.WaitSelector("#public-reported")
	reports := 0
	metas, _ := store.List(context.Background())
	for _, m := range metas {
		if strings.HasPrefix(m.ID, "report/") {
			reports++
		}
	}
	v.OK(reports == 1, "the report reached the operator's store")
	v.NoErrors()

	e := site.Open(t, api+"#"+fragmentOf(edit))
	e.WaitSelector("#public-edit")
	e.OK(e.Value("#public-edit") == edit && e.Value("#public-view") == view, "the same two links from the other browser")
	e.WaitFor("the document", `document.getElementById('state').textContent.includes('Hello public')`)
	_, tok := client.ParseLink(fragmentOf(edit))
	cl := &client.Client{BaseURL: offer.URL, Token: tok}
	_, before, err := cl.Download(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.ToggleMode()
	e.CanvasClick(240, 164)
	e.Focus("#input")
	e.Press("End")
	e.Type("and more ")
	deadline := time.Now().Add(20 * time.Second)
	var after docfile.Document
	for time.Now().Before(deadline) {
		data, doc, err := cl.Download(context.Background())
		if err == nil && doc.Expires.After(before.Expires) {
			after, _ = docfile.Open.Unmarshal(data)
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	text := ""
	for _, it := range after.Content {
		if it.Chars != nil {
			text += it.Chars.Text
		}
	}
	d.OK(strings.Contains(text, "and more"), "the edit was sent, and renewed the copy: "+text)
	e.NoErrors()

	d.Click("#public-discard")
	d.WaitSelector("#public-publish")
	g := site.Open(t, api+"#"+fragmentOf(view))
	g.WaitFor("the refusal", `(document.getElementById('public-error') || {textContent: ''}).textContent.includes('gone')`)
	g.NoErrors()
	d.NoErrors()
}
