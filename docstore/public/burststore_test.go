package public

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
)

func TestBurstWatchIsSharedByReplicasThroughTheStore(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, func(o *Options) {
		o.Config.PriorityBurstDocs = 2
		o.SharedBurstWatch = true
	})
	a := e.svc
	b, err := New(Options{Store: e.store, Now: e.clk.Now, Config: e.svc.cfg, SharedBurstWatch: true})
	if err != nil {
		t.Fatal(err)
	}
	const net = "203.0.113.0/24"
	h, err := a.reporterHash(ctx, net)
	if err != nil {
		t.Fatal(err)
	}
	if over, _, err := a.burst.note(ctx, h, "doc1", e.clk.Now(), 2); err != nil || over {
		t.Fatalf("%v %v", over, err)
	}
	if over, _, err := b.burst.note(ctx, h, "doc2", e.clk.Now(), 2); err != nil || over {
		t.Fatalf("%v %v", over, err)
	}
	over, first, err := a.burst.note(ctx, h, "doc3", e.clk.Now(), 2)
	if err != nil || !over || !first {
		t.Fatalf("the third document is over and flags it: %v %v %v", over, first, err)
	}
	over, first, _ = b.burst.note(ctx, h, "doc4", e.clk.Now(), 2)
	if !over || first {
		t.Errorf("over %v first %v: the alarm is once an hour for all replicas", over, first)
	}
	if _, _, err := a.burst.note(ctx, "someone-else", "docA", e.clk.Now(), 2); err != nil {
		t.Fatal(err)
	}
	if over, _, _ := a.burst.note(ctx, "someone-else", "docA", e.clk.Now(), 2); over {
		t.Error("the same document twice is one")
	}
	if a.Health().PriorityBursts != 1 || b.Health().PriorityBursts != 1 {
		t.Errorf("bursts: %d and %d", a.Health().PriorityBursts, b.Health().PriorityBursts)
	}
	// The records name no address.
	for _, k := range e.keys(BurstKeyPrefix) {
		if strings.Contains(k, "203.0.113") {
			t.Errorf("a burst record names the network: %s", k)
		}
	}
	e.clk.Add(2 * hour)
	if a.Health().PriorityBursts != 0 {
		t.Errorf("an old burst still counts: %d", a.Health().PriorityBursts)
	}
	if n, err := a.burst.sweep(ctx, e.clk.Now()); err != nil || n != 2 {
		t.Errorf("swept %d: %v", n, err)
	}
	if left := e.keys(BurstKeyPrefix); len(left) != 0 {
		t.Errorf("left: %v", left)
	}
}

func TestSharedBurstWatchFlagsOneNetworkOnceThroughTheRoutes(t *testing.T) {
	logs := captureLog(t)
	e := newEnv(t, func(o *Options) { behindProxy(o); o.Config.PriorityBurstDocs = 3; o.SharedBurstWatch = true })
	var docs []docResponse
	for range 5 {
		tok, _ := e.session()
		docs = append(docs, e.create(tok, "x"))
	}
	for i, d := range docs[:3] {
		e.want(e.reportCat(d.ViewID, CategoryIllegal, "x", fmt.Sprintf("203.0.113.%d", i+1)), 202)
	}
	if e.svc.Health().PriorityBursts != 0 {
		t.Fatal("flagged at the limit")
	}
	e.want(e.reportCat(docs[3].ViewID, CategoryCSAM, "x", "203.0.113.10"), 202)
	e.want(e.reportCat(docs[4].ViewID, CategoryCSAM, "x", "203.0.113.11"), 202)
	if h := e.svc.Health(); h.PriorityBursts != 1 || !h.OK {
		t.Fatalf("health %+v", h)
	}
	if n := strings.Count(logs.String(), "priority reports for more than 3 different documents"); n != 1 {
		t.Fatalf("logged %d times: %s", n, logs.String())
	}
	if len(e.keys(BurstKeyPrefix)) != 1 {
		t.Errorf("records: %v", e.keys(BurstKeyPrefix))
	}
}

func TestSharedBurstWatchNeedsAConditionalStore(t *testing.T) {
	_, err := New(Options{Store: plainOnly{docstore.NewMemoryAdapter()}, SharedBurstWatch: true,
		Config: Config{AdminToken: adminToken, NoProxy: true}})
	if err == nil {
		t.Fatal("accepted a store with no conditional writes")
	}
}

func TestGlobalReportCountIsRecountedFromTheStoreByEveryReplica(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, nil)
	b, err := New(Options{Store: e.store, Now: e.clk.Now, Config: e.svc.cfg})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		tok, _ := e.session()
		d := e.create(tok, "x")
		e.want(e.report(d.ViewID, "spam", fmt.Sprintf("198.51.100.%d", i+1)), 202)
	}
	if all, _ := b.reports.held(""); all != 0 {
		t.Fatalf("the other replica knew of %d reports before a sweep", all)
	}
	if _, err := b.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if all, _ := b.reports.held(""); all != 3 {
		t.Errorf("after its sweep the other replica counts %d reports, want 3", all)
	}
}
