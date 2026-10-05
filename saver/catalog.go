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

type catalogEntry struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Format  string   `json:"format"`
	Size    int      `json:"size,omitempty"`
	Updated int64    `json:"updated,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Trashed int64    `json:"trashed,omitempty"`
	Folder  string   `json:"folder,omitempty"` // the folder it is in; "" at the top level
}

type folderEntry struct {
	Name   string `json:"name"`
	Parent string `json:"parent,omitempty"`
}

type catalog struct {
	Docs    map[string]catalogEntry `json:"docs"`
	Folders map[string]folderEntry  `json:"folders,omitempty"`
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
	stored, err := t.Adapter.Load(ctx, CatalogKey)
	if errors.Is(err, docstore.ErrNotFound) {
		return s.rebuild(ctx, t)
	}
	if err != nil {
		return nil, err
	}
	var c catalog
	if err := json.Unmarshal(stored.Data, &c); err != nil || c.Docs == nil {
		return s.rebuild(ctx, t)
	}
	if c.Folders == nil {
		c.Folders = map[string]folderEntry{}
	}
	return &c, nil
}

func (s *Saver) writeCatalog(ctx context.Context, t Target, c *catalog) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return t.Adapter.Save(ctx, CatalogKey, data)
}

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
	c, err := s.readCatalog(ctx, t)
	if err != nil {
		return err
	}
	var later []func()
	if err := f(t, c, &later); err != nil {
		return err
	}
	if err := s.writeCatalog(ctx, t, c); err != nil {
		return err
	}
	for _, g := range later {
		g()
	}
	return nil
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

func (s *Saver) rebuild(ctx context.Context, t Target) (*catalog, error) {
	metas, err := t.Adapter.List(ctx)
	if err != nil {
		return nil, err
	}
	out := &catalog{Docs: map[string]catalogEntry{}, Folders: map[string]folderEntry{}}
	for _, m := range metas {
		if id, ok := strings.CutPrefix(m.ID, FolderRecordPrefix); ok && docfile.ValidID(id) {
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
			continue
		}
		key, size := m.ID, m.Size
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
			key, size = newKey, len(data)
		}
		// Two files with one ID (a crash between writing a new format and deleting the
		// old): the newer one is the document.
		if prev, ok := out.Docs[id]; ok && prev.Updated >= m.UpdatedAt.UnixNano() {
			continue
		}
		out.Docs[id] = catalogEntry{Key: key, Name: doc.Name(), Format: c.Name(), Size: size, Updated: m.UpdatedAt.UnixNano(), Tags: doc.Tags(), Trashed: doc.Trashed(), Folder: doc.Folder()}
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
	if err := s.writeCatalog(ctx, t, out); err != nil {
		return nil, err
	}
	return out, nil
}

// touch rewrites a document's own metadata (name, tags, trash flag) so the
// document and the catalog agree and a rebuilt catalog loses nothing. The
// document's Updated time is not changed.
func (s *Saver) touch(ctx context.Context, t Target, e *catalogEntry, f func(*docfile.Document)) error {
	c, _ := s.codecOf(e.Key)
	if c == nil {
		return nil
	}
	stored, err := t.Adapter.Load(ctx, e.Key)
	if err != nil {
		return err
	}
	doc, err := c.Unmarshal(stored.Data)
	if err != nil {
		return err
	}
	f(&doc)
	data, err := c.Marshal(doc)
	if err != nil {
		return err
	}
	e.Size = len(data)
	return t.Adapter.Save(ctx, e.Key, data)
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
