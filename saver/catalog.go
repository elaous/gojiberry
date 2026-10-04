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
// documents hold the same facts (Document.ID, Name and Tags), so a lost or
// damaged catalog is rebuilt from them (Saver.Rebuild).
const CatalogKey = "catalog:index"

type catalogEntry struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Format  string   `json:"format"`
	Size    int      `json:"size,omitempty"`
	Updated int64    `json:"updated,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Trashed int64    `json:"trashed,omitempty"`
}

type catalog struct {
	Docs map[string]catalogEntry `json:"docs"`
}

func (c *catalog) entry(s *Saver, id string) Entry {
	e := c.Docs[id]
	codec, _ := s.Codec(e.Format)
	out := Entry{ID: id, Key: e.Key, Name: e.Name, Format: codec, Size: e.Size, Updated: time.Unix(0, e.Updated), Tags: e.Tags}
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
	return &c, nil
}

func (s *Saver) writeCatalog(ctx context.Context, t Target, c *catalog) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return t.Adapter.Save(ctx, CatalogKey, data)
}

// update runs f on the target's catalog and writes it back, one update at a
// time within this process.
func (s *Saver) update(ctx context.Context, target string, f func(t Target, c *catalog) error) error {
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
	if err := f(t, c); err != nil {
		return err
	}
	return s.writeCatalog(ctx, t, c)
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
	old := &catalog{Docs: map[string]catalogEntry{}}
	if stored, err := t.Adapter.Load(ctx, CatalogKey); err == nil {
		_ = json.Unmarshal(stored.Data, old) // trash flags survive a rebuild
		if old.Docs == nil {
			old.Docs = map[string]catalogEntry{}
		}
	}
	metas, err := t.Adapter.List(ctx)
	if err != nil {
		return nil, err
	}
	out := &catalog{Docs: map[string]catalogEntry{}}
	for _, m := range metas {
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
		id := doc.ID()
		if !docfile.ValidID(id) {
			id = stem
		}
		name := doc.Name()
		if name == "" {
			name = stem
		}
		e := catalogEntry{Key: m.ID, Name: name, Format: c.Name(), Size: m.Size, Updated: m.UpdatedAt.UnixNano(), Tags: doc.Tags()}
		if prev, ok := old.Docs[id]; ok {
			e.Trashed = prev.Trashed
			if len(prev.Tags) > 0 {
				e.Tags = prev.Tags
			}
		}
		out.Docs[id] = e
	}
	if err := s.writeCatalog(ctx, t, out); err != nil {
		return nil, err
	}
	return out, nil
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
