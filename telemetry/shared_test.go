package telemetry

import (
	"context"
	"sync"
	"testing"
	"time"
)

// memShared is a SharedStore in memory with a version that changes on every write.
type memShared struct {
	mu   sync.Mutex
	data []byte
	ver  int
	has  bool
	// beforeSave, if set, runs once inside SaveIf before the check (to let another process write first).
	beforeSave func()
}

func (m *memShared) Load(ctx context.Context) ([]byte, bool, error) {
	d, _, f, err := m.LoadVersion(ctx)
	return d, f, err
}
func (m *memShared) Save(ctx context.Context, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data, m.has = append([]byte(nil), data...), true
	m.ver++
	return nil
}
func (m *memShared) LoadVersion(context.Context) ([]byte, string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.has {
		return nil, "", false, nil
	}
	return append([]byte(nil), m.data...), string(rune('a' + m.ver)), true, nil
}
func (m *memShared) SaveIf(ctx context.Context, data []byte, version string) error {
	if f := m.beforeSave; f != nil {
		m.beforeSave = nil
		f()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := ""
	if m.has {
		cur = string(rune('a' + m.ver))
	}
	if cur != version {
		return ErrConflict
	}
	m.data, m.has = append([]byte(nil), data...), true
	m.ver++
	return nil
}

func batchOf(n int) Batch {
	return Batch{Day: "2030-05-01", Counts: []Count{{Feature, "find", n}}}
}

// Two processes on one store each count their own batches and neither loses the other's: the store holds the sum,
// and each, after a Sync, reports the sum.
func TestProcessesOnOneStoreAddUp(t *testing.T) {
	now := func() time.Time { return time.Date(2030, 5, 1, 12, 0, 0, 0, time.UTC) }
	st := &memShared{}
	ctx := context.Background()
	a, b := NewCounters(), NewCounters()
	if err := a.PersistShared(ctx, st, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := b.PersistShared(ctx, st, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	a.Add(batchOf(3), now())
	b.Add(batchOf(4), now())
	if err := a.Sync(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := b.Sync(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(ctx, now); err != nil { // a looks again and sees b's
		t.Fatal(err)
	}
	for name, c := range map[string]*Counters{"a": a, "b": b} {
		if got := dayTotal(c, "2030-05-01", Event{Feature, "find"}); got != 7 {
			t.Errorf("process %s counts %d, want 7", name, got)
		}
	}
	// counting again adds only the new batch, not the old ones a second time
	a.Add(batchOf(2), now())
	_ = a.Sync(ctx, now)
	_ = b.Sync(ctx, now)
	if got := dayTotal(b, "2030-05-01", Event{Feature, "find"}); got != 9 {
		t.Errorf("after one more batch of 2: %d, want 9", got)
	}
	_ = a.Sync(ctx, now)
	_ = a.Sync(ctx, now)
	if got := dayTotal(a, "2030-05-01", Event{Feature, "find"}); got != 9 {
		t.Errorf("syncing again changed the total to %d", got)
	}
}

// A write that loses the race is read again and added again: nothing is lost and nothing is counted twice.
func TestSharedWriteRetriesAfterAConflict(t *testing.T) {
	now := func() time.Time { return time.Date(2030, 5, 1, 12, 0, 0, 0, time.UTC) }
	st := &memShared{}
	ctx := context.Background()
	a, b := NewCounters(), NewCounters()
	_ = a.PersistShared(ctx, st, time.Hour, now)
	_ = b.PersistShared(ctx, st, time.Hour, now)
	a.Add(batchOf(5), now())
	b.Add(batchOf(6), now())
	st.beforeSave = func() { _ = b.Sync(ctx, now) } // b writes between a's read and a's write
	if err := a.Sync(ctx, now); err != nil {
		t.Fatal(err)
	}
	if got := dayTotal(a, "2030-05-01", Event{Feature, "find"}); got != 11 {
		t.Errorf("after the conflict a counts %d, want 11", got)
	}
	data, _, _, _ := st.LoadVersion(ctx)
	r, err := decodeSnapshot(data, now())
	if err != nil || r.days["2030-05-01"][Event{Feature, "find"}] != 11 {
		t.Errorf("the store holds %+v (%v), want 11", r, err)
	}
}

// Counts that arrive while a sync is running are kept for the next one.
func TestCountsDuringASyncAreKept(t *testing.T) {
	now := func() time.Time { return time.Date(2030, 5, 1, 12, 0, 0, 0, time.UTC) }
	st := &memShared{}
	ctx := context.Background()
	a := NewCounters()
	_ = a.PersistShared(ctx, st, time.Hour, now)
	a.Add(batchOf(1), now())
	st.beforeSave = func() { a.Add(batchOf(10), now()) }
	if err := a.Sync(ctx, now); err != nil {
		t.Fatal(err)
	}
	if got := dayTotal(a, "2030-05-01", Event{Feature, "find"}); got != 11 {
		t.Errorf("local total %d, want 11", got)
	}
	_ = a.Sync(ctx, now)
	data, _, _, _ := st.LoadVersion(ctx)
	r, _ := decodeSnapshot(data, now())
	if r.days["2030-05-01"][Event{Feature, "find"}] != 11 {
		t.Errorf("the store holds %d, want 11", r.days["2030-05-01"][Event{Feature, "find"}])
	}
}

func dayTotal(c *Counters, day string, e Event) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.days[day][e]
}
