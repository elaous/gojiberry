package hub

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/crdt"
	"github.com/elaous/gojiberry/docstore"
)

func charDoc(r string) *crdt.Doc { return crdt.NewDocWithStrategy(r, crdt.MergeCharCRDT) }

func TestAuthorizeTable(t *testing.T) {
	server := charDoc("hub")
	server.Put(crdt.Block{ID: "doc", Content: "hello"})
	server.Put(crdt.Block{ID: "comment:c0", Kind: "lww:side", Content: "{}"})
	server.Put(crdt.Block{ID: "note:n0", Kind: "lww:side", Content: "{}"})

	cl := charDoc("viewer")
	cl.Apply(server.Snapshot()...)
	formatOp, _ := cl.Format("doc", 0, 3, "comment", "c1", false)
	boldOp, _ := cl.Format("doc", 0, 3, "bold", "1", true)
	insertOp, _ := cl.Splice("doc", 5, 0, []rune("!"))
	newComment := cl.Put(crdt.Block{ID: "comment:c1", Kind: "lww:side", Content: "{}"})
	newReply := cl.Put(crdt.Block{ID: "reply:c1:1", Kind: "lww:side", Content: "{}"})
	newNote := cl.Put(crdt.Block{ID: "note:n1", Kind: "lww:side", Content: "{}"})
	editComment := cl.Put(crdt.Block{ID: "comment:c0", Kind: "lww:side", Content: `{"x":1}`})
	delComment := cl.Delete("comment:c0")
	hijack := formatOp
	hijack.Block.Deleted = true // a comment mark smuggling a block delete
	retype := formatOp
	retype.Block.Kind = "other"
	reorder := formatOp
	reorder.Block.Order = 99
	metaOnly := crdt.Op{Block: formatOp.Block, TS: formatOp.TS}
	unknown := crdt.Op{Block: crdt.Block{ID: "other"}, TS: formatOp.TS, CharOps: formatOp.CharOps}
	nilMark := crdt.Op{Block: formatOp.Block, TS: formatOp.TS, CharOps: []crdt.CharOp{{Kind: crdt.CharMark}}}
	newPlain := crdt.Op{Block: crdt.Block{ID: "reply:x", Content: "x"}} // not an lww kind
	for _, tc := range []struct {
		name string
		role Role
		ops  []crdt.Op
		ok   bool
	}{
		{"editor edits anything", Editor, []crdt.Op{insertOp, boldOp, editComment, delComment, newNote}, true},
		{"owner edits anything", Owner, []crdt.Op{insertOp}, true},
		{"no role", "", []crdt.Op{formatOp}, false},
		{"viewer comments on text", Viewer, []crdt.Op{formatOp, newComment, newReply}, true},
		{"viewer cannot insert", Viewer, []crdt.Op{insertOp}, false},
		{"viewer cannot bold", Viewer, []crdt.Op{boldOp}, false},
		{"viewer cannot add notes", Viewer, []crdt.Op{newNote}, false},
		{"viewer cannot edit a comment", Viewer, []crdt.Op{editComment}, false},
		{"viewer cannot delete a comment", Viewer, []crdt.Op{delComment}, false},
		{"viewer cannot smuggle a delete", Viewer, []crdt.Op{hijack}, false},
		{"viewer cannot change the kind", Viewer, []crdt.Op{retype}, false},
		{"viewer cannot reorder", Viewer, []crdt.Op{reorder}, false},
		{"viewer cannot send a metadata-only op", Viewer, []crdt.Op{metaOnly}, false},
		{"viewer cannot create blocks", Viewer, []crdt.Op{unknown}, false},
		{"viewer cannot send a mark with no mark", Viewer, []crdt.Op{nilMark}, false},
		{"viewer cannot send a plain block", Viewer, []crdt.Op{newPlain}, false},
		{"one bad op refuses the batch", Viewer, []crdt.Op{formatOp, insertOp}, false},
	} {
		err := authorize(server, tc.role, tc.ops)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
	if !Owner.CanEdit() || Viewer.CanEdit() || !Viewer.CanComment() || Role("").CanRead() || !Viewer.CanRead() {
		t.Fatal("role abilities")
	}
}

func TestACLRoles(t *testing.T) {
	store := docstore.NewMemoryAdapter()
	acl, _ := json.Marshal(docstore.ACL{Owner: "o@x", Grants: map[string]docstore.Role{"e@x": docstore.RoleEditor, "v@x": docstore.RoleViewer}})
	_ = store.Save(context.Background(), "acl:shared", acl)
	_ = store.Save(context.Background(), "acl:broken", []byte("{not json"))
	roles := ACLRoles(store, func(r *http.Request) string { return r.Header.Get("X-User") })
	as := func(user, doc string) Role {
		req := httptest.NewRequest("GET", "/", nil)
		if user != "" {
			req.Header.Set("X-User", user)
		}
		return roles(req, doc)
	}
	for _, tc := range []struct {
		user, doc string
		want      Role
	}{
		{"o@x", "shared", Owner}, {"e@x", "shared", Editor}, {"v@x", "shared", Viewer},
		{"stranger@x", "shared", ""}, {"", "shared", ""},
		{"anyone", "unshared", Owner}, {"o@x", "broken", ""},
	} {
		if got := as(tc.user, tc.doc); got != tc.want {
			t.Errorf("%s on %s: %q, want %q", tc.user, tc.doc, got, tc.want)
		}
	}
	anon := ACLRoles(store, nil)
	if anon(httptest.NewRequest("GET", "/", nil), "shared") != "" {
		t.Fatal("no identity function means anonymous")
	}
}

func TestEndpoints(t *testing.T) {
	h := New(Config{})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	do := func(method, path, body string) (*http.Response, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		return resp, b.String()
	}
	if resp, body := do("GET", "/docs/d/role", ""); resp.StatusCode != 200 || !strings.Contains(body, `"owner"`) {
		t.Fatalf("role: %d %s", resp.StatusCode, body)
	}
	if resp, body := do("GET", "/docs/d/snapshot", ""); resp.StatusCode != 200 || strings.TrimSpace(body) != "[]" && strings.TrimSpace(body) != "null" {
		t.Fatalf("empty snapshot: %d %q", resp.StatusCode, body)
	}
	if resp, _ := do("POST", "/docs/d/ops", "{not json"); resp.StatusCode != 400 {
		t.Fatalf("bad JSON: %d", resp.StatusCode)
	}
	if resp, _ := do("POST", "/docs/d/presence", "{}"); resp.StatusCode != 400 {
		t.Fatalf("presence without a replica: %d", resp.StatusCode)
	}
	if resp, _ := do("POST", "/docs/d/presence", "{bad"); resp.StatusCode != 400 {
		t.Fatalf("bad presence: %d", resp.StatusCode)
	}
	if resp, _ := do("POST", "/docs/d/presence", `{"replica":"r1","name":"x"}`); resp.StatusCode != 204 {
		t.Fatalf("presence: %d", resp.StatusCode)
	}
	if resp, _ := do("OPTIONS", "/docs/d/ops", ""); resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight: %d", resp.StatusCode)
	}
	d := charDoc("c")
	d.Put(crdt.Block{ID: "doc", Content: "hi"})
	ops, _ := json.Marshal(d.Drain())
	if resp, _ := do("POST", "/docs/d/ops", string(ops)); resp.StatusCode != 204 {
		t.Fatalf("ops: %d", resp.StatusCode)
	}
	if _, body := do("GET", "/docs/d/snapshot", ""); !strings.Contains(body, "Content") {
		t.Fatalf("snapshot after ops: %s", body)
	}
	small := New(Config{MaxBody: 10})
	ts := httptest.NewServer(small.Handler())
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/docs/d/ops", "application/json", strings.NewReader(strings.Repeat("x", 100)))
	if err != nil || resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: %v %v", resp, err)
	}
	// A request for a no-access role is refused everywhere.
	closed := New(Config{Roles: func(*http.Request, string) Role { return "" }})
	cs := httptest.NewServer(closed.Handler())
	defer cs.Close()
	for _, p := range []string{"/docs/d/events", "/docs/d/snapshot"} {
		if r, _ := http.Get(cs.URL + p); r.StatusCode != 403 {
			t.Errorf("%s: %d", p, r.StatusCode)
		}
	}
	if r, _ := http.Post(cs.URL+"/docs/d/ops", "application/json", strings.NewReader("[]")); r.StatusCode != 403 {
		t.Errorf("ops: %d", r.StatusCode)
	}
}

func TestEventStreamKeepAliveAndSnapshot(t *testing.T) {
	h := New(Config{KeepAlive: 10 * time.Millisecond})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/docs/d/events?replica=r", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal(resp.Header)
	}
	sc := bufio.NewScanner(resp.Body)
	var seen []string
	for sc.Scan() {
		seen = append(seen, sc.Text())
		joined := strings.Join(seen, "\n")
		if strings.Contains(joined, "event: hello") && strings.Contains(joined, "event: snapshot") && strings.Contains(joined, ": keepalive") {
			return
		}
	}
	t.Fatalf("stream ended early: %v", seen)
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	rm := &room{subs: map[*sub]bool{}}
	slow := &sub{replica: "slow", ch: make(chan event, 1)}
	self := &sub{replica: "me", ch: make(chan event, 1)}
	rm.subs[slow], rm.subs[self] = true, true
	rm.broadcast("me", event{name: "ops"})
	if len(self.ch) != 0 || len(slow.ch) != 1 {
		t.Fatal("the sender is skipped; others are sent to")
	}
	rm.broadcast("me", event{name: "ops"}) // slow's buffer is full
	if rm.subs[slow] {
		t.Fatal("a subscriber that cannot keep up is dropped")
	}
	if _, open := <-slow.ch; !open {
		// the buffered event first, then closed
		if _, open := <-slow.ch; open {
			t.Fatal("channel closed")
		}
	}
}

func TestSaveFailureAndBadStore(t *testing.T) {
	store := docstore.NewMemoryAdapter()
	_ = store.Save(context.Background(), StoreKey("bad"), []byte("{not json"))
	h := New(Config{Store: store})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	if resp, _ := http.Get(srv.URL + "/docs/bad/snapshot"); resp.StatusCode != 500 {
		t.Fatalf("an unreadable saved document is an error: %d", resp.StatusCode)
	}
	if resp, _ := http.Get(srv.URL + "/docs/a%2Fb/snapshot"); resp.StatusCode == 200 {
		t.Fatal("slashes in ids are refused")
	}
	// Without a store, Flush and Close are harmless.
	m := New(Config{})
	if err := m.Flush(); err != nil || m.Close() != nil {
		t.Fatal("no store")
	}
}
