package docstore

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// expiryRecord is what is known about a document's life: the deadline ("expiry:<id>", one line)
// and when the document was created ("expiry-created:<id>"; zero when unknown: a record written
// by an older build, or by hand).
type expiryRecord struct {
	deadline time.Time
	created  time.Time
}

// errBadRecord marks a deadline record that cannot be parsed.
var errBadRecord = errors.New("docstore: unreadable expiry record")

// deadlineBytes is the body of "expiry:<id>": the deadline alone, the only format older builds read.
func (r expiryRecord) deadlineBytes() []byte {
	return []byte(r.deadline.UTC().Format(time.RFC3339Nano))
}

func (r expiryRecord) createdBytes() []byte {
	return []byte(r.created.UTC().Format(time.RFC3339Nano))
}

// parseRecord reads a deadline record. The first line is the deadline. A second line (written by
// a pre-release build that kept the creation time there) is read as the creation time; it is
// never written any more.
func parseRecord(b []byte) (expiryRecord, error) {
	first, rest, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	var r expiryRecord
	var err error
	if r.deadline, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(first)); err != nil {
		return expiryRecord{}, fmt.Errorf("%w: %v", errBadRecord, err)
	}
	if rest = strings.TrimSpace(rest); rest != "" {
		if r.created, err = time.Parse(time.RFC3339Nano, rest); err != nil {
			r.created = time.Time{} // the creation time is advisory: ignore a damaged one
		}
	}
	return r, nil
}

// readRecordFull returns the stored record of id. A deadline record that cannot be parsed is
// logged (once per id) and counts as absent: one damaged record must never fail the Load or Save
// of its document, nor abort a sweep; the next write of the document replaces it, and the first
// sweep adopts a document that has none.
func (e *ExpiringAdapter) readRecordFull(ctx context.Context, id string) (expiryRecord, bool, error) {
	d, err := e.Inner.Load(ctx, ExpiryPrefix+id)
	if errors.Is(err, ErrNotFound) {
		return expiryRecord{}, false, nil
	}
	if err != nil {
		return expiryRecord{}, false, err
	}
	r, err := parseRecord(d.Data)
	if err != nil {
		e.noteBadRecord(id, err)
		return expiryRecord{}, false, nil
	}
	if c, cerr := e.Inner.Load(ctx, ExpiryCreatedPrefix+id); cerr == nil {
		if t, perr := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(c.Data))); perr == nil {
			r.created = t
		}
	} else if !errors.Is(cerr, ErrNotFound) {
		return expiryRecord{}, false, cerr
	}
	return r, true, nil
}

// noteBadRecord logs an unreadable deadline record once per id.
func (e *ExpiringAdapter) noteBadRecord(id string, err error) {
	e.badMu.Lock()
	if e.bad == nil {
		e.bad = map[string]bool{}
	}
	seen := e.bad[id]
	if !seen && len(e.bad) < 1024 {
		e.bad[id] = true
	}
	e.badMu.Unlock()
	if !seen {
		log.Printf("docstore: skipping the unreadable expiry record of %q (%v); it is replaced by the next write of the document", id, err)
	}
}

// readRecord returns the instant document id expires (its deadline, with MaxAge applied).
func (e *ExpiringAdapter) readRecord(ctx context.Context, id string) (time.Time, bool, error) {
	r, ok, err := e.readRecordFull(ctx, id)
	if err != nil || !ok {
		return time.Time{}, false, err
	}
	return e.effective(r), true, nil
}

// writeRecord stores r: the creation time first (only when it is known), then the deadline, so a
// crash between the two leaves a deadline record with its old or no creation time, never the
// reverse. A record without a creation time removes a stale sibling.
func (e *ExpiringAdapter) writeRecord(ctx context.Context, id string, r expiryRecord) error {
	defer e.invalidate()
	if r.created.IsZero() {
		if err := e.Inner.Delete(ctx, ExpiryCreatedPrefix+id); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	} else if err := e.Inner.Save(ctx, ExpiryCreatedPrefix+id, r.createdBytes()); err != nil {
		return err
	}
	return e.Inner.Save(ctx, ExpiryPrefix+id, r.deadlineBytes())
}

// writeDeadline rewrites the deadline record only (the creation time is unchanged).
func (e *ExpiringAdapter) writeDeadline(ctx context.Context, id string, deadline time.Time) error {
	defer e.invalidate()
	return e.Inner.Save(ctx, ExpiryPrefix+id, expiryRecord{deadline: deadline}.deadlineBytes())
}

// deleteRecord removes the creation time first and the deadline record last: a crash in between
// leaves the deadline record, so the next sweep finishes, and a creation time can never outlive
// its deadline record to be inherited by a new document (a new record always writes its own).
func (e *ExpiringAdapter) deleteRecord(ctx context.Context, id string) error {
	defer e.invalidate()
	if err := e.Inner.Delete(ctx, ExpiryCreatedPrefix+id); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
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

// invalidate drops the cached deadlines (a record is about to change or has changed).
func (e *ExpiringAdapter) invalidate() {
	e.cacheMu.Lock()
	e.cache, e.cacheGen = nil, e.cacheGen+1
	e.cacheMu.Unlock()
}

// readDeadlines reads every deadline record: one listing and one Load per record.
func (e *ExpiringAdapter) readDeadlines(ctx context.Context) (map[string]time.Time, error) {
	out, _, err := e.readDeadlinesBad(ctx)
	return out, err
}

// readDeadlinesBad is readDeadlines that also returns the ids whose record is unreadable: they
// are skipped (logged once), never an error, so one damaged record cannot abort a sweep.
func (e *ExpiringAdapter) readDeadlinesBad(ctx context.Context) (out map[string]time.Time, bad []string, err error) {
	recs, err := ListPrefix(ctx, e.Inner, ExpiryPrefix)
	if err != nil {
		return nil, nil, err
	}
	out = make(map[string]time.Time, len(recs))
	for _, m := range recs {
		id := strings.TrimPrefix(m.ID, ExpiryPrefix)
		t, ok, err := e.readRecord(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			out[id] = t
		} else {
			bad = append(bad, id) // listed but unreadable (or deleted since the listing)
		}
	}
	return out, bad, nil
}

// healBad repairs the records readDeadlinesBad could not read, under the lock and after
// re-reading: a record whose document exists gets a fresh full lifetime (the same grace adoption
// gives), one with no document is deleted. Without it a document with a damaged record would
// never expire.
func (e *ExpiringAdapter) healBad(ctx context.Context, bad []string) error {
	var first error
	for _, id := range bad {
		if err := ctx.Err(); err != nil {
			return err
		}
		e.mu.Lock()
		err := func() error {
			if _, ok, err := e.readRecordFull(ctx, id); err != nil || ok {
				return err // fine now (written since), or the store failed
			}
			if _, err := e.Inner.Load(ctx, ExpiryPrefix+id); errors.Is(err, ErrNotFound) {
				return nil // deleted since the listing
			} else if err != nil {
				return err
			}
			if _, err := e.Inner.Load(ctx, id); errors.Is(err, ErrNotFound) {
				return e.deleteRecord(ctx, id) // a record with no document
			} else if err != nil {
				return err
			}
			now := e.now()
			ttl, ok := e.Policy.TTL(id, now)
			if !ok {
				return e.deleteRecord(ctx, id)
			}
			log.Printf("docstore: gave %q a new deadline in place of its unreadable expiry record", id)
			return e.writeRecord(ctx, id, expiryRecord{deadline: now.Add(ttl), created: now})
		}()
		e.mu.Unlock()
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// deadlines returns the deadlines for a listing: the cached copy while it is fresh. The map is
// shared and must not be modified.
func (e *ExpiringAdapter) deadlines(ctx context.Context) (map[string]time.Time, error) {
	ttl := e.DeadlineCacheTTL
	if ttl == 0 {
		ttl = 5 * time.Second
	}
	e.cacheMu.Lock()
	if ttl > 0 && e.cache != nil && e.now().Sub(e.cacheAt) < ttl && !e.now().Before(e.cacheAt) {
		m := e.cache
		e.cacheMu.Unlock()
		return m, nil
	}
	gen := e.cacheGen
	e.cacheMu.Unlock()
	m, err := e.readDeadlines(ctx)
	if err != nil {
		return nil, err
	}
	e.cacheMu.Lock()
	if e.cacheGen == gen && ttl > 0 {
		e.cache, e.cacheAt = m, e.now()
	}
	e.cacheMu.Unlock()
	return m, nil
}

// Expiries implements Expirer, from the cache described on ExpiringAdapter. Documents already
// past their deadline are included, since they are what the next Sweep deletes; List and Load
// already hide them.
func (e *ExpiringAdapter) Expiries(ctx context.Context) (map[string]time.Time, error) {
	m, err := e.deadlines(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out, nil
}
