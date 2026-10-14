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

// TestChangedUnderYouIsSaid: two people type in one paragraph a moment apart; the one whose paragraph was
// changed by the other hears who did it and that both changes were kept.
func TestChangedUnderYouIsSaid(t *testing.T) {
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
	site := e2etest.NewSite(t)
	q := func(name, token string) string {
		return "/d/live4?lang=en&hub=" + url.QueryEscape(srv.URL) + "&token=" + token + "&transport=http&name=" + name
	}
	a := site.Open(t, q("Ana", "owner"))
	a.Wait("s.live.status === 'online' && s.live.role === 'owner'")
	a.Sleep(400)
	b := site.Open(t, q("Bo", "editor"))
	b.Wait("s.live.status === 'online' && s.live.role === 'editor'")
	b.Wait("s.text.length > 100")
	a.ToggleMode()
	b.ToggleMode()
	a.CanvasClick(240, 164)
	a.Focus("#input")
	a.Press("End")
	a.Type(" ANA")
	b.Wait("s.text.includes(' ANA')")
	b.CanvasClick(240, 164)
	b.Focus("#input")
	b.Press("End")
	b.Type(" BO")
	a.WaitForTimeout("Ana hears that Bo changed her paragraph",
		`(document.querySelector('.gj-announcer') || {textContent: ''}).textContent.includes('Bo changed the paragraph you were editing. Both changes were kept.')`, 20*time.Second)
	a.Wait("s.text.includes(' ANA') && s.text.includes(' BO')")
}
