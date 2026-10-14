package docstore

import (
	"context"
	"errors"
	"github.com/elaous/gojiberry/observe"
	"log"
	"strings"
	"time"
)

// adopt gives every document without a record that the policy covers a deadline: now + TTL, or
// UpdatedAt + TTL with AdoptUpdatedAt. It runs once, in the first successful Sweep, and logs how
// many it adopted.
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
		// A Save since the listing has written a record of its own: keep it.
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

// Sweep deletes the documents whose deadline has passed, with what Related names for them, and
// then their records; it also drops the orphan records of documents that are gone and, with
// Orphans set, the orphaned shared-editing state (counted in the result). It returns
// how many documents it deleted. A failure on one document does not stop the others: Sweep
// returns the first error after trying them all, and what was left is retried by the next one.
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
		// Re-read under the lock: a Save may have renewed it since the listing.
		cur, ok, rerr := e.readRecord(ctx, id)
		if rerr == nil && ok && e.expired(cur) {
			var found bool
			found, rerr = e.purgeLocked(ctx, id, true)
			if errors.Is(rerr, errRenewed) {
				rerr = nil // another process renewed it: it is live, not an error
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

// sweepCreated deletes the creation-time records that have no deadline record (a crash between
// the two writes of writeRecord). The check is repeated under the lock, which a Save holds
// across both writes.
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

// OrphanPolicy decides which entries of a system keyspace that belong to a document have outlived
// it. A collaboration hub that only ever holds a document (never saved to the store as a
// document) has nothing to renew and no deadline, so its "collab:<id>" state would stay for
// ever; Sweep asks the policy about each such entry whose document does not exist.
type OrphanPolicy interface {
	// Prefix is the keyspace swept ("collab:").
	Prefix() string
	// Expired reports whether an entry last written at updated, whose document would live ttl
	// after a write, is to be deleted at now.
	Expired(updated, now time.Time, ttl time.Duration) bool
}

// DefaultOrphanGrace is the grace period CollabOrphans uses when given none.
const DefaultOrphanGrace = time.Hour

type collabOrphans struct{ grace time.Duration }

// CollabOrphans is the OrphanPolicy for the hub's "collab:<id>" state: it is deleted once its
// own UpdatedAt is older than the document TTL plus grace (DefaultOrphanGrace when not positive)
// and no live document has that id (no exact key and no live saver generation). Every hub save
// bumps UpdatedAt, so a hub-only document that is being edited stays; one that nobody has
// touched for TTL + grace is removed. The grace period keeps a document that was just saved to
// the store, then expired and re-created, and clock skew from deleting live state.
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

// hasLiveDocument reports whether a live (not expired) document with exactly this key, or a
// live saver generation of this logical id, exists.
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

// sweepOrphans deletes the entries Orphans names whose document is gone (see OrphanPolicy).
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

// dropOrphan re-checks under the lock (a hub save or a new document since the listing keeps the
// entry) and deletes prefix+id.
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

// Run sweeps now and then every interval until ctx is done. Errors go to OnError (the log).
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
