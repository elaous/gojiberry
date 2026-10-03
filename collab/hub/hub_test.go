package hub

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/elaous/gojiberry/guard"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	erase, _ := cl.Format("doc", 0, 5, "comment", "", false)            // would clear every comment on the text
	farFuture := formatOp
	farFuture.TS.Counter = 1 << 40
	farMark := formatOp
	farMark.CharOps = append([]crdt.CharOp(nil), formatOp.CharOps...)
	for i := range farMark.CharOps {
		if farMark.CharOps[i].Mark != nil {
			m := *farMark.CharOps[i].Mark
			m.ID.Counter = 1 << 40
			farMark.CharOps[i].Mark = &m
		}
	}
	hugeComment := cl.Put(crdt.Block{ID: "comment:big", Kind: "lww:side", Content: strings.Repeat("x", maxCommentSize+1)})
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
		{"viewer cannot erase comment marks", Viewer, []crdt.Op{erase}, false},
		{"viewer cannot jump the op clock", Viewer, []crdt.Op{farFuture}, false},
		{"viewer cannot jump a mark's clock", Viewer, []crdt.Op{farMark}, false},
		{"viewer cannot add a huge comment", Viewer, []crdt.Op{hugeComment}, false},
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
		t.Fatal("the event buffered before the drop is still delivered")
	}
	if _, open := <-slow.ch; open {
		t.Fatal("then the channel is closed")
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

func TestPresenceGoneWhenStreamCloses(t *testing.T) {
	h := New(Config{KeepAlive: 20 * time.Millisecond})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	open := func(ctx context.Context, replica string) *bufio.Scanner {
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/docs/d/events?replica="+replica, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return bufio.NewScanner(resp.Body)
	}
	a := open(ctx, "a")
	bctx, bcancel := context.WithCancel(ctx)
	open(bctx, "b")
	resp, err := http.Post(srv.URL+"/docs/d/presence", "application/json", strings.NewReader(`{"replica":"b","name":"Bo"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	bcancel()
	var seen []string
	for a.Scan() {
		seen = append(seen, a.Text())
		if strings.Contains(strings.Join(seen, "\n"), `"gone":true`) {
			return
		}
	}
	t.Fatalf("no gone event: %v", seen)
}

// failingStore fails loads (other than not-found) and saves on demand.
type failingStore struct {
	docstore.Adapter
	mu                 sync.Mutex
	failLoad, failSave bool
	saves              int
}

func (f *failingStore) Load(ctx context.Context, id string) (docstore.Document, error) {
	f.mu.Lock()
	fail := f.failLoad
	f.mu.Unlock()
	if fail {
		return docstore.Document{}, errors.New("backend down")
	}
	return f.Adapter.Load(ctx, id)
}

func (f *failingStore) Save(ctx context.Context, id string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSave {
		return errors.New("backend down")
	}
	f.saves++
	return f.Adapter.Save(ctx, id, data)
}

func TestACLRolesFailClosedOnStoreError(t *testing.T) {
	store := &failingStore{Adapter: docstore.NewMemoryAdapter(), failLoad: true}
	roles := ACLRoles(store, nil)
	if got := roles(httptest.NewRequest("GET", "/", nil), "private"); got != "" {
		t.Fatalf("a store error gave %q; it must not open the document", got)
	}
}

func TestFailedSaveIsRetried(t *testing.T) {
	store := &failingStore{Adapter: docstore.NewMemoryAdapter(), failSave: true}
	h := New(Config{Store: store, SaveDelay: 10 * time.Millisecond})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	doc := charDoc("c")
	op := doc.Put(crdt.Block{ID: "doc", Content: "x"})
	body, _ := json.Marshal([]crdt.Op{op})
	resp, err := http.Post(srv.URL+"/docs/d/ops", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != 204 {
		t.Fatalf("post: %v %v", resp, err)
	}
	time.Sleep(60 * time.Millisecond) // a few failed attempts
	store.mu.Lock()
	store.failSave = false
	store.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for {
		store.mu.Lock()
		n := store.saves
		store.mu.Unlock()
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the failed save was never retried")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRoomLimitAndStoreErrorsAreNotLeaked(t *testing.T) {
	store := &failingStore{Adapter: docstore.NewMemoryAdapter(), failLoad: true}
	h := New(Config{Store: store, MaxRooms: 1})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/docs/a/snapshot")
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 500 || strings.Contains(string(b), "backend down") {
		t.Fatalf("a store error is a plain 500: %d %q", resp.StatusCode, b)
	}
	store.mu.Lock()
	store.failLoad = false
	store.mu.Unlock()
	if resp, _ := http.Get(srv.URL + "/docs/a/snapshot"); resp.StatusCode != 200 {
		t.Fatalf("a failed load is retried: %d", resp.StatusCode)
	}
	if resp, _ := http.Get(srv.URL + "/docs/b/snapshot"); resp.StatusCode != 503 {
		t.Fatalf("the room limit: %d", resp.StatusCode)
	}
}

func TestStreamEndsWhenRoleChanges(t *testing.T) {
	var mu sync.Mutex
	role := Editor
	h := New(Config{KeepAlive: 10 * time.Millisecond, Roles: func(*http.Request, string) Role {
		mu.Lock()
		defer mu.Unlock()
		return role
	}})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/docs/d/events?replica=r", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	mu.Lock()
	role = ""
	mu.Unlock()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("the stream should end cleanly once access is gone: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("the stream stayed open after access was revoked")
	}
}

func TestViewerCannotPushTheClockThroughCharOps(t *testing.T) {
	server := charDoc("hub")
	server.Put(crdt.Block{ID: "doc", Content: "hello"})
	cl := charDoc("viewer")
	cl.Apply(server.Snapshot()...)
	op, _ := cl.Format("doc", 0, 3, "comment", "c1", false)
	for i := range op.CharOps {
		op.CharOps[i].ID.Counter = ^uint64(0) // the op's own and mark ids stay small
	}
	if err := authorize(server, Viewer, []crdt.Op{op}); err == nil {
		t.Fatal("a char operation with a huge counter was accepted")
	}
	if server.Clock() > 1<<20 {
		t.Fatalf("clock moved to %d", server.Clock())
	}
}

func TestStopRefusesWritesAndEndsStreams(t *testing.T) {
	h := New(Config{KeepAlive: 10 * time.Millisecond})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/docs/d/events?replica=r", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	h.Stop()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil || ctx.Err() != nil {
		t.Fatalf("the stream should end once the hub stops: %v", err)
	}
	post, err := http.Post(srv.URL+"/docs/d/ops", "application/json", strings.NewReader("[]"))
	if err != nil || post.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a write after Stop: %v %v", post, err)
	}
}

func TestExpiredPresenceIsAnnouncedGone(t *testing.T) {
	now := time.Now()
	h := New(Config{PresenceTTL: time.Second, KeepAlive: 10 * time.Millisecond})
	h.now = func() time.Time { return now }
	rm := &room{subs: map[*sub]bool{}, pres: map[string]presenceEntry{"idle": {p: Presence{Replica: "idle"}, seen: now.Add(-time.Minute)}}}
	watcher := &sub{replica: "w", ch: make(chan event, 4)}
	rm.subs[watcher] = true
	rm.mu.Lock()
	h.prunePresence(rm)
	rm.mu.Unlock()
	select {
	case e := <-watcher.ch:
		if !strings.Contains(string(e.data), `"gone":true`) {
			t.Fatalf("%s", e.data)
		}
	default:
		t.Fatal("no gone event for the expired cursor")
	}
}

func TestGuardOriginsAndTokenApplyToTheHub(t *testing.T) {
	h := New(Config{Guard: guard.Chain(guard.Origins("https://app.example.com"), guard.Token("tok", guard.Options{TokenHeader: "X-Gojiberry-Token"}))})
	call := func(origin, token string) int {
		req := httptest.NewRequest("GET", "/docs/d/snapshot", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if token != "" {
			req.Header.Set("X-Gojiberry-Token", token)
		}
		rec := httptest.NewRecorder()
		h.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	if call("https://app.example.com", "tok") != 200 || call("https://evil.example", "tok") != 403 || call("https://app.example.com", "") != 401 {
		t.Fatal("the hub's guard was not applied")
	}
}

func TestReplicaIDsAreBoundToTheUserWhoFirstUsedThem(t *testing.T) {
	h := New(Config{User: func(r *http.Request) string { return r.Header.Get("X-User") }})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	post := func(user, replica string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/docs/d/ops?replica="+replica, strings.NewReader("[]"))
		req.Header.Set("X-User", user)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if post("ana@x", "r1") != 204 || post("ana@x", "r1") != 204 {
		t.Fatal("the owner of a replica id keeps using it")
	}
	if post("bo@x", "r1") != 403 {
		t.Fatal("another user spoke under ana's replica id")
	}
	if post("bo@x", "r2") != 204 || post("", "r1") != 204 {
		t.Fatal("other ids are free, and anonymous callers are not bound")
	}
	pres, _ := http.NewRequest("POST", srv.URL+"/docs/d/presence", strings.NewReader(`{"replica":"r1","name":"x"}`))
	pres.Header.Set("X-User", "bo@x")
	resp, _ := http.DefaultClient.Do(pres)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("presence under another user's replica id: %d", resp.StatusCode)
	}
}
