package telemetry

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Several server processes on one store (roadmap 57). Each process counts the batches it receives; the store holds
// the sum. A process does not overwrite the record with its own totals (the last write would win and the others'
// counts would be lost): it adds what it counted since it last looked, to what the store holds now, and writes the
// sum only if the record is still the one it read (a conditional write); if another process wrote in between it
// reads again and adds again. What it has counted but not written is its delta against the totals it last saw.

// ErrConflict is returned by SharedStore.SaveIf when the record is no longer the version that was read.
var ErrConflict = errors.New("telemetry: the stored totals changed")

// SharedStore is a Store whose write is conditional, so processes sharing it can add to each other's totals.
type SharedStore interface {
	Store
	// LoadVersion reads the record and a token for the version read ("" when there is none).
	LoadVersion(ctx context.Context) (data []byte, version string, found bool, err error)
	// SaveIf writes data if the record is still at version (version "" means: if there is no record yet),
	// else it writes nothing and returns ErrConflict.
	SaveIf(ctx context.Context, data []byte, version string) error
}

const syncTries = 8

// PersistShared is Persist for several processes on one store. The totals are loaded at once; after a change they
// are added to the store's after at most delay (a burst is one write), and Sync does the same on request, so that a
// report can show the other processes' counts too.
func (c *Counters) PersistShared(ctx context.Context, st SharedStore, delay time.Duration, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	if delay <= 0 {
		delay = 5 * time.Second
	}
	c.mu.Lock()
	c.shared = st
	c.base = &decoded{days: map[string]map[Event]int{}}
	c.mu.Unlock()
	loadErr := c.syncShared(ctx, now)
	var mu sync.Mutex
	var timer *time.Timer
	c.mu.Lock()
	c.onChange = func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			return
		}
		timer = time.AfterFunc(delay, func() {
			mu.Lock()
			timer = nil
			mu.Unlock()
			err := c.syncShared(context.WithoutCancel(ctx), now)
			c.mu.Lock()
			c.saveErr = err
			c.mu.Unlock()
		})
	}
	c.mu.Unlock()
	return loadErr
}

// Sync adds what this process counted since the last time to the shared store and takes the store's totals as its
// own, so the next report includes the other processes. Without PersistShared it does nothing.
func (c *Counters) Sync(ctx context.Context, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	return c.syncShared(ctx, now)
}

// Shared says whether the totals are kept in a store several processes may count into.
func (c *Counters) Shared() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.shared != nil }

func (c *Counters) syncShared(ctx context.Context, now func() time.Time) error {
	c.mu.Lock()
	st := c.shared
	c.mu.Unlock()
	if st == nil {
		return nil
	}
	var last error
	for range syncTries {
		c.mu.Lock()
		local := cloneDecoded(&decoded{days: c.days, accepted: c.accepted, refused: c.refused, seed: c.seedLocked()})
		base := cloneDecoded(c.base)
		c.mu.Unlock()

		data, version, found, err := st.LoadVersion(ctx)
		if err != nil {
			return err
		}
		remote := &decoded{days: map[string]map[Event]int{}}
		if found {
			if r, derr := decodeSnapshot(data, now()); derr == nil {
				remote = r
			} // a record that cannot be read is replaced, not trusted
		}
		merged := mergeDelta(remote, local, base)
		if merged.seed == nil {
			merged.seed = local.seed
		}
		changed := !equalDecoded(merged, remote) // nothing counted and nothing stored: no record is made
		if changed {
			if err := st.SaveIf(ctx, encodeDecoded(merged), version); err != nil {
				if errors.Is(err, ErrConflict) {
					last = err
					continue
				}
				return err
			}
		}
		// adopt the sum; what was counted while this ran is added on top of it
		c.mu.Lock()
		now2 := cloneDecoded(&decoded{days: c.days, accepted: c.accepted, refused: c.refused})
		during := mergeDelta(&decoded{days: map[string]map[Event]int{}}, now2, local)
		c.days, c.accepted, c.refused = addDecoded(merged, during)
		c.seed = merged.seed
		c.base = merged
		c.mu.Unlock()
		return nil
	}
	return last
}

func cloneDecoded(d *decoded) *decoded {
	out := &decoded{days: map[string]map[Event]int{}, seed: append([]byte(nil), d.seed...)}
	if d.seed == nil {
		out.seed = nil
	}
	out.accepted, out.refused = d.accepted, d.refused
	for day, m := range d.days {
		mm := make(map[Event]int, len(m))
		for e, n := range m {
			mm[e] = n
		}
		out.days[day] = mm
	}
	return out
}

// mergeDelta is remote plus what local gained over base (a total that is lower than its base, because a day was
// dropped by the retention, gains nothing). The result is a new value.
func mergeDelta(remote, local, base *decoded) *decoded {
	out := cloneDecoded(remote)
	if remote.seed != nil {
		out.seed = remote.seed
	}
	out.accepted += max(0, local.accepted-base.accepted)
	out.refused += max(0, local.refused-base.refused)
	for day, m := range local.days {
		for e, n := range m {
			d := n - base.days[day][e]
			if d <= 0 {
				continue
			}
			if out.days[day] == nil {
				out.days[day] = map[Event]int{}
			}
			out.days[day][e] = min(out.days[day][e]+d, MaxTotal)
		}
	}
	out.accepted, out.refused = min(out.accepted, MaxTotal), min(out.refused, MaxTotal)
	return out
}

func addDecoded(a, b *decoded) (map[string]map[Event]int, int, int) {
	s := mergeDelta(a, b, &decoded{days: map[string]map[Event]int{}})
	return s.days, s.accepted, s.refused
}

func equalDecoded(a, b *decoded) bool {
	if a.accepted != b.accepted || a.refused != b.refused || len(a.days) != len(b.days) {
		return false
	}
	for day, m := range a.days {
		o := b.days[day]
		if len(m) != len(o) {
			return false
		}
		for e, n := range m {
			if o[e] != n {
				return false
			}
		}
	}
	return true
}

func encodeDecoded(d *decoded) []byte {
	s := snapshot{Version: snapshotVersion, Accepted: d.accepted, Refused: d.refused, Days: map[string][]Total{}}
	if d.seed != nil {
		s.Seed = hex.EncodeToString(d.seed)
	}
	for day, m := range d.days {
		for e, n := range m {
			s.Days[day] = append(s.Days[day], Total{e.Name, e.Value, n})
		}
	}
	out, _ := json.Marshal(s)
	return out
}
