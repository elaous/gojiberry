package saver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// CatalogKey is where a target keeps its catalog: one small document listing
// every saved document by ID with its name, tags and whether it is in the
// trash, so a document list is one read, not one read per document. The
// documents hold the same facts (Document.ID, Name, Tags and Trashed), kept in
// step by every operation, so a lost or damaged catalog is rebuilt from them
// (Saver.Rebuild).
const CatalogKey = "catalog:index"

// FolderRecordPrefix starts the key each folder is also written under
// (catalog:folder/<id>), so a rebuild finds folders that hold no document.
const FolderRecordPrefix = "catalog:folder/"

// catalogKey is where this target keeps its catalog: Target.CatalogKey, or CatalogKey.
func (t Target) catalogKey() string {
	if t.CatalogKey != "" {
		return t.CatalogKey
	}
	return CatalogKey
}

// folderPrefix is where this target keeps its folder records: next to its catalog
// ("catalog:index" has "catalog:folder/", "catalog:u/ab12/index" has
// "catalog:u/ab12/folder/").
func (t Target) folderPrefix() string {
	return strings.TrimSuffix(t.catalogKey(), "index") + "folder/"
}

// UserCatalogKey is a catalog key for one person's view of a store several people
// share (Target.CatalogKey): the catalog lists documents, so one catalog for everyone
// would be one person's list, owned by whoever saved first. The key hides the
// identifier (it is a hash of it).
func UserCatalogKey(userID string) string { return docstore.UserCatalogPrefix(userID) + "index" }

type catalogEntry struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Format  string   `json:"format"`
	Size    int      `json:"size,omitempty"`
	Updated int64    `json:"updated,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Trashed int64    `json:"trashed,omitempty"`
	Folder  string   `json:"folder,omitempty"` // the folder it is in; "" at the top level
	// Sum is the SHA-256 (hex) of the current file's bytes, "" for a document saved
	// before checksums (Verify gives it one). Prev is the file of the save before the
	// current one, with its checksum, kept as a light redundancy: if the current file is
	// missing or damaged the document is read from it (see integrity.go).
	Sum     string `json:"sum,omitempty"`
	Prev    string `json:"prev,omitempty"`
	PrevSum string `json:"prevSum,omitempty"`
}

type folderEntry struct {
	Name   string `json:"name"`
	Parent string `json:"parent,omitempty"`
}

type catalog struct {
	Docs    map[string]catalogEntry `json:"docs"`
	Folders map[string]folderEntry  `json:"folders,omitempty"`
	// Skipped are the files the last rebuild left out (unreadable, or the older of two
	// files with one ID), so that Repair does not take them for work to do each time.
	Skipped []string `json:"skipped,omitempty"`
}

func (c *catalog) entry(s *Saver, id string) Entry {
	e := c.Docs[id]
	codec, _ := s.Codec(e.Format)
	out := Entry{ID: id, Key: e.Key, Name: e.Name, Format: codec, Size: e.Size, Updated: time.Unix(0, e.Updated), Tags: e.Tags, Folder: e.Folder}
	if e.Trashed != 0 {
		out.Trashed = time.Unix(0, e.Trashed)
	}
	return out
}

// readCatalog loads the target's catalog, building it from the documents when
// there is none.
func (s *Saver) readCatalog(ctx context.Context, t Target) (*catalog, error) {
	c, _, err := s.readCatalogRev(ctx, t)
	return c, err
}

// readCatalogRev is readCatalog and the digest of what was stored, for a
// conditional write back. A missing or unreadable catalog is built from the
// documents and stored only if nobody else got there first (else it is read again).
func (s *Saver) readCatalogRev(ctx context.Context, t Target) (*catalog, string, error) {
	for try := 0; try < maxUpdateTries; try++ {
		stored, err := t.Adapter.Load(ctx, t.catalogKey())
		rev := ""
		if err == nil {
			rev = docstore.Digest(stored.Data)
			var c catalog
			if json.Unmarshal(stored.Data, &c) == nil && c.Docs != nil {
				if c.Folders == nil {
					c.Folders = map[string]folderEntry{}
				}
				return &c, rev, nil
			}
		} else if !errors.Is(err, docstore.ErrNotFound) {
			return nil, "", err
		}
		c, err := s.build(ctx, t)
		if err != nil {
			return nil, "", err
		}
		ok, err := s.writeCatalogIf(ctx, t, c, rev)
		if err != nil {
			return nil, "", err
		}
		if ok {
			data, err := json.Marshal(c)
			if err != nil {
				return nil, "", err
			}
			return c, docstore.Digest(data), nil
		}
	}
	return nil, "", ErrBusy
}

func (s *Saver) writeCatalog(ctx context.Context, t Target, c *catalog) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return t.Adapter.Save(ctx, t.catalogKey(), data)
}

// writeCatalogIf writes the catalog only if it is still the one read (rev, from
// readCatalogRev), when the target's adapter can say; ok is false then and the
// caller read again. An adapter that cannot (or a store behind one that cannot)
// just writes.
func (s *Saver) writeCatalogIf(ctx context.Context, t Target, c *catalog, rev string) (ok bool, err error) {
	cond, can := t.Adapter.(docstore.ConditionalAdapter)
	if !can {
		return true, s.writeCatalog(ctx, t, c)
	}
	data, err := json.Marshal(c)
	if err != nil {
		return false, err
	}
	switch err := cond.SaveIf(ctx, t.catalogKey(), data, rev); {
	case err == nil:
		return true, nil
	case errors.Is(err, docstore.ErrConflict):
		return false, nil
	case errors.Is(err, docstore.ErrConditionalUnsupported):
		return true, s.writeCatalog(ctx, t, c)
	default:
		return false, err
	}
}

// maxUpdateTries is how often update starts over when another process changes the
// catalog under it.
const maxUpdateTries = 12

// update runs f on the target's catalog and writes it back, one update at a time
// within this process. f may add to later the work to do once the catalog is
// written (deleting files): the catalog goes first, so a failure in between leaves
// an orphan file, which a rebuild can find, never an entry pointing at nothing.
func (s *Saver) update(ctx context.Context, target string, f func(t Target, c *catalog, later *[]func()) error) error {
	t, err := s.Target(target)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A store that others share but that cannot write conditionally (a browser's
	// localStorage) offers a lock instead: hold it across the read and the write.
	if l, ok := t.Adapter.(docstore.Locker); ok {
		unlock, err := l.Lock(ctx)
		if err != nil {
			return err
		}
		defer unlock()
	}
	// With an adapter that can write conditionally, a catalog changed by another
	// process since it was read is read again and f is run again on it, so f must
	// be safe to repeat (it is: files are keyed by ID).
	for try := 0; try < maxUpdateTries; try++ {
		c, rev, err := s.readCatalogRev(ctx, t)
		if err != nil {
			return err
		}
		var later []func()
		if err := f(t, c, &later); err != nil {
			return err
		}
		ok, err := s.writeCatalogIf(ctx, t, c, rev)
		if err != nil {
			return err
		}
		if !ok {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(try+1) * 5 * time.Millisecond):
			}
			continue
		}
		for _, g := range later {
			g()
		}
		return nil
	}
	return ErrBusy
}

// view runs f on the target's catalog without writing it.
func (s *Saver) view(ctx context.Context, target string, f func(t Target, c *catalog) error) error {
	t, err := s.Target(target)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.readCatalog(ctx, t)
	if err != nil {
		return err
	}
	return f(t, c)
}

// Rebuild rewrites the target's catalog from the documents in it: for a store
// whose catalog was lost, or that was written to by something else. A document
// with no ID of its own (one saved before IDs, under its name) is listed under
// its key.
func (s *Saver) Rebuild(ctx context.Context, target string) error {
	t, err := s.Target(target)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.rebuild(ctx, t)
	return err
}

// rebuild builds the catalog from the documents and writes it, over whatever is
// there: for a repair, where the stored catalog is what is wrong.
func (s *Saver) rebuild(ctx context.Context, t Target) (*catalog, error) {
	out, err := s.build(ctx, t)
	if err != nil {
		return nil, err
	}
	if err := s.writeCatalog(ctx, t, out); err != nil {
		return nil, err
	}
	return out, nil
}

// build reads the catalog the documents in the target add up to, writing nothing
// but the files of documents that had no ID.
func (s *Saver) build(ctx context.Context, t Target) (*catalog, error) {
	metas, err := t.Adapter.List(ctx)
	if err != nil {
		return nil, err
	}
	out := &catalog{Docs: map[string]catalogEntry{}, Folders: map[string]folderEntry{}}
	for _, m := range metas {
		if id, ok := strings.CutPrefix(m.ID, t.folderPrefix()); ok && docfile.ValidID(id) {
			if stored, err := t.Adapter.Load(ctx, m.ID); err == nil {
				var f folderEntry
				if json.Unmarshal(stored.Data, &f) == nil && f.Name != "" {
					out.Folders[id] = f
				}
			}
			continue
		}
		if strings.HasPrefix(m.ID, FontPrefix) || reserved(m.ID) {
			continue
		}
		c, stem := s.codecOf(m.ID)
		if c == nil {
			continue
		}
		stored, err := t.Adapter.Load(ctx, m.ID)
		if err != nil {
			return nil, err
		}
		doc, err := c.Unmarshal(stored.Data)
		if err != nil {
			out.Skipped = append(out.Skipped, m.ID)
			continue
		}
		key, size, sum := m.ID, m.Size, docstore.Digest(stored.Data)
		id := doc.ID()
		if !docfile.ValidID(id) {
			// A document from before IDs, under its name: it gets an ID, and its file
			// the key that goes with it.
			id = stem
			if !docfile.ValidID(id) {
				id = docfile.NewID()
			}
			if doc.Name() == "" {
				doc.SetName(stem)
			}
			doc.SetID(id)
			data, err := c.Marshal(doc)
			if err != nil {
				return nil, err
			}
			newKey := id + c.Ext()
			if err := t.Adapter.Save(ctx, newKey, data); err != nil {
				return nil, err
			}
			if newKey != m.ID {
				_ = t.Adapter.Delete(ctx, m.ID)
			}
			key, size, sum = newKey, len(data), docstore.Digest(data)
		}
		// Two files with one ID are a document and its previous copy (or the leftovers of
		// a crash between writing a new save and the catalog): the newer is the document,
		// the next newer its previous copy, any others are left out.
		entry := catalogEntry{Key: key, Name: doc.Name(), Format: c.Name(), Size: size, Updated: m.UpdatedAt.UnixNano(), Tags: doc.Tags(), Trashed: doc.Trashed(), Folder: doc.Folder(), Sum: sum}
		if cur, ok := out.Docs[id]; ok {
			older, newer := entry, cur
			if cur.Updated < entry.Updated {
				older, newer = cur, entry
			}
			if s.previousCopies() > 0 && newer.Prev == "" {
				newer.Prev, newer.PrevSum = older.Key, older.Sum
			} else {
				out.Skipped = append(out.Skipped, older.Key)
			}
			if older.Prev != "" { // the older one's own previous copy
				out.Skipped = append(out.Skipped, older.Prev)
			}
			entry = newer
		}
		out.Docs[id] = entry
	}
	// A folder whose parent is gone (deleted since its record was written), or that
	// sits in a cycle, goes to the top level.
	for id, f := range out.Folders {
		if _, ok := out.Folders[f.Parent]; f.Parent != "" && !ok || out.depth(id) > MaxFolderDepth+1 {
			f.Parent = ""
			out.Folders[id] = f
		}
	}
	for id, e := range out.Docs {
		if e.Name == "" {
			e.Name = id
		}
		if _, ok := out.Folders[e.Folder]; e.Folder != "" && !ok {
			e.Folder = "" // its folder is gone: the top level
		}
		out.Docs[id] = e
	}
	return out, nil
}

// touch rewrites a document's own metadata (name, tags, trash flag, folder) so
// the document and the catalog agree and a rebuilt catalog loses nothing. The
// document's Updated time is not changed.
func (s *Saver) touch(ctx context.Context, t Target, id string, e *catalogEntry, later *[]func(), f func(*docfile.Document)) error {
	write, err := s.prepareTouch(ctx, t, id, e, later, f)
	if err != nil {
		return err
	}
	return write()
}

// prepareTouch is touch in two steps: everything that can fail without changing
// anything (reading, checking, converting) now, and the write it returns. Operations
// on several documents prepare all of them first. Like a save it writes a new file
// (the entry e is given its key, checksum and previous copy here; the catalog entry is
// the caller's to store), and refuses a document whose file does not match its checksum
// (ErrUnreadable) rather than copying damage into a fresh, valid-looking file.
func (s *Saver) prepareTouch(ctx context.Context, t Target, id string, e *catalogEntry, later *[]func(), f func(*docfile.Document)) (write func() error, err error) {
	c, _ := s.codecOf(e.Key)
	if c == nil {
		return func() error { return nil }, nil
	}
	doc, _, err := s.tryRead(ctx, t, e.Key, e.Sum)
	if err != nil {
		if missingOrDamaged(err) {
			return nil, fmt.Errorf("%w: %w", ErrUnreadable, err)
		}
		return nil, err
	}
	f(&doc)
	data, err := c.Marshal(doc)
	if err != nil {
		return nil, err
	}
	key := genKey(id, c)
	s.rotate(ctx, t, e, key, data, later)
	return func() error { return t.Adapter.Save(ctx, key, data) }, nil
}

// sorted returns the catalog's entries (those in or out of the trash), newest
// first.
func (c *catalog) sorted(s *Saver, trashed bool) []Entry {
	var out []Entry
	for id, e := range c.Docs {
		if (e.Trashed != 0) == trashed {
			out = append(out, c.entry(s, id))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Updated, out[j].Updated
		if trashed {
			a, b = out[i].Trashed, out[j].Trashed
		}
		if !a.Equal(b) {
			return a.After(b)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Repair makes the catalog agree with what is in the store, if it does not: a
// document file nobody listed, a listed document whose file is gone, a folder
// record the catalog lacks, or the other way round. These are what a crash between
// the two writes of an operation leaves behind (the file or record is written
// first, so the leftovers are orphans, never lost data). It costs one listing of
// the store when everything agrees, and a rebuild (which reads the documents)
// when not; it reports whether it had to repair.
func (s *Saver) Repair(ctx context.Context, target string) (repaired bool, err error) {
	t, err := s.Target(target)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	grace := s.RepairGrace
	if grace == 0 {
		grace = DefaultRepairGrace
	}
	for try := 0; try < maxUpdateTries; try++ {
		stored, err := t.Adapter.Load(ctx, t.catalogKey())
		var c catalog
		if err == nil && (json.Unmarshal(stored.Data, &c) != nil || c.Docs == nil) {
			err = errors.New("unreadable catalog")
		}
		if err != nil {
			if !errors.Is(err, docstore.ErrNotFound) && err.Error() != "unreadable catalog" {
				return false, err
			}
			// None, or not readable: readCatalogRev builds one (and only stores it if nobody
			// else did first).
			if _, _, err := s.readCatalogRev(ctx, t); err != nil {
				return false, err
			}
			return true, nil
		}
		bad, err := s.disagrees(ctx, t, &c)
		if err != nil || !bad {
			return false, err
		}
		// Another process may be half way through an operation (a delete takes the
		// document out of the catalog first and removes its file after): look again
		// after a moment, and only repair what is still wrong.
		if grace > 0 {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(grace):
			}
			stored, err = t.Adapter.Load(ctx, t.catalogKey())
			if err != nil {
				continue
			}
			c = catalog{}
			if json.Unmarshal(stored.Data, &c) != nil || c.Docs == nil {
				continue
			}
			if bad, err = s.disagrees(ctx, t, &c); err != nil || !bad {
				return false, err
			}
		}
		built, err := s.build(ctx, t)
		if err != nil {
			return false, err
		}
		ok, err := s.writeCatalogIf(ctx, t, built, docstore.Digest(stored.Data))
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, ErrBusy
}

// disagrees reports whether the catalog and the files and folder records in the
// store do not match (ignoring the files the last rebuild chose to skip).
func (s *Saver) disagrees(ctx context.Context, t Target, c *catalog) (bool, error) {
	metas, err := t.Adapter.List(ctx)
	if err != nil {
		return false, err
	}
	skipped := map[string]bool{}
	for _, k := range c.Skipped {
		skipped[k] = true
	}
	files, records := map[string]bool{}, map[string]bool{}
	for _, m := range metas {
		if id, ok := strings.CutPrefix(m.ID, t.folderPrefix()); ok {
			records[id] = true
			continue
		}
		if strings.HasPrefix(m.ID, FontPrefix) || reserved(m.ID) || skipped[m.ID] {
			continue
		}
		if codec, _ := s.codecOf(m.ID); codec != nil {
			files[m.ID] = true
		}
	}
	// Every file there is must be one the catalog knows (a document's current file or
	// its previous copy), and every current file must be there; a previous copy that is
	// gone does not matter.
	known := map[string]bool{}
	same := len(records) == len(c.Folders)
	for _, e := range c.Docs {
		known[e.Key] = true
		if e.Prev != "" {
			known[e.Prev] = true
		}
		same = same && files[e.Key]
	}
	for k := range files {
		same = same && known[k]
	}
	for id := range c.Folders {
		same = same && records[id]
	}
	return !same, nil
}
