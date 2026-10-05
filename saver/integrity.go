package saver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// ErrDamaged is wrapped (with ErrUnreadable) in the error for a document whose stored
// bytes are not the ones that were saved: they do not match the checksum the catalog
// keeps, or no longer parse, and there is no good previous copy to fall back to.
var ErrDamaged = errors.New("saver: the document's file is damaged")

// DefaultPreviousCopies is how many earlier saves of each document are kept next to
// the current one when Saver.PreviousCopies is 0.
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

// genKey is the key a new generation of a document is written under: the ID, a
// random tag, and the format's extension. A save never overwrites the file the
// catalog points at; it writes a new one and the catalog switches to it (after which
// the old one is the previous copy). A crash in between leaves the old, intact file
// current, and two writers never write the same key.
func genKey(id string, c docfile.Codec) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return id + "~" + hex.EncodeToString(b[:]) + c.Ext()
}

// rotateTouch is rotate for a change of metadata only: the new file takes the place of
// the current one. If the document has an earlier save as its previous copy, that stays
// (the old current file is dropped, it only differs in metadata); if it has none, the
// old file becomes the previous copy, so the rewrite is never the only copy.
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

// rotate makes data the current generation of the entry: it gets key as its file and
// a checksum, the file that was current becomes the previous copy, and the copy
// before that (and, with no previous copies kept, the old current one) is queued for
// deletion once the catalog is written.
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

// tryRead loads one stored file of a document and checks it: against its checksum when
// the catalog has one, and by parsing it. A missing file is ErrNotFound; a file that
// is there but wrong is ErrDamaged. Any other error is the store's own.
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

// missingOrDamaged says whether a tryRead error means this copy is gone or wrong (and
// the previous copy may stand in), as opposed to the store failing (and nothing may
// be assumed: an older copy must not be shown as if it were the current one).
func missingOrDamaged(err error) bool {
	return errors.Is(err, docstore.ErrNotFound) || errors.Is(err, ErrDamaged)
}

// readEntry reads a document by its catalog entry. If the current file is missing or
// damaged and a previous copy reads fine, that is returned, with fromPrev set; if both
// are bad the error wraps ErrUnreadable and ErrDamaged.
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
	// Gone for good only when every copy is missing; a damaged one is something to
	// keep (callers that delete what is gone do not touch it).
	if errors.Is(err, docstore.ErrNotFound) && prevGone {
		return docfile.Document{}, nil, false, fmt.Errorf("%w: %w: %w", ErrUnreadable, ErrDamaged, err)
	}
	if errors.Is(err, ErrDamaged) {
		return docfile.Document{}, nil, false, fmt.Errorf("%w: %w", ErrUnreadable, err)
	}
	return docfile.Document{}, nil, false, fmt.Errorf("%w: %w: %v", ErrUnreadable, ErrDamaged, err)
}

// Recovery says a document had to be read from its previous copy.
type Recovery struct {
	// FromPrevious is set when the current file was missing or damaged and the previous
	// copy was used (and made the current one again).
	FromPrevious bool
	// Saved is when the copy that was used was saved.
	Saved time.Time
}

// LoadChecked is Load that tells whether the document had to be recovered. A document
// whose current file is missing or does not match its checksum is read from its
// previous copy, and the catalog is switched to that copy, so the next read is of
// something good; the damaged file is deleted. When both are bad the error wraps
// ErrUnreadable (and ErrDamaged for a damaged file).
func (s *Saver) LoadChecked(ctx context.Context, target, id string) (docfile.Document, Recovery, error) {
	var rec Recovery
	// The catalog is read under the lock and the files outside it. A save that lands in
	// between replaces both files the snapshot names; a document that cannot be read
	// through a snapshot that is no longer current is looked at again.
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
		doc.SetName(e.Name)
		doc.SetTags(e.Tags)
		doc.SetFolder(e.Folder)
		return doc, rec, nil
	}
}

// changed says whether the document's entry is no longer the one e (or it is gone).
func (s *Saver) changed(ctx context.Context, target, id string, e catalogEntry) bool {
	same := false
	err := s.view(ctx, target, func(_ Target, cat *catalog) error {
		cur, ok := cat.Docs[id]
		same = ok && cur.Key == e.Key && cur.Prev == e.Prev
		return nil
	})
	return err == nil && !same
}

// promoteEntry makes doc (read from the entry's previous copy) the current file of the
// entry: written as a new generation with the metadata the catalog has (the old file
// holds the name, tags, folder and trash flag of its time), so that a catalog rebuilt
// later from the files loses nothing. The bad file and the old previous copy are dropped.
func (s *Saver) promoteEntry(ctx context.Context, t Target, cat *catalog, id string, e *catalogEntry, doc docfile.Document, later *[]func()) error {
	c, _ := s.codecOf(e.Prev)
	if c == nil {
		c, _ = s.codecOf(e.Key)
	}
	if c == nil {
		return errors.New("saver: unknown format")
	}
	doc.SetID(id)
	doc.SetName(e.Name)
	doc.SetTags(e.Tags)
	doc.SetFolder(e.Folder)
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

// promotePrevious makes the previous copy of a document the current one, if the entry is
// still as it was when it was found damaged (somebody may have saved since).
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
	// Checked is how many documents (in the trash too) were read and compared with their
	// checksums.
	Checked int
	// Restored names the documents whose current file was missing or damaged and were
	// put back from their previous copy.
	Restored []string
	// Lost names the documents that are damaged and have no good previous copy.
	Lost []string
	// Sealed counts documents saved before checksums existed that were read, found
	// fine, and given one.
	Sealed int
}

// Verify reads every document of the target and checks it against the checksum the
// catalog keeps (and parses it): a damaged or missing file is replaced by the previous
// copy when there is a good one; a document with no checksum yet gets one. It reports
// what it did; documents it could not restore are Lost and stay as they are, so
// nothing more is destroyed. The reads are made without holding the saver's lock.
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
	type fix struct {
		item
		doc      docfile.Document
		data     []byte
		fromPrev bool
	}
	var fixes []fix
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		doc, data, fromPrev, err := s.readEntry(ctx, t, it.e)
		rep.Checked++
		switch {
		case err == nil && fromPrev:
			fixes = append(fixes, fix{it, doc, data, true})
			rep.Restored = append(rep.Restored, it.e.Name)
		case err == nil && it.e.Sum == "":
			fixes = append(fixes, fix{it, doc, data, false})
			rep.Sealed++
		case err == nil:
		case errors.Is(err, ErrUnreadable):
			rep.Lost = append(rep.Lost, it.e.Name)
		default:
			return rep, err // the store failing is not a finding
		}
	}
	if len(fixes) == 0 {
		return rep, nil
	}
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
			if e.Sum == "" {
				e.Sum = docstore.Digest(f.data)
			}
			cat.Docs[f.id] = e
		}
		return nil
	})
	return rep, err
}
