package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/crdt"
)

func next(t *testing.T, c *Conn, kind Kind) Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-c.Events():
			if !ok {
				t.Fatal("events closed")
			}
			if e.Kind == kind {
				return e
			}
		case <-timeout:
			t.Fatalf("no event of kind %d", kind)
		}
	}
}

func op(id string, content string) crdt.Op {
	d := crdt.NewDocWithStrategy("c", crdt.MergeCharCRDT)
	d.Put(crdt.Block{ID: id, Content: content})
	return d.Drain()[0]
}

func TestStreamParsingAndStatus(t *testing.T) {
	ops, _ := json.Marshal([]crdt.Op{op("doc", "hi")})
	mux := http.NewServeMux()
	var gotHeader, gotReplica string
	mux.HandleFunc("GET /docs/d/events", func(w http.ResponseWriter, r *http.Request) {
		gotHeader, gotReplica = r.Header.Get("Authorization"), r.URL.Query().Get("replica")
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		_, _ = fmt.Fprint(w, ": a comment\n\nevent: hello\ndata: {\"role\":\"editor\"}\n\n")
		_, _ = fmt.Fprint(w, "event: snapshot\ndata: "+string(ops)+"\n\n")
		_, _ = fmt.Fprint(w, "event: ops\ndata: [\ndata: ]\n\n") // data on two lines
		_, _ = fmt.Fprint(w, "event: presence\ndata: {\"replica\":\"p\",\"name\":\"Pat\"}\n\n")
		_, _ = fmt.Fprint(w, "event: presence\ndata: not json\n\n") // ignored
		_, _ = fmt.Fprint(w, "event: mystery\ndata: x\n\n")         // ignored
		_, _ = fmt.Fprint(w, "event: snapshot\ndata: not json\n\n") // ignored
		fl.Flush()
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := Dial(Options{BaseURL: srv.URL + "/", Doc: "d", Replica: "r1", Header: http.Header{"Authorization": {"Bearer tok"}}})
	defer c.Close()
	if e := next(t, c, Hello); e.Role != wire.Editor {
		t.Fatalf("%+v", e)
	}
	if c.Status() != Online {
		t.Fatalf("status %v", c.Status())
	}
	if e := next(t, c, Snapshot); len(e.Ops) != 1 || e.Ops[0].Block.ID != "doc" {
		t.Fatalf("%+v", e)
	}
	if e := next(t, c, Ops); len(e.Ops) != 0 {
		t.Fatalf("multi-line data joins: %+v", e)
	}
	if e := next(t, c, PresenceEvent); e.Presence.Name != "Pat" {
		t.Fatalf("%+v", e)
	}
	if gotHeader != "Bearer tok" || gotReplica != "r1" {
		t.Fatalf("header %q replica %q", gotHeader, gotReplica)
	}
	for i, want := range []string{"connecting", "online", "offline", "closed"} {
		if Status(i).String() != want {
			t.Fatal(Status(i).String())
		}
	}
	c.Close()
	c.Close() // idempotent
	if c.Status() != Closed {
		t.Fatal("closed")
	}
}

func TestSendRetriesAndReportsRejections(t *testing.T) {
	var mu sync.Mutex
	var posted [][]crdt.Op
	fails := 2
	mode := "ok"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /docs/d/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: hello\ndata: {\"role\":\"editor\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("POST /docs/d/ops", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if fails > 0 {
			fails--
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		switch mode {
		case "deny":
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":"read only"}`)
			return
		case "plain":
			http.Error(w, "plain text refusal", http.StatusForbidden)
			return
		case "empty":
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var ops []crdt.Op
		_ = json.NewDecoder(r.Body).Decode(&ops)
		posted = append(posted, ops)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /docs/d/presence", func(w http.ResponseWriter, r *http.Request) {
		var p wire.Presence
		_ = json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		posted = append(posted, []crdt.Op{{Block: crdt.Block{ID: "presence:" + p.Replica + ":" + p.Name}}})
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	var errs int
	var emu sync.Mutex
	c := Dial(Options{BaseURL: srv.URL, Doc: "d", Replica: "r", Retry: 5 * time.Millisecond, MaxRetry: 10 * time.Millisecond,
		OnError: func(error) { emu.Lock(); errs++; emu.Unlock() }})
	defer c.Close()
	next(t, c, Hello)
	c.Send(nil) // nothing to send
	c.Send([]crdt.Op{op("a", "1")})
	c.Send([]crdt.Op{op("b", "2")})
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := 0
		for _, b := range posted {
			n += len(b)
		}
		mu.Unlock()
		if n >= 2 && c.Pending() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not delivered: pending %d", c.Pending())
		}
		time.Sleep(2 * time.Millisecond)
	}
	emu.Lock()
	if errs < 2 {
		t.Fatalf("server errors were reported and retried: %d", errs)
	}
	emu.Unlock()

	for _, m := range []struct{ mode, want string }{{"deny", "read only"}, {"plain", "plain text refusal"}, {"empty", "403"}} {
		mu.Lock()
		mode = m.mode
		mu.Unlock()
		c.Send([]crdt.Op{op("x", "y")})
		e := next(t, c, Rejected)
		if !strings.Contains(e.Err.Error(), m.want) || len(e.Ops) != 1 || c.Pending() != 0 {
			t.Fatalf("%s: %v %d %d", m.mode, e.Err, len(e.Ops), c.Pending())
		}
	}
	c.SetPresence(wire.Presence{Name: "Me", Has: true})
	deadline = time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		found := false
		for _, b := range posted {
			if b[0].Block.ID == "presence:r:Me" {
				found = true
			}
		}
		mu.Unlock()
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("presence not posted with this replica's id")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestOfflineThenOnlineAndErrors(t *testing.T) {
	var mu sync.Mutex
	up := false
	var opened int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /docs/d/events", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ok := up
		if ok {
			opened++
		}
		mu.Unlock()
		if !ok {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: hello\ndata: {\"role\":\"viewer\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	var errs []error
	var emu sync.Mutex
	c := Dial(Options{BaseURL: srv.URL, Doc: "d", Replica: "r", Retry: 5 * time.Millisecond, MaxRetry: 10 * time.Millisecond,
		OnError: func(err error) { emu.Lock(); errs = append(errs, err); emu.Unlock() }})
	defer c.Close()
	if e := next(t, c, StatusEvent); e.Status != Connecting && e.Status != Offline {
		t.Fatalf("%v", e.Status)
	}
	for c.Status() != Offline {
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	up = true
	mu.Unlock()
	if e := next(t, c, Hello); e.Role != wire.Viewer {
		t.Fatal(e.Role)
	}
	emu.Lock()
	defer emu.Unlock()
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "503") {
		t.Fatalf("the refusal was reported: %v", errs)
	}
}

func TestBadURLAndSlowReader(t *testing.T) {
	c := Dial(Options{BaseURL: "http://127.0.0.1:1", Doc: "d", Replica: "r", Retry: 2 * time.Millisecond, MaxRetry: 4 * time.Millisecond, Client: &http.Client{Timeout: time.Second}})
	time.Sleep(60 * time.Millisecond)
	var statuses int
	timeout := time.After(time.Second)
loop:
	for {
		select {
		case e := <-c.Events():
			if e.Kind == StatusEvent {
				statuses++
			}
			if statuses >= 2 {
				break loop
			}
		case <-timeout:
			t.Fatalf("only %d status changes", statuses)
		}
	}
	c.Close()
	if _, open := <-c.Events(); open {
		for range c.Events() {
		}
	}
	bad := Dial(Options{BaseURL: "http://[::1", Doc: "d", Replica: "r", Retry: 2 * time.Millisecond})
	time.Sleep(20 * time.Millisecond)
	bad.Send([]crdt.Op{op("a", "b")})
	bad.SetPresence(wire.Presence{})
	bad.Close()
}

func TestSetPresenceIsThrottledAndTheLatestIsSent(t *testing.T) {
	var mu sync.Mutex
	var names []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/presence") {
			var p wire.Presence
			if json.NewDecoder(r.Body).Decode(&p) == nil {
				mu.Lock()
				names = append(names, p.Name)
				mu.Unlock()
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "no stream here", http.StatusNotFound)
	}))
	defer srv.Close()
	c := Dial(Options{BaseURL: srv.URL, Doc: "d", Replica: "r", PresenceInterval: 60 * time.Millisecond, Retry: time.Second})
	defer c.Close()
	for i := 0; i < 30; i++ {
		c.SetPresence(wire.Presence{Name: "n" + strconv.Itoa(i)})
		time.Sleep(4 * time.Millisecond)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := append([]string(nil), names...)
		mu.Unlock()
		if len(got) > 0 && got[len(got)-1] == "n29" {
			if len(got) > 6 {
				t.Fatalf("30 updates in ~120ms became %d requests: %v", len(got), got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the latest update was never sent: %v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
