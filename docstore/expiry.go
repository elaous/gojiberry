package docstore

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path"
	"strings"
	"sync"
	"time"
)

// ExpiryPrefix is the keyspace of the deadline records an ExpiringAdapter keeps beside the
// documents: "expiry:<id>" holds the instant (RFC 3339, UTC) after which document <id> is gone.
// Server reserves it from the document API.
const ExpiryPrefix = "expiry:"

// ExpiryPolicy decides how long a document lives after a write. TTL answers for id at time now:
// the lifetime, and whether the document expires at all (false: it lives until deleted).
// Alternative policies (per tenant, per size, by id prefix) plug in here.
type ExpiryPolicy interface {
	TTL(id string, now time.Time) (time.Duration, bool)
}

// ExpiryPolicyFunc makes a function an ExpiryPolicy.
type ExpiryPolicyFunc func(id string, now time.Time) (time.Duration, bool)

// TTL implements ExpiryPolicy.
func (f ExpiryPolicyFunc) TTL(id string, now time.Time) (time.Duration, bool) { return f(id, now) }

// exemptPrefixes are the keyspaces other parts of the program keep in the same store; they never
// expire by themselves (the ones that belong to a document go with it, see Related).
var exemptPrefixes = []string{ExpiryPrefix, aclKeyPrefix, "collab:", "catalog:", "versions/", DirectoryPrefix, "passkey/"}

// FixedTTL is the policy "every document lives d after its last write", except the keys that are
// not documents (access lists, version history, the directory, passkey accounts, ...).
func FixedTTL(d time.Duration) ExpiryPolicy {
	return ExpiryPolicyFunc(func(id string, _ time.Time) (time.Duration, bool) {
		for _, p := range exemptPrefixes {
			if strings.HasPrefix(id, p) {
				return 0, false
			}
		}
		return d, d > 0
	})
}

// Expirer is an optional capability of an Adapter: it knows when documents expire. Server uses it
// to answer X-Gojiberry-Expires and the "expires" field of listings. ExpiringAdapter has it.
type Expirer interface {
	// Expiry returns the instant document id expires, and false if it does not.
	Expiry(ctx context.Context, id string) (time.Time, bool, error)
	// Expiries returns the deadline of every document that has one.
	Expiries(ctx context.Context) (map[string]time.Time, error)
}

// DefaultRelated lists what belongs to document id and goes with it: its access list and
// shared-editing state, and its version history ("versions/<id>/" and, for an id with a file
// extension, "versions/<id without extension>/", the saver's layout).
func DefaultRelated(id string) (exact, prefixes []string) {
	exact = []string{aclKeyPrefix + id, "collab:" + id}
	prefixes = []string{"versions/" + id + "/"}
	if stem := strings.TrimSuffix(id, path.Ext(id)); stem != id && stem != "" {
		prefixes = append(prefixes, "versions/"+stem+"/")
	}
	return exact, prefixes
}

// ExpiringAdapter gives any Adapter documents that expire: a document lives for the TTL its
// Policy names after its last Save ("delete 24 hours after the last edit"). Save and SaveIf renew;
// Load and List do not, so reading cannot keep a document alive. An expired document is invisible
// at once (Load and Delete answer ErrNotFound, List leaves it out) even before Sweep deletes it.
//
// It is storage-agnostic: the deadline of document <id> is a small record "expiry:<id>" in the
// same store, written through the same adapter, so it works over memory, local files, SQLite and
// every backend, and over a stored deadline surviving a restart. Optional capabilities of the
// inner adapter are passed through: ConditionalAdapter (SaveIf, else ErrConditionalUnsupported),
// Locker (else a no-op lock), PrefixLister and QuotaReporter.
//
// Order and crash safety. Save writes the deadline record first, then the document; Sweep
// deletes the document (and what Related names), then the record last. So a crash can leave only
// two things, both harmless and both repaired by Sweep:
//   - a record whose document is missing (crash inside Save, or after the sweep's delete): it is
//     an orphan, dropped by the sweep once its deadline has passed (not before, since another
//     process may be in the middle of its Save);
//   - never a document that should expire but has no record, except documents that were in the
//     store before it was wrapped, or ones the policy did not cover when written. A document
//     without a record never expires on its own. The first Sweep after construction gives every
//     such document (that the policy covers) the deadline UpdatedAt + TTL, so a store wrapped
//     late is cleaned up, and one that is already past that is deleted by that sweep.
//
// A Save that fails after the record was written puts the previous record back, so a failed or
// conflicting write does not extend a document's life. Writes and sweeps of one ExpiringAdapter
// are serialized per process, so a renewal cannot be lost to a concurrent sweep; two processes
// sharing one store can still race at the instant of expiry (a Save at the very second of the
// deadline may be deleted), which for a 24-hour life is immaterial.
type ExpiringAdapter struct {
	Inner  Adapter
	Policy ExpiryPolicy
	// Now is the clock (time.Now when nil); tests inject a fake one.
	Now func() time.Time
	// Related names what is deleted along with a document (DefaultRelated when nil): exact ids
	// and id prefixes.
	Related func(id string) (exact, prefixes []string)
	// OnError receives the errors of Run's sweeps (the log when nil).
	OnError func(error)

	mu      sync.Mutex // serializes writes and the per-document part of a sweep
	adopted bool       // the first sweep has given unrecorded documents a deadline
}

// NewExpiringAdapter wraps inner with the policy.
func NewExpiringAdapter(inner Adapter, policy ExpiryPolicy) *ExpiringAdapter {
	return &ExpiringAdapter{Inner: inner, Policy: policy}
}

var (
	_ ConditionalAdapter = (*ExpiringAdapter)(nil)
	_ Locker             = (*ExpiringAdapter)(nil)
	_ PrefixLister       = (*ExpiringAdapter)(nil)
	_ QuotaReporter      = (*ExpiringAdapter)(nil)
	_ Expirer            = (*ExpiringAdapter)(nil)
)

func (e *ExpiringAdapter) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *ExpiringAdapter) related(id string) ([]string, []string) {
	if e.Related != nil {
		return e.Related(id)
	}
	return DefaultRelated(id)
}

func isExpiryKey(id string) bool { return strings.HasPrefix(id, ExpiryPrefix) }

// readRecord returns the stored deadline of id.
func (e *ExpiringAdapter) readRecord(ctx context.Context, id string) (time.Time, bool, error) {
	d, err := e.Inner.Load(ctx, ExpiryPrefix+id)
	if errors.Is(err, ErrNotFound) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	t, err := parseDeadline(d.Data)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("docstore: expiry record of %q: %w", id, err)
	}
	return t, true, nil
}

func parseDeadline(b []byte) (time.Time, error) { return time.Parse(time.RFC3339Nano, string(b)) }

func (e *ExpiringAdapter) writeRecord(ctx context.Context, id string, t time.Time) error {
	return e.Inner.Save(ctx, ExpiryPrefix+id, []byte(t.UTC().Format(time.RFC3339Nano)))
}

func (e *ExpiringAdapter) deleteRecord(ctx context.Context, id string) error {
	if err := e.Inner.Delete(ctx, ExpiryPrefix+id); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// expired reports whether a record's deadline has passed.
func (e *ExpiringAdapter) expired(t time.Time) bool { return !e.now().Before(t) }

// Expiry implements Expirer.
func (e *ExpiringAdapter) Expiry(ctx context.Context, id string) (time.Time, bool, error) {
	if isExpiryKey(id) {
		return time.Time{}, false, nil
	}
	t, ok, err := e.readRecord(ctx, id)
	if err != nil || !ok {
		return time.Time{}, false, err
	}
	return t, true, nil
}

// Expiries implements Expirer. Documents already past their deadline are included, since they
// are what the next Sweep deletes; List and Load already hide them.
func (e *ExpiringAdapter) Expiries(ctx context.Context) (map[string]time.Time, error) {
	recs, err := ListPrefix(ctx, e.Inner, ExpiryPrefix)
	if err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(recs))
	for _, m := range recs {
		id := strings.TrimPrefix(m.ID, ExpiryPrefix)
		t, ok, err := e.readRecord(ctx, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out[id] = t
		}
	}
	return out, nil
}

// purgeLocked deletes document id, what Related names and, last, its record. e.mu is held.
// It reports whether the document itself was there.
func (e *ExpiringAdapter) purgeLocked(ctx context.Context, id string) (bool, error) {
	found := true
	if err := e.Inner.Delete(ctx, id); errors.Is(err, ErrNotFound) {
		found = false
	} else if err != nil {
		return false, err
	}
	exact, prefixes := e.related(id)
	for _, r := range exact {
		if err := e.Inner.Delete(ctx, r); err != nil && !errors.Is(err, ErrNotFound) {
			return found, err
		}
	}
	for _, p := range prefixes {
		metas, err := ListPrefix(ctx, e.Inner, p)
		if err != nil {
			return found, err
		}
		for _, m := range metas {
			if err := e.Inner.Delete(ctx, m.ID); err != nil && !errors.Is(err, ErrNotFound) {
				return found, err
			}
		}
	}
	return found, e.deleteRecord(ctx, id)
}

// renewed runs write (the inner Save) with the new deadline recorded before it. An expired
// document that was never swept is purged first, so a new document does not inherit the old
// one's versions. If write fails the previous record is put back.
func (e *ExpiringAdapter) renewed(ctx context.Context, id string, write func() error) error {
	if isExpiryKey(id) {
		return fmt.Errorf("docstore: %q is in the reserved expiry keyspace", id)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	prev, had, err := e.readRecord(ctx, id)
	if err != nil {
		return err
	}
	if had && !now.Before(prev) {
		if _, err := e.purgeLocked(ctx, id); err != nil {
			return err
		}
		had = false
	}
	ttl, ok := e.Policy.TTL(id, now)
	if !ok {
		if had {
			if err := e.deleteRecord(ctx, id); err != nil {
				return err
			}
		}
		return write()
	}
	if err := e.writeRecord(ctx, id, now.Add(ttl)); err != nil {
		return err
	}
	if err := write(); err != nil {
		if had {
			_ = e.writeRecord(ctx, id, prev)
		} else {
			_ = e.deleteRecord(ctx, id)
		}
		return err
	}
	return nil
}

// Save implements Adapter: it stores the document and renews its deadline.
func (e *ExpiringAdapter) Save(ctx context.Context, id string, data []byte) error {
	return e.renewed(ctx, id, func() error { return e.Inner.Save(ctx, id, data) })
}

// SaveIf implements ConditionalAdapter over an inner one; an expired document counts as absent.
func (e *ExpiringAdapter) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	c, ok := e.Inner.(ConditionalAdapter)
	if !ok {
		return ErrConditionalUnsupported
	}
	return e.renewed(ctx, id, func() error { return c.SaveIf(ctx, id, data, prev) })
}

// Load implements Adapter. It does not renew; an expired document is ErrNotFound.
func (e *ExpiringAdapter) Load(ctx context.Context, id string) (Document, error) {
	if isExpiryKey(id) {
		return Document{}, ErrNotFound
	}
	t, ok, err := e.readRecord(ctx, id)
	if err != nil {
		return Document{}, err
	}
	if ok && e.expired(t) {
		return Document{}, ErrNotFound
	}
	return e.Inner.Load(ctx, id)
}

// visible drops the deadline records and the expired documents from metas.
func (e *ExpiringAdapter) visible(ctx context.Context, metas []Meta) ([]Meta, error) {
	deadlines, err := e.Expiries(ctx)
	if err != nil {
		return nil, err
	}
	out := metas[:0:0]
	for _, m := range metas {
		if isExpiryKey(m.ID) {
			continue
		}
		if t, ok := deadlines[m.ID]; ok && e.expired(t) {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// List implements Adapter, leaving out expired documents and the deadline records. It does not renew.
func (e *ExpiringAdapter) List(ctx context.Context) ([]Meta, error) {
	metas, err := e.Inner.List(ctx)
	if err != nil {
		return nil, err
	}
	return e.visible(ctx, metas)
}

// ListPrefix implements PrefixLister (through the inner one's when it has it).
func (e *ExpiringAdapter) ListPrefix(ctx context.Context, prefix string) ([]Meta, error) {
	if isExpiryKey(prefix) {
		return nil, nil
	}
	metas, err := ListPrefix(ctx, e.Inner, prefix)
	if err != nil {
		return nil, err
	}
	return e.visible(ctx, metas)
}

// Delete implements Adapter. An expired document is already gone (ErrNotFound; it is purged).
// Deleting does not remove related records, as before; Sweep does for expired documents.
func (e *ExpiringAdapter) Delete(ctx context.Context, id string) error {
	if isExpiryKey(id) {
		return ErrNotFound
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok, err := e.readRecord(ctx, id)
	if err != nil {
		return err
	}
	if ok && e.expired(t) {
		_, err := e.purgeLocked(ctx, id)
		if err != nil {
			return err
		}
		return ErrNotFound
	}
	err = e.Inner.Delete(ctx, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if derr := e.deleteRecord(ctx, id); derr != nil {
		return derr
	}
	return err
}

// Lock implements Locker by the inner store's, if it has one (else it does nothing, which is
// what a store with no shared lock gets from an encrypting adapter too).
func (e *ExpiringAdapter) Lock(ctx context.Context) (func(), error) {
	if l, ok := e.Inner.(Locker); ok {
		return l.Lock(ctx)
	}
	return func() {}, nil
}

// Usage implements QuotaReporter by the inner store's; without one it is an error, which Server
// reports as "not available".
func (e *ExpiringAdapter) Usage(ctx context.Context) (Quota, error) {
	if q, ok := e.Inner.(QuotaReporter); ok {
		return q.Usage(ctx)
	}
	return Quota{}, errors.New("docstore: the store reports no usage")
}

// adopt gives every document without a record that the policy covers the deadline
// UpdatedAt + TTL. It runs once, in the first successful Sweep.
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
	deadlines, err := e.Expiries(ctx)
	if err != nil {
		return err
	}
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
		base := m.UpdatedAt
		if base.IsZero() {
			base = now
		}
		e.mu.Lock()
		// A Save since the listing has written a record of its own: keep it.
		_, has, err := e.readRecord(ctx, m.ID)
		if err == nil && !has {
			err = e.writeRecord(ctx, m.ID, base.Add(ttl))
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
	}
	e.mu.Lock()
	e.adopted = true
	e.mu.Unlock()
	return nil
}

// Sweep deletes the documents whose deadline has passed, with what Related names for them, and
// then their records; it also drops the orphan records of documents that are gone. It returns
// how many documents it deleted. A failure on one document does not stop the others: Sweep
// returns the first error after trying them all, and what was left is retried by the next one.
func (e *ExpiringAdapter) Sweep(ctx context.Context) (deleted int, err error) {
	if err := e.adopt(ctx); err != nil {
		return 0, err
	}
	deadlines, err := e.Expiries(ctx)
	if err != nil {
		return 0, err
	}
	var first error
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
			found, rerr = e.purgeLocked(ctx, id)
			if rerr == nil && found {
				deleted++
			}
		}
		e.mu.Unlock()
		if rerr != nil && first == nil {
			first = rerr
		}
	}
	return deleted, first
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
