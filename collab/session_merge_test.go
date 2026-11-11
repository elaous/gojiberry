package collab

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/collab/client"
	"github.com/elaous/gojiberry/collab/hub"
	"github.com/elaous/gojiberry/collab/wire"
)

func TestSessionCountsMergedAndRejectedChanges(t *testing.T) {
	var gate atomic.Bool // true: the network is down
	h := hub.New(hub.Config{SaveDelay: 10 * time.Millisecond, Roles: byHeader})
	rg := &rig{t: t, conns: map[net.Conn]bool{}}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gate.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		h.Handler().ServeHTTP(w, r)
	}))
	srv.Config.ConnState = rg.track
	srv.Start()
	t.Cleanup(func() { srv.Close(); _ = h.Close() })
	connect := func(replica, role string, flow *canvas.TextFlow) *Session {
		hdr := http.Header{}
		hdr.Set("X-Role", role)
		s, err := ConnectWith(testTransport, replica, flow, client.Options{BaseURL: srv.URL, Doc: "d1", Header: hdr, Retry: 10 * time.Millisecond, MaxRetry: 50 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		return s
	}
	a := connect("a", "owner", newFlow(canvas.Chars{Text: "one two"}))
	waitFor(t, "a ready", func() bool { return a.Bridge.Ready() }, a)
	b := connect("b", "owner", newFlow())
	waitFor(t, "b has it", func() bool { return text(b.Bridge.Flow) == "one two" }, a, b)
	if a.Merged != 0 || b.Merged != 0 {
		t.Fatalf("nothing waited at the first connection: %d %d", a.Merged, b.Merged)
	}

	gate.Store(true)
	rg.cut()
	waitFor(t, "both to notice", func() bool { return a.Status != client.Online && b.Status != client.Online }, a, b)
	click(a.Bridge.Flow, 7)
	a.Bridge.Flow.InsertText("!")
	click(b.Bridge.Flow, 0)
	b.Bridge.Flow.InsertText(">")
	waiting := a.Pending()
	if waiting == 0 {
		t.Fatal("the offline change must wait")
	}
	gate.Store(false)
	waitFor(t, "the reconnection and the merge", func() bool {
		return text(a.Bridge.Flow) == ">one two!" && normalized(a.Bridge.Flow) == normalized(b.Bridge.Flow) && a.Merged > 0 && b.Merged > 0
	}, a, b)
	if a.Merged != waiting {
		t.Fatalf("a merged %d changes, %d had waited", a.Merged, waiting)
	}
	before := a.Merged
	click(a.Bridge.Flow, 0)
	a.Bridge.Flow.InsertText("?")
	waitFor(t, "an online edit to arrive", func() bool { return text(b.Bridge.Flow) == "?>one two!" }, a, b)
	if a.Merged != before {
		t.Fatalf("an edit made online is not a merge: %d", a.Merged)
	}

	v := connect("v", "viewer", newFlow())
	v.NoRepair = true
	waitFor(t, "viewer has the text", func() bool { return text(v.Bridge.Flow) == "?>one two!" && v.Role == wire.Viewer }, a, v)
	click(v.Bridge.Flow, 3)
	v.Bridge.Flow.InsertText("X")
	waitFor(t, "the rejection", func() bool { return v.Err != nil }, a, v)
	if v.Rejected != 1 || v.Merged != 0 {
		t.Fatalf("rejected %d merged %d", v.Rejected, v.Merged)
	}
	if err := v.Repair(); err != nil {
		t.Fatal(err)
	}
	if v.Rejected != 1 {
		t.Fatalf("a repair keeps the count: %d", v.Rejected)
	}
}

func TestSessionMergedCountsWhatWaitedAtHello(t *testing.T) {
	b, err := Join("a", newFlow())
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{Bridge: b, Peers: map[string]wire.Presence{}}
	s.handle(client.Event{Kind: client.Hello, Role: wire.Owner, Waiting: 4})
	s.handle(client.Event{Kind: client.Snapshot})
	if s.Merged != 4 {
		t.Fatalf("%d", s.Merged)
	}
	s.handle(client.Event{Kind: client.Snapshot}) // the next snapshot has nothing waiting behind it
	if s.Merged != 4 {
		t.Fatalf("%d", s.Merged)
	}
	s.handle(client.Event{Kind: client.Hello, Role: wire.Viewer, Waiting: 2}) // a viewer's changes are refused, not merged
	s.handle(client.Event{Kind: client.Snapshot})
	if s.Merged != 4 {
		t.Fatalf("%d", s.Merged)
	}
}
