package saver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

var ErrDamaged = errors.New("saver: the document's file is damaged")

const DefaultPreviousCopies = 1

func (s *Saver) previousCopies() int {
	switch {
	case s.PreviousCopies < 0:
		return 0
	case s.PreviousCopies == 0:
		return DefaultPreviousCopies
	}
	return s.PreviousCopies
}

// new key per save: never overwrite the catalogued file
func genKey(id string, c docfile.Codec) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return id + "~" + hex.EncodeToString(b[:]) + c.Ext()
}

func (s *Saver) rotateTouch(ctx context.Context, t Target, e *catalogEntry, key string, data []byte, later *[]func()) {
	if old := e.Key; old != "" {
		if e.Prev == "" && s.previousCopies() > 0 {
			e.Prev, e.PrevSum = old, e.Sum
		} else {
			*later = append(*later, func() { _ = t.Adapter.Delete(ctx, old) })
		}
	}
	e.Key, e.Sum, e.Size = key, docstore.Digest(data), len(data)
}

func (s *Saver) rotate(ctx context.Context, t Target, e *catalogEntry, key string, data []byte, later *[]func()) {
	drop := func(k string) {
		if k != "" {
			*later = append(*later, func() { _ = t.Adapter.Delete(ctx, k) })
		}
	}
	if s.previousCopies() > 0 {
		drop(e.Prev)
		e.Prev, e.PrevSum = e.Key, e.Sum
	} else {
		drop(e.Key)
		drop(e.Prev)
		e.Prev, e.PrevSum = "", ""
	}
	e.Key, e.Sum, e.Size = key, docstore.Digest(data), len(data)
}

func (s *Saver) tryRead(ctx context.Context, t Target, key, sum string) (docfile.Document, []byte, error) {
	stored, err := t.Adapter.Load(ctx, key)
	if err != nil {
		return docfile.Document{}, nil, err
	}
	if sum != "" && docstore.Digest(stored.Data) != sum {
		return docfile.Document{}, nil, fmt.Errorf("%w: %s does not match its checksum", ErrDamaged, key)
	}
	c, _ := s.codecOf(key)
	if c == nil {
		c = docfile.Detect(stored.Data)
	}
	doc, err := c.Unmarshal(stored.Data)
	if err != nil {
		return docfile.Document{}, nil, fmt.Errorf("%w: %s does not parse: %w", ErrDamaged, key, err)
	}
	return doc, stored.Data, nil
}

// a store failure is not missing: nothing stands in
func missingOrDamaged(err error) bool {
	return errors.Is(err, docstore.ErrNotFound) || errors.Is(err, ErrDamaged)
}

func (s *Saver) readEntry(ctx context.Context, t Target, e catalogEntry) (doc docfile.Document, data []byte, fromPrev bool, err error) {
	doc, data, err = s.tryRead(ctx, t, e.Key, e.Sum)
	if err == nil {
		return doc, data, false, nil
	}
	if !missingOrDamaged(err) {
		return docfile.Document{}, nil, false, err
	}
	prevGone := true // no previous copy, or it is missing too
	if e.Prev != "" {
		pdoc, pdata, perr := s.tryRead(ctx, t, e.Prev, e.PrevSum)
		switch {
		case perr == nil:
			return pdoc, pdata, true, nil
		case !missingOrDamaged(perr):
			return docfile.Document{}, nil, false, perr
		}
		prevGone = errors.Is(perr, docstore.ErrNotFound)
	}
	if errors.Is(err, docstore.ErrNotFound) && prevGone {
		return docfile.Document{}, nil, false, fmt.Errorf("%w: %w: %w", ErrUnreadable, ErrDamaged, err)
	}
	if errors.Is(err, ErrDamaged) {
		return docfile.Document{}, nil, false, fmt.Errorf("%w: %w", ErrUnreadable, err)
	}
	return docfile.Document{}, nil, false, fmt.Errorf("%w: %w: %v", ErrUnreadable, ErrDamaged, err)
}

type Recovery struct {
	FromPrevious bool
	// Saved is when the copy that was used was saved.
	Saved time.Time
}

func (s *Saver) LoadChecked(ctx context.Context, target, id string) (docfile.Document, Recovery, error) {
	var rec Recovery
	for try := 0; ; try++ {
		var e catalogEntry
		var t Target
		err := s.view(ctx, target, func(tg Target, cat *catalog) error {
			var ok bool
			if e, ok = cat.Docs[id]; !ok {
				return ErrNotFound
			}
			t = tg
			return nil
		})
		if err != nil {
			return docfile.Document{}, rec, err
		}
		doc, _, fromPrev, err := s.readEntry(ctx, t, e)
		if err != nil {
			if try < 3 && errors.Is(err, ErrUnreadable) && s.changed(ctx, target, id, e) {
				continue
			}
			return docfile.Document{}, rec, err
		}
		if fromPrev {
			rec = Recovery{FromPrevious: true, Saved: time.Unix(0, doc.Updated)}
			_ = s.promotePrevious(ctx, target, id, e, doc) // best effort: the document is readable either way
		}
		doc.SetID(id)
		e.withCatalog(&doc)
		return doc, rec, nil
	}
}

func (s *Saver) changed(ctx context.Context, target, id string, e catalogEntry) bool {
	same := false
	err := s.view(ctx, target, func(_ Target, cat *catalog) error {
		cur, ok := cat.Docs[id]
		same = ok && cur.Key == e.Key && cur.Prev == e.Prev
		return nil
	})
	return err == nil && !same
}

func (s *Saver) promoteEntry(ctx context.Context, t Target, cat *catalog, id string, e *catalogEntry, doc docfile.Document, later *[]func()) error {
	c, _ := s.codecOf(e.Prev)
	if c == nil {
		c, _ = s.codecOf(e.Key)
	}
	if c == nil {
		return errors.New("saver: unknown format")
	}
	doc.SetID(id)
	e.withCatalog(&doc)
	doc.SetTrashed(e.Trashed)
	data, err := c.Marshal(doc)
	if err != nil {
		return err
	}
	key := genKey(id, c)
	cat.fresh = append(cat.fresh, key)
	if err := t.Adapter.Save(ctx, key, data); err != nil {
		return err
	}
	for _, k := range []string{e.Key, e.Prev} {
		if k != "" {
			k := k
			*later = append(*later, func() { _ = t.Adapter.Delete(ctx, k) })
		}
	}
	e.Key, e.Sum, e.Size = key, docstore.Digest(data), len(data)
	e.Prev, e.PrevSum = "", ""
	return nil
}

func (s *Saver) promotePrevious(ctx context.Context, target, id string, found catalogEntry, doc docfile.Document) error {
	return s.update(ctx, target, func(t Target, cat *catalog, later *[]func()) error {
		e, ok := cat.Docs[id]
		if !ok || e.Key != found.Key || e.Prev != found.Prev {
			return nil
		}
		if err := s.promoteEntry(ctx, t, cat, id, &e, doc, later); err != nil {
			return err
		}
		cat.Docs[id] = e
		return nil
	})
}

// VerifyReport is what Verify found.
type VerifyReport struct {
	Checked      int
	Restored     []string
	Lost         []string
	BackupsLost  []string
	Orphans      int
	Sealed       int
	Locked       int
	Downgraded   []string
	Pending      []string
	PendingStuck []string
	Unsigned     []string
	Warnings     []string
}

func (s *Saver) Verify(ctx context.Context, target string) (VerifyReport, error) {
	var rep VerifyReport
	t, err := s.Target(target)
	if err != nil {
		return rep, err
	}
	type item struct {
		id string
		e  catalogEntry
	}
	var items []item
	if err := s.view(ctx, target, func(_ Target, cat *catalog) error {
		for id, e := range cat.Docs {
			items = append(items, item{id, e})
		}
		return nil
	}); err != nil {
		return rep, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].id < items[j].id })
	if s.PendingCheck != nil {
		rep.Pending, rep.PendingStuck, err = s.PendingCheck(ctx, target)
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return rep, cerr
			}
			rep.Warnings = append(rep.Warnings, "pending check incomplete (the documents it could read are reported): "+err.Error())
		}
	}
	if s.UnsignedCheck != nil {
		rep.Unsigned, err = s.UnsignedCheck(ctx, target)
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return rep, cerr
			}
			rep.Warnings = append(rep.Warnings, "unsigned check incomplete (the documents it could read are reported): "+err.Error())
		}
	}
	type fix struct {
		item
		doc      docfile.Document
		data     []byte
		fromPrev bool
		seal     bool // no checksum yet: the one just read
		dropPrev bool // the previous copy is missing or damaged: forget it
	}
	var fixes []fix
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		doc, data, fromPrev, err := s.readEntry(ctx, t, it.e)
		if errors.Is(err, docstore.ErrLocked) {
			rep.Locked++
			continue
		}
		if errors.Is(err, docstore.ErrDowngrade) {
			rep.Downgraded = append(rep.Downgraded, it.id)
			continue
		}
		rep.Checked++
		switch {
		case err == nil && fromPrev:
			fixes = append(fixes, fix{item: it, doc: doc, data: data, fromPrev: true})
			rep.Restored = append(rep.Restored, it.e.Name)
		case err == nil:
			f := fix{item: it, doc: doc, data: data, seal: it.e.Sum == ""}
			if f.seal {
				rep.Sealed++
			}
			if it.e.Prev != "" {
				_, _, perr := s.tryRead(ctx, t, it.e.Prev, it.e.PrevSum)
				switch {
				case perr == nil:
				case missingOrDamaged(perr):
					f.dropPrev = true
					rep.BackupsLost = append(rep.BackupsLost, it.e.Name)
				default:
					return rep, perr
				}
			}
			if f.seal || f.dropPrev {
				fixes = append(fixes, f)
			}
		case errors.Is(err, ErrUnreadable):
			rep.Lost = append(rep.Lost, it.e.Name)
		default:
			return rep, err // the store failing is not a finding
		}
	}
	if len(fixes) > 0 {
		err = s.update(ctx, target, func(t Target, cat *catalog, later *[]func()) error {
			for _, f := range fixes {
				e, ok := cat.Docs[f.id]
				if !ok || e.Key != f.e.Key || e.Prev != f.e.Prev {
					continue // saved or removed since: not this fix's to apply
				}
				if f.fromPrev {
					if err := s.promoteEntry(ctx, t, cat, f.id, &e, f.doc, later); err != nil {
						return err
					}
				}
				if f.dropPrev {
					if bad := e.Prev; bad != "" {
						*later = append(*later, func() { _ = t.Adapter.Delete(ctx, bad) })
					}
					e.Prev, e.PrevSum = "", ""
				}
				if e.Sum == "" {
					e.Sum = docstore.Digest(f.data)
				}
				cat.Docs[f.id] = e
			}
			return nil
		})
		if err != nil {
			return rep, err
		}
	}
	rep.Orphans, err = s.CollectOrphans(ctx, target, OrphanAge)
	return rep, err
}

// a save in progress must never be taken for a leftover
const OrphanAge = time.Hour

var generationKey = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}~[0-9a-f]{12}\.[a-z]+$`)

func (s *Saver) CollectOrphans(ctx context.Context, target string, olderThan time.Duration) (int, error) {
	n := 0
	err := s.update(ctx, target, func(t Target, cat *catalog, later *[]func()) error {
		n = 0 // update may run this again
		metas, err := t.Adapter.List(ctx)
		if err != nil {
			return err
		}
		used := map[string]bool{}
		for _, e := range cat.Docs {
			used[e.Key], used[e.Prev] = true, true
		}
		gone := map[string]bool{}
		for _, m := range metas {
			if used[m.ID] || !generationKey.MatchString(m.ID) || time.Since(m.UpdatedAt) < olderThan {
				continue
			}
			key := m.ID
			gone[key] = true
			n++
			*later = append(*later, func() { _ = t.Adapter.Delete(ctx, key) })
		}
		if len(gone) > 0 && len(cat.Skipped) > 0 {
			kept := cat.Skipped[:0]
			for _, k := range cat.Skipped {
				if !gone[k] {
					kept = append(kept, k)
				}
			}
			cat.Skipped = kept
		}
		return nil
	})
	return n, err
}
