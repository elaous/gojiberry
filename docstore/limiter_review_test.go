//go:build !js

package docstore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreLimiterBoundsARecordFromAFarFutureClock(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryAdapter()
	behind, ahead := newClock(), newClock()
	ahead.Add(3 * time.Hour)
	la := newStoreLimiter(store, ahead, "skew", 60, 1)
	lb := newStoreLimiter(store, behind, "skew", 60, 1)
	if ok, _ := la.Allow(ctx, "k"); !ok {
		t.Fatal("first request through the fast replica")
	}
	if ok, _ := lb.Allow(ctx, "k"); ok {
		t.Fatal("the bucket is empty")
	}
	behind.Add(10 * time.Second)
	if ok, wait := lb.Allow(ctx, "k"); !ok {
		t.Fatalf("a record written by a clock 3 hours ahead locks the other replicas out (wait %v)", wait)
	}
}

type flakyStore struct {
	*MemoryAdapter
	bad          string
	beforeDelete func(id string)
	beforeSave   func(id string)
}

func (f *flakyStore) Load(ctx context.Context, id string) (Document, error) {
	if f.bad != "" && strings.Contains(id, f.bad) {
		return Document{}, errors.New("disk on fire")
	}
	return f.MemoryAdapter.Load(ctx, id)
}

func (f *flakyStore) DeleteIf(ctx context.Context, id, prev string) error {
	if f.beforeDelete != nil {
		f.beforeDelete(id)
	}
	return f.MemoryAdapter.DeleteIf(ctx, id, prev)
}

func (f *flakyStore) Delete(ctx context.Context, id string) error {
	if f.beforeDelete != nil {
		f.beforeDelete(id)
	}
	return f.MemoryAdapter.Delete(ctx, id)
}

func (f *flakyStore) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	if f.beforeSave != nil {
		f.beforeSave(id)
	}
	return f.MemoryAdapter.SaveIf(ctx, id, data, prev)
}

func TestSweepRateLimitsSkipsARecordItCannotReadAndGoesOn(t *testing.T) {
	ctx := context.Background()
	store := &flakyStore{MemoryAdapter: NewMemoryAdapter(), bad: "/zzbad"}
	clk := newClock()
	l := newStoreLimiter(store, clk, "s", 60, 3)
	for i := range 4 {
		l.Allow(ctx, fmt.Sprint("idle", i))
	}
	_ = store.Save(ctx, RateLimitPrefix+"s/zzbad", []byte(`{"t":0,"a":1,"f":1}`))
	clk.Add(10 * time.Minute)
	n, err := SweepRateLimits(ctx, store, clk.Now())
	if err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Errorf("the failure is reported: %v", err)
	}
	if n != 4 {
		t.Errorf("a record that cannot be read stopped the sweep: deleted %d of 4", n)
	}
}

func TestSweepRateLimitsDeletesOnlyTheRecordItRead(t *testing.T) {
	ctx := context.Background()
	{
		store := &flakyStore{MemoryAdapter: NewMemoryAdapter()}
		clk := newClock()
		l := newStoreLimiter(store, clk, "s", 60, 3)
		l.Allow(ctx, "k")
		id := l.recordID("k")
		clk.Add(10 * time.Minute) // full for ten minutes: stale
		fired := false
		store.beforeDelete = func(did string) {
			if did == id && !fired {
				fired = true
				if ok, _ := l.Allow(ctx, "k"); !ok {
					t.Error("the returning caller is allowed")
				}
			}
		}
		n, err := SweepRateLimits(ctx, store, clk.Now())
		if err != nil || !fired {
			t.Fatalf("%s: %d %v fired=%v", "conditional delete", n, err, fired)
		}
		if n != 0 {
			t.Errorf("%s: deleted %d", "conditional delete", n)
		}
		doc, err := store.MemoryAdapter.Load(ctx, id)
		if err != nil {
			t.Fatalf("%s: the record of a caller who came back was deleted: %v", "conditional delete", err)
		}
		var st limitState
		if jsonUnmarshal(doc.Data, &st) != nil || st.Tokens != 2 {
			t.Errorf("%s: %s", "conditional delete", doc.Data)
		}
	}
}

func TestSweepRateLimitsWithoutAConditionalDelete(t *testing.T) {
	ctx := context.Background()
	inner := &flakyStore{MemoryAdapter: NewMemoryAdapter()}
	store := noDeleteIf{inner}
	clk := newClock()
	l := newStoreLimiter(store, clk, "s", 60, 3)
	l.Allow(ctx, "k")
	id := l.recordID("k")
	clk.Add(10 * time.Minute)
	fired := false
	inner.beforeSave = func(sid string) {
		if sid == id && !fired {
			fired = true
			_ = inner.Save(ctx, id, []byte(`{"t":1,"a":1,"f":`+fmt.Sprint(clk.Now().UnixNano()+int64(time.Hour))+`}`))
		}
	}
	n, err := SweepRateLimits(ctx, store, clk.Now())
	if err != nil || !fired || n != 0 {
		t.Fatalf("%d %v fired=%v", n, err, fired)
	}
	if _, err := inner.MemoryAdapter.Load(ctx, id); err != nil {
		t.Errorf("the changed record was deleted: %v", err)
	}
	inner.beforeSave = nil
	_ = inner.MemoryAdapter.Delete(ctx, id)
	l.Allow(ctx, "k2")
	clk.Add(10 * time.Minute)
	if n, err := SweepRateLimits(ctx, store, clk.Now()); err != nil || n != 1 {
		t.Errorf("%d %v", n, err)
	}
}

type noDeleteIf struct{ f *flakyStore }

func (n noDeleteIf) Save(ctx context.Context, id string, d []byte) error { return n.f.Save(ctx, id, d) }
func (n noDeleteIf) Load(ctx context.Context, id string) (Document, error) {
	return n.f.Load(ctx, id)
}
func (n noDeleteIf) List(ctx context.Context) ([]Meta, error)    { return n.f.List(ctx) }
func (n noDeleteIf) Delete(ctx context.Context, id string) error { return n.f.Delete(ctx, id) }
func (n noDeleteIf) SaveIf(ctx context.Context, id string, d []byte, prev string) error {
	return n.f.SaveIf(ctx, id, d, prev)
}

func TestSweepRateLimitsKeepsAtMostTheCapAndEvictsTheOldest(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryAdapter()
	clk := newClock()
	l := newStoreLimiter(store, clk, "flood", 1, 5)
	for i := range 50 { // 50 callers, none idle: an unauthenticated flood of keys
		l.Allow(ctx, fmt.Sprint("ip-", i))
		time.Sleep(time.Millisecond) // distinct write times
	}
	n, err := SweepRateLimitsMax(ctx, store, clk.Now(), 20)
	if err != nil || n != 30 {
		t.Fatalf("%d %v", n, err)
	}
	metas, _ := ListPrefix(ctx, store, RateLimitPrefix)
	if len(metas) != 20 {
		t.Fatalf("%d records left", len(metas))
	}
	left := map[string]bool{}
	for _, m := range metas {
		left[m.ID] = true
	}
	for i := 30; i < 50; i++ { // the newest survive
		if !left[l.recordID(fmt.Sprint("ip-", i))] {
			t.Errorf("ip-%d, one of the newest, was evicted", i)
		}
	}
	if n, err := SweepRateLimits(ctx, store, clk.Now()); err != nil || n != 0 {
		t.Errorf("default cap: %d %v", n, err)
	}
	if MaxRateLimitRecords < 1000 {
		t.Errorf("MaxRateLimitRecords = %d", MaxRateLimitRecords)
	}
}

func TestDeleteIfDeletesOnlyWhatWasRead(t *testing.T) {
	ctx := context.Background()
	sq, err := NewSQLiteAdapter(ctx, filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sq.Close() })
	fs, err := NewLocalFSAdapter(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exp := NewExpiringAdapter(NewMemoryAdapter(), FixedTTL(time.Hour))
	for name, a := range map[string]ConditionalDeleter{"memory": NewMemoryAdapter(), "sqlite": sq, "localfs": fs, "expiring": exp} {
		id := RateLimitPrefix + "x/y" // a system key: deletable conditionally through the expiring adapter too
		if err := a.Save(ctx, id, []byte("one")); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := a.DeleteIf(ctx, id, Digest([]byte("other"))); !errors.Is(err, ErrConflict) {
			t.Errorf("%s: stale delete: %v", name, err)
		}
		if err := a.DeleteIf(ctx, id, ""); !errors.Is(err, ErrConflict) {
			t.Errorf("%s: an empty digest never matches: %v", name, err)
		}
		if _, err := a.Load(ctx, id); err != nil {
			t.Fatalf("%s: a refused delete deleted: %v", name, err)
		}
		if err := a.DeleteIf(ctx, id, Digest([]byte("one"))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := a.Load(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: still there: %v", name, err)
		}
		if err := a.DeleteIf(ctx, id, Digest([]byte("one"))); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: deleting what is gone: %v", name, err)
		}
	}
	_ = exp.Save(ctx, "doc", []byte("x"))
	if err := exp.DeleteIf(ctx, "doc", Digest([]byte("x"))); !errors.Is(err, ErrConditionalUnsupported) {
		t.Errorf("a document: %v", err)
	}
}

func TestSupportsConditionalProbesTheStore(t *testing.T) {
	ctx := context.Background()
	if !SupportsConditional(ctx, NewMemoryAdapter()) {
		t.Error("memory is conditional")
	}
	if SupportsConditional(ctx, plain{NewMemoryAdapter()}) {
		t.Error("a plain store is not")
	}
	if SupportsConditional(ctx, refuses{NewMemoryAdapter()}) {
		t.Error("a store that refuses SaveIf is not")
	}
	m := NewMemoryAdapter()
	SupportsConditional(ctx, m)
	if metas, _ := m.List(ctx); len(metas) != 0 {
		t.Errorf("the probe left records: %v", metas)
	}
}

type refuses struct{ *MemoryAdapter }

func (refuses) SaveIf(context.Context, string, []byte, string) error {
	return ErrConditionalUnsupported
}

type fakeConditional struct{ *MemoryAdapter }

func (f fakeConditional) SaveIf(ctx context.Context, id string, data []byte, _ string) error {
	return f.Save(ctx, id, data)
}

func TestSupportsConditionalIsNotFooledByASaveIfThatIgnoresItsCondition(t *testing.T) {
	if SupportsConditional(context.Background(), fakeConditional{NewMemoryAdapter()}) {
		t.Error("a SaveIf that overwrites is not a conditional write")
	}
}
