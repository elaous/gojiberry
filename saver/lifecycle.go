package saver

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// The life of a document beyond saving it: versions (snapshots to go back to),
// the trash (a flag in the catalog: the document stays where it is, so nothing is
// overwritten and restoring is instant), copies, names and tags. Versions are
// kept in the same target under keys of their own:
//
//	versions/<id>/<time><ext>   a snapshot of the document <id>, <time> in nanoseconds
//
// List does not show them, and the document methods refuse them.
const VersionPrefix = "versions/"

// DefaultMaxVersions is how many snapshots of one document are kept when
// Saver.MaxVersions is 0; the oldest go first.
const DefaultMaxVersions = 50

// ErrNotFound is returned for a document or version that is not there.
var ErrNotFound = docstore.ErrNotFound

// Version is a snapshot of a document.
type Version struct {
	Key  string // what LoadVersion and DeleteVersion take
	ID   string // the document's ID
	Time time.Time
	Size int
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

// SaveVersion stores a snapshot of doc as a version of the document it
// belongs to (doc.ID(), which must be set: save the document first), and drops
// the oldest snapshots beyond Saver.MaxVersions.
func (s *Saver) SaveVersion(ctx context.Context, target string, c docfile.Codec, doc docfile.Document) (Version, error) {
	t, err := s.Target(target)
	if err != nil {
		return Version{}, err
	}
	id := doc.ID()
	if !docfile.ValidID(id) {
		return Version{}, ErrID
	}
	if _, err := s.Get(ctx, target, id); err != nil {
		return Version{}, err // a late snapshot of a deleted document would be an orphan
	}
	now := time.Now()
	doc.Updated = now.UnixNano()
	data, err := c.Marshal(doc)
	if err != nil {
		return Version{}, err
	}
	key := versionKey(id, c, now)
	// Two snapshots in one nanosecond cannot happen on a real clock, but keys
	// must not collide on a coarse one.
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
	if vs, err := s.Versions(ctx, target, id); err == nil && len(vs) > s.maxVersions() {
		for _, old := range vs[s.maxVersions():] {
			_ = t.Adapter.Delete(ctx, old.Key)
		}
	}
	return Version{Key: key, ID: id, Time: now, Size: len(data)}, nil
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
		out = append(out, Version{Key: m.ID, ID: id, Time: time.Unix(0, ns), Size: m.Size})
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
	return t.Adapter.Delete(ctx, key)
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

// mark sets or clears the trash flag of each document, in the document and the
// catalog; it changes nothing unless every ID is there.
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

// Trash moves documents to the trash. They stay where they are, with their
// versions, so restoring them is instant and loses nothing.
func (s *Saver) Trash(ctx context.Context, target string, ids ...string) error {
	return s.mark(ctx, target, ids, true)
}

// Restore takes documents out of the trash.
func (s *Saver) Restore(ctx context.Context, target string, ids ...string) error {
	return s.mark(ctx, target, ids, false)
}

// Trashed lists the documents in the trash, the most recently trashed first.
func (s *Saver) Trashed(ctx context.Context, target string) ([]Entry, error) {
	var out []Entry
	err := s.view(ctx, target, func(_ Target, cat *catalog) error {
		out = cat.sorted(s, true)
		return nil
	})
	return out, err
}

// Purge deletes documents in the trash for good. It checks every ID first and
// changes nothing if one is missing or not in the trash.
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

// EmptyTrash deletes everything in the trash and returns how many it removed.
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

// Duplicate saves a copy of the document as a new document, with a new ID and the
// same format, tags and contents, and returns it. With newName "" the copy is
// called "Copy of <name>"; names need not be unique.
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
		e.Name = name
		cat.Docs[id] = e
		return nil
	})
}

// SetTags replaces the tags of a document (trimmed, without repeats; see
// Document.SetTags).
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
		e.Tags = clean.Tags()
		cat.Docs[id] = e
		return nil
	})
}

// restoreHistory stores the revisions of an imported document as its versions, at the
// times they were saved, and keeps the newest MaxVersions of all of them.
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

// ExportOpen writes a document as one .open file (docfile.Open), with its history when
// history is set: the document and each of its versions, oldest first. Saving the file
// somewhere else (Saver.Save) brings the versions back.
func (s *Saver) ExportOpen(ctx context.Context, target, id string, history bool) ([]byte, error) {
	data, _, err := s.exportOpen(ctx, target, id, history)
	return data, err
}

func (s *Saver) exportOpen(ctx context.Context, target, id string, history bool) ([]byte, int, error) {
	doc, err := s.Load(ctx, target, id)
	if err != nil {
		return nil, 0, err
	}
	n := 0
	if history {
		vs, err := s.Versions(ctx, target, id)
		if err != nil {
			return nil, 0, err
		}
		for i := len(vs) - 1; i >= 0; i-- { // Versions is newest first
			d, err := s.LoadVersion(ctx, target, vs[i].Key)
			if err != nil {
				return nil, 0, err
			}
			d.History = nil
			doc.History = append(doc.History, docfile.Revision{Saved: vs[i].Time.UnixNano(), Doc: &d})
			n++
		}
	}
	data, err := docfile.Open.Marshal(doc)
	return data, n, err
}

// OpenSizes says how big a .open file of a document would be, in bytes: Current is the
// file with no history, History how much the versions add to it, and Versions how many
// there are.
type OpenSizes struct {
	Current, History int64
	Versions         int
}

// OpenSizes measures the .open file of a document, with and without its history.
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
