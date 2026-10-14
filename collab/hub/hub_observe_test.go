package hub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/observe"
)

// capture makes the process observer keep its totals in a builtin sink for the test.
func capture(t *testing.T) *observe.Builtin {
	t.Helper()
	b := observe.NewBuiltin()
	old := observe.Default()
	observe.SetDefault(observe.New(observe.Options{Operator: b}))
	t.Cleanup(func() { observe.SetDefault(old) })
	return b
}

// The hub reports its own work: batches of operations and how they end, and the rooms it holds.
func TestHubReportsItsOperationsAndRooms(t *testing.T) {
	b := capture(t)
	h := New(Config{})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	post := func(path, body string) int {
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if post("/docs/one/ops", "[]") != http.StatusNoContent || post("/docs/two/ops", "[]") != http.StatusNoContent {
		t.Fatal("empty batches are accepted")
	}
	if got := b.Count(observe.WorkRuns, "work", "hub_ops", "result", "ok"); got != 2 {
		t.Errorf("hub_ops ok = %d, want 2", got)
	}
	series, _, _ := b.Snapshot()
	rooms := -1.0
	for _, s := range series {
		if s.Metric == observe.WorkOpen && s.Labels["work"] == "hub_rooms" {
			rooms = s.Last
		}
	}
	if rooms != 2 {
		t.Errorf("open rooms = %v, want 2", rooms)
	}
	if post("/docs/one/ops", "{not json") != http.StatusBadRequest {
		t.Fatal("bad JSON is refused")
	}
	if b.Count(observe.WorkRuns, "work", "hub_ops") != 2 {
		t.Error("a request refused before any work is not counted as work")
	}
}
