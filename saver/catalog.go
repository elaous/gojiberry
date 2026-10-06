package saver

import (
	"context"
	"encoding/json"
	"errors"
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
	// Protection is ProtectionE2EE for a confidential document, whose Name is then the
	// placeholder and Tags empty (the real ones are in the document); "" is the server's.
	Protection string `json:"protection,omitempty"`
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
	// Protection is ProtectionE2EE for a confidential folder, whose Name is then
	// ConfidentialFolderName (the real one is in the encrypted record folderSecretKey);
	// "" is the server's. The record stays readable: the shape of the tree is not hidden.
	Protection string `json:"protection,omitempty"`
}

type catalog struct {
	Docs    map[string]catalogEntry `json:"docs"`
	Folders map[string]folderEntry  `json:"folders,omitempty"`
	// Skipped are the files the last rebuild left out (unreadable, or the older of two
	// files with one ID), so that Repair does not take them for work to do each time.
	Skipped []string `json:"skipped,omitempty"`

	// fresh are the files this attempt of an update wrote under new keys: if the
	// catalog is not written (a conflict, an error), nobody points at them, and update
	// deletes them.
	fresh []string
}

func (c *catalog) entry(s *Saver, id string) Entry {
	e := c.Docs[id]
	codec, _ := s.Codec(e.Format)
	out := Entry{ID: id, Key: e.Key, Name: e.Name, Format: codec, Size: e.Size, Updated: time.Unix(0, e.Updated), Tags: e.Tags, Folder: e.Folder, Protection: level(e.Protection)}
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
		c, err := s.build(ctx, t, nil)
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
	// (An adapter that writes conditionally needs no lock for the catalog: it detects
	// the other writer and the update is made again.)
	if _, conditional := t.Adapter.(docstore.ConditionalAdapter); !conditional {
		unlock, err := lockTarget(ctx, t)
		if err != nil {
			return err
		}
		defer unlock()
	}
	// With an adapter that can write conditionally, a catalog changed by another
	// process since it was read is read again and f is run again on it, so f must
	// be safe to repeat (it is: a file it wrote is listed in c.fresh, and removed).
	for try := 0; try < maxUpdateTries; try++ {
		c, rev, err := s.readCatalogRev(ctx, t)
		if err != nil {
			return err
		}
		var later []func()
		// Files this attempt wrote that no catalog points at are removed when it fails.
		discard := func() {
			for _, k := range c.fresh {
				_ = t.Adapter.Delete(ctx, k)
			}
		}
		if err := f(t, c, &later); err != nil {
			discard()
			return err
		}
		ok, err := s.writeCatalogIf(ctx, t, c, rev)
		if err != nil {
			discard()
			return err
		}
		if !ok {
			discard()
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

// lockTarget takes the store's cross-process lock if it has one (docstore.Locker), and
// returns what releases it.
func lockTarget(ctx context.Context, t Target) (func(), error) {
	if l, ok := t.Adapter.(docstore.Locker); ok {
		return l.Lock(ctx)
	}
	return func() {}, nil
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
	unlock, err := lockTarget(ctx, t)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.rebuild(ctx, t, s.peekCatalog(ctx, t))
	return err
}

// rebuild builds the catalog from the documents and writes it, over whatever is
// there: for a repair, where the stored catalog is what is wrong.
func (s *Saver) rebuild(ctx context.Context, t Target, known *catalog) (*catalog, error) {
	out, err := s.build(ctx, t, known)
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
//
// known is the catalog that was there, if it could be read: its checksums stay the
// authority for the files it lists, so a file that no longer matches is not given a new,
// matching checksum by the rebuild (a read of it still finds it damaged and falls back to
// the previous copy). With no known catalog the files are taken as they are.
//
// Two files with one ID are the document and its previous copy: the newer is the
// document. The exception is a newer file the old catalog has a checksum for that no
// longer matches while the older file is vouched for (the old catalog has a non-empty
// checksum for it and it matches): then the older is the document, and the changed file
// is left in Skipped, to be removed by CollectOrphans once it is old enough. An older
// file the old catalog knows nothing about is never put back over a newer one.
func (s *Saver) build(ctx context.Context, t Target, known *catalog) (*catalog, error) {
	knownSum := map[string]string{}
	knownEntry := map[string]catalogEntry{} // by file
	if known != nil {
		for _, e := range known.Docs {
			knownEntry[e.Key], knownEntry[e.Prev] = e, e
			if e.Sum != "" {
				knownSum[e.Key] = e.Sum
			}
			if e.PrevSum != "" {
				knownSum[e.Prev] = e.PrevSum
			}
		}
	}
	metas, err := t.Adapter.List(ctx)
	if err != nil {
		return nil, err
	}
	out := &catalog{Docs: map[string]catalogEntry{}, Folders: map[string]folderEntry{}}
	modified := map[string]int64{} // each file's modification time, to break a tie
	actual := map[string]string{}  // each file's checksum as it is now
	// changed says a file is not what the catalog being replaced recorded for it.
	changed := func(key string) bool { ks := knownSum[key]; return ks != "" && actual[key] != ks }
	// vouched says the old catalog has a checksum for a file and the file still has it.
	// A file it knows nothing about is not vouched for.
	vouched := func(key string) bool {
		ks := knownSum[key]
		if ks == "" {
			return false
		}
		if _, ok := actual[key]; !ok {
			stored, err := t.Adapter.Load(ctx, key)
			if err != nil {
				return false
			}
			actual[key] = docstore.Digest(stored.Data)
		}
		return actual[key] == ks
	}
	for _, m := range metas {
		if id, ok := strings.CutPrefix(m.ID, t.folderPrefix()); ok && docfile.ValidID(id) {
			stored, err := t.Adapter.Load(ctx, m.ID)
			if err == nil {
				var f folderEntry
				if json.Unmarshal(stored.Data, &f) == nil && f.Name != "" {
					if f.Protection == ProtectionE2EE {
						f.Name = ConfidentialFolderName // whatever the record says: the name is in the secret
					}
					out.Folders[id] = f
				}
			} else if errors.Is(err, docstore.ErrLocked) {
				// A store whose folder records are encrypted and no key here opens one: it is
				// listed as a confidential folder under the placeholder, with its place in the
				// tree from the catalog being replaced (the top level if that did not list it).
				f := folderEntry{}
				if known != nil {
					f = known.Folders[id]
				}
				f.Name, f.Protection = ConfidentialFolderName, ProtectionE2EE
				out.Folders[id] = f
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
		if errors.Is(err, docstore.ErrLocked) {
			// A file no key here opens (an end-to-end document, rebuilt without its key): it
			// is listed without being read, as confidential unless the catalog being replaced
			// says otherwise. Its folder and trash flag are in the file, so they are lost
			// unless that catalog had them.
			id := stem
			if i := strings.IndexByte(m.ID, '~'); i >= 0 {
				id = m.ID[:i]
			}
			if !docfile.ValidID(id) {
				out.Skipped = append(out.Skipped, m.ID)
				continue
			}
			entry := lockedEntry(m, knownEntry)
			if ks := knownSum[m.ID]; ks != "" {
				entry.Sum = ks
			} else {
				entry.Sum = ""
			}
			modified[m.ID] = m.UpdatedAt.UnixNano()
			if cur, ok := out.Docs[id]; ok {
				older, newer := entry, cur
				if modified[cur.Key] < modified[entry.Key] {
					older, newer = cur, entry
				}
				if s.previousCopies() > 0 && newer.Prev == "" {
					newer.Prev, newer.PrevSum = older.Key, older.Sum
				} else {
					out.Skipped = append(out.Skipped, older.Key)
				}
				entry = newer
			}
			out.Docs[id] = entry
			continue
		}
		if err != nil {
			return nil, err
		}
		doc, err := c.Unmarshal(stored.Data)
		if err != nil {
			out.Skipped = append(out.Skipped, m.ID)
			continue
		}
		key, size, sum := m.ID, m.Size, docstore.Digest(stored.Data)
		if ks := knownSum[m.ID]; ks != "" {
			sum = ks
		}
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
		// The document's own save time (to the nanosecond, in the file) says which file is
		// newer; a store's modification times can be coarse, and equal for two files.
		saved := doc.Updated
		if saved == 0 {
			saved = m.UpdatedAt.UnixNano()
		}
		entry := catalogEntry{Key: key, Name: doc.Name(), Format: c.Name(), Size: size, Updated: saved, Tags: doc.Tags(), Trashed: doc.Trashed(), Folder: doc.Folder(), Sum: sum}
		// The document says it is confidential (or the catalog being replaced did): its real
		// name and tags stay in it, and the catalog lists the placeholder.
		if k, ok := knownEntry[m.ID]; doc.Protection() == ProtectionE2EE || ok && k.confidential() {
			entry.conceal()
		}
		modified[key] = m.UpdatedAt.UnixNano()
		actual[key] = docstore.Digest(stored.Data)
		if cur, ok := out.Docs[id]; ok {
			older, newer := entry, cur
			if cur.Updated < entry.Updated || cur.Updated == entry.Updated && modified[cur.Key] < modified[entry.Key] {
				older, newer = cur, entry
			}
			// The newer file is not the one the old catalog vouched for, and the older
			// is (the old catalog has a checksum for it and it still matches): the older
			// is the document. (The changed file is left out; it is removed with the other
			// leftovers once it is old enough, see CollectOrphans.)
			if changed(newer.Key) && vouched(older.Key) {
				out.Skipped = append(out.Skipped, newer.Key)
				if newer.Prev != "" && newer.Prev != older.Key { // a stray taken as its previous copy
					out.Skipped = append(out.Skipped, newer.Prev)
				}
				if older.Prev != "" && !vouched(older.Prev) {
					out.Skipped = append(out.Skipped, older.Prev)
					older.Prev, older.PrevSum = "", ""
				}
				out.Docs[id] = older
				continue
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
func (s *Saver) touch(ctx context.Context, t Target, cat *catalog, id string, e *catalogEntry, later *[]func(), f func(*docfile.Document)) error {
	write, err := s.prepareTouch(ctx, t, cat, id, e, later, f)
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
func (s *Saver) prepareTouch(ctx context.Context, t Target, cat *catalog, id string, e *catalogEntry, later *[]func(), f func(*docfile.Document)) (write func() error, err error) {
	c, _ := s.codecOf(e.Key)
	if c == nil {
		return func() error { return nil }, nil
	}
	doc, _, fromPrev, err := s.readEntry(ctx, t, *e)
	if err != nil {
		return nil, err // wraps ErrUnreadable when no copy is good
	}
	if fromPrev {
		// The current file is bad; the previous copy stands in, and the new file
		// replaces the bad one.
		bad := e.Key
		e.Key, e.Sum = e.Prev, e.PrevSum
		e.Prev, e.PrevSum = "", ""
		*later = append(*later, func() { _ = t.Adapter.Delete(ctx, bad) })
	}
	f(&doc)
	doc.SetProtection(e.Protection) // the file always says what the catalog says
	data, err := c.Marshal(doc)
	if err != nil {
		return nil, err
	}
	key := genKey(id, c)
	// Only the metadata changes: the previous copy stays the previous save, and the
	// file it replaces is dropped.
	s.rotateTouch(ctx, t, e, key, data, later)
	return func() error {
		cat.fresh = append(cat.fresh, key)
		return t.Adapter.Save(ctx, key, data)
	}, nil
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
	unlock, err := lockTarget(ctx, t)
	if err != nil {
		return false, err
	}
	defer unlock()
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
		built, err := s.build(ctx, t, &c)
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

// peekCatalog reads the stored catalog if there is a readable one, and nil otherwise.
func (s *Saver) peekCatalog(ctx context.Context, t Target) *catalog {
	stored, err := t.Adapter.Load(ctx, t.catalogKey())
	if err != nil {
		return nil
	}
	var c catalog
	if json.Unmarshal(stored.Data, &c) != nil || c.Docs == nil {
		return nil
	}
	return &c
}
