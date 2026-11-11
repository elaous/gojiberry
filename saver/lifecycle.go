package saver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

const VersionPrefix = "versions/"

const DefaultMaxVersions = 50

var ErrNotFound = docstore.ErrNotFound

// Version is a snapshot of a document.
type Version struct {
	Key    string // what LoadVersion and DeleteVersion take
	ID     string // the document's ID
	Time   time.Time
	Size   int
	Name   string
	Pinned bool
	Author string
}

type VersionMeta struct {
	Name   string `json:"name,omitempty"`
	Pinned bool   `json:"pinned,omitempty"`
	Author string `json:"author,omitempty"`
}

var ErrPinned = errors.New("saver: this version is pinned: unpin it before deleting it")

const maxVersionName = 120

func metaKey(id string) string { return VersionPrefix + id + "/meta.json" }

func versionDoc(key string) string {
	rest := strings.TrimPrefix(key, VersionPrefix)
	id, _, _ := strings.Cut(rest, "/")
	return id
}

func (s *Saver) loadMeta(ctx context.Context, t Target, id string) (map[string]VersionMeta, error) {
	stored, err := t.Adapter.Load(ctx, metaKey(id))
	if errors.Is(err, docstore.ErrNotFound) {
		return map[string]VersionMeta{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]VersionMeta{}
	if json.Unmarshal(stored.Data, &m) != nil {
		return map[string]VersionMeta{}, nil // an unreadable note is no note: the snapshots stay usable
	}
	return m, nil
}

func (s *Saver) saveMeta(ctx context.Context, t Target, id string, m map[string]VersionMeta) error {
	if len(m) == 0 {
		err := t.Adapter.Delete(ctx, metaKey(id))
		if errors.Is(err, docstore.ErrNotFound) {
			return nil
		}
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return t.Adapter.Save(ctx, metaKey(id), data)
}

func (s *Saver) SetVersionMeta(ctx context.Context, target, key string, edit func(*VersionMeta)) error {
	if err := checkVersionKey(key); err != nil {
		return err
	}
	t, err := s.Target(target)
	if err != nil {
		return err
	}
	if _, err := t.Adapter.Load(ctx, key); err != nil {
		return err
	}
	id := versionDoc(key)
	m, err := s.loadMeta(ctx, t, id)
	if err != nil {
		return err
	}
	v := m[key]
	edit(&v)
	v.Name = strings.TrimSpace(v.Name)
	if len(v.Name) > maxVersionName {
		v.Name = strings.ToValidUTF8(v.Name[:maxVersionName], "")
	}
	if v == (VersionMeta{}) {
		delete(m, key)
	} else {
		m[key] = v
	}
	return s.saveMeta(ctx, t, id, m)
}

// RenameVersion gives a snapshot a name ("" removes it).
func (s *Saver) RenameVersion(ctx context.Context, target, key, name string) error {
	return s.SetVersionMeta(ctx, target, key, func(v *VersionMeta) { v.Name = name })
}

func (s *Saver) PinVersion(ctx context.Context, target, key string, pinned bool) error {
	return s.SetVersionMeta(ctx, target, key, func(v *VersionMeta) { v.Pinned = pinned })
}

func (s *Saver) maxVersions() int {
	if s.MaxVersions > 0 {
		return s.MaxVersions
	}
	return DefaultMaxVersions
}

func versionKey(id string, c docfile.Codec, t time.Time) string {
	return fmt.Sprintf("%s%s/%020d%s", VersionPrefix, id, t.UnixNano(), c.Ext())
}

func (s *Saver) SaveVersion(ctx context.Context, target string, c docfile.Codec, doc docfile.Document) (Version, error) {
	return s.SaveVersionBy(ctx, target, c, doc, "")
}

func (s *Saver) SaveVersionBy(ctx context.Context, target string, c docfile.Codec, doc docfile.Document, author string) (Version, error) {
	t, err := s.Target(target)
	if err != nil {
		return Version{}, err
	}
	id := doc.ID()
	if !docfile.ValidID(id) {
		return Version{}, ErrID
	}
	if _, err := s.Get(ctx, target, id); err != nil {
		return Version{}, err
	}
	now := time.Now()
	doc.Updated = now.UnixNano()
	data, err := c.Marshal(doc)
	if err != nil {
		return Version{}, err
	}
	key := versionKey(id, c, now)
	// keys must not collide on a coarse clock
	for i := 1; ; i++ {
		if _, err := t.Adapter.Load(ctx, key); errors.Is(err, docstore.ErrNotFound) {
			break
		} else if err != nil {
			return Version{}, err
		}
		key = versionKey(id, c, now.Add(time.Duration(i)))
	}
	if err := t.Adapter.Save(ctx, key, data); err != nil {
		return Version{}, err
	}
	if author = strings.TrimSpace(author); author != "" {
		_ = s.SetVersionMeta(ctx, target, key, func(v *VersionMeta) { v.Author = author }) // a lost note is not a lost snapshot
	}
	if vs, err := s.Versions(ctx, target, id); err == nil {
		kept := 0
		meta, _ := s.loadMeta(ctx, t, id)
		for _, v := range vs { // newest first; a pinned snapshot is never dropped or counted
			if v.Pinned {
				continue
			}
			if kept++; kept > s.maxVersions() {
				_ = t.Adapter.Delete(ctx, v.Key)
				delete(meta, v.Key)
			}
		}
		if kept > s.maxVersions() {
			_ = s.saveMeta(ctx, t, id, meta)
		}
	}
	return Version{Key: key, ID: id, Time: now, Size: len(data), Author: author}, nil
}

// Versions lists the snapshots of the document, newest first.
func (s *Saver) Versions(ctx context.Context, target, id string) ([]Version, error) {
	t, err := s.Target(target)
	if err != nil {
		return nil, err
	}
	if !docfile.ValidID(id) {
		return nil, ErrID
	}
	metas, err := t.Adapter.List(ctx)
	if err != nil {
		return nil, err
	}
	prefix := VersionPrefix + id + "/"
	notes, _ := s.loadMeta(ctx, t, id)
	var out []Version
	for _, m := range metas {
		rest, ok := strings.CutPrefix(m.ID, prefix)
		if !ok || strings.Contains(rest, "/") {
			continue
		}
		c, stamp := s.codecOf(rest)
		if c == nil {
			continue
		}
		ns, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil {
			continue
		}
		n := notes[m.ID]
		out = append(out, Version{Key: m.ID, ID: id, Time: time.Unix(0, ns), Size: m.Size, Name: n.Name, Pinned: n.Pinned, Author: n.Author})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key > out[j].Key })
	return out, nil
}

func checkVersionKey(key string) error {
	if !strings.HasPrefix(key, VersionPrefix) || strings.Contains(key, "..") || strings.Count(key, "/") != 2 {
		return ErrName
	}
	return nil
}

// LoadVersion reads a snapshot back.
func (s *Saver) LoadVersion(ctx context.Context, target, key string) (docfile.Document, error) {
	if err := checkVersionKey(key); err != nil {
		return docfile.Document{}, err
	}
	t, err := s.Target(target)
	if err != nil {
		return docfile.Document{}, err
	}
	stored, err := t.Adapter.Load(ctx, key)
	if err != nil {
		return docfile.Document{}, err
	}
	c, _ := s.codecOf(key)
	if c == nil {
		c = docfile.Detect(stored.Data)
	}
	return c.Unmarshal(stored.Data)
}

// DeleteVersion removes one snapshot.
func (s *Saver) DeleteVersion(ctx context.Context, target, key string) error {
	if err := checkVersionKey(key); err != nil {
		return err
	}
	t, err := s.Target(target)
	if err != nil {
		return err
	}
	id := versionDoc(key)
	notes, _ := s.loadMeta(ctx, t, id)
	if notes[key].Pinned {
		return ErrPinned
	}
	if err := t.Adapter.Delete(ctx, key); err != nil {
		return err
	}
	if _, had := notes[key]; had {
		delete(notes, key)
		_ = s.saveMeta(ctx, t, id, notes)
	}
	return nil
}

func (s *Saver) dropVersions(ctx context.Context, t Target, id string) {
	metas, err := t.Adapter.List(ctx)
	if err != nil {
		return
	}
	for _, m := range metas {
		if strings.HasPrefix(m.ID, VersionPrefix+id+"/") {
			_ = t.Adapter.Delete(ctx, m.ID)
		}
	}
}

func (s *Saver) mark(ctx context.Context, target string, ids []string, trashed bool) error {
	return s.update(ctx, target, func(t Target, cat *catalog, later *[]func()) error {
		for _, id := range ids {
			if _, ok := cat.Docs[id]; !ok {
				return fmt.Errorf("%w: %s", ErrNotFound, id)
			}
		}
		now := time.Now().UnixNano()
		for _, id := range ids {
			e := cat.Docs[id]
			stamp := int64(0)
			if trashed {
				stamp = now
			}
			if err := s.touch(ctx, t, cat, id, &e, later, func(d *docfile.Document) { d.SetTrashed(stamp) }); err != nil {
				return err
			}
			e.Trashed = stamp
			cat.Docs[id] = e
		}
		return nil
	})
}

// Trash moves documents to the trash.
func (s *Saver) Trash(ctx context.Context, target string, ids ...string) error {
	return s.mark(ctx, target, ids, true)
}

// Restore takes documents out of the trash.
func (s *Saver) Restore(ctx context.Context, target string, ids ...string) error {
	return s.mark(ctx, target, ids, false)
}

func (s *Saver) Trashed(ctx context.Context, target string) ([]Entry, error) {
	var out []Entry
	err := s.view(ctx, target, func(_ Target, cat *catalog) error {
		out = cat.sorted(s, true)
		return nil
	})
	return out, err
}

// Purge deletes documents in the trash for good.
func (s *Saver) Purge(ctx context.Context, target string, ids ...string) error {
	return s.update(ctx, target, func(t Target, cat *catalog, later *[]func()) error {
		for _, id := range ids {
			e, ok := cat.Docs[id]
			if !ok {
				return fmt.Errorf("%w: %s", ErrNotFound, id)
			}
			if e.Trashed == 0 {
				return fmt.Errorf("saver: %s is not in the trash", id)
			}
		}
		for _, id := range ids {
			e := cat.Docs[id]
			delete(cat.Docs, id)
			*later = append(*later, func() {
				_ = t.Adapter.Delete(ctx, e.Key)
				if e.Prev != "" {
					_ = t.Adapter.Delete(ctx, e.Prev)
				}
				s.dropVersions(ctx, t, id)
			})
		}
		return nil
	})
}

func (s *Saver) EmptyTrash(ctx context.Context, target string) (int, error) {
	list, err := s.Trashed(ctx, target)
	if err != nil {
		return 0, err
	}
	ids := make([]string, len(list))
	for i, e := range list {
		ids[i] = e.ID
	}
	return len(ids), s.Purge(ctx, target, ids...)
}

func (s *Saver) Duplicate(ctx context.Context, target, id, newName string) (Entry, error) {
	doc, err := s.Load(ctx, target, id)
	if err != nil {
		return Entry{}, err
	}
	if newName == "" {
		newName = "Copy of " + doc.Name()
	}
	e, err := s.Get(ctx, target, id)
	if err != nil {
		return Entry{}, err
	}
	c := e.Format
	if c == nil {
		c = docfile.JSON
	}
	doc.SetID(docfile.NewID())
	doc.SetName(newName)
	return s.Save(ctx, target, c, doc)
}

// Rename gives a document a new name.
func (s *Saver) Rename(ctx context.Context, target, id, name string) error {
	if !ValidName(name) {
		return ErrName
	}
	return s.update(ctx, target, func(t Target, cat *catalog, later *[]func()) error {
		e, ok := cat.Docs[id]
		if !ok {
			return ErrNotFound
		}
		if err := s.touch(ctx, t, cat, id, &e, later, func(d *docfile.Document) { d.SetName(name) }); err != nil {
			return err
		}
		if !e.confidential() { // the catalog lists the placeholder: the name is in the document
			e.Name = name
		}
		cat.Docs[id] = e
		return nil
	})
}

func (s *Saver) SetTags(ctx context.Context, target, id string, tags []string) error {
	var clean docfile.Document
	clean.SetTags(tags)
	return s.update(ctx, target, func(t Target, cat *catalog, later *[]func()) error {
		e, ok := cat.Docs[id]
		if !ok {
			return ErrNotFound
		}
		if err := s.touch(ctx, t, cat, id, &e, later, func(d *docfile.Document) { d.SetTags(clean.Tags()) }); err != nil {
			return err
		}
		if !e.confidential() { // the tags are in the document, not the catalog
			e.Tags = clean.Tags()
		}
		cat.Docs[id] = e
		return nil
	})
}

func (s *Saver) restoreHistory(ctx context.Context, target, id string, c docfile.Codec, history []docfile.Revision) error {
	t, err := s.Target(target)
	if err != nil {
		return err
	}
	for _, r := range history {
		if r.Doc == nil {
			continue
		}
		d := *r.Doc
		d.History = nil
		d.SetID(id)
		data, err := c.Marshal(d)
		if err != nil {
			return err
		}
		if err := t.Adapter.Save(ctx, versionKey(id, c, time.Unix(0, r.Saved)), data); err != nil {
			return err
		}
	}
	if vs, err := s.Versions(ctx, target, id); err == nil && len(vs) > s.maxVersions() {
		for _, old := range vs[s.maxVersions():] {
			_ = t.Adapter.Delete(ctx, old.Key)
		}
	}
	return nil
}

func (s *Saver) ExportOpen(ctx context.Context, target, id string, history bool) ([]byte, error) {
	data, _, err := s.exportOpen(ctx, target, id, history)
	return data, err
}

func (s *Saver) exportOpen(ctx context.Context, target, id string, history bool) ([]byte, int, error) {
	doc, n, err := s.loadWithHistory(ctx, target, id, history)
	if err != nil {
		return nil, 0, err
	}
	data, err := docfile.Open.Marshal(doc)
	return data, n, err
}

func (s *Saver) loadWithHistory(ctx context.Context, target, id string, history bool) (docfile.Document, int, error) {
	doc, err := s.Load(ctx, target, id)
	if err != nil {
		return docfile.Document{}, 0, err
	}
	n := 0
	if history {
		vs, err := s.Versions(ctx, target, id)
		if err != nil {
			return docfile.Document{}, 0, err
		}
		for i := len(vs) - 1; i >= 0; i-- { // Versions is newest first
			d, err := s.LoadVersion(ctx, target, vs[i].Key)
			if err != nil {
				return docfile.Document{}, 0, err
			}
			d.History = nil
			doc.History = append(doc.History, docfile.Revision{Saved: vs[i].Time.UnixNano(), Doc: &d})
			n++
		}
	}
	return doc, n, nil
}

type OpenSizes struct {
	Current, History int64
	Versions         int
}

func (s *Saver) OpenSizes(ctx context.Context, target, id string) (OpenSizes, error) {
	bare, _, err := s.exportOpen(ctx, target, id, false)
	if err != nil {
		return OpenSizes{}, err
	}
	full, n, err := s.exportOpen(ctx, target, id, true)
	if err != nil {
		return OpenSizes{}, err
	}
	return OpenSizes{Current: int64(len(bare)), History: int64(len(full) - len(bare)), Versions: n}, nil
}
