package public

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/observe"
	"log"
	"strings"
	"time"
)

// ExpiringHolder is implemented by a store that wraps an *docstore.ExpiringAdapter (to count, log or
// encrypt around it): the public service puts its snapshot hooks (Guard, OnDelete) on the adapter it
// returns, as it does on a bare *docstore.ExpiringAdapter. Without it a wrapper gets no hooks, and the
// service logs that snapshots are then counted only when Sweep lists the store.
type ExpiringHolder interface {
	Expiring() *docstore.ExpiringAdapter
}

// expiringOf finds the ExpiringAdapter behind a store: the store itself or the one an ExpiringHolder
// names; nil if there is none, which is logged.
func expiringOf(store docstore.Adapter) *docstore.ExpiringAdapter {
	switch v := store.(type) {
	case *docstore.ExpiringAdapter:
		return v
	case ExpiringHolder:
		if ea := v.Expiring(); ea != nil {
			return ea
		}
	}
	log.Printf("public docstore: the store is not an *docstore.ExpiringAdapter and names none (public.ExpiringHolder): version snapshots are not refused over the byte budget and are counted only when Sweep lists the store")
	return nil
}

// NoteVersionWrite tells the byte budget that a version snapshot of a public document was written:
// key is the snapshot's store key ("versions/pubdoc/<docID>/<name>") and size its length; any other
// key is ignored. A Service over an *docstore.ExpiringAdapter wires this to its OnSave hook itself;
// a deployment whose snapshots are written some other way calls it from there. Without it the
// snapshots are counted only when Sweep lists the store.
func (s *Service) NoteVersionWrite(key string, size int64) {
	if id, ok := versionDoc(key); ok {
		s.bytes.addExtra(id, size)
	}
}

// versionDoc is the public document a version snapshot key belongs to ("versions/pubdoc/<id>/..."), or false.
func versionDoc(key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, versionsKeyPrefix)
	if !ok {
		return "", false
	}
	id, _, _ := strings.Cut(rest, "/")
	return id, validID(id)
}

// ---- sweep

// recordGrace is how long a document record may exist without its document before the sweep
// calls it an orphan: creation writes the records first.
const recordGrace = time.Minute

// Sweep removes what the ExpiringAdapter does not: the records of documents that expired or are
// gone (metadata and view-ID records, which are in a system keyspace and so never expire by
// themselves), the reports and tombstones past their own TTL, and the reports over the global
// bound (oldest first); it reaps expired sessions, and reconciles the byte and report counters
// with the store. It returns how many records it deleted. Run calls it on an interval.
//
// Cost. Its listings are by prefix, so with a PrefixLister they cost what the public data costs
// (live documents, records, reports, tombstones: all bounded by the caps). Over a store without
// prefix listing every listing is the whole store, so the listing part runs at most every
// Config.FullSweepInterval there; the other calls only reap sessions.
func (s *Service) Sweep(ctx context.Context) (removed int, err error) {
	done := observe.Default().Sweep("public")
	defer func() { done(removed, err) }()
	var first error
	defer func() {
		s.healthMu.Lock()
		s.lastSweep = s.now()
		s.sweepFailing = first != nil || err != nil
		s.healthMu.Unlock()
	}()
	note := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	del := func(key string) bool {
		if err := s.deleteKey(ctx, key); err != nil {
			note(err)
			return false
		}
		removed++
		return true
	}
	now := s.now()
	if sq, ok := s.quota.(interface {
		Sweep(context.Context) (int, error)
	}); ok {
		// The counters of a shared quota: the windows that ended.
		if _, err := sq.Sweep(ctx); err != nil {
			note(err)
		}
	}
	if sq, ok := s.prioQuota.(interface {
		Sweep(context.Context) (int, error)
	}); ok {
		if _, err := sq.Sweep(ctx); err != nil {
			note(err)
		}
	}
	if r, ok := s.sessions.(SessionReaper); ok {
		if _, err := r.Reap(ctx, now); err != nil {
			note(err)
		}
	}
	if !s.prefixOK {
		s.healthMu.Lock()
		skip := !s.lastFull.IsZero() && now.Sub(s.lastFull) < s.cfg.FullSweepInterval
		if !skip {
			s.lastFull = now
		}
		s.healthMu.Unlock()
		if skip {
			return 0, first
		}
	}

	repWin := s.reports.begin()
	repDone := false
	defer func() {
		if !repDone {
			s.reports.abort(repWin)
		}
	}()
	bytesWin := s.bytes.begin()
	listed, err := s.listBudget(ctx)
	if err != nil {
		s.bytes.abort(bytesWin)
		return 0, err
	}
	s.bytes.set(bytesWin, listed) // version snapshots count with their documents
	live := make(map[string]bool, len(listed))
	for id, e := range listed {
		if e.doc > 0 {
			live[id] = true
		}
	}
	docs, err := docstore.ListPrefix(ctx, s.store, docRecordPrefix)
	if err != nil {
		return 0, err
	}
	known := map[string]bool{} // views of document records that stay
	for _, m := range docs {
		docID := strings.TrimPrefix(m.ID, docRecordPrefix)
		if live[docID] {
			known[viewIDOf(docID)] = true
			continue
		}
		var meta docMeta
		found, err := s.getJSON(ctx, m.ID, &meta)
		if err != nil {
			note(err)
			continue
		}
		if !found || now.Sub(meta.Created) < recordGrace {
			known[viewIDOf(docID)] = true
			continue
		}
		unlock := s.lock(docID)
		if ok, err := s.live(ctx, docID); err != nil {
			note(err)
			known[viewIDOf(docID)] = true
		} else if ok {
			known[viewIDOf(docID)] = true
		} else {
			del(viewRecordPrefix + viewIDOf(docID))
			del(m.ID)
		}
		unlock()
	}
	views, err := docstore.ListPrefix(ctx, s.store, viewRecordPrefix)
	if err != nil {
		note(err)
	}
	for _, m := range views {
		view := strings.TrimPrefix(m.ID, viewRecordPrefix)
		if known[view] {
			continue
		}
		var rec viewRecord
		if found, err := s.getJSON(ctx, m.ID, &rec); err != nil {
			note(err)
			continue
		} else if found && validID(rec.Doc) {
			if found, err := s.getJSON(ctx, docRecordPrefix+rec.Doc, &docMeta{}); err != nil {
				note(err)
				continue
			} else if found {
				continue
			}
		}
		del(m.ID)
	}
	s.sweepOld(ctx, tombPrefix, s.cfg.TombstoneTTL, func(raw []byte) (time.Time, reportInfo, bool) {
		var t tombstone
		ok := json.Unmarshal(raw, &t) == nil
		return t.At, reportInfo{}, ok
	}, del, note, nil)
	kept := s.sweepOld(ctx, docstore.ReportPrefix, s.cfg.ReportTTL, func(raw []byte) (time.Time, reportInfo, bool) {
		var r Report
		ok := json.Unmarshal(raw, &r) == nil
		return r.At, reportInfo{reporter: r.Reporter, doc: r.DocID, priority: r.Priority, at: r.At}, ok
	}, del, note, func(key string, info reportInfo) {
		// a report past its TTL leaves its document's record too
		s.dropRef(ctx, info.doc, strings.TrimPrefix(key, docstore.ReportPrefix))
	})
	if kept != nil {
		removed += s.evictReports(ctx, kept.metas, kept.info)
	}
	s.prio.prune(now)
	if err := s.recountReportsIn(ctx, repWin); err != nil {
		note(err)
	}
	repDone = true
	return removed, first
}

// swept is what sweepOld leaves.
type swept struct {
	metas []docstore.Meta
	info  map[string]reportInfo
}

// sweepOld deletes the records under prefix whose own timestamp is older than ttl; one that
// cannot be read as a record is deleted too once it is, by its adapter timestamp, that old. It
// returns the records that stay, with what at read from them (nil if the listing failed).
func (s *Service) sweepOld(ctx context.Context, prefix string, ttl time.Duration, at func([]byte) (time.Time, reportInfo, bool), del func(string) bool, note func(error), onDeleted func(key string, info reportInfo)) *swept {
	metas, err := docstore.ListPrefix(ctx, s.store, prefix)
	if err != nil {
		note(err)
		return nil
	}
	now := s.now()
	out := &swept{info: map[string]reportInfo{}}
	for _, m := range metas {
		d, err := s.store.Load(ctx, m.ID)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			note(err)
			out.metas = append(out.metas, m)
			continue
		}
		t, info, ok := at(d.Data)
		if !ok {
			t = d.UpdatedAt
		}
		if now.Sub(t) >= ttl {
			if del(m.ID) && onDeleted != nil {
				onDeleted(m.ID, info)
			}
			continue
		}
		out.metas = append(out.metas, m)
		out.info[m.ID] = info
	}
	return out
}

// Run sweeps now and then every interval until ctx is done (the first sweep reconciles the
// counters, so the byte budget is right from the start); errors go to the log.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	s.healthMu.Lock()
	s.sweepEvery = interval
	s.healthMu.Unlock()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
			log.Printf("public docstore: sweep: %s", redact(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
