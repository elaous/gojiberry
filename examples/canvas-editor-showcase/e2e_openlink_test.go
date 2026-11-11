//go:build !(js && wasm)

package main

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/internal/e2etest"
)

type headerLog struct {
	next http.Handler
	mu   sync.Mutex
	seen []string
}

func (h *headerLog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.seen = append(h.seen, r.Header.Get(docstore.ShareHeader))
	h.mu.Unlock()
	h.next.ServeHTTP(w, r)
}

func (h *headerLog) count(token string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, s := range h.seen {
		if s == token {
			n++
		}
	}
	return n
}

func (h *headerLog) reset() { h.mu.Lock(); h.seen = nil; h.mu.Unlock() }

func TestOpenAShareLinkFromTheAddress(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	log := &headerLog{next: docstore.NewServer(docstore.NewMemoryAdapter(), "memory").Handler()}
	site.Handle("/docs", log)
	const token = "tok-0123456789abcdefghijklmnopqrstuvwxyz-ABCDEFG"
	base := "/index.html?docstore=" + url.QueryEscape(site.URL)

	d := site.OpenURL(t, base+"#share="+token)
	d.WaitSelector("#incoming")
	d.OK(d.Attr(`button[data-tab="people"]`, "aria-selected") == "true", "the People tab opens on a link")
	d.OK(!strings.Contains(d.Str(`location.href`), token) && d.Str(`location.hash`) == "", "the secret is taken out of the address: "+d.Str(`location.href`))
	d.OK(d.Text("#incoming-heading") == "This address has a share link" && strings.Contains(d.Text("#incoming-text"), "Nothing happens until you choose"), "the offer asks: "+d.Text("#incoming-text"))
	d.OK(log.count(token) == 0, "nothing was sent with the link before the choice")
	d.Click("#incoming-use")
	d.WaitSelector("#incoming-note")
	d.OK(strings.Contains(d.Text("#incoming-note"), "no server"), "without a server it says so: "+d.Text("#incoming-note"))
	d.OK(!d.Has("#incoming-use"), "the offer is spent")

	d.Click("#dir-offer-review")
	confirmDialog(d)
	d.Eval(`location.assign(location.pathname + location.search + '&again=1#share=`+token+`')`, nil)
	d.WaitSelector("#incoming-ignore")
	d.Click("#incoming-ignore")
	d.WaitFor("the offer to go", `!document.getElementById('incoming')`)
	d.OK(log.count(token) == 0, "an ignored link is never sent")

	d.Eval(`location.assign(location.pathname + location.search + '&again=2#share=`+token+`')`, nil)
	d.WaitSelector("#incoming-use")
	log.reset()
	d.Click("#incoming-use")
	d.WaitSelector("#incoming-stop")
	for i := 0; i < 50 && log.count(token) == 0; i++ {
		d.Sleep(100)
	}
	d.OK(log.count(token) > 0, "the server target sends the link once it is used")
	d.Click("#incoming-stop")
	d.WaitFor("the notice to go", `!document.getElementById('incoming-stop')`)
	d.Sleep(700)
	log.reset()
	d.Click(`button[data-tab="save"]`)
	d.Sleep(500)
	d.Click(`button[data-tab="people"]`)
	d.OK(log.count(token) == 0, "after stopping, the link is not sent")

	for _, v := range d.Axe("#links", axeDisabled...) {
		t.Errorf("the links section: %s", v)
	}
	d.NoErrors()
}

func TestAPersonsAccessEndsInThePeopleTab(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.OnNewDocument(skewClock)
	d.Reload()
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#person-life")
	d.Select("#person-life", "1d")
	d.Fill(".gj-people-invite-id", "ana@example.com")
	d.WaitFor("the invite button", `!document.querySelector('.gj-people-invite').disabled`)
	d.Click(".gj-people-invite")
	d.WaitFor("ana in the list", `!!document.querySelector('#who-list li[data-kind="person"]')`)
	text := d.Text(`#who-list li[data-kind="person"]`)
	d.OK(strings.Contains(text, "ana@example.com: Can edit, until"), "the end is written: "+text)
	d.OK(d.Bool(`(() => { const ms = Date.parse(document.querySelector('#who-list li[data-kind="person"]').dataset.expires) - Date.now(); return ms > 23.9 * 3600e3 && ms <= 24 * 3600e3; })()`), "access ends in a day")

	d.Select("#person-life", "never")
	d.Fill(".gj-people-invite-id", "bo@example.com")
	d.WaitFor("the invite button", `!document.querySelector('.gj-people-invite').disabled`)
	d.Click(".gj-people-invite")
	d.WaitFor("two people", `document.querySelectorAll('#who-list li[data-kind="person"]').length === 2`)

	d.Eval(`window.__skew = 2 * 24 * 3600 * 1000`, nil)
	d.Click(`button[data-tab="save"]`)
	d.Click(`button[data-tab="people"]`)
	d.WaitFor("ana to be gone", `document.querySelectorAll('#who-list li[data-kind="person"]').length === 1`)
	d.OK(strings.Contains(d.Text(`#who-list li[data-kind="person"]`), "bo@example.com") && !strings.Contains(d.Text("#who-list"), "ana@"), "only the person without an end is left")
	d.NoErrors()
}
