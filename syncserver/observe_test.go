package syncserver

import (
	"net/http/httptest"
	"testing"

	"github.com/elaous/gojiberry/crdt"
	"github.com/elaous/gojiberry/observe"
)

func TestSyncReportsItsMerges(t *testing.T) {
	b := observe.NewBuiltin()
	old := observe.Default()
	observe.SetDefault(observe.New(observe.Options{Operator: b}))
	defer observe.SetDefault(old)
	srv := httptest.NewServer(NewServer(Config{}).Handler())
	defer srv.Close()
	d := crdt.NewDoc("a")
	d.Put(crdt.Block{ID: "1", Kind: "note", Content: "x", Order: 0})
	postOps(t, srv, d.Drain())
	if got := b.Count(observe.WorkRuns, "work", "sync_merge", "result", "ok"); got != 1 {
		t.Errorf("sync_merge ok = %d, want 1", got)
	}
}
