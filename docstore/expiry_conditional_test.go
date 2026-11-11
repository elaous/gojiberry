package docstore

import (
	"context"
	"errors"
	"testing"
)

// raceStore is a conditional store for tests.
type raceStore struct {
	*MemoryAdapter
	before func()
}

func (r *raceStore) DeleteIf(ctx context.Context, id, prev string) error {
	if r.before != nil {
		f := r.before
		r.before = nil
		f()
	}
	return r.MemoryAdapter.DeleteIf(ctx, id, prev)
}

// twoProcesses is one store shared by two processes.
func twoProcesses() (p1, p2 *ExpiringAdapter, race *raceStore, clk *fakeClock) {
	mem := NewMemoryAdapter()
	race = &raceStore{MemoryAdapter: mem}
	p1, clk = newExpiring(race)
	p2 = NewExpiringAdapter(mem, FixedTTL(day))
	p2.Now = clk.Now
	return p1, p2, race, clk
}

// A document another process renewed survives.
func TestSweepKeepsADocumentAnotherProcessSavedAtItsDeadline(t *testing.T) {
	ctx := context.Background()
	p1, p2, race, clk := twoProcesses()
	if err := p1.Save(ctx, "doc", []byte("old")); err != nil {
		t.Fatal(err)
	}
	clk.Add(day + 1)
	race.before = func() {
		if err := p2.Save(ctx, "doc", []byte("new")); err != nil {
			t.Error(err)
		}
	}
	n, err := p1.Sweep(ctx)
	if err != nil || n != 0 {
		t.Fatalf("sweep = %d, %v; want nothing deleted and no error", n, err)
	}
	d, err := p2.Load(ctx, "doc")
	if err != nil || string(d.Data) != "new" {
		t.Fatalf("the newer save was lost: %q, %v", d.Data, err)
	}
	if at, ok, err := p2.Expiry(ctx, "doc"); err != nil || !ok || !at.After(clk.Now()) {
		t.Fatalf("the record is not the live one: %v %v %v", at, ok, err)
	}
	if n, err := p1.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("second sweep = %d, %v", n, err)
	}
	clk.Add(day + 1)
	if n, err := p1.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("after the new deadline: %d, %v", n, err)
	}
	if _, err := p2.Load(ctx, "doc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("still there: %v", err)
	}
}

func TestSaveOverAnExpiredDocumentToleratesAnotherProcessRenewingIt(t *testing.T) {
	ctx := context.Background()
	p1, p2, race, clk := twoProcesses()
	if err := p1.Save(ctx, "doc", []byte("old")); err != nil {
		t.Fatal(err)
	}
	clk.Add(day + 1)
	race.before = func() {
		if err := p2.Save(ctx, "doc", []byte("theirs")); err != nil {
			t.Error(err)
		}
	}
	if err := p1.Save(ctx, "doc", []byte("mine")); err != nil {
		t.Fatal(err)
	}
	d, err := p1.Load(ctx, "doc")
	if err != nil || string(d.Data) != "mine" {
		t.Fatalf("%q, %v", d.Data, err)
	}
	if at, ok, _ := p1.Expiry(ctx, "doc"); !ok || !at.After(clk.Now()) {
		t.Fatalf("no live deadline: %v %v", at, ok)
	}
}

func TestSweepWithoutConditionalDeleteStillDeletes(t *testing.T) {
	ctx := context.Background()
	ea, clk := newExpiring(plainStore{NewMemoryAdapter()})
	if err := ea.Save(ctx, "doc", []byte("x")); err != nil {
		t.Fatal(err)
	}
	clk.Add(day + 1)
	if n, err := ea.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("%d, %v", n, err)
	}
}

// plainStore hides every optional method.
type plainStore struct{ m *MemoryAdapter }

func (p plainStore) Save(ctx context.Context, id string, d []byte) error { return p.m.Save(ctx, id, d) }
func (p plainStore) Load(ctx context.Context, id string) (Document, error) {
	return p.m.Load(ctx, id)
}
func (p plainStore) List(ctx context.Context) ([]Meta, error)    { return p.m.List(ctx) }
func (p plainStore) Delete(ctx context.Context, id string) error { return p.m.Delete(ctx, id) }

func TestSweepWhenTheStoreDeclinesConditionalDeleteStillDeletes(t *testing.T) {
	ctx := context.Background()
	ea, clk := newExpiring(decliningDeleter{plainStore{NewMemoryAdapter()}})
	if err := ea.Save(ctx, "doc", []byte("x")); err != nil {
		t.Fatal(err)
	}
	clk.Add(day + 1)
	if n, err := ea.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("%d, %v", n, err)
	}
	if _, err := ea.Load(ctx, "doc"); !errors.Is(err, ErrNotFound) {
		t.Errorf("still there: %v", err)
	}
}

type decliningDeleter struct{ plainStore }

func (decliningDeleter) DeleteIf(context.Context, string, string) error {
	return ErrConditionalUnsupported
}
