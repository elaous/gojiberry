package passkey

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

type deleteFails struct {
	*docstore.MemoryAdapter
	deletes atomic.Int32
}

func (d *deleteFails) Delete(context.Context, string) error {
	d.deletes.Add(1)
	return errors.New("cannot delete")
}

type listFails struct {
	*docstore.MemoryAdapter
	lists atomic.Int32
}

func (l *listFails) ListPrefix(context.Context, string) ([]docstore.Meta, error) {
	l.lists.Add(1)
	return nil, errors.New("cannot list")
}

func (l *listFails) List(context.Context) ([]docstore.Meta, error) {
	l.lists.Add(1)
	return nil, errors.New("cannot list")
}

func TestStartChallengeSweepRetriesOnlyAFailedListing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Now()
	clk := func() time.Time { return now }

	mem := docstore.NewMemoryAdapter()
	del := &deleteFails{MemoryAdapter: mem}
	sc, err := NewStoreChallenges(del, 0)
	if err != nil {
		t.Fatal(err)
	}
	sc.Now = clk
	old := ChallengePrefix + "1/" + strings.Repeat("a", 64)
	if err := mem.Save(ctx, old, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	sc.StartChallengeSweep(ctx, time.Hour)
	if took := time.Since(start); took > 400*time.Millisecond {
		t.Errorf("a record that cannot be deleted delayed the start by %v", took)
	}
	if n := del.deletes.Load(); n != 1 {
		t.Errorf("the delete was tried %d times at start, want 1", n)
	}

	lf := &listFails{MemoryAdapter: docstore.NewMemoryAdapter()}
	sc2, err := NewStoreChallenges(lf, 0)
	if err != nil {
		t.Fatal(err)
	}
	cctx, ccancel := context.WithTimeout(ctx, 10*time.Second)
	defer ccancel()
	sc2.StartChallengeSweep(cctx, time.Hour)
	if n := lf.lists.Load(); n != startSweepAttempts {
		t.Errorf("a failing listing was tried %d times, want %d", n, startSweepAttempts)
	}
}
