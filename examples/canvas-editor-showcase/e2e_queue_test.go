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

	_ = srv.Listener.Close()
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
	defer func() { go srv.Close() }()

	a.Reload() // as if the tab had been closed with changes waiting
	a.Wait("s.live.status === 'online'")
	a.WaitFor("the stored changes are sent", `!Object.keys(localStorage).some(k => k.startsWith('showcase-queue:queue1:Ana'))`)
	a.WaitForTimeout("the merge is announced", `/\d+ changes? (was|were) merged\./.test((document.querySelector('.gj-announcer') || {textContent: ''}).textContent)`, 20*time.Second)

	b := site.Open(t, q("Bo", "editor"))
	b.Wait("s.live.status === 'online' && s.live.role === 'editor'")
	b.Wait("s.text.includes(' KEPT')")
}

func TestGoingOfflineInTheBrowser(t *testing.T) {
	h := hub.New(hub.Config{KeepAlive: 300 * time.Millisecond, Roles: func(r *http.Request, _ string) hub.Role {
		return hub.Role(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}})
	srv := httptest.NewServer(h.Handler())
	defer func() { go srv.Close(); _ = h.Close() }()
	site := e2etest.NewSite(t)
	q := func(name, token string) string {
		return "/d/queue2?hub=" + url.QueryEscape(srv.URL) + "&token=" + token + "&name=" + name
	}
	a := site.Open(t, q("Ana", "owner"))
	a.Wait("s.live.status === 'online' && s.live.role === 'owner'")
	a.Sleep(400)
	a.ToggleMode()
	a.CanvasClick(240, 164)
	a.Focus("#input")
	a.Press("End")

	a.Offline(true)
	srv.CloseClientConnections()
	a.Type(" OFFLINE")
	a.Wait("s.live.status === 'offline' || s.live.status === 'connecting'")
	a.Offline(false)
	a.Wait("s.live.status === 'online'")

	b := site.Open(t, q("Bo", "editor"))
	b.Wait("s.live.status === 'online' && s.live.role === 'editor'")
	b.Wait("s.text.includes(' OFFLINE')")
}

func TestWhatOthersChangedWhileOffline(t *testing.T) {
	h := hub.New(hub.Config{KeepAlive: 300 * time.Millisecond, Roles: func(r *http.Request, _ string) hub.Role {
		return hub.Role(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}})
	srv := httptest.NewServer(h.Handler())
	defer func() { go srv.Close(); _ = h.Close() }()
	site := e2etest.NewSite(t)
	q := func(name, token string) string {
		return "/d/queue3?lang=en&hub=" + url.QueryEscape(srv.URL) + "&token=" + token + "&name=" + name
	}
	a := site.Open(t, q("Ana", "owner"))
	a.Wait("s.live.status === 'online' && s.live.role === 'owner'")
	a.Sleep(400)
	b := site.Open(t, q("Bo", "editor"))
	b.Wait("s.live.status === 'online' && s.live.role === 'editor'")
	b.Wait("s.text.length > 100")

	a.Offline(true)
	srv.CloseClientConnections()
	a.Wait("s.live.status === 'offline' || s.live.status === 'connecting'")
	b.Wait("s.live.status === 'online'")
	b.ToggleMode()
	b.CanvasClick(240, 164)
	b.Focus("#input")
	b.Press("End")
	b.Type(" FROMBO")
	b.Sleep(600)

	a.Offline(false)
	a.Wait("s.live.status === 'online' && s.text.includes(' FROMBO')")
	a.WaitForTimeout("the changes made meanwhile are listed", `!!document.querySelector('#away-diff .gj-diff-item')`, 20*time.Second)
	if got := a.Text("#away-diff"); !strings.Contains(got, "FROMBO") {
		t.Errorf("the list does not show Bo's change: %q", got)
	}
	a.Click("#away .gj-button, #away button")
	a.Wait("true")
	if a.Has("#away") {
		t.Error("the list stays after Dismiss")
	}
}
