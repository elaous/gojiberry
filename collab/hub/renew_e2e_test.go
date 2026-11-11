//go:build !(js && wasm)

package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/crdt"
	"github.com/elaous/gojiberry/docstore"
)

const day = 24 * time.Hour

func newExpiringStore(clk *clock) (*docstore.ExpiringAdapter, docstore.Adapter) {
	inner := docstore.NewMemoryAdapter()
	ea := docstore.NewExpiringAdapter(inner, docstore.FixedTTL(day))
	ea.Now = clk.Now
	return ea, inner
}

func TestEndToEndHubEditsKeepADocumentAlive(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	ea, inner := newExpiringStore(clk)
	h, e := newRenewHub(t, Config{Store: ea, Renewer: ea, Now: clk.Now})

	if err := ea.Save(ctx, "doc", []byte("saved directly at t=0")); err != nil {
		t.Fatal(err)
	}
	clk.Add(23 * time.Hour)
	if s := e.post(t, "doc", "editor", "", "r1", textOps(t, "c1", "typed through the hub at t=23h")); s != 204 {
		t.Fatalf("hub edit: %d", s)
	}
	_ = h.Flush()

	if _, ok, _ := ea.Expiry(ctx, "collab:doc"); ok {
		t.Fatal("collab:doc got a deadline")
	}
	if got, ok, _ := ea.Expiry(ctx, "doc"); !ok || !got.Equal(clk.Now().Add(day)) {
		t.Fatalf("deadline after the edit = %v %v, want t=23h + 24h", got, ok)
	}

	clk.Add(2 * time.Hour) // t = 25h, past the original deadline
	if n, err := ea.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("Sweep at 25h = %d, %v: the document is being edited", n, err)
	}
	if d, err := ea.Load(ctx, "doc"); err != nil || string(d.Data) != "saved directly at t=0" {
		t.Fatalf("document at 25h: %q %v", d.Data, err)
	}
	if _, err := inner.Load(ctx, StoreKey("doc")); err != nil {
		t.Fatalf("hub state at 25h: %v", err)
	}

	clk.Add(23 * time.Hour) // t = 48h, no further edits
	if _, err := ea.Load(ctx, "doc"); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("document at 48h = %v, want gone", err)
	}
	if n, err := ea.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("Sweep at 48h = %d, %v", n, err)
	}
	for _, k := range []string{"doc", StoreKey("doc"), "expiry:doc"} {
		if _, err := inner.Load(ctx, k); !errors.Is(err, docstore.ErrNotFound) {
			t.Fatalf("%s survived the sweep: %v", k, err)
		}
	}

	if s := e.post(t, "doc", "editor", "", "r1", textOps(t, "c2", "too late")); s != 204 {
		t.Fatalf("late edit: %d", s)
	}
	_ = h.Flush()
	if _, err := ea.Load(ctx, "doc"); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("the hub revived a dead document: %v", err)
	}
}

func TestEndToEndViewingAndCommentingDoNotKeepADocumentAlive(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	ea, _ := newExpiringStore(clk)
	h, e := newRenewHub(t, Config{Store: ea, Renewer: ea, Now: clk.Now})
	_ = ea.Save(ctx, "doc", []byte("x"))
	clk.Add(23 * time.Hour)
	e.get(t, "/docs/doc/events?replica=r", "editor")
	e.get(t, "/docs/doc/snapshot", "viewer")
	cd := charDoc("cm")
	cb, _ := json.Marshal([]crdt.Op{cd.Put(crdt.Block{ID: "comment:c1", Kind: "lww:side", Content: "{}"})})
	if s := e.post(t, "doc", "viewer", "", "rv", string(cb)); s != 204 {
		t.Fatalf("comment: %d", s)
	}
	_ = h.Flush()
	clk.Add(2 * time.Hour)
	if _, err := ea.Load(ctx, "doc"); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("document at 25h = %v, want gone", err)
	}
}

func TestEndToEndWebSocketEditRenews(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	clk := newClock()
	ea, _ := newExpiringStore(clk)
	h, e := newRenewHub(t, Config{Store: ea, Renewer: ea, Now: clk.Now})
	_ = ea.Save(ctx, "doc", []byte("x"))
	clk.Add(23 * time.Hour)

	hdr := http.Header{}
	hdr.Set("X-Role", "editor")
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/docs/doc/ws?replica=w1", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	readFrame(t, ctx, c, "snapshot")
	b, _ := json.Marshal(wire.Frame{Event: "ops", ID: 1, Data: json.RawMessage(textOps(t, "wc", "typed"))})
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	readFrame(t, ctx, c, "ack")
	_ = h.Flush()
	if got, _, _ := ea.Expiry(ctx, "doc"); !got.Equal(clk.Now().Add(day)) {
		t.Fatalf("deadline after a socket edit = %v", got)
	}
}

func TestEndToEndHubRenewsThroughTheDocstoreServer(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	ea, _ := newExpiringStore(clk)
	var sawAuth, sawToken string
	ds := docstore.NewServer(ea, "memory").Handler()
	dsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/renew") {
			sawAuth, sawToken = r.Header.Get("Authorization"), r.Header.Get("X-Gojiberry-Token")
		}
		ds.ServeHTTP(w, r)
	}))
	defer dsrv.Close()
	remote := docstore.NewHTTPAdapter(dsrv.URL, nil)
	remote.Header.Set("X-Gojiberry-Token", "svc")

	h, e := newRenewHub(t, Config{Renewer: remote, Now: clk.Now})
	_ = ea.Save(ctx, "doc", []byte("x"))
	clk.Add(23 * time.Hour)
	if s := e.post(t, "doc", "editor", "ana", "r", textOps(t, "c", "hi")); s != 204 {
		t.Fatalf("edit: %d", s)
	}
	_ = h.Flush()
	if got, _, _ := ea.Expiry(ctx, "doc"); !got.Equal(clk.Now().Add(day)) {
		t.Fatalf("deadline = %v", got)
	}
	if sawAuth != "Bearer tok-ana" || sawToken != "svc" {
		t.Fatalf("the docstore saw Authorization=%q token=%q", sawAuth, sawToken)
	}

	clk.Add(2 * day)
	if s := e.post(t, "doc", "editor", "ana", "r", textOps(t, "c2", "late")); s != 204 {
		t.Fatalf("late edit: %d", s)
	}
	_ = h.Flush()
	if _, err := ea.Load(ctx, "doc"); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("revived: %v", err)
	}
}
