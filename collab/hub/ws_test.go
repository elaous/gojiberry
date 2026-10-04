//go:build !(js && wasm)

package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/crdt"
)

// readFrame reads frames until one has the event.
func readFrame(t *testing.T, ctx context.Context, c *websocket.Conn, event string) wire.Frame {
	t.Helper()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %q: %v", event, err)
		}
		var f wire.Frame
		if json.Unmarshal(data, &f) != nil {
			t.Fatalf("bad frame %s", data)
		}
		if f.Event == event {
			return f
		}
	}
}

func TestWebSocketTokenInQueryActsAsBearer(t *testing.T) {
	h := New(Config{Roles: func(r *http.Request, _ string) Role {
		return Role(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/docs/d/ws?replica=r"

	c, _, err := websocket.Dial(ctx, wsURL+"&access_token=editor", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	var hello struct{ Role wire.Role }
	if err := json.Unmarshal(readFrame(t, ctx, c, "hello").Data, &hello); err != nil || hello.Role != wire.Editor {
		t.Fatalf("hello %+v %v", hello, err)
	}
	if _, resp, err := websocket.Dial(ctx, wsURL, nil); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a socket with no token must be refused: %v %v", resp, err)
	}
}

func TestWebSocketAcksAndRejectsOps(t *testing.T) {
	h := New(Config{Roles: func(r *http.Request, _ string) Role { return Role(r.Header.Get("X-Role")) }})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dial := func(role string) *websocket.Conn {
		hdr := http.Header{}
		hdr.Set("X-Role", role)
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/docs/d/ws?replica=r-"+role, &websocket.DialOptions{HTTPHeader: hdr})
		if err != nil {
			t.Fatal(err)
		}
		readFrame(t, ctx, c, "snapshot")
		return c
	}
	doc := charDoc("c")
	ops, _ := json.Marshal([]crdt.Op{doc.Put(crdt.Block{ID: "doc", Content: "x"})})
	send := func(c *websocket.Conn, id uint64) wire.Frame {
		b, _ := json.Marshal(wire.Frame{Event: "ops", ID: id, Data: ops})
		if err := c.Write(ctx, websocket.MessageText, b); err != nil {
			t.Fatal(err)
		}
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var f wire.Frame
			_ = json.Unmarshal(data, &f)
			if f.Event == "ack" || f.Event == "reject" {
				return f
			}
		}
	}
	editor := dial("editor")
	defer func() { _ = editor.CloseNow() }()
	if f := send(editor, 7); f.Event != "ack" || f.ID != 7 {
		t.Fatalf("an editor's ops: %+v", f)
	}
	viewer := dial("viewer")
	defer func() { _ = viewer.CloseNow() }()
	if f := send(viewer, 9); f.Event != "reject" || f.ID != 9 || f.Error == "" {
		t.Fatalf("a viewer's text edit: %+v", f)
	}
}

func TestWebSocketSpeaksOnlyForItsOwnReplica(t *testing.T) {
	h := New(Config{PresenceInterval: -1})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	base := "ws" + strings.TrimPrefix(srv.URL, "http") + "/docs/d/ws?replica="
	watcher, _, err := websocket.Dial(ctx, base+"watcher", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.CloseNow() }()
	mover, _, err := websocket.Dial(ctx, base+"mover", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mover.CloseNow() }()
	// The mover claims to be someone else in the body: the socket's own replica wins.
	b, _ := json.Marshal(wire.Frame{Event: "presence", Data: json.RawMessage(`{"replica":"watcher","name":"Imposter"}`)})
	if err := mover.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	var p wire.Presence
	for {
		f := readFrame(t, ctx, watcher, "presence")
		_ = json.Unmarshal(f.Data, &p)
		if p.Name == "Imposter" {
			break
		}
	}
	if p.Replica != "mover" {
		t.Fatalf("a socket spoke for %q", p.Replica)
	}
}

func TestWebSocketRoleIsCheckedOnEveryFrame(t *testing.T) {
	var role atomic.Value
	role.Store("editor")
	h := New(Config{KeepAlive: time.Hour, Roles: func(*http.Request, string) Role { return Role(role.Load().(string)) }})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/docs/d/ws?replica=r", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	readFrame(t, ctx, c, "snapshot")
	role.Store("viewer") // downgraded; the next keep-alive tick is an hour away
	ops, _ := json.Marshal([]crdt.Op{charDoc("c").Put(crdt.Block{ID: "doc", Content: "x"})})
	b, _ := json.Marshal(wire.Frame{Event: "ops", ID: 1, Data: ops})
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatalf("closed with %v, want a policy violation", err)
			}
			return
		}
		var f wire.Frame
		_ = json.Unmarshal(data, &f)
		if f.Event == "ack" {
			t.Fatal("a downgraded user's ops were accepted")
		}
	}
}

func TestWebSocketWithCookiesNeedsASameOriginOrAListedOne(t *testing.T) {
	h := New(Config{CookieOrigins: []string{"https://app.example"}})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dial := func(origin, cookie string) (int, error) {
		hdr := http.Header{}
		if origin != "" {
			hdr.Set("Origin", origin)
		}
		if cookie != "" {
			hdr.Set("Cookie", cookie)
		}
		c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/docs/d/ws?replica=r", &websocket.DialOptions{HTTPHeader: hdr})
		if err == nil {
			_ = c.CloseNow()
			return http.StatusSwitchingProtocols, nil
		}
		if resp != nil {
			return resp.StatusCode, err
		}
		return 0, err
	}
	for _, c := range []struct {
		name, origin, cookie string
		want                 int
	}{
		{"another site with cookies", "https://evil.example", "sid=1", http.StatusForbidden},
		{"a listed origin with cookies", "https://app.example", "sid=1", http.StatusSwitchingProtocols},
		{"the same host with cookies", srv.URL, "sid=1", http.StatusSwitchingProtocols},
		{"another site without cookies (bearer tokens)", "https://evil.example", "", http.StatusSwitchingProtocols},
	} {
		if got, err := dial(c.origin, c.cookie); got != c.want {
			t.Errorf("%s: status %d (%v), want %d", c.name, got, err, c.want)
		}
	}
}

func TestWebSocketAnswers413ToAnOversizedFrame(t *testing.T) {
	h := New(Config{MaxBody: 1 << 10})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/docs/d/ws?replica=r", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	readFrame(t, ctx, c, "snapshot")
	big, _ := json.Marshal(strings.Repeat("x", 2<<10))
	b, _ := json.Marshal(wire.Frame{Event: "ops", ID: 3, Data: big})
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	f := readFrame(t, ctx, c, "reject")
	if f.ID != 3 {
		t.Fatalf("reject %+v", f)
	}
}
