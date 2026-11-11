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

// ExpiringHolder is implemented by a store that wraps an ExpiringAdapter.
type ExpiringHolder interface {
	Expiring() *docstore.ExpiringAdapter
}

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

// NoteVersionWrite tells the byte budget that a version was written.
func (s *Service) NoteVersionWrite(key string, size int64) {
	if id, ok := versionDoc(key); ok {
		s.bytes.addExtra(id, size)
	}
}

func versionDoc(key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, versionsKeyPrefix)
	if !ok {
		return "", false
	}
	id, _, _ := strings.Cut(rest, "/")
	return id, validID(id)
}

// creation writes the records first
const recordGrace = time.Minute

// Sweep removes what the ExpiringAdapter does not.
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
	s.bytes.set(bytesWin, listed)
	// version snapshots count with their documents
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
	known := map[string]bool{}
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
		// a report past its TTL leaves its document record too
		s.dropRef(ctx, info.doc, strings.TrimPrefix(key, docstore.ReportPrefix))
	})
	if kept != nil {
		removed += s.evictReports(ctx, kept.metas, kept.info)
	}
	s.prio.prune(now)
	if s.burst != nil {
		if _, err := s.burst.sweep(ctx, now); err != nil {
			note(err)
		}
	}
	if err := s.recountReportsIn(ctx, repWin); err != nil {
		note(err)
	}
	repDone = true
	return removed, first
}

type swept struct {
	metas []docstore.Meta
	info  map[string]reportInfo
}

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

// Run sweeps now and then every interval until ctx ends.
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
