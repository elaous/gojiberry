//go:build !(js && wasm)

package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/crdt"
	"github.com/elaous/gojiberry/docstore"
)

type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.b.Write(p) }
func (l *logBuf) String() string              { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// captureLog sends the standard logger to a buffer for the test.
func captureLog(t *testing.T) *logBuf {
	t.Helper()
	lb := &logBuf{}
	log.SetOutput(lb)
	t.Cleanup(func() { log.SetOutput(defaultLogOut) })
	return lb
}

var defaultLogOut = log.Writer()

func countLines(s, sub string) int { return strings.Count(s, sub) }

// --- M1: a document that expired under an open room

func exists(t *testing.T, a docstore.Adapter, id string) bool {
	t.Helper()
	_, err := a.Load(context.Background(), id)
	if err != nil && !errors.Is(err, docstore.ErrNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

func TestExpiredDocumentClosesTheStaleRoom(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	ea, inner := newExpiringStore(clk)
	h, e := newRenewHub(t, Config{Store: ea, Renewer: ea, RenewInterval: -1, Now: clk.Now})

	if err := ea.Save(ctx, "doc", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if s := e.post(t, "doc", "editor", "", "r1", textOps(t, "c1", "alive")); s != 204 {
		t.Fatalf("edit: %d", s)
	}
	_ = h.Flush() // saves collab:doc and waits for the (successful) renewal
	if !exists(t, inner, StoreKey("doc")) {
		t.Fatal("hub state was not saved")
	}

	clk.Add(25 * time.Hour) // the document expires ...
	if n, err := ea.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v", n, err)
	}
	if exists(t, inner, StoreKey("doc")) {
		t.Fatal("the sweeper left the hub state")
	}
	// ... but a tab keeps its room. The next edit is acknowledged (the renewal that finds out
	// is asynchronous) and would, before the fix, mark the room dirty and rewrite collab:doc.
	if s := e.post(t, "doc", "editor", "", "r1", textOps(t, "c2", "stale tab")); s != 204 {
		t.Fatalf("first stale edit: %d", s)
	}
	h.waitRenewals()
	// From now on the room is dead: 410 with a clear message, for editors and commenters alike.
	body := func(role string) (int, string) {
		req, _ := http.NewRequest("POST", e.srv.URL+"/docs/doc/ops?replica=r1", strings.NewReader(textOps(t, "c3", "more")))
		req.Header.Set("X-Role", role)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	for _, role := range []string{"editor", "owner", "viewer"} {
		if st, msg := body(role); st != http.StatusGone || !strings.Contains(msg, "expired") {
			t.Fatalf("%s edit of a dead room: %d %q, want 410 with an explanation", role, st, msg)
		}
	}
	if err := h.Flush(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	for _, k := range []string{StoreKey("doc"), "doc", "expiry:doc"} {
		if exists(t, inner, k) {
			t.Fatalf("%s was left behind (the expired document was resurrected)", k)
		}
	}
	h.mu.Lock()
	rooms := len(h.rooms)
	h.mu.Unlock()
	if rooms != 0 {
		t.Fatalf("%d rooms still held", rooms)
	}
	if got := h.Health().ExpiredRooms; got != 1 {
		t.Fatalf("expiredRooms = %d", got)
	}
}

// A save that wins the race with the renewal that discovers the expiry is undone by it.
func TestExpiryDiscoveredAfterASaveRemovesTheResurrectedState(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	ea, inner := newExpiringStore(clk)
	gate := make(chan struct{})
	var armed atomic.Bool
	slow := rf(func(ctx context.Context, id string) error {
		if armed.Load() {
			<-gate
		}
		return ea.Renew(ctx, id)
	})
	h, e := newRenewHub(t, Config{Store: ea, Renewer: slow, RenewInterval: -1, Now: clk.Now})
	_ = ea.Save(ctx, "doc", []byte("v1"))
	e.post(t, "doc", "editor", "", "r1", textOps(t, "c1", "alive"))
	_ = h.Flush()
	clk.Add(25 * time.Hour)
	_, _ = ea.Sweep(ctx)

	armed.Store(true)
	e.post(t, "doc", "editor", "", "r1", textOps(t, "c2", "stale tab"))
	h.mu.Lock()
	rm := h.rooms["doc"]
	h.mu.Unlock()
	if err := h.save(rm); err != nil { // the save timer fires before the renewal returns
		t.Fatal(err)
	}
	if !exists(t, inner, StoreKey("doc")) {
		t.Fatal("test setup: the save did not resurrect the state")
	}
	close(gate)
	h.waitRenewals()
	if exists(t, inner, StoreKey("doc")) {
		t.Fatal("collab:doc survived the discovery of the expiry")
	}
	if err := h.save(rm); err != nil || exists(t, inner, StoreKey("doc")) {
		t.Fatalf("a dead room saved again: %v", err)
	}
	// A caller that still holds the room (a connection that was mid-request) is refused too,
	// and the room does not schedule a save.
	var ops []crdt.Op
	_ = json.Unmarshal([]byte(textOps(t, "c7", "late")), &ops)
	req, _ := http.NewRequest("POST", "/", nil)
	if st, msg, renew := h.applyOps(rm, Editor, req, "r1", ops); st != http.StatusGone || renew || !strings.Contains(msg, "expired") {
		t.Fatalf("a held dead room accepted ops: %d %q renew=%v", st, msg, renew)
	}
	rm.mu.Lock()
	dirty := rm.dirty
	rm.mu.Unlock()
	if dirty {
		t.Fatal("a dead room scheduled a save")
	}
}

type rf func(ctx context.Context, id string) error

func (f rf) Renew(ctx context.Context, id string) error { return f(ctx, id) }

// A document that exists only in the hub always renews to "not found" and must keep working.
func TestHubOnlyDocumentIsNeverRefused(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	ea, inner := newExpiringStore(clk)
	h, e := newRenewHub(t, Config{Store: ea, Renewer: ea, RenewInterval: -1, Now: clk.Now})
	for i := 0; i < 5; i++ {
		clk.Add(10 * time.Hour)
		if s := e.post(t, "hubonly", "editor", "", "r1", textOps(t, "c"+string(rune('a'+i)), "x")); s != 204 {
			t.Fatalf("edit %d of a hub-only document: %d", i, s)
		}
		h.waitRenewals()
	}
	_ = h.Flush()
	if !exists(t, inner, StoreKey("hubonly")) {
		t.Fatal("hub-only state was not saved")
	}
	if h.Health().ExpiredRooms != 0 {
		t.Fatal("a hub-only room was closed")
	}
	_ = ctx
}

// A 404 only counts after a success: success, then not found, closes; not found first does not.
func TestDeadRoomNeedsAnEarlierSuccess(t *testing.T) {
	var calls atomic.Int32
	h, e := newRenewHub(t, Config{RenewInterval: -1, Renewer: rf(func(context.Context, string) error {
		if calls.Add(1) == 1 {
			return nil
		}
		return docstore.ErrNotFound
	})})
	post := func(i int) int {
		s := e.post(t, "d", "editor", "", "r", textOps(t, "c"+string(rune('a'+i)), "x"))
		h.waitRenewals()
		return s
	}
	if s := post(0); s != 204 { // renewed
		t.Fatal(s)
	}
	if s := post(1); s != 204 { // not found: discovered now
		t.Fatal(s)
	}
	if s := post(2); s != http.StatusGone {
		t.Fatalf("after success then 404: %d, want 410", s)
	}
}

// A stale tab that reconnects does not start a new room that writes the content back; later the
// id can be used again.
func TestDeadDocumentIsRefusedOnReconnectForAWhile(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	ea, inner := newExpiringStore(clk)
	h, e := newRenewHub(t, Config{Store: ea, Renewer: ea, RenewInterval: -1, Now: clk.Now})
	_ = ea.Save(ctx, "doc", []byte("v1"))
	e.post(t, "doc", "editor", "", "r1", textOps(t, "c1", "alive"))
	h.waitRenewals()
	clk.Add(25 * time.Hour)
	_, _ = ea.Sweep(ctx)
	e.post(t, "doc", "editor", "", "r1", textOps(t, "c2", "stale"))
	h.waitRenewals()

	for _, path := range []string{"/docs/doc/snapshot", "/docs/doc/events?replica=x", "/docs/doc/ws?replica=x"} {
		if st := e.get(t, path, "editor"); st != http.StatusGone {
			t.Fatalf("GET %s = %d, want 410", path, st)
		}
	}
	if s := e.post(t, "doc", "editor", "", "r2", textOps(t, "c9", "reconnected tab")); s != http.StatusGone {
		t.Fatalf("a reconnected tab's edit = %d, want 410", s)
	}
	_ = h.Flush()
	if exists(t, inner, StoreKey("doc")) {
		t.Fatal("a reconnecting tab resurrected the state")
	}
	clk.Add(2 * time.Hour) // the memory ends: the id is usable again (a new document)
	if st := e.get(t, "/docs/doc/snapshot", "editor"); st != http.StatusOK {
		t.Fatalf("after the memory ended: %d", st)
	}
}

// Over a WebSocket the client is told with a close frame carrying the reason.
func TestDeadRoomClosesTheWebSocketWithAReason(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	clk := newClock()
	ea, _ := newExpiringStore(clk)
	h, e := newRenewHub(t, Config{Store: ea, Renewer: ea, RenewInterval: -1, Now: clk.Now})
	_ = ea.Save(ctx, "doc", []byte("x"))
	hdr := http.Header{"X-Role": {"editor"}}
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/docs/doc/ws?replica=w1", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	readFrame(t, ctx, c, "snapshot")
	send := func(id uint64, text string) {
		b, _ := json.Marshal(wire.Frame{Event: "ops", ID: id, Data: json.RawMessage(textOps(t, "wc"+text, text))})
		if err := c.Write(ctx, websocket.MessageText, b); err != nil {
			t.Fatal(err)
		}
	}
	send(1, "a")
	readFrame(t, ctx, c, "ack")
	h.waitRenewals()
	clk.Add(25 * time.Hour)
	_, _ = ea.Sweep(ctx)
	send(2, "b") // discovers the expiry
	for {
		_, _, err := c.Read(ctx)
		if err == nil {
			continue // the ack, presence...
		}
		if websocket.CloseStatus(err) != 4410 || !strings.Contains(err.Error(), "expired") {
			t.Fatalf("socket ended with %v, want close 4410 and the reason", err)
		}
		break
	}
}

// --- m3: role disagreement shows up

func TestRenewalRefusedIsWarnedOnceAndCounted(t *testing.T) {
	lb := captureLog(t)
	h, e := newRenewHub(t, Config{RenewInterval: -1, Renewer: rf(func(context.Context, string) error {
		return &docstore.StatusError{Status: http.StatusForbidden, Text: "403 Forbidden", Body: "no write access"}
	})})
	for i := 0; i < 3; i++ {
		if s := e.post(t, "d", "editor", "", "r", textOps(t, "c"+string(rune('a'+i)), "x")); s != 204 {
			t.Fatalf("edit: %d", s)
		}
		h.waitRenewals()
	}
	e.post(t, "other", "editor", "", "r", textOps(t, "cz", "x"))
	h.waitRenewals()
	out := lb.String()
	if n := countLines(out, "WARNING the store refused to renew"); n != 2 { // once per document
		t.Fatalf("%d warnings, want 1 per document (2):\n%s", n, out)
	}
	if !strings.Contains(out, "NO_ACL_ROLE") || !strings.Contains(out, "REQUIRE_ACL") || !strings.Contains(out, "must agree") {
		t.Fatalf("the warning does not explain the cause:\n%s", out)
	}

	// /healthz reports it.
	req, _ := http.NewRequest("GET", e.srv.URL+"/healthz", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var hl map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&hl); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || hl["renewFailures"] != float64(4) || hl["renewDenied"] != float64(4) || hl["ok"] != true {
		t.Fatalf("healthz = %d %v", resp.StatusCode, hl)
	}
	if got := h.Health(); got.RenewFailures != 4 {
		t.Fatalf("Health() = %+v", got)
	}
}

func TestOtherRenewalErrorsCountButDoNotWarnAboutRoles(t *testing.T) {
	lb := captureLog(t)
	h, e := newRenewHub(t, Config{RenewInterval: -1, Renewer: rf(func(context.Context, string) error { return errors.New("store down") })})
	e.post(t, "d", "editor", "", "r", textOps(t, "c", "x"))
	h.waitRenewals()
	if h.Health().RenewFailures != 1 || h.Health().RenewDenied != 0 || strings.Contains(lb.String(), "WARNING") {
		t.Fatalf("%+v\n%s", h.Health(), lb.String())
	}
	// A document that is just not in the store is not a failure.
	h2, e2 := newRenewHub(t, Config{RenewInterval: -1, Renewer: rf(func(context.Context, string) error { return docstore.ErrNotFound })})
	e2.post(t, "d", "editor", "", "r", textOps(t, "c", "x"))
	h2.waitRenewals()
	if h2.Health().RenewFailures != 0 {
		t.Fatal("not found counted as a failure")
	}
}

// --- n1: secrets stay out of the log

func TestRenewalLogsCarryNoCredentials(t *testing.T) {
	lb := captureLog(t)
	// A server that fails with a 500 (and one nothing listens on) behind a URL with userinfo.
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer failing.Close()
	for _, base := range []string{
		strings.Replace(failing.URL, "http://", "http://svcuser:hunter2@", 1),
		"http://svcuser:hunter2@127.0.0.1:1",
	} {
		r := docstore.NewHTTPAdapter(base, nil)
		r.Header.Set("X-Gojiberry-Token", "RENEWTOKENSECRET")
		h, e := newRenewHub(t, Config{RenewInterval: -1, Renewer: r})
		if s := e.post(t, "d", "editor", "ana", "r", textOps(t, "c", "x")); s != 204 {
			t.Fatal(s)
		}
		h.waitRenewals()
	}
	out := lb.String()
	if !strings.Contains(out, "renewing the expiry") {
		t.Fatalf("test setup: nothing was logged:\n%s", out)
	}
	for _, secret := range []string{"hunter2", "svcuser", "RENEWTOKENSECRET", "tok-ana"} {
		if strings.Contains(out, secret) {
			t.Fatalf("the log leaks %q:\n%s", secret, out)
		}
	}
}

// --- n2: interval clamp, token refresh, panics

func TestRenewIntervalClampedToAQuarterOfTheTTL(t *testing.T) {
	lb := captureLog(t)
	for _, tc := range []struct {
		name         string
		ttl, in, out time.Duration
		warn, lower  bool
	}{
		{"default under a short TTL", 2 * time.Minute, 0, 30 * time.Second, false, true},
		{"default under a long TTL", 24 * time.Hour, 0, time.Minute, false, false},
		{"configured above the TTL", time.Hour, 3 * time.Hour, 15 * time.Minute, true, true},
		{"configured between TTL/4 and TTL", time.Hour, 40 * time.Minute, 15 * time.Minute, false, true},
		{"configured below TTL/4", time.Hour, 5 * time.Minute, 5 * time.Minute, false, false},
		{"TTL unknown", 0, 3 * time.Hour, 3 * time.Hour, false, false},
		{"every edit", time.Hour, -1, -1, false, false},
	} {
		before := lb.String()
		h := New(Config{ExpiryTTL: tc.ttl, RenewInterval: tc.in})
		if h.cfg.RenewInterval != tc.out {
			t.Errorf("%s: interval = %v, want %v", tc.name, h.cfg.RenewInterval, tc.out)
		}
		added := strings.TrimPrefix(lb.String(), before)
		if got := strings.Contains(added, "WARNING"); got != tc.warn {
			t.Errorf("%s: warned = %v, want %v (%q)", tc.name, got, tc.warn, added)
		}
		if got := strings.Contains(added, "lowered"); got != tc.lower {
			t.Errorf("%s: lowered notice = %v, want %v (%q)", tc.name, got, tc.lower, added)
		}
	}
}

// With a 4-hour TTL and the clamp, a document that is edited every 3 hours stays alive; at the
// unclamped default of one minute nothing changes. Behaviourally: the interval in force is
// what rate-limits renewals.
func TestClampedIntervalRateLimitsRenewals(t *testing.T) {
	fr := &fakeRenewer{}
	clk := newClock()
	h, e := newRenewHub(t, Config{Renewer: fr, ExpiryTTL: 2 * time.Minute, Now: clk.Now}) // interval -> 30s
	for i := 0; i < 4; i++ {
		e.post(t, "d", "editor", "", "r", textOps(t, "c"+string(rune('a'+i)), "x"))
		_ = h.Flush()
		clk.Add(31 * time.Second)
	}
	if got := len(fr.got()); got != 4 {
		t.Fatalf("%d renewals with edits 31s apart and a 30s interval, want 4", got)
	}
}

// The renewal of a long-lived WebSocket uses a token that is read afresh each time.
func TestAuthFuncRefreshesTheTokenOfALongLivedSocket(t *testing.T) {
	lb := captureLog(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	var seen []string
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := r.Header.Get("Authorization")
		mu.Lock()
		seen = append(seen, a)
		mu.Unlock()
		if a != "Bearer fresh-2" && a != "Bearer fresh-3" { // the token the socket was opened with has expired
			http.Error(w, "token expired", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ds.Close()
	var n atomic.Int32
	run := func(authFunc func(*http.Request) string) (*Hub, string) {
		h, e := newRenewHub(t, Config{Renewer: docstore.NewHTTPAdapter(ds.URL, nil), RenewInterval: -1, AuthFunc: authFunc})
		hdr := http.Header{"X-Role": {"editor"}, "Authorization": {"Bearer opened-with"}}
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/docs/doc/ws?replica=w1", &websocket.DialOptions{HTTPHeader: hdr})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.CloseNow() })
		readFrame(t, ctx, c, "snapshot")
		for i := uint64(1); i <= 3; i++ {
			b, _ := json.Marshal(wire.Frame{Event: "ops", ID: i, Data: json.RawMessage(textOps(t, "wc"+string(rune('a'+int(i))), "x"))})
			if err := c.Write(ctx, websocket.MessageText, b); err != nil {
				t.Fatal(err)
			}
			readFrame(t, ctx, c, "ack")
			h.waitRenewals()
		}
		mu.Lock()
		defer mu.Unlock()
		return h, strings.Join(seen, ",")
	}

	// Without AuthFunc every renewal carries the upgrade request's token, which is refused.
	h, got := run(nil)
	if got != "Bearer opened-with,Bearer opened-with,Bearer opened-with" {
		t.Fatalf("without AuthFunc the renewals carried %q", got)
	}
	if h.Health().RenewDenied != 3 || countLines(lb.String(), "WARNING the store refused") != 1 {
		t.Fatalf("denied=%d, want 3 with one warning:\n%s", h.Health().RenewDenied, lb.String())
	}

	// With AuthFunc each renewal reads the current one: first still stale (1), then fresh.
	mu.Lock()
	seen = nil
	mu.Unlock()
	h, got = run(func(r *http.Request) string {
		if r.Header.Get("Authorization") != "Bearer opened-with" {
			t.Errorf("AuthFunc got another request's header %q", r.Header.Get("Authorization"))
		}
		return "Bearer fresh-" + string(rune('0'+n.Add(1)))
	})
	if got != "Bearer fresh-1,Bearer fresh-2,Bearer fresh-3" {
		t.Fatalf("with AuthFunc the renewals carried %q", got)
	}
	if h.Health().RenewDenied != 1 { // fresh-1 was refused, the rest renewed
		t.Fatalf("denied = %d, want 1", h.Health().RenewDenied)
	}
}

func TestPanickingRenewerOrAuthFuncDoesNotCrashTheHub(t *testing.T) {
	lb := captureLog(t)
	for name, cfg := range map[string]Config{
		"renewer": {Renewer: rf(func(context.Context, string) error { panic("renewer exploded") })},
		"auth":    {Renewer: &fakeRenewer{}, AuthFunc: func(*http.Request) string { panic("authfunc exploded") }},
	} {
		cfg.RenewInterval = -1
		h, e := newRenewHub(t, cfg)
		for i := 0; i < 2; i++ {
			if s := e.post(t, "d", "editor", "", "r", textOps(t, "c"+string(rune('a'+i)), "x")); s != 204 {
				t.Fatalf("%s: edit %d: %d", name, i, s)
			}
			h.waitRenewals() // must return: the in-flight count is released
		}
		if got := h.Health().RenewFailures; got != 2 {
			t.Fatalf("%s: renewFailures = %d", name, got)
		}
		if s := e.post(t, "d", "editor", "", "r", textOps(t, "cz", "x")); s != 204 { // still serving
			t.Fatalf("%s: hub stopped serving: %d", name, s)
		}
		h.waitRenewals()
	}
	if !strings.Contains(lb.String(), "renewer exploded") || !strings.Contains(lb.String(), "authfunc exploded") {
		t.Fatalf("panics were not logged:\n%s", lb.String())
	}
}

var _ = crdt.Op{}
