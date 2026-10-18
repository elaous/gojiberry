//go:build !(js && wasm)

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/collab/hub"
	"github.com/elaous/gojiberry/internal/e2etest"
)

// Two people at one equation: while Ana is changing it, Bo sees it busy (the announcer says who) and his change is
// refused; once Ana has been quiet for a while the hold ends and Bo can change it.
func TestAnObjectOnePersonIsChangingIsBusyForTheOther(t *testing.T) {
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
	q := func(name, token, color string) string {
		return "/d/locks1?hub=" + url.QueryEscape(srv.URL) + "&token=" + token + "&transport=ws&flow=" + url.QueryEscape("*") + "&name=" + name + "&color=" + url.QueryEscape(color)
	}
	a := site.Open(t, q("Ana", "owner", "#e11d48"))
	a.Wait("s.live.status === 'online' && s.live.role === 'owner'")
	a.Sleep(400)
	b := site.Open(t, q("Bo", "editor", "#2563eb"))
	b.Wait("s.live.status === 'online' && s.live.role === 'editor'")
	b.Wait("s.text.length > 100")

	a.ToggleMode()
	a.CanvasClick(240, 164)
	a.Click(`button[data-tab="images"]`)
	a.WaitSelector("#eq-formula")
	a.Fill("#eq-formula", "x^2")
	a.Fill("#eq-alt", "x squared")
	a.Click("#eq-insert")
	a.WaitSelector("#eq-formula-0")
	b.ToggleMode()
	b.Click(`button[data-tab="images"]`)
	b.WaitSelector("#eq-formula-0")

	a.Fill("#eq-formula-0", "x^3") // Ana is at it
	b.WaitFor("Bo sees Ana holding it", `JSON.parse(document.getElementById("state").textContent).live.holding.includes('Ana=equation:0')`)
	b.Fill("#eq-formula-0", "y^2") // and Bo's change is refused
	b.WaitFor("Bo is told it is busy", `document.querySelector('.gj-announcer').textContent.includes('Busy: Ana is changing this equation.')`)
	b.Sleep(500)
	b.OK(b.Str(`document.getElementById('eq-formula-0').value`) != "y^2", "Bo's text was put back: "+b.Str(`document.getElementById('eq-formula-0').value`))

	// Ana stops; the hold runs out a few seconds later and Bo can change it
	b.WaitForTimeout("the hold ended", `!JSON.parse(document.getElementById("state").textContent).live.holding.includes('Ana=equation:0')`, 25*time.Second)
	b.Fill("#eq-formula-0", "z^2")
	a.WaitFor("Ana sees Bo's change", `document.getElementById('eq-formula-0') && document.getElementById('eq-formula-0').value === 'z^2'`)
	a.NoErrors()
	b.NoErrors()
}
