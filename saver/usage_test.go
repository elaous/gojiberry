package saver

import (
	"context"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

func TestUsageMeasuresTheStoreAgainstItsCapacity(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}, {Name: "sized", Adapter: docstore.NewMemoryAdapter(), Capacity: 4000}}}
	// No capacity and an adapter that gives none: used only.
	a, _ := s.Save(ctx, "m", docfile.JSON, named("a"))
	u, err := s.Usage(ctx, "m")
	if err != nil || u.Known || u.Total != 0 || u.Used < int64(a.Size) || u.Documents != 1 {
		t.Fatalf("%+v %v", u, err)
	}
	// With a capacity: the percentage, and what is left.
	e, _ := s.Save(ctx, "sized", docfile.JSON, named("b"))
	_ = s.Trash(ctx, "sized", e.ID)
	_, _ = s.Save(ctx, "sized", docfile.JSON, named("c"))
	u, _ = s.Usage(ctx, "sized")
	if !u.Known || u.Total != 4000 || u.Documents != 1 || u.Trashed != 1 || u.Left != u.Total-u.Used || u.Percent <= 0 || u.Percent >= 100 {
		t.Fatalf("%+v", u)
	}
	want := float64(u.Used) / 4000 * 100
	if u.Percent < want-0.001 || u.Percent > want+0.001 {
		t.Errorf("percent %v want %v", u.Percent, want)
	}
	// Over capacity: nothing left, over 100%.
	tiny := &Saver{Targets: []Target{{Name: "t", Adapter: docstore.NewMemoryAdapter(), Capacity: 10}}}
	_, _ = tiny.Save(ctx, "t", docfile.JSON, named("big"))
	if u, _ = tiny.Usage(ctx, "t"); u.Left != 0 || u.Percent <= 100 {
		t.Errorf("%+v", u)
	}
	if _, err := s.Usage(ctx, "nope"); err == nil {
		t.Error("unknown target")
	}
	// The adapter's own quota is used when there is no Capacity.
	rep := &Saver{Targets: []Target{{Name: "q", Adapter: quotaAdapter{docstore.NewMemoryAdapter(), 1 << 20}}}}
	if u, _ = rep.Usage(ctx, "q"); !u.Known || u.Total != 1<<20 {
		t.Errorf("%+v", u)
	}
	// A session shows it.
	ss := NewSession(s, func() docfile.Document { return docfile.Document{} }, nil)
	ss.Pending.Inline = true
	_ = ss.SetTarget(ctx, "sized")
	if p := ss.Props(); p.Capacity == nil || p.Capacity.Total != 4000 || p.Capacity.Used == 0 || p.Capacity.Documents != 1 {
		t.Errorf("%+v", p.Capacity)
	}
}

type quotaAdapter struct {
	docstore.Adapter
	total int64
}

func (q quotaAdapter) Usage(context.Context) (docstore.Quota, error) {
	return docstore.Quota{Total: q.total}, nil
}
