package docstore

import (
	"context"
	"errors"
	"github.com/elaous/gojiberry/observe"
	"log"
	"strings"
	"time"
)

func (e *ExpiringAdapter) adopt(ctx context.Context) error {
	e.mu.Lock()
	done := e.adopted
	e.mu.Unlock()
	if done {
		return nil
	}
	metas, err := e.Inner.List(ctx)
	if err != nil {
		return err
	}
	deadlines, err := e.readDeadlines(ctx)
	if err != nil {
		return err
	}
	n := 0
	for _, m := range metas {
		if isExpiryKey(m.ID) {
			continue
		}
		if _, has := deadlines[m.ID]; has {
			continue
		}
		now := e.now()
		ttl, ok := e.Policy.TTL(m.ID, now)
		if !ok {
			continue
		}
		base := now
		if e.AdoptUpdatedAt && !m.UpdatedAt.IsZero() {
			base = m.UpdatedAt
		}
		e.mu.Lock()
		// a Save since the listing wrote its own record: keep it
		_, has, err := e.readRecord(ctx, m.ID)
		if err == nil && !has {
			err = e.writeRecord(ctx, m.ID, expiryRecord{deadline: base.Add(ttl), created: base})
			if err == nil {
				n++
			}
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
	}
	e.mu.Lock()
	e.adopted = true
	e.mu.Unlock()
	if n > 0 {
		how := "a full grace period from now"
		if e.AdoptUpdatedAt {
			how = "last update + TTL"
		}
		log.Printf("docstore: expiry adopted %d existing documents without a deadline (%s)", n, how)
	}
	return nil
}

// Sweep deletes the documents whose deadline has passed.
func (e *ExpiringAdapter) Sweep(ctx context.Context) (deleted int, err error) {
	done := observe.Default().Sweep("expiry")
	defer func() {
		done(deleted, err)
		if deleted > 0 && e.OnExpired != nil {
			e.OnExpired(deleted)
		}
	}()
	if err := e.adopt(ctx); err != nil {
		return 0, err
	}
	deadlines, bad, err := e.readDeadlinesBad(ctx)
	if err != nil {
		return 0, err
	}
	var first error
	if herr := e.healBad(ctx, bad); herr != nil {
		first = herr
	}
	for id, t := range deadlines {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		if !e.expired(t) {
			continue
		}
		e.mu.Lock()
		// a Save may have renewed it since the listing
		cur, ok, rerr := e.readRecord(ctx, id)
		if rerr == nil && ok && e.expired(cur) {
			var found bool
			found, rerr = e.purgeLocked(ctx, id, true)
			if errors.Is(rerr, errRenewed) {
				rerr = nil
				// another process renewed it: live, not an error
			} else if rerr == nil && found {
				deleted++
			}
		}
		e.mu.Unlock()
		if rerr != nil && first == nil {
			first = rerr
		}
	}
	if cerr := e.sweepCreated(ctx); cerr != nil && first == nil {
		first = cerr
	}
	if e.Orphans != nil {
		n, oerr := e.sweepOrphans(ctx)
		deleted += n
		if oerr != nil && first == nil {
			first = oerr
		}
	}
	return deleted, first
}

// re-checked under the lock, which a Save holds across both writes
func (e *ExpiringAdapter) sweepCreated(ctx context.Context) error {
	metas, err := ListPrefix(ctx, e.Inner, ExpiryCreatedPrefix)
	if err != nil {
		return err
	}
	var first error
	for _, m := range metas {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := strings.TrimPrefix(m.ID, ExpiryCreatedPrefix)
		if id == "" {
			continue
		}
		e.mu.Lock()
		_, derr := e.Inner.Load(ctx, ExpiryPrefix+id)
		if errors.Is(derr, ErrNotFound) {
			derr = e.Inner.Delete(ctx, m.ID)
			if errors.Is(derr, ErrNotFound) {
				derr = nil
			}
		}
		e.mu.Unlock()
		if derr != nil && first == nil {
			first = derr
		}
	}
	return first
}

// OrphanPolicy decides which entries are orphaned.
type OrphanPolicy interface {
	// Prefix is the keyspace swept for orphans.
	Prefix() string
	// Expired reports whether an entry is orphaned.
	Expired(updated, now time.Time, ttl time.Duration) bool
}

// DefaultOrphanGrace is the grace period before deleting an orphan.
const DefaultOrphanGrace = time.Hour

type collabOrphans struct{ grace time.Duration }

// CollabOrphans is the OrphanPolicy for collaboration records.
func CollabOrphans(grace time.Duration) OrphanPolicy {
	if grace <= 0 {
		grace = DefaultOrphanGrace
	}
	return collabOrphans{grace}
}

func (collabOrphans) Prefix() string { return collabPrefix }

func (c collabOrphans) Expired(updated, now time.Time, ttl time.Duration) bool {
	return !updated.IsZero() && now.Sub(updated) > ttl+c.grace
}

func (e *ExpiringAdapter) hasLiveDocument(ctx context.Context, id string) (bool, error) {
	metas, err := ListPrefix(ctx, e.Inner, id)
	if err != nil {
		return false, err
	}
	for _, m := range metas {
		if isExpiryKey(m.ID) || (m.ID != id && (!IsDocumentKey(m.ID) || logicalID(m.ID) != id)) {
			continue
		}
		t, ok, err := e.readRecord(ctx, m.ID)
		if err != nil {
			return false, err
		}
		if !ok || e.now().Before(t) {
			return true, nil
		}
	}
	return false, nil
}

func (e *ExpiringAdapter) sweepOrphans(ctx context.Context) (deleted int, first error) {
	prefix := e.Orphans.Prefix()
	metas, err := ListPrefix(ctx, e.Inner, prefix)
	if err != nil {
		return 0, err
	}
	for _, m := range metas {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		id := strings.TrimPrefix(m.ID, prefix)
		if id == "" {
			continue
		}
		now := e.now()
		ttl, ok := e.Policy.TTL(id, now)
		if !ok || !e.Orphans.Expired(m.UpdatedAt, now, ttl) {
			continue
		}
		ok, err := e.dropOrphan(ctx, prefix, id, ttl)
		if err != nil && first == nil {
			first = err
		}
		if ok {
			deleted++
		}
	}
	return deleted, first
}

// re-checks under the lock: a hub save keeps the entry
func (e *ExpiringAdapter) dropOrphan(ctx context.Context, prefix, id string, ttl time.Duration) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cur, err := e.Inner.Load(ctx, prefix+id)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !e.Orphans.Expired(cur.UpdatedAt, e.now(), ttl) {
		return false, nil
	}
	if live, err := e.hasLiveDocument(ctx, id); err != nil || live {
		return false, err
	}
	if err := e.Inner.Delete(ctx, prefix+id); err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}
	return true, nil
}

func orElse(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// Run sweeps now and then every interval.
func (e *ExpiringAdapter) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := e.Sweep(ctx); err != nil && ctx.Err() == nil {
			if e.OnError != nil {
				e.OnError(err)
			} else {
				log.Printf("docstore: expiry sweep: %v", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
