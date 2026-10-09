package sessionstore

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

// counting counts every call that reaches the store.
type counting struct {
	*docstore.MemoryAdapter
	loads, deletes, deleteIfs, lists, sessionLists, saveIfs atomic.Int64
}

func (c *counting) Load(ctx context.Context, id string) (docstore.Document, error) {
	c.loads.Add(1)
	return c.MemoryAdapter.Load(ctx, id)
}
func (c *counting) Delete(ctx context.Context, id string) error {
	c.deletes.Add(1)
	return c.MemoryAdapter.Delete(ctx, id)
}
func (c *counting) DeleteIf(ctx context.Context, id, prev string) error {
	c.deleteIfs.Add(1)
	return c.MemoryAdapter.DeleteIf(ctx, id, prev)
}
func (c *counting) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	c.saveIfs.Add(1)
	return c.MemoryAdapter.SaveIf(ctx, id, data, prev)
}
func (c *counting) List(ctx context.Context) ([]docstore.Meta, error) {
	c.lists.Add(1)
	return c.MemoryAdapter.List(ctx)
}
func (c *counting) ListPrefix(ctx context.Context, prefix string) ([]docstore.Meta, error) {
	c.lists.Add(1)
	if prefix == SessionPrefix {
		c.sessionLists.Add(1)
	}
	return c.MemoryAdapter.ListPrefix(ctx, prefix)
}

// A token that is not one of ours never reaches the store, whatever it holds after the dot.
func TestMalformedTokensNeverReachTheStore(t *testing.T) {
	c := &counting{MemoryAdapter: docstore.NewMemoryAdapter()}
	s, _ := New(c)
	ctx := context.Background()
	bad := []string{
		"", ".", "a.", ".5", "a.-1", "a.01", "a.+5", "a.99999999999999999999", "a.1/../x", "a.1/x", "a.5.6",
		strings.Repeat("x", 127) + ".5", // 129 bytes
		"a.٣",                           // an Arabic-Indic digit
		"a.５",                           // a full-width digit
		"a.5٣", "a. 5", "a.5 ", "a.0x5", "a.5e1", "a.1_0",
		"a." + "9223372036854775807", // fits an int64, beyond any real time
	}
	for _, tok := range bad {
		if u, ok, err := s.GetSession(ctx, tok); ok || err != nil || u != (auth.User{}) {
			t.Errorf("GetSession(%q) = %+v %v %v", tok, u, ok, err)
		}
		if err := s.DeleteSession(ctx, tok); err != nil {
			t.Errorf("DeleteSession(%q): %v", tok, err)
		}
		if st, ok, err := s.TakeState(ctx, tok); ok || err != nil || st != (auth.LoginState{}) {
			t.Errorf("TakeState(%q) = %+v %v %v", tok, st, ok, err)
		}
	}
	if n := c.loads.Load() + c.deletes.Load() + c.deleteIfs.Load() + c.saveIfs.Load() + c.lists.Load(); n != 0 {
		t.Fatalf("malformed tokens made %d store calls (loads %d, deletes %d)", n, c.loads.Load(), c.deletes.Load())
	}
	// A well-formed token does reach the store (the control of the test).
	_, _, _ = s.GetSession(ctx, "abc.5")
	if c.loads.Load() != 1 {
		t.Fatalf("a well-formed token must be looked up: %d loads", c.loads.Load())
	}
}

// lenientDelete is a store whose plain Delete of a missing record is not an error (as on S3,
// where a DELETE of a missing key succeeds), so only a conditional step can tell winner from loser.
type lenientDelete struct {
	*docstore.MemoryAdapter
	deletes atomic.Int64
}

func (l *lenientDelete) Delete(ctx context.Context, id string) error {
	l.deletes.Add(1)
	if err := l.MemoryAdapter.Delete(ctx, id); err != nil && !errors.Is(err, docstore.ErrNotFound) {
		return err
	}
	return nil
}

// conditionalOnlyLenient exposes the ConditionalAdapter methods only, with the lenient Delete.
type conditionalOnlyLenient struct {
	docstore.ConditionalAdapter
	l *lenientDelete
}

func (c conditionalOnlyLenient) Delete(ctx context.Context, id string) error {
	return c.l.Delete(ctx, id)
}

func TestConcurrentTakeHasOneWinnerOnAStoreWhosePlainDeleteNeverFails(t *testing.T) {
	for _, mode := range []string{"DeleteIf", "used mark"} {
		t.Run(mode, func(t *testing.T) {
			l := &lenientDelete{MemoryAdapter: docstore.NewMemoryAdapter()}
			var a docstore.Adapter = l
			if mode == "used mark" {
				a = conditionalOnlyLenient{ConditionalAdapter: l.MemoryAdapter, l: l}
			}
			s, _ := New(a)
			ctx := context.Background()
			for round := 0; round < 40; round++ {
				tok, err := s.PutState(ctx, auth.LoginState{Provider: "p", Nonce: "n"}, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				before := l.deletes.Load()
				var wins atomic.Int32
				var wg sync.WaitGroup
				start := make(chan struct{})
				for g := 0; g < 16; g++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						if _, ok, err := s.TakeState(ctx, tok); err != nil {
							t.Errorf("take: %v", err)
						} else if ok {
							wins.Add(1)
						}
					}()
				}
				close(start)
				wg.Wait()
				if wins.Load() != 1 {
					t.Fatalf("round %d: %d winners, want exactly 1", round, wins.Load())
				}
				// DeleteIf mode never uses the plain Delete; the marker path uses it once, by the winner.
				want := int64(0)
				if mode == "used mark" {
					want = 1
				}
				if got := l.deletes.Load() - before; got != want {
					t.Fatalf("round %d: %d plain deletes, want %d", round, got, want)
				}
			}
			if metas, _ := l.MemoryAdapter.List(ctx); len(metas) != 0 {
				t.Fatalf("left: %+v", metas)
			}
		})
	}
}

// Seconds, not nanoseconds: a very long TTL neither overflows the bucket nor makes a token that
// is issued but never validates.
func TestVeryLongTTLStillValidates(t *testing.T) {
	mem := docstore.NewMemoryAdapter()
	s, _ := New(mem)
	ctx := context.Background()
	for _, ttl := range []time.Duration{200 * 365 * 24 * time.Hour, math.MaxInt64, time.Duration(math.MaxInt64) - 1} {
		tok, err := s.PutSession(ctx, ana, ttl)
		if err != nil {
			t.Fatalf("ttl %v: %v", ttl, err)
		}
		if _, ok, err := s.GetSession(ctx, tok); !ok || err != nil {
			t.Fatalf("ttl %v: a token that was issued does not validate: %v %v", ttl, ok, err)
		}
	}
	// The bucket arithmetic itself, past the year 2262 where UnixNano overflows.
	far := time.Date(2400, 1, 1, 0, 30, 0, 0, time.UTC)
	b := sessionKind.bucketOf(far)
	if b <= 0 || !sessionKind.bucketEnd(b).Equal(time.Date(2400, 1, 1, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("bucket %d ends %v", b, sessionKind.bucketEnd(b))
	}
	// A fractional second rounds up, and a name beyond any real time is left alone by the sweep.
	if sessionKind.bucketOf(time.Unix(3600, 1)) != 2 || sessionKind.bucketOf(time.Unix(3600, 0)) != 1 {
		t.Fatal("rounding")
	}
	_ = mem.Save(ctx, SessionPrefix+"9223372036854775807/abc", []byte("x"))
	if n, err := s.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("sweep = %d, %v; a name with an absurd bucket is not ours", n, err)
	}
}

// The sessions are listed at the first sweep, when the hour changes and every ten minutes; the
// login states at every sweep.
func TestSweepScheduleListsSessionsRarely(t *testing.T) {
	c := &counting{MemoryAdapter: docstore.NewMemoryAdapter()}
	s, _ := New(c)
	now := time.Date(2030, 1, 1, 12, 5, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	ctx := context.Background()
	var sched sweepSchedule
	tick := func(d time.Duration) {
		now = now.Add(d)
		s.step(ctx, &sched)
	}
	s.step(ctx, &sched) // 12:05 first sweep
	if c.sessionLists.Load() != 1 || c.lists.Load() != 2 {
		t.Fatalf("first sweep: %d session listings of %d", c.sessionLists.Load(), c.lists.Load())
	}
	for i := 0; i < 9; i++ { // 12:06 .. 12:14
		tick(time.Minute)
	}
	if c.sessionLists.Load() != 1 || c.lists.Load() != 11 {
		t.Fatalf("by 12:14: %d session listings of %d (states every minute)", c.sessionLists.Load(), c.lists.Load())
	}
	tick(time.Minute) // 12:15: ten minutes since the last listing
	if c.sessionLists.Load() != 2 {
		t.Fatalf("12:15: %d session listings, want 2", c.sessionLists.Load())
	}
	for i := 0; i < 9; i++ { // 12:16 .. 12:24
		tick(time.Minute)
	}
	if c.sessionLists.Load() != 2 {
		t.Fatalf("12:24: %d", c.sessionLists.Load())
	}
	tick(time.Minute) // 12:25
	if c.sessionLists.Load() != 3 {
		t.Fatalf("12:25: %d", c.sessionLists.Load())
	}
	now = time.Date(2030, 1, 1, 12, 59, 0, 0, time.UTC)
	tick(0) // 12:59: more than ten minutes since the last listing: listed
	before := c.sessionLists.Load()
	tick(30 * time.Second) // 12:59:30, not due
	if c.sessionLists.Load() != before {
		t.Fatal("listed again within the hour and ten minutes")
	}
	tick(45 * time.Second) // 13:00:15: the hour changed
	if c.sessionLists.Load() != before+1 {
		t.Fatalf("the hour changed: %d listings, want %d", c.sessionLists.Load(), before+1)
	}
	// A clock that goes back lists again rather than waiting.
	now = now.Add(-time.Hour)
	s.step(ctx, &sched)
	if c.sessionLists.Load() != before+2 {
		t.Fatalf("clock went back: %d", c.sessionLists.Load())
	}
}

// A failed listing of the sessions is retried at the next sweep, not in ten minutes.
func TestSweepScheduleRetriesAFailedSessionListing(t *testing.T) {
	c := &counting{MemoryAdapter: docstore.NewMemoryAdapter()}
	f := &failList{counting: c}
	s, _ := New(f)
	now := time.Date(2030, 1, 1, 12, 5, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	var sched sweepSchedule
	f.fail.Store(true)
	s.step(context.Background(), &sched)
	f.fail.Store(false)
	now = now.Add(time.Minute)
	s.step(context.Background(), &sched)
	if c.sessionLists.Load() != 2 {
		t.Fatalf("session listings = %d, want 2 (the failed one retried)", c.sessionLists.Load())
	}
}

type failList struct {
	*counting
	fail atomic.Bool
}

func (f *failList) ListPrefix(ctx context.Context, prefix string) ([]docstore.Meta, error) {
	n, err := f.counting.ListPrefix(ctx, prefix)
	if f.fail.Load() && prefix == SessionPrefix {
		return nil, errors.New("list failed")
	}
	return n, err
}

// The sessions an hour-old sweep missed are still deleted once the schedule lists them.
func TestScheduledSweepStillDeletesDeadSessions(t *testing.T) {
	mem := docstore.NewMemoryAdapter()
	s, _ := New(mem)
	now := time.Date(2030, 1, 1, 12, 5, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	ctx := context.Background()
	if _, err := s.PutSession(ctx, ana, 10*time.Minute); err != nil { // dead at 12:15, hour ends 13:00
		t.Fatal(err)
	}
	var sched sweepSchedule
	s.step(ctx, &sched)
	for now.Before(time.Date(2030, 1, 1, 13, 30, 0, 0, time.UTC)) {
		now = now.Add(time.Minute)
		s.step(ctx, &sched)
	}
	if metas, _ := mem.List(ctx); len(metas) != 0 {
		t.Fatalf("left after the hour: %+v", metas)
	}
}
