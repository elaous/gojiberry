//go:build !(js && wasm)

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elaous/gojiberry/collab/hub"
	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestRefusedChangeIsAnnounced: an editor whose role is taken away while the page is open types, the
// server refuses the change, and the live region says so (and keeps saying how many).
func TestRefusedChangeIsAnnounced(t *testing.T) {
	var demoted atomic.Bool
	h := hub.New(hub.Config{KeepAlive: 500 * time.Millisecond, Roles: func(r *http.Request, _ string) hub.Role {
		role := hub.Role(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if role == "editor" && demoted.Load() {
			return hub.Viewer
		}
		return role
	}})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	defer func() {
		if err := h.Close(); err != nil {
			t.Errorf("closing the hub: %v", err)
		}
	}()

	site := e2etest.NewSite(t)
	d := site.Open(t, "/d/live2?hub="+url.QueryEscape(srv.URL)+"&token=editor&transport=http&name=Bo&color="+url.QueryEscape("#2563eb"))
	d.Wait("s.live.status === 'online' && s.live.role === 'editor'")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("End")
	demoted.Store(true)
	d.Type(" X")
	d.WaitFor("the refusal is said", `(document.querySelector('.gj-announcer') || {textContent: ''}).textContent.includes('could not be applied')`)
}

// In a shared session a saved version is by the person who saved it, not by the page's placeholder address.
func TestLiveVersionAuthorIsThePerson(t *testing.T) {
	h := hub.New(hub.Config{KeepAlive: 500 * time.Millisecond, Roles: func(r *http.Request, _ string) hub.Role {
		return hub.Role(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	defer func() {
		if err := h.Close(); err != nil {
			t.Errorf("closing the hub: %v", err)
		}
	}()
	d := e2etest.NewSite(t).Open(t, "/d/live3?lang=en&hub="+url.QueryEscape(srv.URL)+"&token=owner&transport=http&name=Ana&color="+url.QueryEscape("#e11d48"))
	d.Wait("s.live.status === 'online' && s.live.role === 'owner'")
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Fill(".gj-saver-name", "plan")
	d.Click(".gj-saver-save")
	d.WaitSelector(".gj-doclist-item")
	d.Click(".gj-versions-save")
	d.WaitSelector(".gj-versions-item")
	if got := d.Text(".gj-versions-author"); !strings.Contains(got, "by Ana") {
		t.Errorf("the version's author = %q, want it to name Ana", got)
	}
}
