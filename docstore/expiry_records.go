package docstore

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// expiryRecord is what is known about a document's deadline.
type expiryRecord struct {
	deadline time.Time
	created  time.Time
}

// errBadRecord marks an unreadable deadline record.
var errBadRecord = errors.New("docstore: unreadable expiry record")

// deadlineBytes is the body of an expiry record.
func (r expiryRecord) deadlineBytes() []byte {
	return []byte(r.deadline.UTC().Format(time.RFC3339Nano))
}

func (r expiryRecord) createdBytes() []byte {
	return []byte(r.created.UTC().Format(time.RFC3339Nano))
}

// parseRecord reads a deadline record.
func parseRecord(b []byte) (expiryRecord, error) {
	first, rest, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	var r expiryRecord
	var err error
	if r.deadline, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(first)); err != nil {
		return expiryRecord{}, fmt.Errorf("%w: %v", errBadRecord, err)
	}
	if rest = strings.TrimSpace(rest); rest != "" {
		if r.created, err = time.Parse(time.RFC3339Nano, rest); err != nil {
			r.created = time.Time{}
		}
	}
	return r, nil
}

// readRecordFull returns the stored record and its digest.
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

// noteBadRecord logs an unreadable deadline record.
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

// readRecord returns the instant a document expires.
func (e *ExpiringAdapter) readRecord(ctx context.Context, id string) (time.Time, bool, error) {
	r, ok, err := e.readRecordFull(ctx, id)
	if err != nil || !ok {
		return time.Time{}, false, err
	}
	return e.effective(r), true, nil
}

// writeRecord stores r with its creation time.
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

// writeDeadline rewrites the deadline of a record.
func (e *ExpiringAdapter) writeDeadline(ctx context.Context, id string, deadline time.Time) error {
	defer e.invalidate()
	return e.Inner.Save(ctx, ExpiryPrefix+id, expiryRecord{deadline: deadline}.deadlineBytes())
}

// deleteRecord removes the creation record.
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

// invalidate drops the cached deadlines.
func (e *ExpiringAdapter) invalidate() {
	e.cacheMu.Lock()
	e.cache, e.cacheGen = nil, e.cacheGen+1
	e.cacheMu.Unlock()
}

// readDeadlines reads every deadline record.
func (e *ExpiringAdapter) readDeadlines(ctx context.Context) (map[string]time.Time, error) {
	out, _, err := e.readDeadlinesBad(ctx)
	return out, err
}

// readDeadlinesBad is readDeadlines that also reports bad records.
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
			bad = append(bad, id)
		}
	}
	return out, bad, nil
}

// healBad repairs the records readDeadlinesBad reported.
func (e *ExpiringAdapter) healBad(ctx context.Context, bad []string) error {
	var first error
	for _, id := range bad {
		if err := ctx.Err(); err != nil {
			return err
		}
		e.mu.Lock()
		err := func() error {
			if _, ok, err := e.readRecordFull(ctx, id); err != nil || ok {
				return err
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

// deadlines returns the deadlines found under a prefix.
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

// Expiries implements Expirer from the stored records.
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
