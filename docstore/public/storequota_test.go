package public

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

func newStoreQuota(t *testing.T, store docstore.Adapter, limit int64, clock *atomic.Int64) *StoreQuota {
	t.Helper()
	q, err := NewStoreQuota(store, limit, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	q.Now = func() time.Time { return time.Unix(0, clock.Load()) }
	return q
}

func TestStoreQuotaIsSharedByTwoReplicasOverOneStore(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	var clock atomic.Int64
	clock.Store(time.Date(2030, 1, 1, 10, 30, 0, 0, time.UTC).UnixNano())
	a, b := newStoreQuota(t, store, 100, &clock), newStoreQuota(t, store, 100, &clock)

	if ok, _, err := a.Take(ctx, "1.2.3.4", 60); !ok || err != nil {
		t.Fatalf("first: %v %v", ok, err)
	}
	ok, retry, err := b.Take(ctx, "1.2.3.4", 60) // the other replica sees the 60 already spent
	if ok || err != nil || retry != 30*time.Minute {
		t.Fatalf("second: %v %v %v", ok, retry, err)
	}
	if ok, _, _ := b.Take(ctx, "5.6.7.8", 60); !ok {
		t.Error("another client has a budget of its own")
	}
	if ok, _, _ := b.Take(ctx, "1.2.3.4", 40); !ok {
		t.Error("the rest of the budget is usable")
	}
	// A refund within the window gives bytes back, from either replica; a late one is dropped.
	_, win, _, _ := a.TakeIn(ctx, "9.9.9.9", 10)
	b.RefundIn(ctx, "9.9.9.9", 10, win)
	if ok, _, _ := a.Take(ctx, "9.9.9.9", 100); !ok {
		t.Error("the refund did not arrive")
	}
	clock.Add(int64(time.Hour))
	a.RefundIn(ctx, "9.9.9.9", 100, win)
	if ok, _, _ := a.Take(ctx, "9.9.9.9", 100); !ok {
		t.Error("a new window starts full")
	}
	// The store holds no address, and Sweep drops the windows that ended.
	metas, _ := docstore.ListPrefix(ctx, store, QuotaPrefix)
	for _, m := range metas {
		if strings.Contains(m.ID, "1.2.3.4") {
			t.Errorf("the key is in the record name: %s", m.ID)
		}
	}
	_ = store.Save(ctx, QuotaPrefix+"junk", []byte("x"))
	n, err := a.Sweep(ctx)
	if err != nil || n != len(metas)-1 {
		t.Fatalf("swept %d of %d, %v", n, len(metas), err)
	}
	if _, err := store.Load(ctx, QuotaPrefix+"junk"); err != nil {
		t.Error("a foreign name was deleted")
	}
}

func TestStoreQuotaNeverLetsMoreThroughThanTheLimitUnderRaces(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	var clock atomic.Int64
	clock.Store(time.Date(2030, 1, 1, 10, 30, 0, 0, time.UTC).UnixNano())
	var passed atomic.Int64
	var wg sync.WaitGroup
	for range 4 {
		q := newStoreQuota(t, store, 100, &clock)
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if ok, _, err := q.Take(ctx, "c", 10); ok && err == nil {
					passed.Add(10)
				}
			}()
		}
	}
	wg.Wait()
	if p := passed.Load(); p > 100 {
		t.Fatalf("%d bytes passed a limit of 100", p)
	}
}

func TestStoreQuotaNeedsConditionalWrites(t *testing.T) {
	if _, err := NewStoreQuota(plainOnly{docstore.NewMemoryAdapter()}, 1, 0); err == nil {
		t.Fatal("accepted a store without conditional writes")
	}
}

// plainOnly hides everything but the Adapter methods.
type plainOnly struct{ a docstore.Adapter }

func (p plainOnly) Save(ctx context.Context, id string, d []byte) error { return p.a.Save(ctx, id, d) }
func (p plainOnly) Load(ctx context.Context, id string) (docstore.Document, error) {
	return p.a.Load(ctx, id)
}
func (p plainOnly) List(ctx context.Context) ([]docstore.Meta, error) { return p.a.List(ctx) }
func (p plainOnly) Delete(ctx context.Context, id string) error       { return p.a.Delete(ctx, id) }
