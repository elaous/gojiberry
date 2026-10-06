package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elaous/gojiberry/crdt"
	"github.com/elaous/gojiberry/docstore"
)

type fakeRenewer struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (f *fakeRenewer) Renew(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, id)
	return f.err
}

func (f *fakeRenewer) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)} }
func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *clock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

// roleByHeader takes the role from X-Role, the user from X-User.
func roleByHeader(r *http.Request, _ string) Role { return Role(r.Header.Get("X-Role")) }

type edit struct {
	srv *httptest.Server
}

// post sends ops for document id as role; it returns the status.
func (e edit) post(t *testing.T, id, role, user, replica, body string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+"/docs/"+id+"/ops?replica="+replica, strings.NewReader(body))
	req.Header.Set("X-Role", role)
	req.Header.Set("X-User", user)
	req.Header.Set("Authorization", "Bearer tok-"+user)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func (e edit) get(t *testing.T, path, role string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
	req.Header.Set("X-Role", role)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		return 0 // the event stream never ends: that is fine
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func textOps(t *testing.T, replica, text string) string {
	t.Helper()
	d := charDoc(replica)
	d.Put(crdt.Block{ID: "doc", Content: text})
	b, _ := json.Marshal(d.Drain())
	return string(b)
}

func newRenewHub(t *testing.T, cfg Config) (*Hub, edit) {
	t.Helper()
	if cfg.Roles == nil {
		cfg.Roles = roleByHeader
	}
	if cfg.GoneConfirmDelay == 0 {
		cfg.GoneConfirmDelay = -1 // confirm at once; the default delay has a test of its own
	}
	h := New(cfg)
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)
	return h, edit{srv}
}

func TestRenewOnlyOnAcceptedEdits(t *testing.T) {
	fr := &fakeRenewer{}
	h, e := newRenewHub(t, Config{Renewer: fr, RenewInterval: -1, User: func(r *http.Request) string { return r.Header.Get("X-User") }})
	check := func(what string, want int) {
		t.Helper()
		_ = h.Flush()
		if got := len(fr.got()); got != want {
			t.Fatalf("%s: %d renewals, want %d", what, got, want)
		}
	}

	// Joining, watching, cursors, snapshots, role queries: never.
	e.get(t, "/docs/d/events?replica=r1", "editor")
	e.get(t, "/docs/d/snapshot", "editor")
	e.get(t, "/docs/d/role", "owner")
	req, _ := http.NewRequest("POST", e.srv.URL+"/docs/d/presence", strings.NewReader(`{"replica":"r1","name":"x"}`))
	req.Header.Set("X-Role", "editor")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 204 {
		t.Fatalf("presence: %v %v", resp, err)
	}
	check("join/view/cursor traffic", 0)

	// An empty batch is not an edit.
	if s := e.post(t, "d", "editor", "ana", "r1", "[]"); s != 204 {
		t.Fatalf("empty batch: %d", s)
	}
	check("an empty batch", 0)

	// Refused: no role, unreadable ops, a viewer's text edit, another user's replica id.
	if s := e.post(t, "d", "", "ana", "r1", textOps(t, "c1", "x")); s != 403 {
		t.Fatalf("no role: %d", s)
	}
	if s := e.post(t, "d", "editor", "ana", "r1", "{bad"); s != 400 {
		t.Fatalf("bad json: %d", s)
	}
	if s := e.post(t, "d", "viewer", "vic", "rv", textOps(t, "c2", "x")); s != 403 {
		t.Fatalf("viewer text edit: %d", s)
	}
	check("refused edits", 0)

	// A viewer's comment is accepted but is not an edit of the document.
	cd := charDoc("cm")
	cop := cd.Put(crdt.Block{ID: "comment:c1", Kind: "lww:side", Content: `{"author":"vic"}`})
	cb, _ := json.Marshal([]crdt.Op{cop})
	if s := e.post(t, "d", "viewer", "vic", "rv", string(cb)); s != 204 {
		t.Fatalf("viewer comment: %d", s)
	}
	check("a viewer's comment", 0)

	// Another user's replica id is refused after it is bound to ana.
	if s := e.post(t, "d", "editor", "ana", "r1", textOps(t, "c3", "hello")); s != 204 {
		t.Fatalf("editor edit: %d", s)
	}
	check("an editor's edit", 1)
	if s := e.post(t, "d", "editor", "bob", "r1", textOps(t, "c4", "stolen")); s != 403 {
		t.Fatalf("replica of another user: %d", s)
	}
	check("a refused replica", 1)

	// An owner's edit renews too, for the right document.
	if s := e.post(t, "other", "owner", "ana", "r9", textOps(t, "c5", "x")); s != 204 {
		t.Fatalf("owner edit: %d", s)
	}
	check("an owner's edit", 2)
	if got := fr.got(); got[0] != "d" || got[1] != "other" {
		t.Fatalf("renewed %v", got)
	}
}

func TestRenewIsRateLimitedPerDocument(t *testing.T) {
	fr := &fakeRenewer{}
	clk := newClock()
	h, e := newRenewHub(t, Config{Renewer: fr, Now: clk.Now}) // default: once a minute
	n := 0
	typeOnce := func(doc string) {
		t.Helper()
		n++
		if s := e.post(t, doc, "editor", "", "r", textOps(t, "c"+string(rune('a'+n)), "x")); s != 204 {
			t.Fatalf("edit: %d", s)
		}
		_ = h.Flush()
	}
	want := func(k int) {
		t.Helper()
		if got := len(fr.got()); got != k {
			t.Fatalf("%d renewals, want %d (at %v)", got, k, clk.Now())
		}
	}
	typeOnce("a")
	want(1) // the first edit renews at once
	for i := 0; i < 20; i++ {
		clk.Add(2 * time.Second)
		typeOnce("a")
	}
	want(1) // 40s of typing: still one
	typeOnce("b")
	want(2) // another document has its own interval
	clk.Add(20 * time.Second)
	typeOnce("a") // 60s after the first
	want(3)
	typeOnce("a")
	want(3)
	clk.Add(59 * time.Second)
	typeOnce("a")
	want(3)
	clk.Add(time.Second)
	typeOnce("a")
	want(4)
	clk.Add(-10 * time.Minute) // a clock that steps back must not silence renewals forever
	typeOnce("a")
	want(5)

	// A negative interval renews on every accepted edit; a custom one is honoured.
	fr2 := &fakeRenewer{}
	h2, e2 := newRenewHub(t, Config{Renewer: fr2, RenewInterval: 5 * time.Second, Now: clk.Now})
	for i := 0; i < 3; i++ {
		e2.post(t, "x", "owner", "", "r", textOps(t, "z"+string(rune('a'+i)), "x"))
		clk.Add(3 * time.Second)
	}
	_ = h2.Flush()
	if got := len(fr2.got()); got != 2 {
		t.Fatalf("5s interval, edits at 0,3,6s: %d renewals, want 2", got)
	}
}

func TestRenewFailureDoesNotFailTheEdit(t *testing.T) {
	fr := &fakeRenewer{err: docstore.ErrNotFound} // an expired document
	h, e := newRenewHub(t, Config{Renewer: fr, RenewInterval: -1})
	if s := e.post(t, "d", "editor", "", "r", textOps(t, "c", "x")); s != 204 {
		t.Fatalf("an edit of an expired document is still a hub edit: %d", s)
	}
	fr.mu.Lock()
	fr.err = errors.New("store down")
	fr.mu.Unlock()
	if s := e.post(t, "d", "editor", "", "r", textOps(t, "c2", "y")); s != 204 {
		t.Fatalf("a failing renewer failed the edit: %d", s)
	}
	_ = h.Flush()
	if len(fr.got()) != 2 {
		t.Fatalf("calls: %v", fr.got())
	}
}

func TestNoRenewerNoCalls(t *testing.T) {
	_, e := newRenewHub(t, Config{})
	if s := e.post(t, "d", "editor", "", "r", textOps(t, "c", "x")); s != 204 {
		t.Fatalf("%d", s)
	}
}

func TestRenewConcurrentEditsRace(t *testing.T) {
	fr := &fakeRenewer{}
	clk := newClock()
	h, e := newRenewHub(t, Config{Renewer: fr, Now: clk.Now})
	docs := []string{"p", "q", "r"}
	var wg sync.WaitGroup
	for w := 0; w < 12; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 15; i++ {
				doc := docs[(w+i)%len(docs)]
				d := charDoc("w" + string(rune('a'+w)))
				d.Put(crdt.Block{ID: "b" + string(rune('a'+i)), Content: "x"})
				b, _ := json.Marshal(d.Drain())
				e.post(t, doc, "editor", "", "rep"+string(rune('a'+w)), string(b))
				if i%5 == 0 {
					clk.Add(30 * time.Second)
				}
				if i%7 == 0 {
					_ = h.Flush()
				}
			}
		}(w)
	}
	wg.Wait()
	_ = h.Flush()
	got := fr.got()
	if len(got) < len(docs) {
		t.Fatalf("every document must renew at least once: %v", got)
	}
	// 12 workers advance the clock 3 times each by 30s: at most 18 minutes pass, so no
	// document can renew more than ~19 times.
	per := map[string]int{}
	for _, id := range got {
		per[id]++
	}
	for id, n := range per {
		if n > 19 {
			t.Fatalf("%s renewed %d times", id, n)
		}
	}
}
