//go:build !(js && wasm)

package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/collab/hub"
	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestChangesWaitingSurviveAReload cuts the hub, types, reloads the page with the changes still
// waiting (they are kept in the browser), brings the hub back and checks that another browser gets them
// and that the person is told the changes were merged.
func TestChangesWaitingSurviveAReload(t *testing.T) {
	h := hub.New(hub.Config{KeepAlive: 300 * time.Millisecond, Roles: func(r *http.Request, _ string) hub.Role {
		return hub.Role(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}})
	defer func() { _ = h.Close() }()
	srv := httptest.NewServer(h.Handler())
	addr := srv.Listener.Addr().String()
	hubURL := srv.URL
	site := e2etest.NewSite(t)
	q := func(name, token string) string {
		return "/d/queue1?hub=" + url.QueryEscape(hubURL) + "&token=" + token + "&name=" + name
	}
	a := site.Open(t, q("Ana", "owner"))
	a.Wait("s.live.status === 'online' && s.live.role === 'owner'")
	a.Sleep(400)
	a.ToggleMode()
	a.CanvasClick(240, 164)
	a.Focus("#input")
	a.Press("End")

	_ = srv.Listener.Close() // the hub is gone: no new connections, and the open ones are cut
	srv.CloseClientConnections()
	a.Type(" KEPT")
	a.WaitFor("the changes are stored in the browser", `Object.keys(localStorage).some(k => k.startsWith('showcase-queue:queue1:Ana'))`)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("could not take the address again: %v", err)
	}
	back := httptest.NewUnstartedServer(h.Handler())
	back.Listener = ln
	back.Start()
	defer func() { back.CloseClientConnections(); go back.Close() }() // the browsers' streams would hold Close
	defer func() { go srv.Close() }()                                 // waits for the cut streams to end; never hold the test for it

	a.Reload() // as if the tab had been closed with changes waiting
	a.Wait("s.live.status === 'online'")
	a.WaitFor("the stored changes are sent", `!Object.keys(localStorage).some(k => k.startsWith('showcase-queue:queue1:Ana'))`)
	// The changes made offline met the shared document: Ana is told, with a number she can check.
	a.WaitForTimeout("the merge is announced", `/\d+ changes? (was|were) merged\./.test((document.querySelector('.gj-announcer') || {textContent: ''}).textContent)`, 20*time.Second)

	b := site.Open(t, q("Bo", "editor"))
	b.Wait("s.live.status === 'online' && s.live.role === 'editor'")
	b.Wait("s.text.includes(' KEPT')")
}
