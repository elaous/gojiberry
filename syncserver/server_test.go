package syncserver

import (
	"bytes"
	"encoding/json"
	"github.com/elaous/gojiberry/guard"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/crdt"
)

func TestConfigFromEnvDefaultsAndOverrides(t *testing.T) {
	_ = os.Unsetenv("GOJIBERRY_SYNC_ADDR")
	_ = os.Unsetenv("GOJIBERRY_SYNC_REGION")
	_ = os.Unsetenv("GOJIBERRY_SYNC_STRATEGY")
	cfg := ConfigFromEnv()
	if cfg.Addr == "" || cfg.Region == "" {
		t.Fatalf("expected non-empty defaults, got %+v", cfg)
	}
	if cfg.Strategy != crdt.MergeLWW {
		t.Fatalf("expected Strategy to default to MergeLWW, got %v", cfg.Strategy)
	}

	t.Setenv("GOJIBERRY_SYNC_ADDR", ":9090")
	t.Setenv("GOJIBERRY_SYNC_REGION", "br-datacenter-1")
	t.Setenv("GOJIBERRY_SYNC_STRATEGY", "char-crdt")
	cfg = ConfigFromEnv()
	if cfg.Addr != ":9090" || cfg.Region != "br-datacenter-1" {
		t.Fatalf("expected env overrides to apply, got %+v", cfg)
	}
	if cfg.Strategy != crdt.MergeCharCRDT {
		t.Fatalf("expected GOJIBERRY_SYNC_STRATEGY=char-crdt to select MergeCharCRDT, got %v", cfg.Strategy)
	}
}

func postOps(t *testing.T, srv *httptest.Server, ops []crdt.Op) {
	t.Helper()
	body, err := json.Marshal(ops)
	if err != nil {
		t.Fatalf("marshal ops: %v", err)
	}
	resp, err := http.Post(srv.URL+"/ops", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /ops: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /ops: expected 204, got %d", resp.StatusCode)
	}
}

func getSnapshot(t *testing.T, srv *httptest.Server) []crdt.Op {
	t.Helper()
	resp, err := http.Get(srv.URL + "/ops")
	if err != nil {
		t.Fatalf("GET /ops: %v", err)
	}
	defer resp.Body.Close()
	var ops []crdt.Op
	if err := json.NewDecoder(resp.Body).Decode(&ops); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	return ops
}

func TestServerRelaysDeltaBetweenTwoClients(t *testing.T) {
	s := NewServer(Config{Region: "br-datacenter-1"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	phone := crdt.NewDoc("phone")
	phone.Put(crdt.Block{ID: "1", Kind: "heading", Content: "Fatura", Order: 0})
	postOps(t, srv, phone.Drain())

	// A second, brand-new device pulls the full snapshot to onboard.
	laptop := crdt.NewDoc("laptop")
	laptop.Apply(getSnapshot(t, srv)...)
	if len(laptop.Blocks()) != 1 {
		t.Fatalf("expected laptop to onboard 1 block from server snapshot, got %d", len(laptop.Blocks()))
	}

	// The laptop edits offline, then pushes its delta back through the
	// same relay -- the server never needs to know this is an edit
	// versus a create, it's all just Ops merged by LWW.
	laptop.Put(crdt.Block{ID: "2", Kind: "note", Content: "desconto", Order: 1})
	postOps(t, srv, laptop.Drain())

	final := getSnapshot(t, srv)
	if len(final) != 2 {
		t.Fatalf("expected server to hold 2 blocks after both pushes, got %d", len(final))
	}
}

// TestMergeCharCRDTServerConvergesConcurrentEdits is the regression
// case for the bug Config.Strategy fixes: relaying MergeCharCRDT
// clients through a MergeLWW server (the old, only option) doesn't
// merely lose the fine-grained merge, it silently empties the block's
// Content for any client applying an op the LWW-strategy server
// stripped CharOps from (see Config.Strategy's doc comment). A server
// configured with the matching strategy must both onboard a fresh
// client with real content and converge two concurrent edits to
// different parts of the same block, same as two directly-connected
// peers would (crdt.TestConcurrentEditSameBlockConvergesRegardlessOfOrder).
func TestMergeCharCRDTServerConvergesConcurrentEdits(t *testing.T) {
	s := NewServer(Config{Region: "br-datacenter-1", Strategy: crdt.MergeCharCRDT})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	a := crdt.NewDocWithStrategy("a", crdt.MergeCharCRDT)
	a.Put(crdt.Block{ID: "x", Kind: "paragraph", Content: "hello", Order: 0})
	postOps(t, srv, a.Drain())

	b := crdt.NewDocWithStrategy("b", crdt.MergeCharCRDT)
	b.Apply(getSnapshot(t, srv)...)
	if blk, ok := b.Get("x"); !ok || blk.Content != "hello" {
		t.Fatalf("expected b to onboard Content %q, got %+v (ok=%v)", "hello", blk, ok)
	}

	// Concurrent edits to different parts of the same block, each
	// pushed through the relay without ever syncing directly.
	a.Put(crdt.Block{ID: "x", Kind: "paragraph", Content: "hello there", Order: 0})
	b.Put(crdt.Block{ID: "x", Kind: "paragraph", Content: "well hello", Order: 0})
	postOps(t, srv, a.Drain())
	postOps(t, srv, b.Drain())

	c := crdt.NewDocWithStrategy("c", crdt.MergeCharCRDT)
	c.Apply(getSnapshot(t, srv)...)
	blk, ok := c.Get("x")
	if !ok {
		t.Fatal("expected block x to exist after merge")
	}
	if blk.Content != "well hello there" {
		t.Fatalf("expected both concurrent edits to survive the merge, got %q", blk.Content)
	}
}

func TestHealthReportsConfiguredRegion(t *testing.T) {
	s := NewServer(Config{Region: "br-datacenter-1"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if body["region"] != "br-datacenter-1" {
		t.Fatalf("expected region %q, got %+v", "br-datacenter-1", body)
	}
}

// TestCORSHeadersAllowCrossOriginClients is this relay's whole reason
// for existing: a client's own static assets are almost never served
// from the same origin as the sync relay (a different host, port, or
// both), and a browser's fetch() enforces CORS before a request ever
// reaches handleOps at all. Confirms both a preflight OPTIONS request
// (what the browser sends ahead of the client's actual POST, since a
// JSON body with an explicit Content-Type isn't a CORS "simple
// request") and the real POST/GET calls carry the header a
// cross-origin fetch() requires.
func TestCORSHeadersAllowCrossOriginClients(t *testing.T) {
	s := NewServer(Config{Region: "br-datacenter-1"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodOptions, srv.URL+"/ops", nil)
	if err != nil {
		t.Fatalf("building OPTIONS request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS /ops: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("OPTIONS /ops: expected 204, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("OPTIONS /ops: expected Access-Control-Allow-Origin: *, got %q", got)
	}

	getResp, err := http.Get(srv.URL + "/ops")
	if err != nil {
		t.Fatalf("GET /ops: %v", err)
	}
	defer getResp.Body.Close()
	if got := getResp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("GET /ops: expected Access-Control-Allow-Origin: *, got %q", got)
	}
}

func postPresence(t *testing.T, srv *httptest.Server, p Presence) {
	t.Helper()
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal presence: %v", err)
	}
	resp, err := http.Post(srv.URL+"/presence", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /presence: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /presence: expected 204, got %d", resp.StatusCode)
	}
}

func getPresence(t *testing.T, srv *httptest.Server, self string) []Presence {
	t.Helper()
	resp, err := http.Get(srv.URL + "/presence?replica=" + self)
	if err != nil {
		t.Fatalf("GET /presence: %v", err)
	}
	defer resp.Body.Close()
	var peers []Presence
	if err := json.NewDecoder(resp.Body).Decode(&peers); err != nil {
		t.Fatalf("decode presence: %v", err)
	}
	return peers
}

// TestPresenceRoundTripsAndExcludesSelf mirrors
// TestServerRelaysDeltaBetweenTwoClients's own two-client shape, but
// for the ephemeral presence channel instead of real document Ops:
// each replica's own last-broadcast position should be visible to
// every *other* replica, but never echoed back to itself.
func TestPresenceRoundTripsAndExcludesSelf(t *testing.T) {
	s := NewServer(Config{Region: "br-datacenter-1"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	postPresence(t, srv, Presence{Replica: "phone", Name: "Alice", Color: "#e11d48", BlockID: "b1", Col: 3})
	postPresence(t, srv, Presence{Replica: "laptop", Name: "Bob", Color: "#2563eb", BlockID: "b2", Col: 7})

	fromPhone := getPresence(t, srv, "phone")
	if len(fromPhone) != 1 || fromPhone[0].Replica != "laptop" || fromPhone[0].Name != "Bob" {
		t.Fatalf("GET /presence?replica=phone = %+v, want exactly laptop/Bob, not phone's own entry", fromPhone)
	}

	fromLaptop := getPresence(t, srv, "laptop")
	if len(fromLaptop) != 1 || fromLaptop[0].Replica != "phone" || fromLaptop[0].Col != 3 {
		t.Fatalf("GET /presence?replica=laptop = %+v, want exactly phone's entry", fromLaptop)
	}

	// A repeat POST from the same replica overwrites, not accumulates.
	postPresence(t, srv, Presence{Replica: "phone", Name: "Alice", Color: "#e11d48", BlockID: "b1", Col: 9})
	fromLaptop = getPresence(t, srv, "laptop")
	if len(fromLaptop) != 1 || fromLaptop[0].Col != 9 {
		t.Fatalf("expected the repeat POST to overwrite phone's own entry (Col=9), got %+v", fromLaptop)
	}
}

// TestPresenceExpiresAfterTTL confirms a replica that stops POSTing
// (tab closed, network dropped) eventually disappears from its peers'
// own GET /presence rather than lingering forever -- using Server's
// own injectable now func to fast-forward past presenceTTL
// deterministically instead of a real sleep.
func TestPresenceExpiresAfterTTL(t *testing.T) {
	s := NewServer(Config{Region: "br-datacenter-1"})
	current := time.Now()
	s.now = func() time.Time { return current }
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	postPresence(t, srv, Presence{Replica: "phone", Name: "Alice", BlockID: "b1"})
	if peers := getPresence(t, srv, "laptop"); len(peers) != 1 {
		t.Fatalf("expected phone's presence to be visible immediately after posting, got %+v", peers)
	}

	current = current.Add(presenceTTL + time.Second)
	if peers := getPresence(t, srv, "laptop"); len(peers) != 0 {
		t.Fatalf("expected phone's presence to have expired after presenceTTL, got %+v", peers)
	}
}

func TestGuardOriginsBlocksOtherSites(t *testing.T) {
	s := NewServer(Config{Guard: guard.Origins("https://app.example.com")})
	do := func(origin string) int {
		req := httptest.NewRequest(http.MethodPost, "/ops", strings.NewReader("[]"))
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	if do("https://app.example.com") != http.StatusNoContent || do("https://evil.example") != http.StatusForbidden {
		t.Fatal("origin allow-list not applied")
	}
}
