package docstore

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type slowPlainStore struct {
	*MemoryAdapter
}

func (p slowPlainStore) Load(ctx context.Context, id string) (Document, error) {
	d, err := p.MemoryAdapter.Load(ctx, id)
	time.Sleep(time.Millisecond)
	return d, err
}

func TestLockedAdapterIsAConformingStore(t *testing.T) {
	testAdapterConformance(t, func() Adapter { return NewLockedAdapter(NewMemoryAdapter(), &MemoryKeyLocker{}) })
}

func TestLockedAdapterWritesConditionally(t *testing.T) {
	ctx := context.Background()
	a := NewLockedAdapter(NewMemoryAdapter(), &MemoryKeyLocker{})
	if !SupportsConditional(ctx, a) {
		t.Fatal("the probe says it does not write conditionally")
	}
	if err := a.SaveIf(ctx, "x", []byte("1"), ""); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveIf(ctx, "x", []byte("2"), ""); !errors.Is(err, ErrConflict) {
		t.Errorf("create over an existing one: %v", err)
	}
	if err := a.SaveIf(ctx, "x", []byte("2"), Digest([]byte("stale"))); !errors.Is(err, ErrConflict) {
		t.Errorf("stale digest: %v", err)
	}
	if err := a.SaveIf(ctx, "x", []byte("2"), Digest([]byte("1"))); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveIf(ctx, "gone", []byte("2"), Digest([]byte("1"))); !errors.Is(err, ErrConflict) {
		t.Errorf("update of a missing document: %v", err)
	}
	if err := a.DeleteIf(ctx, "x", Digest([]byte("1"))); !errors.Is(err, ErrConflict) {
		t.Errorf("delete of a changed one: %v", err)
	}
	if err := a.DeleteIf(ctx, "x", Digest([]byte("2"))); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteIf(ctx, "x", Digest([]byte("2"))); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete of a missing one: %v", err)
	}
}

func TestLockedAdapterLosesNoIncrementOfSeveralWriters(t *testing.T) {
	ctx := context.Background()
	inner := slowPlainStore{NewMemoryAdapter()}
	locks := &MemoryKeyLocker{}
	const writers, each = 6, 10
	var wg sync.WaitGroup
	var retries atomic.Int64
	for range writers {
		a := NewLockedAdapter(inner, locks)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				for {
					prev, n := "", 0
					if d, err := a.Load(ctx, "n"); err == nil {
						prev = Digest(d.Data)
						n, _ = strconv.Atoi(string(d.Data))
					}
					err := a.SaveIf(ctx, "n", []byte(strconv.Itoa(n+1)), prev)
					if err == nil {
						break
					}
					if !errors.Is(err, ErrConflict) {
						t.Error(err)
						return
					}
					retries.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	d, _ := inner.MemoryAdapter.Load(ctx, "n")
	if string(d.Data) != strconv.Itoa(writers*each) {
		t.Errorf("counter %s, want %d (retries %d)", d.Data, writers*each, retries.Load())
	}
}

func TestLockedAdapterHolderWritesThroughItAndOthersWait(t *testing.T) {
	ctx := context.Background()
	a := NewLockedAdapter(NewMemoryAdapter(), &MemoryKeyLocker{})
	unlock, err := a.Lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Save(ctx, "x", []byte("1")); err != nil { // would deadlock if Lock shared the write key
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		u, err := a.Lock(ctx)
		if err == nil {
			u()
		}
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("a second holder got the lock")
	case <-time.After(30 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("the second holder never got it")
	}
}

func TestMemoryKeyLockerGivesUpWhenTheContextEnds(t *testing.T) {
	m := &MemoryKeyLocker{}
	unlock, _ := m.LockKey(context.Background(), "k")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := m.LockKey(ctx, "k"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("%v", err)
	}
	unlock()
	u2, err := m.LockKey(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	u2()
	if len(m.held) != 0 {
		t.Errorf("locks are forgotten when nobody holds or waits: %d", len(m.held))
	}
}
