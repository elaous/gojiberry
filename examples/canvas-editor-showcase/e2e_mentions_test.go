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

// A comment names people by handle. An address of someone with access is written down as their @handle, so the
// address is not in the document; an address of someone without access sends the author back to the question
// (comment) or stays in the draft with the reason (reply), and nothing with it is saved; once that person has
// access, the same address works.
func TestMentionsNeverWriteDownAnAddressWithNoAccess(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("End")
	for range 7 {
		d.Focus("#input")
		d.Press("Shift+ArrowLeft")
	}
	d.Focus("#input")
	d.AnswerDialogs("cc eve@example.org please", "cc @you please")
	d.Press("Control+Alt+n")
	d.Wait("s.comments === 1")
	seen := d.DialogsSeen()
	if len(seen) != 2 || !strings.Contains(seen[1], "eve@example.org has no access to this document") {
		t.Fatalf("the second question should say why the first answer was refused: %q", seen)
	}
	d.Click(`button[data-tab="comments"]`)
	d.WaitSelector(".gj-comment")
	body := d.Text(".gj-comment")
	d.OK(strings.Contains(body, "cc @you please") && !strings.Contains(body, "eve@"), "the comment holds the handle and no address: "+body)

	// a reply with an address of someone with no access stays a draft, with the reason beside it
	d.Fill(".gj-comment-input", "and eve@example.org too")
	d.Click(".gj-comment-send")
	d.WaitSelector(".gj-comment-note")
	d.OK(strings.Contains(d.Text(".gj-comment-note"), "eve@example.org has no access"), "the reason is given: "+d.Text(".gj-comment-note"))
	d.OK(d.Count(".gj-comment-reply") == 0 && d.Value(".gj-comment-input") == "and eve@example.org too", "no reply was saved and the draft is kept")
	d.OK(!strings.Contains(d.Str(`document.getElementById('a11y-text').textContent`), "eve@"), "the address is not in the reading copy either")

	// once she has access, the same words work and become her handle
	d.Click(`button[data-tab="people"]`)
	d.Fill(".gj-people-invite-id", "eve@example.org")
	d.WaitFor("the invite button to enable", `!document.querySelector('.gj-people-invite').disabled`)
	d.Click(".gj-people-invite")
	d.WaitSelector(`.gj-people-item[data-id="eve@example.org"]`)
	d.Click(`button[data-tab="comments"]`)
	d.WaitSelector(".gj-comment-input")
	d.Click(".gj-comment-send")
	d.WaitSelector(".gj-comment-reply")
	reply := d.Text(".gj-comment-reply")
	d.OK(strings.Contains(reply, "@eve") && !strings.Contains(reply, "eve@"), "the reply names her by handle: "+reply)
	d.OK(d.Count(`.gj-comment-reply .gj-mention[data-mention="eve"]`) == 1, "and the mention is marked")
	d.NoErrors()
}

// Two people suggest changes at the same place from two real browsers on one shared document: both see both
// suggestions, the texts agree, and when one accepts a suggestion the other's list follows, down to none.
func TestSuggestionsFromTwoBrowsersConverge(t *testing.T) {
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
		return "/d/review1?hub=" + url.QueryEscape(srv.URL) + "&token=" + token + "&transport=ws&flow=" + url.QueryEscape("*") + "&name=" + name + "&color=" + url.QueryEscape(color)
	}
	a := site.Open(t, q("Ana", "owner", "#e11d48"))
	a.Wait("s.live.status === 'online' && s.live.role === 'owner'")
	a.Sleep(400)
	b := site.Open(t, q("Bo", "editor", "#2563eb"))
	b.Wait("s.live.status === 'online' && s.live.role === 'editor'")
	b.Wait("s.text.length > 100")
	original := a.State().Str("text")

	suggest := func(d *e2etest.Tab, words string) {
		d.ToggleMode()
		d.CanvasClick(240, 164)
		d.Focus("#input")
		d.Press("Home")
		d.Press("Control+Alt+s")
		d.WaitFor("suggesting", `document.querySelector('.gj-announcer').textContent.startsWith('Suggesting')`)
		d.Type(words)
	}
	suggest(a, "RED ")
	suggest(b, "BLUE ")
	for _, d := range []*e2etest.Tab{a, b} {
		d.Wait("s.text.includes('RED ') && s.text.includes('BLUE ')")
		d.Click(`button[data-tab="review"]`)
		d.WaitFor("both suggestions are listed", `document.querySelectorAll('.gj-proposal').length === 2`)
	}
	a.OK(a.State().Str("text") == b.State().Str("text"), "the two documents are the same")
	for _, d := range []*e2etest.Tab{a, b} {
		all := d.Text(".gj-proposals-list")
		d.OK(strings.Contains(all, "RED") && strings.Contains(all, "BLUE") && strings.Contains(all, "Ana") && strings.Contains(all, "Bo"), "each list names both people and both changes: "+all)
	}

	// Ana accepts the first one in her list; Bo's list follows, with the same one left
	left := func(d *e2etest.Tab) string { return d.Text(".gj-proposal-text") }
	a.Click(".gj-proposal:first-child .gj-proposal-accept")
	a.WaitFor("one is left for Ana", `document.querySelectorAll('.gj-proposal').length === 1`)
	b.WaitFor("one is left for Bo", `document.querySelectorAll('.gj-proposal').length === 1`)
	b.OK(left(a) == left(b), "both lists show the same suggestion: "+left(a)+" / "+left(b))

	// Bo rejects what is left; neither list has anything, and the text is the original plus the accepted words
	b.Click(".gj-proposal-reject")
	a.WaitFor("none left for Ana", `!document.querySelector('.gj-proposal')`)
	b.WaitFor("none left for Bo", `!document.querySelector('.gj-proposal')`)
	a.Wait("s.text.length === " + e2etest.J(len([]rune(original))+len("RED ")) + " || s.text.length === " + e2etest.J(len([]rune(original))+len("BLUE ")))
	b.Wait("s.text === " + e2etest.J(a.State().Str("text")))
	a.OK(strings.Count(a.State().Str("text"), "RED ")+strings.Count(a.State().Str("text"), "BLUE ") == 1, "exactly the accepted words are in the text")
	a.NoErrors()
	b.NoErrors()
}

// Bo is told when Ana mentions him in a comment: the announcer says so, the Comments tab lists it until he marks it
// as read, and Ana, who wrote it, has nothing to be told.
func TestAMentionedPersonIsToldAndCanMarkItRead(t *testing.T) {
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
		return "/d/mention1?hub=" + url.QueryEscape(srv.URL) + "&token=" + token + "&transport=ws&flow=" + url.QueryEscape("*") + "&name=" + name + "&color=" + url.QueryEscape(color)
	}
	a := site.Open(t, q("Ana", "owner", "#e11d48"))
	a.Wait("s.live.status === 'online' && s.live.role === 'owner'")
	a.Sleep(400)
	b := site.Open(t, q("Bo", "editor", "#2563eb"))
	b.Wait("s.live.status === 'online' && s.live.role === 'editor'")
	b.Wait("s.text.length > 100")

	a.ToggleMode()
	a.CanvasClick(240, 164)
	a.Focus("#input")
	a.Press("End")
	for range 7 {
		a.Focus("#input")
		a.Press("Shift+ArrowLeft")
	}
	a.Focus("#input")
	a.AnswerDialogs("@Bo can you check this?")
	a.Press("Control+Alt+n")
	a.Wait("s.comments === 1")

	b.Click(`button[data-tab="comments"]`)
	b.WaitSelector("#mentions")
	b.OK(strings.Contains(b.Text("#mention-0"), "Ana") && strings.Contains(b.Text("#mention-0"), "can you check this?"), "the mention is listed: "+b.Text("#mention-0"))
	b.WaitFor("the announcer says it", `document.querySelector('.gj-announcer').textContent.includes('Ana mentioned you')`)
	a.Click(`button[data-tab="comments"]`)
	a.OK(!a.Has("#mentions"), "the author has nothing to be told")
	b.Click("#mention-read-0")
	b.WaitFor("it is gone once read", `!document.getElementById('mentions')`)
	a.NoErrors()
	b.NoErrors()
}
