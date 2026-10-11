package ws_test

import (
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elaous/gojiberry/collab/client"
	"github.com/elaous/gojiberry/collab/hub"
	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/collab/ws"
	"github.com/elaous/gojiberry/crdt"
)

func ops(t *testing.T, n, size int) []crdt.Op {
	t.Helper()
	d := crdt.NewDoc("r1")
	for i := range n {
		d.Put(crdt.Block{ID: "side:" + string(rune('a'+i)), Kind: crdt.LWWKindPrefix + ":side", Content: strings.Repeat("x", size)})
	}
	return d.Drain()
}

// run sends batch to a hub whose request limit is limit and reports whether
// everything was accepted, and what the client heard on the way.
func run(t *testing.T, limit int64, batch []crdt.Op) (pending int, rejected bool) {
	t.Helper()
	h := hub.New(hub.Config{MaxBody: limit, SaveDelay: 10 * time.Millisecond, KeepAlive: time.Second})
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(func() { srv.Close(); _ = h.Close() })
	conn := ws.WebSockets().Dial(client.Options{BaseURL: srv.URL, Doc: "d", Replica: "r1", Retry: 10 * time.Millisecond, MaxRetry: 50 * time.Millisecond, OnError: func(e error) { t.Logf("conn error: %v", e) }})
	t.Cleanup(conn.Close)
	conn.Send(batch)
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case e := <-conn.Events():
			if e.Kind == client.Rejected {
				return conn.Pending(), true
			}
		case <-time.After(5 * time.Millisecond):
		}
		if conn.Pending() == 0 {
			// A refused operation leaves the queue before its Rejected event is handed over: look for
			// the event a moment longer before deciding that everything was accepted.
			settle := time.After(200 * time.Millisecond)
			for {
				select {
				case e := <-conn.Events():
					if e.Kind == client.Rejected {
						return 0, true
					}
				case <-settle:
					return 0, false
				}
			}
		}
	}
	return conn.Pending(), false
}

// Many operations that together exceed the hub's limit are sent as several
// frames instead of one the hub would refuse (and then be sent again forever).
func TestLargeBatchIsSplit(t *testing.T) {
	if pending, rejected := run(t, 300<<10, ops(t, 12, 100<<10)); pending != 0 || rejected {
		t.Fatalf("pending %d, rejected %v", pending, rejected)
	}
}

// One operation the hub will not take is reported as refused, not retried
// without end.
func TestOversizedOperationIsRefusedNotRetried(t *testing.T) {
	pending, rejected := run(t, 64<<10, ops(t, 1, 200<<10))
	if !rejected {
		t.Fatalf("expected a rejection (pending %d)", pending)
	}
}

// dial connects replica to a doc on srv and collects what it hears until the test ends.
type listener struct {
	conn   *ws.Conn
	events chan client.Event
}

func dialDoc(t *testing.T, srv *httptest.Server, replica string, opts client.Options) *listener {
	t.Helper()
	opts.BaseURL, opts.Doc, opts.Replica = srv.URL, "d", replica
	if opts.Retry == 0 {
		opts.Retry, opts.MaxRetry = 10*time.Millisecond, 50*time.Millisecond
	}
	conn := ws.Dial(opts)
	l := &listener{conn: conn, events: make(chan client.Event, 256)}
	go func() {
		for e := range conn.Events() {
			l.events <- e
		}
		close(l.events)
	}()
	t.Cleanup(conn.Close)
	return l
}

func (l *listener) wait(t *testing.T, what string, ok func(client.Event) bool) client.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e, open := <-l.events:
			if !open {
				t.Fatalf("closed while waiting for %s", what)
			}
			if ok(e) {
				return e
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func newHub(t *testing.T) *httptest.Server {
	t.Helper()
	h := hub.New(hub.Config{SaveDelay: 10 * time.Millisecond, KeepAlive: time.Second})
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(func() { srv.Close(); _ = h.Close() })
	return srv
}

// A replica says hello, goes online, and what one sends the other receives; a cursor update reaches
// the other too, a burst of them merged into the last.
func TestTwoReplicasExchangeOpsAndPresence(t *testing.T) {
	srv := newHub(t)
	a := dialDoc(t, srv, "ra", client.Options{PresenceInterval: 20 * time.Millisecond})
	b := dialDoc(t, srv, "rb", client.Options{PresenceInterval: 20 * time.Millisecond})
	a.wait(t, "a's hello", func(e client.Event) bool { return e.Kind == client.Hello })
	b.wait(t, "b's hello", func(e client.Event) bool { return e.Kind == client.Hello })
	if a.conn.Status() != client.Online {
		t.Fatalf("status %v", a.conn.Status())
	}

	a.conn.Send(ops(t, 1, 10))
	b.wait(t, "the op", func(e client.Event) bool { return e.Kind == client.Ops && len(e.Ops) > 0 })
	deadline := time.Now().Add(3 * time.Second)
	for a.conn.Pending() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.conn.Pending() != 0 {
		t.Fatalf("pending %d after the hub accepted it", a.conn.Pending())
	}

	for _, name := range []string{"first", "second", "third"} {
		a.conn.SetPresence(wire.Presence{Name: name, Has: true})
	}
	last := b.wait(t, "the last cursor", func(e client.Event) bool {
		return e.Kind == client.PresenceEvent && e.Presence.Replica == "ra" && e.Presence.Name == "third"
	})
	if !last.Presence.Has {
		t.Error("the presence fields travel")
	}
	a.conn.Send(nil) // nothing to send is nothing
}

func TestCloseIsFinalAndRepeatable(t *testing.T) {
	srv := newHub(t)
	a := dialDoc(t, srv, "ra", client.Options{})
	a.wait(t, "hello", func(e client.Event) bool { return e.Kind == client.Hello })
	a.conn.Close()
	a.conn.Close() // twice is fine
	if a.conn.Status() != client.Closed {
		t.Fatalf("status %v", a.conn.Status())
	}
	a.conn.Send(ops(t, 1, 5)) // sending to a closed connection must not block or panic
	a.conn.SetPresence(wire.Presence{Name: "late"})
}

// A bad address is an error told to OnError, and the connection goes on trying, backing off, rather than
// spinning or giving up.
func TestBadAddressIsReportedAndRetried(t *testing.T) {
	var mu sync.Mutex
	var errs []string
	conn := ws.Dial(client.Options{BaseURL: "ftp://nowhere", Doc: "d", Replica: "r", Retry: 5 * time.Millisecond, MaxRetry: 20 * time.Millisecond,
		OnError: func(err error) { mu.Lock(); errs = append(errs, err.Error()); mu.Unlock() }})
	go func() {
		for range conn.Events() {
		}
	}()
	defer conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(errs)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(errs) < 3 || !strings.Contains(errs[0], "BaseURL") {
		t.Fatalf("errors: %q", errs)
	}
	if conn.Status() == client.Online {
		t.Fatal("online with a bad address")
	}
}

// A server that is not there is an error each try, and the replica comes online once it is.
func TestConnectsWhenTheServerComesUp(t *testing.T) {
	h := hub.New(hub.Config{SaveDelay: 10 * time.Millisecond, KeepAlive: time.Second})
	defer func() { _ = h.Close() }()
	srv := httptest.NewUnstartedServer(h.Handler())
	addr := srv.Listener.Addr().String()
	_ = srv.Listener.Close() // nothing listens yet
	var failed atomic.Int32
	conn := ws.Dial(client.Options{BaseURL: "http://" + addr, Doc: "d", Replica: "r", Retry: 10 * time.Millisecond, MaxRetry: 40 * time.Millisecond,
		OnError: func(error) { failed.Add(1) }})
	events := make(chan client.Event, 64)
	go func() {
		for e := range conn.Events() {
			events <- e
		}
	}()
	defer conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for failed.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if failed.Load() < 2 {
		t.Fatal("no errors while the server was down")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("could not take the address again: %v", err)
	}
	srv.Listener = ln
	srv.Start()
	defer srv.Close()
	for {
		select {
		case e := <-events:
			if e.Kind == client.Hello {
				return
			}
		case <-time.After(5 * time.Second):
			t.Fatal("never came online")
		}
	}
}
