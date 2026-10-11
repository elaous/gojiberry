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

// TestLiveEndToEnd opens the showcase in several browsers on one shared
// document served by a collab hub: shared typing, cursors, per-user undo, and
// a viewer who can comment but not edit. The page takes its role from the
// bearer token it sends, which this test's hub reads as the role name. It
// runs over both transports (?transport=http|ws).
func TestLiveEndToEnd(t *testing.T) {
	// The WebSocket run shares everything (?flow=*), the HTTP one the text only.
	for _, c := range []struct{ transport, flow string }{{"http", ""}, {"ws", "*"}} {
		transport, flow := c.transport, c.flow
		t.Run(transport, func(t *testing.T) {
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
			liveEndToEnd(t, srv.URL, transport, flow)
		})
	}
}

func liveEndToEnd(t *testing.T, hubURL, transport, flow string) {
	site := e2etest.NewSite(t)
	q := func(name, token, color string) string {
		return "/d/live1?hub=" + url.QueryEscape(hubURL) + "&token=" + token + "&transport=" + transport + "&flow=" + url.QueryEscape(flow) + "&name=" + name + "&color=" + url.QueryEscape(color)
	}
	edit := func(d *e2etest.Tab, x, y float64, key string) {
		d.CanvasClick(x, y)
		d.Focus("#input")
		if key != "" {
			d.Press(key)
		}
	}
	a := site.Open(t, q("Ana", "owner", "#e11d48"))
	a.Wait("s.live.status === 'online' && s.live.role === 'owner'")
	a.OK(a.State().Str("role") == "owner", "the owner is an owner")
	a.WaitFor("the sharing status", `(document.querySelector('.gj-status-item[data-item="sync"]') || {textContent: ''}).textContent === 'All changes shared'`)
	a.OK(a.Attr(`.gj-status-item[data-item="sync"]`, "data-state") == "synced", "and says so in the status bar")
	a.Sleep(400) // her content reaches the hub

	b := site.Open(t, q("Bo", "editor", "#2563eb"))
	b.Wait("s.live.status === 'online' && s.live.role === 'editor'")
	aText := a.State().Str("text")
	b.Wait("s.text.length > 100")
	b.OK(b.State().Str("text") == aText, "the second browser receives the first one's document")

	// Ana hears, through the live region, that Bo joined.
	a.WaitFor("Bo's arrival is announced", `(document.querySelector('.gj-announcer') || {textContent: ''}).textContent.includes('Bo joined.')`)

	// Both go into edit mode; Ana types at the end of a line, Bo on another line.
	a.ToggleMode()
	b.ToggleMode()
	edit(a, 240, 164, "End")
	a.Type(" ANA")
	b.Wait("s.text.includes(' ANA')")
	edit(b, 240, 164+22, "End")
	b.Type(" BO")
	a.Wait("s.text.includes(' BO')")
	b.Wait("s.text.includes(' ANA')")
	a.OK(a.State().Str("text") == b.State().Str("text"), "concurrent typing converges: both documents are the same")

	// Presence: each sees the other and a cursor drawn for them.
	a.Wait("s.live.peers.includes('Bo') && s.live.remote >= 1")
	b.Wait("s.live.peers.includes('Ana') && s.live.remote >= 1")
	a.OK(a.Count("#presence .gj-presence-avatar") == 2, "the presence list shows two people")
	a.OK(strings.HasPrefix(a.Text("#presence .gj-presence-status"), "Online"), "and that it is online")

	// Per-user undo: Ana undoes her typing; Bo's stays.
	a.Focus("#input")
	a.Press("Control+z")
	b.Wait("!s.text.includes(' ANA')")
	a.Wait("!s.text.includes(' ANA') && s.text.includes(' BO')")
	b.OK(strings.Contains(b.State().Str("text"), " BO"), "Ctrl+Z undid only Ana's own typing, Bo's stayed")
	a.Press("Control+Shift+z")
	b.Wait("s.text.includes(' ANA')")
	a.OK(true, "redo brings it back for everyone")

	// A viewer: held in view mode, cannot type, can comment on selected text.
	c := site.Open(t, q("Vee", "viewer", "#059669"))
	c.Wait("s.live.status === 'online' && s.live.role === 'viewer'")
	c.Wait("s.text.length > 100")
	c.OK(c.Text("#presence .gj-presence-badge") == "View only", "a viewer sees the View only badge")
	// The viewer joins while the others' last edits are still arriving: take the text for the
	// comparison once it has stopped changing.
	before := c.State().Str("text")
	for i := 0; i < 20; i++ {
		time.Sleep(150 * time.Millisecond)
		now := c.State().Str("text")
		if now == before {
			break
		}
		before = now
	}
	c.ToggleMode()
	c.OK(c.State().Str("mode") == "view", "a viewer cannot switch to edit mode")
	c.CanvasClick(240, 164)
	c.Type("nope")
	c.OK(c.State().Str("text") == before, "typing does nothing for a viewer")
	c.CanvasDblClick(240, 164) // selects a word
	c.Click(`button[data-tab="comments"]`)
	c.AnswerDialog("A viewer's comment")
	c.Click(`[data-action="add-comment"]`)
	a.Wait("s.comments === 1")
	a.WaitFor("the other person's comment announced", `document.querySelector('.gj-announcer').textContent.includes('commented')`)
	c.OK(c.State().Str("live.error") == "", "the server accepted the viewer's comment")
	a.OK(a.State().Str("text") == c.State().Str("text"), "and commenting changed no text")
	a.Click(`button[data-tab="comments"]`)
	a.OK(a.Text(".gj-comment .gj-markdown") == "A viewer's comment", "Ana sees it in her comments list")
	c.OK(c.Count(".gj-comment-reply-row") == 1 && c.Count(".gj-comment-actions") == 0, "the viewer can reply but not resolve or delete")

	// Closing a tab removes its cursor for the others.
	c.Close()
	a.WaitForTimeout("Vee to leave", `(s => !s.live.peers.includes('Vee'))(JSON.parse(document.getElementById('state').textContent))`, 40*time.Second)
	a.OK(true, "a closed browser leaves the presence list")
	a.OK(len(a.Errors()) == 0 && len(b.Errors()) == 0, "no page errors: "+strings.Join(append(a.Errors(), b.Errors()...), "; "))
}
