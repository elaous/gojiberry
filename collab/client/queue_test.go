package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/elaous/gojiberry/crdt"
)

type memQueue struct {
	mu  sync.Mutex
	ops []crdt.Op
}

func (q *memQueue) Load() []crdt.Op  { q.mu.Lock(); defer q.mu.Unlock(); return q.ops }
func (q *memQueue) Save(o []crdt.Op) { q.mu.Lock(); q.ops = o; q.mu.Unlock() }
func (q *memQueue) len() int         { q.mu.Lock(); defer q.mu.Unlock(); return len(q.ops) }

// Ops sent while the server is down are kept in the queue.
func TestQueueSurvivesARestart(t *testing.T) {
	var mu sync.Mutex
	var got []crdt.Op
	up := false
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
		if !up {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		var ops []crdt.Op
		_ = json.NewDecoder(r.Body).Decode(&ops)
		got = append(got, ops...)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	q := &memQueue{}
	opts := Options{BaseURL: srv.URL, Doc: "d", Replica: "r", Retry: 5 * time.Millisecond, MaxRetry: 10 * time.Millisecond, Queue: q}
	c := Dial(opts)
	c.Send([]crdt.Op{op("a", "1"), op("b", "2")})
	if q.len() != 2 {
		t.Fatalf("queue holds %d ops, want 2", q.len())
	}
	c.Close()
	if q.len() != 2 {
		t.Fatalf("closing lost the queue: %d", q.len())
	}

	mu.Lock()
	up = true
	mu.Unlock()
	c2 := Dial(opts)
	defer c2.Close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 2 && q.len() == 0 && c2.Pending() == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("not delivered: server got %d, queue %d, pending %d", len(got), q.len(), c2.Pending())
}
