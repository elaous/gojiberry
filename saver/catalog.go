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

const CatalogKey = "catalog:index"

// a rebuild finds folders that hold no document
const FolderRecordPrefix = "catalog:folder/"

func (t Target) catalogKey() string {
	if t.CatalogKey != "" {
		return t.CatalogKey
	}
	return CatalogKey
}

func (t Target) folderPrefix() string {
	return strings.TrimSuffix(t.catalogKey(), "index") + "folder/"
}

func UserCatalogKey(userID string) string { return docstore.UserCatalogPrefix(userID) + "index" }

type catalogEntry struct {
	Key        string   `json:"key"`
	Name       string   `json:"name"`
	Format     string   `json:"format"`
	Size       int      `json:"size,omitempty"`
	Updated    int64    `json:"updated,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Trashed    int64    `json:"trashed,omitempty"`
	Folder     string   `json:"folder,omitempty"` // the folder it is in; "" at the top level
	Protection string   `json:"protection,omitempty"`
	Sum        string   `json:"sum,omitempty"`
	Prev       string   `json:"prev,omitempty"`
	PrevSum    string   `json:"prevSum,omitempty"`
}

type folderEntry struct {
	Name       string `json:"name"`
	Parent     string `json:"parent,omitempty"`
	Protection string `json:"protection,omitempty"`
}

type catalog struct {
	Docs    map[string]catalogEntry `json:"docs"`
	Folders map[string]folderEntry  `json:"folders,omitempty"`
	Skipped []string                `json:"skipped,omitempty"`

	// files written under new keys: removed if the catalog write fails
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

func (s *Saver) readCatalog(ctx context.Context, t Target) (*catalog, error) {
	c, _, err := s.readCatalogRev(ctx, t)
	return c, err
}

// built catalog is stored only if nobody got there first
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

const maxUpdateTries = 12

// one update at a time within this process
func (s *Saver) update(ctx context.Context, target string, f func(t Target, c *catalog, later *[]func()) error) error {
	t, err := s.Target(target)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, conditional := t.Adapter.(docstore.ConditionalAdapter); !conditional {
		unlock, err := lockTarget(ctx, t)
		if err != nil {
			return err
		}
		defer unlock()
	}
	for try := 0; try < maxUpdateTries; try++ {
		c, rev, err := s.readCatalogRev(ctx, t)
		if err != nil {
			return err
		}
		var later []func()
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

func lockTarget(ctx context.Context, t Target) (func(), error) {
	if l, ok := t.Adapter.(docstore.Locker); ok {
		return l.Lock(ctx)
	}
	return func() {}, nil
}

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
	changed := func(key string) bool { ks := knownSum[key]; return ks != "" && actual[key] != ks }
	// a file the old catalog knows nothing about is not vouched for
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
			// a document from before IDs gets an ID and matching file key
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
		saved := doc.Updated
		if saved == 0 {
			saved = m.UpdatedAt.UnixNano()
		}
		entry := catalogEntry{Key: key, Name: doc.Name(), Format: c.Name(), Size: size, Updated: saved, Tags: doc.Tags(), Trashed: doc.Trashed(), Folder: doc.Folder(), Sum: sum}
		// confidential: real name and tags stay in it; catalog lists the placeholder
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
	// a folder missing its parent, or in a cycle, goes top level
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

func (s *Saver) touch(ctx context.Context, t Target, cat *catalog, id string, e *catalogEntry, later *[]func(), f func(*docfile.Document)) error {
	write, err := s.prepareTouch(ctx, t, cat, id, e, later, f)
	if err != nil {
		return err
	}
	return write()
}

// prepare first: nothing changes until the returned write runs
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
		// the current file is bad: the previous copy replaces it
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
	// metadata only: the previous copy stays the previous save
	s.rotateTouch(ctx, t, e, key, data, later)
	return func() error {
		cat.fresh = append(cat.fresh, key)
		return t.Adapter.Save(ctx, key, data)
	}, nil
}

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
			// readCatalogRev builds one, stored only if nobody else did first
			if _, _, err := s.readCatalogRev(ctx, t); err != nil {
				return false, err
			}
			return true, nil
		}
		bad, err := s.disagrees(ctx, t, &c)
		if err != nil || !bad {
			return false, err
		}
		// another process may be half way through an operation: look again
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
