package syncserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/elaous/gojiberry/crdt"
)

func TestConfigFromEnvDefaultsAndOverrides(t *testing.T) {
	os.Unsetenv("GOJIBERRY_SYNC_ADDR")
	os.Unsetenv("GOJIBERRY_SYNC_REGION")
	cfg := ConfigFromEnv()
	if cfg.Addr == "" || cfg.Region == "" {
		t.Fatalf("expected non-empty defaults, got %+v", cfg)
	}

	t.Setenv("GOJIBERRY_SYNC_ADDR", ":9090")
	t.Setenv("GOJIBERRY_SYNC_REGION", "br-datacenter-1")
	cfg = ConfigFromEnv()
	if cfg.Addr != ":9090" || cfg.Region != "br-datacenter-1" {
		t.Fatalf("expected env overrides to apply, got %+v", cfg)
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
