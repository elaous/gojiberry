package ws_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/collab/client"
	"github.com/elaous/gojiberry/collab/hub"
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
			return 0, false
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
