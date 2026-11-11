package collab

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/collab/client"
	"github.com/elaous/gojiberry/collab/hub"
	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/collab/ws"
	"github.com/elaous/gojiberry/docstore"
)

// testTransport is what the rig's sessions connect over; TestOverWebSockets
// runs the same scenarios again with the WebSocket transport.
var testTransport Transport = HTTPEndpoints()

type rig struct {
	t   *testing.T
	hub *hub.Hub
	srv *httptest.Server

	mu    sync.Mutex
	conns map[net.Conn]bool
}

// track remembers the server's connections (ConnState sees hijacked ones too).
func (r *rig) track(c net.Conn, st http.ConnState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st == http.StateClosed {
		delete(r.conns, c)
	} else {
		r.conns[c] = true
	}
}

// cut closes every connection the server has, hijacked ones (WebSockets)
// included, as a network failure would.
func (r *rig) cut() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for c := range r.conns {
		_ = c.Close()
	}
	r.conns = map[net.Conn]bool{}
}

func newRig(t *testing.T, cfg hub.Config) *rig {
	t.Helper()
	if cfg.SaveDelay == 0 {
		cfg.SaveDelay = 10 * time.Millisecond
	}
	h := hub.New(cfg)
	r := &rig{t: t, hub: h, conns: map[net.Conn]bool{}}
	srv := httptest.NewUnstartedServer(h.Handler())
	srv.Config.ConnState = r.track
	srv.Start()
	r.srv = srv
	t.Cleanup(func() { srv.Close(); _ = h.Close() })
	return r
}

func (r *rig) connect(replica, role string, flow *canvas.TextFlow) *Session {
	r.t.Helper()
	hdr := http.Header{}
	if role != "" {
		hdr.Set("X-Role", role)
	}
	s, err := ConnectWith(testTransport, replica, flow, client.Options{BaseURL: r.srv.URL, Doc: "d1", Header: hdr, Retry: 10 * time.Millisecond, MaxRetry: 50 * time.Millisecond})
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(s.Close)
	return s
}

func byHeader(req *http.Request, _ string) hub.Role { return hub.Role(req.Header.Get("X-Role")) }

// waitFor polls the sessions until cond holds.
func waitFor(t *testing.T, what string, cond func() bool, sessions ...*Session) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, s := range sessions {
			s.Poll()
		}
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(3 * time.Millisecond)
	}
}

func TestTwoSessionsShareEdits(t *testing.T) {
	r := newRig(t, hub.Config{})
	a := r.connect("a", "", newFlow(canvas.Chars{Text: "hello world"}))
	waitFor(t, "a to be sharing", func() bool { return a.Bridge.Ready() && a.Role.CanEdit() }, a)
	b := r.connect("b", "", newFlow())
	waitFor(t, "b to receive the document", func() bool { return text(b.Bridge.Flow) == "hello world" }, a, b)

	click(a.Bridge.Flow, 5)
	a.Bridge.Flow.InsertText(",")
	waitFor(t, "b to see the comma", func() bool { return text(b.Bridge.Flow) == "hello, world" }, a, b)

	b.Bridge.Flow.SetCursors([]canvas.Cursor{{Anchor: 7, Caret: 12}})
	b.Bridge.Flow.ApplyFormat(func(f *canvas.Format) { f.FontWeight = 700 })
	waitFor(t, "a to see the bold", func() bool { return normalized(a.Bridge.Flow) == normalized(b.Bridge.Flow) }, a, b)
	if a.Status != client.Online {
		t.Fatalf("status %v", a.Status)
	}
	// Both type at once; both end the same.
	click(a.Bridge.Flow, 0)
	a.Bridge.Flow.InsertText(">")
	click(b.Bridge.Flow, 13)
	b.Bridge.Flow.InsertText("!")
	waitFor(t, "convergence", func() bool {
		return text(a.Bridge.Flow) == ">hello, world!" && normalized(a.Bridge.Flow) == normalized(b.Bridge.Flow)
	}, a, b)
}

func TestPresenceAndPeerCursors(t *testing.T) {
	r := newRig(t, hub.Config{})
	a := r.connect("a", "", newFlow(canvas.Chars{Text: "hello world"}))
	waitFor(t, "a ready", func() bool { return a.Bridge.Ready() }, a)
	b := r.connect("b", "", newFlow())
	waitFor(t, "b has the text", func() bool { return text(b.Bridge.Flow) == "hello world" }, a, b)
	a.Name, a.Color = "Ana", "#f00"
	a.Bridge.Flow.SetCursors([]canvas.Cursor{{Anchor: 6, Caret: 11}})
	a.PublishCursor()
	waitFor(t, "b to see a's cursor", func() bool { return len(b.PeerCursors()) == 1 }, a, b)
	pc := b.PeerCursors()[0]
	if pc.Presence.Name != "Ana" || pc.Presence.Color != "#f00" || pc.Anchor != 6 || pc.Caret != 11 || pc.Presence.Role != wire.Owner {
		t.Fatalf("%+v", pc)
	}
	if rc := b.Bridge.Flow.RemoteCursors; len(rc) != 1 || rc[0].Name != "Ana" || rc[0].Color != "#f00" || rc[0].Anchor != 6 || rc[0].Caret != 11 {
		t.Fatalf("the flow draws the peer: %+v", rc)
	}
	if b.Pending() != 0 {
		t.Fatal("nothing waiting")
	}
	click(b.Bridge.Flow, 0)
	b.Bridge.Flow.InsertText(">>")
	waitFor(t, "the peer cursor to move", func() bool {
		pcs := b.PeerCursors()
		return len(pcs) == 1 && pcs[0].Anchor == 8 && pcs[0].Caret == 13
	}, a, b)
	a.Bridge.Flow.SetCursors([]canvas.Cursor{canvas.At(0)})
	a.PublishCursor()
	waitFor(t, "a cursor at the start", func() bool {
		pcs := b.PeerCursors()
		return len(pcs) == 1 && pcs[0].Caret == 0
	}, a, b)
	a.Close()
	waitFor(t, "a to leave", func() bool { return len(b.Peers) == 0 }, b)
	if len(b.Bridge.Flow.RemoteCursors) != 0 {
		t.Fatal("a left, so its cursor goes")
	}
	// Not ready: nothing to publish.
	idle, _ := Join("z", newFlow())
	(&Session{Bridge: idle}).PublishCursor()
}

func TestRolesAreEnforced(t *testing.T) {
	r := newRig(t, hub.Config{Roles: byHeader})
	owner := r.connect("o", "owner", newFlow(canvas.Chars{Text: "the text"}))
	waitFor(t, "owner ready", func() bool { return owner.Bridge.Ready() }, owner)
	viewer := r.connect("v", "viewer", newFlow())
	waitFor(t, "viewer has the text", func() bool { return text(viewer.Bridge.Flow) == "the text" && viewer.Role == wire.Viewer }, owner, viewer)

	click(viewer.Bridge.Flow, 3)
	viewer.Bridge.Flow.InsertText("X")
	waitFor(t, "the viewer's session to repair itself", func() bool {
		return viewer.Repairs > 0 && viewer.Bridge.Ready() && text(viewer.Bridge.Flow) == "the text"
	}, owner, viewer)
	if viewer.Err != nil || viewer.Conn.Pending() != 0 {
		t.Fatalf("a repaired session starts clean: err %v pending %d", viewer.Err, viewer.Conn.Pending())
	}
	time.Sleep(30 * time.Millisecond)
	owner.Poll()
	if text(owner.Bridge.Flow) != "the text" {
		t.Fatalf("a viewer changed the text: %q", text(owner.Bridge.Flow))
	}

	v2 := r.connect("v2", "viewer", newFlow())
	waitFor(t, "v2 has the text", func() bool { return text(v2.Bridge.Flow) == "the text" }, owner, v2)
	v2.Bridge.Flow.SetCursors([]canvas.Cursor{{Anchor: 0, Caret: 3}})
	c := v2.Bridge.Flow.CommentSet().Add(canvas.Comment{Author: "vee", Text: "a viewer's note"})
	v2.Bridge.Flow.ApplyFormat(func(f *canvas.Format) { f.Comment = c.ID })
	waitFor(t, "the owner to see the viewer's comment", func() bool {
		got, ok := owner.Bridge.Flow.Comments.Get(c.ID)
		return ok && got.Text == "a viewer's note"
	}, owner, v2)
	if v2.Err != nil {
		t.Fatalf("commenting was refused: %v", v2.Err)
	}
	if owner.Bridge.Flow.CommentExcerpts()[c.ID] != "the" {
		t.Fatalf("the comment is on its text: %v", owner.Bridge.Flow.CommentExcerpts())
	}

	// No role at all: the stream never opens.
	var errs []error
	var mu sync.Mutex
	f := newFlow()
	s, _ := Connect("n", f, client.Options{BaseURL: r.srv.URL, Doc: "d1", Retry: 10 * time.Millisecond, MaxRetry: 20 * time.Millisecond,
		OnError: func(err error) { mu.Lock(); errs = append(errs, err); mu.Unlock() }})
	defer s.Close()
	waitFor(t, "a refused connection to be reported", func() bool { mu.Lock(); defer mu.Unlock(); return len(errs) > 0 }, s)
	if s.Role != "" || s.Bridge.Ready() || s.Status == client.Online {
		t.Fatalf("no access: role %q ready %v status %v", s.Role, s.Bridge.Ready(), s.Status)
	}
}

func TestDocumentsPersistAndReload(t *testing.T) {
	store := docstore.NewMemoryAdapter()
	r := newRig(t, hub.Config{Store: store})
	a := r.connect("a", "", newFlow(canvas.Chars{Text: "saved text", Format: canvas.Format{FontWeight: 700}}))
	waitFor(t, "a ready", func() bool { return a.Bridge.Ready() }, a)
	click(a.Bridge.Flow, 10)
	a.Bridge.Flow.InsertText("!")
	waitFor(t, "the document to be saved", func() bool {
		_, err := store.Load(t.Context(), hub.StoreKey("d1"))
		return err == nil && r.hub.Flush() == nil
	}, a)
	time.Sleep(50 * time.Millisecond)
	if err := r.hub.Flush(); err != nil {
		t.Fatal(err)
	}

	// A new hub on the same store serves the same document.
	r2 := newRig(t, hub.Config{Store: store})
	b := r2.connect("b", "", newFlow())
	waitFor(t, "b to get the saved document", func() bool { return text(b.Bridge.Flow) == "saved text!" }, b)
	if len(b.Bridge.Flow.ContentItems()) == 0 || b.Bridge.Flow.ContentItems()[0].(canvas.Chars).Format.FontWeight != 700 {
		t.Fatalf("formatting survived: %#v", b.Bridge.Flow.ContentItems())
	}
}

func TestReconnectResyncsAndSendsWhatWaited(t *testing.T) {
	r := newRig(t, hub.Config{})
	a := r.connect("a", "", newFlow(canvas.Chars{Text: "one two"}))
	waitFor(t, "a ready", func() bool { return a.Bridge.Ready() }, a)
	b := r.connect("b", "", newFlow())
	waitFor(t, "b has it", func() bool { return text(b.Bridge.Flow) == "one two" }, a, b)

	r.cut() // cut every connection
	click(a.Bridge.Flow, 7)
	a.Bridge.Flow.InsertText("!")
	click(b.Bridge.Flow, 0)
	b.Bridge.Flow.InsertText(">")
	waitFor(t, "both to reconnect and converge", func() bool {
		return text(a.Bridge.Flow) == ">one two!" && normalized(a.Bridge.Flow) == normalized(b.Bridge.Flow)
	}, a, b)
	waitFor(t, "nothing left waiting", func() bool { return a.Conn.Pending() == 0 }, a, b)
}

func TestConnectRefusesUnsupportedContent(t *testing.T) {
	odd := newFlow(canvas.Chars{Text: "x"})
	odd.Items = append(odd.Items, canvas.Inline{Object: oddObject{}})
	if _, err := Connect("a", odd, client.Options{BaseURL: "http://127.0.0.1:1", Doc: "d"}); err == nil {
		t.Fatal("unshareable content")
	}
}

func TestClientResendsWhatARestartedHubLost(t *testing.T) {
	var cur atomic.Pointer[hub.Hub]
	cur.Store(hub.New(hub.Config{}))
	tr := &rig{t: t, conns: map[net.Conn]bool{}}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cur.Load().Handler().ServeHTTP(w, r) }))
	srv.Config.ConnState = tr.track
	srv.Start()
	t.Cleanup(srv.Close)
	connect := func(replica string, flow *canvas.TextFlow) *Session {
		s, err := ConnectWith(testTransport, replica, flow, client.Options{BaseURL: srv.URL, Doc: "d1", Retry: 10 * time.Millisecond, MaxRetry: 50 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		return s
	}
	a := connect("a", newFlow(canvas.Chars{Text: "one two"}))
	waitFor(t, "a ready", func() bool { return a.Bridge.Ready() }, a)
	click(a.Bridge.Flow, 7)
	a.Bridge.Flow.InsertText("!")
	waitFor(t, "the edit to be acknowledged", func() bool { return a.Conn.Pending() == 0 }, a)

	cur.Store(hub.New(hub.Config{})) // the hub restarts with nothing
	tr.cut()
	b := connect("b", newFlow())
	waitFor(t, "b to get the document from the reconnected a", func() bool { return text(b.Bridge.Flow) == "one two!" }, a, b)
}

func TestNoRepairLeavesTheErrorForTheCaller(t *testing.T) {
	r := newRig(t, hub.Config{Roles: byHeader})
	owner := r.connect("o", "owner", newFlow(canvas.Chars{Text: "the text"}))
	waitFor(t, "owner ready", func() bool { return owner.Bridge.Ready() }, owner)
	viewer := r.connect("v", "viewer", newFlow())
	viewer.NoRepair = true
	waitFor(t, "viewer has the text", func() bool { return text(viewer.Bridge.Flow) == "the text" && viewer.Role == wire.Viewer }, owner, viewer)
	click(viewer.Bridge.Flow, 3)
	viewer.Bridge.Flow.InsertText("X")
	waitFor(t, "the rejection", func() bool { return viewer.Err != nil }, owner, viewer)
	if viewer.Repairs != 0 || text(viewer.Bridge.Flow) != "theX text" {
		t.Fatalf("NoRepair must leave the session as it is: repairs %d text %q", viewer.Repairs, text(viewer.Bridge.Flow))
	}
	if err := viewer.Repair(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the manual repair", func() bool { return viewer.Bridge.Ready() && text(viewer.Bridge.Flow) == "the text" }, owner, viewer)
}

// TestOverWebSockets runs the session scenarios over WebSockets.
func TestOverWebSockets(t *testing.T) {
	testTransport = ws.WebSockets()
	defer func() { testTransport = HTTPEndpoints() }()
	for name, fn := range map[string]func(*testing.T){
		"share edits":          TestTwoSessionsShareEdits,
		"presence":             TestPresenceAndPeerCursors,
		"roles and repair":     TestRolesAreEnforced,
		"no repair":            TestNoRepairLeavesTheErrorForTheCaller,
		"persist and reload":   TestDocumentsPersistAndReload,
		"reconnect and resend": TestReconnectResyncsAndSendsWhatWaited,
		"hub restart resend":   TestClientResendsWhatARestartedHubLost,
		"merged and rejected":  TestSessionCountsMergedAndRejectedChanges,
	} {
		t.Run(name, fn)
	}
}
